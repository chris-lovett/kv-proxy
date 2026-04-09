package proxy

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/consul-kv-proxy/internal/rules"
)

// Config holds proxy configuration.
type Config struct {
	ConsulAddr    string
	ConsulToken   string
	TLSSkipVerify bool
	Rules         *rules.RuleSet
}

// Proxy is an HTTP handler that intercepts Consul KV writes.
type Proxy struct {
	config   Config
	target   *url.URL
	reverseP *httputil.ReverseProxy
}

// New creates a new Proxy.
func New(cfg Config) *Proxy {
	target, err := url.Parse(cfg.ConsulAddr)
	if err != nil {
		log.Fatalf("Invalid consul address %q: %v", cfg.ConsulAddr, err)
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.TLSSkipVerify,
		},
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = transport

	// Rewrite request to target
	rp.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		// Inject Consul token
		req.Header.Set("X-Consul-Token", cfg.ConsulToken)
	}

	return &Proxy{
		config:   cfg,
		target:   target,
		reverseP: rp,
	}
}

// ServeHTTP handles incoming requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Health check endpoint
	if r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok"}`)
		return
	}

	// Only intercept KV PUT/DELETE writes
	isKVWrite := strings.HasPrefix(r.URL.Path, "/v1/kv/") && r.Method == http.MethodPut

	if isKVWrite {
		if err := p.enforceRules(w, r); err != nil {
			log.Printf("[REJECTED] %s %s (%v) reason=%s", r.Method, r.URL.Path, time.Since(start), err.Error())
			return
		}
	}

	log.Printf("[FORWARD] %s %s", r.Method, r.URL.Path)
	p.reverseP.ServeHTTP(w, r)
}

// enforceRules reads the request body, runs all rules, and rejects if needed.
func (p *Proxy) enforceRules(w http.ResponseWriter, r *http.Request) error {
	// Extract key from path: /v1/kv/<key>
	key := strings.TrimPrefix(r.URL.Path, "/v1/kv/")

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return fmt.Errorf("read body: %w", err)
	}
	// Restore body for forwarding
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Evaluate rules
	if violation := p.config.Rules.Evaluate(key, body); violation != nil {
		writeError(w, http.StatusBadRequest, violation.Error())
		return violation
	}

	return nil
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
