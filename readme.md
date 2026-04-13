# Consul KV Proxy

A lightweight HTTP reverse proxy (written in Go) that sits in front of a Consul (Enterprise or OSS) HTTP API endpoint and **enforces write-time controls for Consul KV** before forwarding requests to Consul.

This project is intended as a **demonstration** of how a platform team can block sensitive data from being written to Consul KV (e.g., instead of writing Sentinel policies), and how to centralize KV write enforcement by routing applications through a proxy.

## What it does (high level)

When your application writes to Consul KV via the HTTP API:

- The application sends requests to **this proxy** instead of directly to Consul.
- The proxy forwards requests to the configured Consul backend.
- **Only KV writes** are inspected and can be blocked:
  - `PUT /v1/kv/<key>` (Consul KV write)
- KV reads and other requests are passed through transparently.

If a write violates a rule, the proxy **rejects** the request and returns an HTTP error (rather than allowing the write to reach Consul).

## Why use this

This proxy demonstrates a pattern that can be useful when you want:

- A simple **“policy enforcement point”** in front of Consul KV writes.
- To block common sensitive values (credentials/secrets) from being stored in KV.
- To enforce operational guardrails (example: maximum KV value size).
- To avoid modifying applications beyond changing `CONSUL_HTTP_ADDR` to point at the proxy.

> Note: Consul already provides ACLs, namespaces, and other control mechanisms. This proxy focuses on **content-based validation of KV values** and other “write-time” checks.

## Rules enforced (current)

The proxy currently enforces the following rules on KV writes:

1. **Maximum value size (100 KB)**  
   Rejects values larger than 100KB.

2. **Sensitive data detection**  
   Rejects writes when the value appears to contain sensitive data such as:
   - AWS access/secret keys
   - GitHub tokens
   - Stripe keys
   - Private keys (RSA, EC, OpenSSH)
   - API tokens/keys
   - Passwords

The rule set is configured in `cmd/proxy/main.go`.

## How it works

Request flow:

```text
App → consul-kv-proxy (listen addr, default :8080) → Consul HTTP API (default https://127.0.0.1:8501)
           |
           |-- on PUT /v1/kv/* :
           |     - read request body (value)
           |     - apply rules
           |     - if pass: forward to Consul
           |     - if fail: return error to client (no write reaches Consul)
           |
           └-- all other requests:
                 - forwarded to Consul transparently
```

## Configuration (environment variables)

| Variable | Default | Required | Description |
|---|---:|:---:|---|
| `CONSUL_HTTP_ADDR` | `https://127.0.0.1:8501` |  | Consul backend address (the proxy forwards to this). |
| `CONSUL_HTTP_TOKEN` | *(none)* |  | Fallback Consul ACL token used **only** when the caller does not supply `X-Consul-Token`. No longer required; omit it when `PROXY_REQUIRE_CALLER_TOKEN=true`. |
| `PROXY_REQUIRE_CALLER_TOKEN` | `false` |  | When `true`, requests without a caller-supplied `X-Consul-Token` header are rejected with **401 Unauthorized**. When `false`, the proxy falls back to `CONSUL_HTTP_TOKEN` (if set) for unauthenticated requests. |
| `CONSUL_HTTP_SSL_VERIFY` | `true` |  | If set to `false`, the proxy will **skip TLS certificate verification** when talking to Consul (useful for demos/self-signed certs). |
| `PROXY_LISTEN_ADDR` | `:8080` |  | Address/port the proxy listens on. |

## Authorization behavior

The proxy now performs **per-caller authorization** instead of always using a shared proxy token:

1. **Caller token present** (`X-Consul-Token` header set by the caller):  
   The header is forwarded to Consul **unchanged**. Consul ACL enforcement is applied using the caller's own token, enabling per-user / per-namespace access control.

2. **Caller token absent — fallback mode** (`PROXY_REQUIRE_CALLER_TOKEN=false`, default):  
   If `CONSUL_HTTP_TOKEN` is configured, it is used as a fallback token for unauthenticated callers. If neither is set, the request is forwarded without an auth token (Consul will evaluate it against the default ACL policy).

3. **Caller token absent — strict mode** (`PROXY_REQUIRE_CALLER_TOKEN=true`):  
   The proxy immediately rejects the request with **401 Unauthorized** and logs the rejection. No request reaches Consul without a caller-supplied token.

The `X-Consul-Namespace` header is always forwarded from the caller unchanged, enabling per-namespace routing without any proxy-side hardcoding.

**Observability**: every forwarded request is logged with method, path, auth source (`caller` / `fallback` / `none`), and namespace (if present). Token values are **never logged**.

## Running locally

### Prerequisites

- Go installed (matching `go.mod`)
- A reachable Consul HTTP API endpoint (often `:8501` for HTTPS)
- A Consul ACL token the proxy can use

### Run

```bash
export CONSUL_HTTP_ADDR="https://<your-consul-host>:8501"

# Mode A: forward each caller's own X-Consul-Token (require it; no fallback)
export PROXY_REQUIRE_CALLER_TOKEN=true

# Mode B: use a fallback proxy token when the caller doesn't supply one
# export CONSUL_HTTP_TOKEN="<your-fallback-consul-acl-token>"

# Optional: for demos with self-signed certs ONLY
# export CONSUL_HTTP_SSL_VERIFY=false

# Optional: change listen address
# export PROXY_LISTEN_ADDR=":8080"

go run ./cmd/proxy
```

You should see a log similar to:

```text
Consul KV Proxy listening on :8080 -> https://<your-consul-host>:8501 (require-caller-token=true)
```

## Build and run as a container

### Build

```bash
docker build -t <your-registry>/consul-kv-proxy:latest .
docker push <your-registry>/consul-kv-proxy:latest
```

### Run

```bash
docker run --rm -p 8080:8080 \
  -e CONSUL_HTTP_ADDR="https://<your-consul-host>:8501" \
  -e CONSUL_HTTP_TOKEN="<your-consul-acl-token>" \
  <your-registry>/consul-kv-proxy:latest
```

If you need to skip TLS verification (demo/self-signed):

```bash
docker run --rm -p 8080:8080 \
  -e CONSUL_HTTP_ADDR="https://<your-consul-host>:8501" \
  -e CONSUL_HTTP_TOKEN="<your-consul-acl-token>" \
  -e CONSUL_HTTP_SSL_VERIFY=false \
  <your-registry>/consul-kv-proxy:latest
```

## Pointing applications at the proxy

To use the proxy, configure clients to talk to the proxy instead of Consul directly.

Example (Kubernetes in-cluster DNS):

```bash
export CONSUL_HTTP_ADDR="http://consul-kv-proxy.consul.svc.cluster.local:80"
```

At that point, any client using Consul’s HTTP API for KV operations will go through the proxy.

### What gets inspected?

- KV writes:
  - `PUT /v1/kv/<key>` → inspected and potentially blocked

### What passes through unchanged?

- KV reads (e.g. `GET /v1/kv/<key>`, list queries, etc.)
- Non-KV endpoints (as routed through the proxy)

## Testing

### Write a safe value with a caller token (should pass)

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/safe" \
  -H "X-Consul-Token: $CONSUL_HTTP_TOKEN" \
  -H "X-Consul-Namespace: ait-123" \
  -d "hello-world"
```

### Attempt to write without a token when PROXY_REQUIRE_CALLER_TOKEN=true (should return 401)

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/safe" -d "hello-world"
```

### Attempt to write a large value (should fail)

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/toobig" \
  -H "X-Consul-Token: $CONSUL_HTTP_TOKEN" \
  -d "$(head -c 200000 /dev/urandom | base64)"
```

### Attempt to write sensitive-looking data (should fail)

AWS access key example:

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/awskey" \
  -H "X-Consul-Token: $CONSUL_HTTP_TOKEN" \
  -d "AKIAIOSFODNN7EXAMPLE"
```

Password example:

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/creds" \
  -H "X-Consul-Token: $CONSUL_HTTP_TOKEN" \
  -d "password=supersecret123"
```

## Security and operational notes

- **Per-caller authorization**: by default the proxy forwards each request using the caller’s own `X-Consul-Token`. Set `PROXY_REQUIRE_CALLER_TOKEN=true` to enforce that every request includes a caller token; omit it (default `false`) to allow the proxy’s fallback `CONSUL_HTTP_TOKEN` for unauthenticated callers.
- **`CONSUL_HTTP_TOKEN` is now optional** — it acts as a fallback only. If you set `PROXY_REQUIRE_CALLER_TOKEN=true` you do not need to supply it.
- **No secrets are logged** — the proxy logs which auth source was used (`caller` / `fallback` / `none`) but never the token value itself.
- If you set `CONSUL_HTTP_SSL_VERIFY=false`, TLS verification is disabled between the proxy and Consul. This is convenient for demos but **not recommended** for production.
- This project is intentionally small and focused; it’s a good starting point for adding additional write-time policy checks.

## Repository layout

- `cmd/proxy/` — application entrypoint
- `internal/proxy/` — HTTP proxy implementation and request handling
- `internal/rules/` — rule engine and rule implementations
- `deployments/` — example Kubernetes/OpenShift deployment manifests
- `Dockerfile` — container build

## Extending the proxy

To add a new rule:

1. Implement the rule in `internal/rules/` (or similar).
2. Register the rule in `cmd/proxy/main.go` by adding it to `rules.NewRuleSet(...)`.
3. Rebuild
