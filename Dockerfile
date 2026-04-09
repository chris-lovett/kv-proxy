FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o consul-kv-proxy ./cmd/proxy

# OpenShift-compatible base image (runs as non-root by default)
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

WORKDIR /app
COPY --from=builder /app/consul-kv-proxy .

# OpenShift requires non-root user
RUN chown -R 1001:0 /app && chmod -R g=u /app
USER 1001

EXPOSE 8080

ENTRYPOINT ["/app/consul-kv-proxy"]
