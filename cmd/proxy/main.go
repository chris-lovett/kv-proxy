package main

import (
	"log"
	"net/http"
	"os"

	"github.com/hashicorp/consul-kv-proxy/internal/proxy"
	"github.com/hashicorp/consul-kv-proxy/internal/rules"
)

func main() {
	consulAddr := getEnv("CONSUL_HTTP_ADDR", "https://127.0.0.1:8501")
	consulToken := getEnv("CONSUL_HTTP_TOKEN", "")
	listenAddr := getEnv("PROXY_LISTEN_ADDR", ":8080")
	tlsSkipVerify := getEnv("CONSUL_HTTP_SSL_VERIFY", "true") == "false"
	requireCallerToken := getEnv("PROXY_REQUIRE_CALLER_TOKEN", "false") == "true"

	if consulToken == "" && !requireCallerToken {
		log.Println("WARNING: CONSUL_HTTP_TOKEN is not set and PROXY_REQUIRE_CALLER_TOKEN is false; " +
			"requests without a caller-supplied X-Consul-Token will be forwarded unauthenticated")
	}

	ruleSet := rules.NewRuleSet(
		rules.MaxValueSize(100*1024), // 100KB
		rules.NoSensitiveData(),
	)

	p := proxy.New(proxy.Config{
		ConsulAddr:         consulAddr,
		ConsulToken:        consulToken,
		RequireCallerToken: requireCallerToken,
		TLSSkipVerify:      tlsSkipVerify,
		Rules:              ruleSet,
	})

	log.Printf("Consul KV Proxy listening on %s -> %s (require-caller-token=%v)", listenAddr, consulAddr, requireCallerToken)
	if err := http.ListenAndServe(listenAddr, p); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
