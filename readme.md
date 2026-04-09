# Consul KV Proxy

A lightweight Go proxy that enforces write-time rules on Consul KV writes before forwarding to Consul. Deployed as a pod in OpenShift.

## Rules Enforced

1. **Max value size** — rejects values over 100KB
2. **Sensitive data detection** — blocks writes containing:
   - AWS access/secret keys
   - GitHub tokens
   - Stripe keys
   - Private keys (RSA, EC, OpenSSH)
   - API tokens/keys
   - Passwords

## How It Works

```
App → consul-kv-proxy:8080 → Consul:8501
         ↓ (on PUT /v1/kv/*)
      Enforce rules
         ↓ pass
      Forward to Consul
         ↓ fail
      Return 400 + JSON error
```

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `CONSUL_HTTP_ADDR` | `https://127.0.0.1:8501` | Consul backend address |
| `CONSUL_HTTP_TOKEN` | required | Consul ACL token |
| `CONSUL_HTTP_SSL_VERIFY` | `true` | Set to `false` to skip TLS verification |
| `PROXY_LISTEN_ADDR` | `:8080` | Address the proxy listens on |

## Build & Deploy

```bash
# Build image
docker build -t <your-registry>/consul-kv-proxy:latest .
docker push <your-registry>/consul-kv-proxy:latest

# Update token in secret
sed -i 's/<your-consul-token>/your-actual-token/' deployments/openshift.yaml

# Update image in deployment
sed -i 's|<your-registry>|your.registry.com|' deployments/openshift.yaml

# Deploy to OpenShift
kubectl apply -f deployments/openshift.yaml

# Verify
kubectl get pods -n consul -l app=consul-kv-proxy
```

## Testing

```bash
# Should PASS
curl -X PUT http://consul-kv-proxy/v1/kv/test/safe -d "hello-world"

# Should FAIL — too large
curl -X PUT http://consul-kv-proxy/v1/kv/test/toobig -d "$(head -c 200000 /dev/urandom | base64)"

# Should FAIL — AWS key
curl -X PUT http://consul-kv-proxy/v1/kv/test/awskey -d "AKIAIOSFODNN7EXAMPLE"

# Should FAIL — password
curl -X PUT http://consul-kv-proxy/v1/kv/test/creds -d "password=supersecret123"
```

## Pointing Apps at the Proxy

Instead of connecting directly to Consul, apps should use:
```
CONSUL_HTTP_ADDR=http://consul-kv-proxy.consul.svc.cluster.local:80
```

All KV reads are passed through transparently. Only KV PUT writes are inspected.
# kv-proxy
