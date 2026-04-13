package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/consul-kv-proxy/internal/rules"
)

// newBackend creates a test HTTP backend that records received headers and
// returns 200 OK with "ok" body.
func newBackend(t *testing.T) (*httptest.Server, func() http.Header) {
	t.Helper()
	var captured http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() http.Header { return captured }
}

func newProxy(t *testing.T, backendURL string, cfg Config) *Proxy {
	t.Helper()
	cfg.ConsulAddr = backendURL
	if cfg.Rules == nil {
		cfg.Rules = rules.NewRuleSet()
	}
	return New(cfg)
}

// TestCallerTokenForwarded verifies that a caller-supplied X-Consul-Token is
// forwarded to the backend unchanged, without the proxy overwriting it.
func TestCallerTokenForwarded(t *testing.T) {
	backend, headers := newBackend(t)
	p := newProxy(t, backend.URL, Config{
		ConsulToken: "proxy-fallback-token",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/kv/test", nil)
	req.Header.Set("X-Consul-Token", "caller-token-abc")
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if got := headers().Get("X-Consul-Token"); got != "caller-token-abc" {
		t.Errorf("expected backend to receive caller token %q, got %q", "caller-token-abc", got)
	}
}

// TestFallbackTokenUsedWhenNoCallerToken verifies that the proxy's fallback
// token is injected when the caller does not supply X-Consul-Token.
func TestFallbackTokenUsedWhenNoCallerToken(t *testing.T) {
	backend, headers := newBackend(t)
	p := newProxy(t, backend.URL, Config{
		ConsulToken: "proxy-fallback-token",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/kv/test", nil)
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if got := headers().Get("X-Consul-Token"); got != "proxy-fallback-token" {
		t.Errorf("expected backend to receive fallback token %q, got %q", "proxy-fallback-token", got)
	}
}

// TestRequireCallerTokenRejectsWhenMissing verifies that when RequireCallerToken
// is true, requests without X-Consul-Token are rejected with 401.
func TestRequireCallerTokenRejectsWhenMissing(t *testing.T) {
	backend, _ := newBackend(t)
	p := newProxy(t, backend.URL, Config{
		RequireCallerToken: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/kv/test", nil)
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", rr.Code)
	}
}

// TestRequireCallerTokenAllowsWhenPresent verifies that when RequireCallerToken
// is true, requests with X-Consul-Token are forwarded.
func TestRequireCallerTokenAllowsWhenPresent(t *testing.T) {
	backend, headers := newBackend(t)
	p := newProxy(t, backend.URL, Config{
		RequireCallerToken: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/kv/test", nil)
	req.Header.Set("X-Consul-Token", "user-token-xyz")
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if got := headers().Get("X-Consul-Token"); got != "user-token-xyz" {
		t.Errorf("expected backend to receive caller token %q, got %q", "user-token-xyz", got)
	}
}

// TestNamespaceHeaderForwarded verifies that X-Consul-Namespace from the caller
// is forwarded to the backend unchanged.
func TestNamespaceHeaderForwarded(t *testing.T) {
	backend, headers := newBackend(t)
	p := newProxy(t, backend.URL, Config{
		ConsulToken: "proxy-token",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/kv/test", nil)
	req.Header.Set("X-Consul-Token", "caller-token")
	req.Header.Set("X-Consul-Namespace", "ait-123")
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if got := headers().Get("X-Consul-Namespace"); got != "ait-123" {
		t.Errorf("expected backend to receive namespace %q, got %q", "ait-123", got)
	}
}

// TestKVWriteRulesEnforced verifies that KV PUT rules are still applied
// and the body is restored for forwarding on a passing write.
func TestKVWriteRulesEnforced(t *testing.T) {
	backend, _ := newBackend(t)
	ruleSet := rules.NewRuleSet(rules.MaxValueSize(5))
	p := newProxy(t, backend.URL, Config{
		ConsulToken: "proxy-token",
		Rules:       ruleSet,
	})

	// A value that exceeds the 5-byte limit should be rejected.
	req := httptest.NewRequest(http.MethodPut, "/v1/kv/test/key", strings.NewReader("toolarge"))
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for oversized value, got %d", rr.Code)
	}
}

// TestKVWriteBodyRestoredForForwarding verifies that after rule inspection the
// request body is still forwarded to the backend intact.
func TestKVWriteBodyRestoredForForwarding(t *testing.T) {
	var receivedBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = b
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	ruleSet := rules.NewRuleSet(rules.MaxValueSize(100))
	p := newProxy(t, backend.URL, Config{
		ConsulToken: "proxy-token",
		Rules:       ruleSet,
	})

	const value = "hello world"
	req := httptest.NewRequest(http.MethodPut, "/v1/kv/test/key", strings.NewReader(value))
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	if string(receivedBody) != value {
		t.Errorf("expected backend to receive body %q, got %q", value, string(receivedBody))
	}
}

// TestHealthEndpoint verifies the /health endpoint returns 200 without auth.
func TestHealthEndpoint(t *testing.T) {
	backend, _ := newBackend(t)
	p := newProxy(t, backend.URL, Config{
		RequireCallerToken: true,
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	p.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /health, got %d", rr.Code)
	}
}
