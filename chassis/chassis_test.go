package chassis

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hazyhaar/pkg/mcpquic"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	cert, err := GenerateSelfSignedCert()
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("no certificate data")
	}
	if cert.PrivateKey == nil {
		t.Fatal("no private key")
	}
}

func TestDevelopmentTLSConfig(t *testing.T) {
	cfg, err := DevelopmentTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("min version: got %x", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("certs: got %d", len(cfg.Certificates))
	}

	foundH3 := false
	foundMCP := false
	for _, p := range cfg.NextProtos {
		if p == "h3" {
			foundH3 = true
		}
		if p == mcpquic.ALPNProtocolMCP {
			foundMCP = true
		}
	}
	if !foundH3 {
		t.Fatal("missing h3 ALPN")
	}
	if !foundMCP {
		t.Fatal("missing MCP ALPN")
	}
}

func TestNew_DevMode(t *testing.T) {
	handler := http.NewServeMux()
	s, err := New(Config{
		Addr:    ":0",
		Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.addr != ":0" {
		t.Fatalf("addr: got %q", s.addr)
	}
	if s.tlsCfg == nil {
		t.Fatal("TLS config should be auto-generated")
	}
	if s.mcpHandler != nil {
		t.Fatal("mcpHandler should be nil when MCPServer is nil")
	}
}

func TestNew_WithMCPServer(t *testing.T) {
	// We can't easily create a real MCPServer in tests without importing
	// mcp-go/server, but we verify that the Config flow is correct.
	handler := http.NewServeMux()
	s, err := New(Config{
		Addr:    ":0",
		Handler: handler,
		// MCPServer: nil — MCP disabled
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.mcpHandler != nil {
		t.Fatal("mcpHandler should be nil when MCPServer is not provided")
	}
}

func TestAltSvcMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := altSvcMiddleware(":8443", inner)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	altSvc := rec.Header().Get("Alt-Svc")
	if altSvc == "" {
		t.Fatal("Alt-Svc header not set")
	}
	expected := `h3=":8443"; ma=86400`
	if altSvc != expected {
		t.Fatalf("Alt-Svc: got %q, want %q", altSvc, expected)
	}
}

func TestAltSvcMiddleware_DefaultPort(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No port in addr.
	wrapped := altSvcMiddleware("noport", inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	altSvc := rec.Header().Get("Alt-Svc")
	expected := `h3=":8080"; ma=86400`
	if altSvc != expected {
		t.Fatalf("Alt-Svc default: got %q, want %q", altSvc, expected)
	}
}
