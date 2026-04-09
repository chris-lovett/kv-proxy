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
| `CONSUL_HTTP_TOKEN` | *(none)* | **Yes** | Consul ACL token used by the proxy when calling Consul. |
| `CONSUL_HTTP_SSL_VERIFY` | `true` |  | If set to `false`, the proxy will **skip TLS certificate verification** when talking to Consul (useful for demos/self-signed certs). |
| `PROXY_LISTEN_ADDR` | `:8080` |  | Address/port the proxy listens on. |

## Running locally

### Prerequisites

- Go installed (matching `go.mod`)
- A reachable Consul HTTP API endpoint (often `:8501` for HTTPS)
- A Consul ACL token the proxy can use

### Run

```bash
export CONSUL_HTTP_ADDR="https://<your-consul-host>:8501"
export CONSUL_HTTP_TOKEN="<your-consul-acl-token>"

# Optional: for demos with self-signed certs ONLY
# export CONSUL_HTTP_SSL_VERIFY=false

# Optional: change listen address
# export PROXY_LISTEN_ADDR=":8080"

go run ./cmd/proxy
```

You should see a log similar to:

```text
Consul KV Proxy listening on :8080 -> https://<your-consul-host>:8501
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

### Write a safe value (should pass)

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/safe" -d "hello-world"
```

### Attempt to write a large value (should fail)

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/toobig" \
  -d "$(head -c 200000 /dev/urandom | base64)"
```

### Attempt to write sensitive-looking data (should fail)

AWS access key example:

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/awskey" \
  -d "AKIAIOSFODNN7EXAMPLE"
```

Password example:

```bash
curl -i -X PUT "http://localhost:8080/v1/kv/test/creds" \
  -d "password=supersecret123"
```

## Security and operational notes

- **The proxy requires a Consul ACL token** (`CONSUL_HTTP_TOKEN`) and uses it to communicate with Consul.
  - Choose a token with the minimum privileges required for the KV operations you expect.
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
