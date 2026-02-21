package mcpquic

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// --- Magic bytes ---

func TestSendMagicBytes(t *testing.T) {
	var buf bytes.Buffer
	if err := SendMagicBytes(&buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != MagicBytesMCP {
		t.Fatalf("magic: got %q, want %q", buf.String(), MagicBytesMCP)
	}
}

func TestValidateMagicBytes_Valid(t *testing.T) {
	r := bytes.NewReader([]byte(MagicBytesMCP))
	if err := ValidateMagicBytes(r); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMagicBytes_Invalid(t *testing.T) {
	r := bytes.NewReader([]byte("HTTP"))
	err := ValidateMagicBytes(r)
	if err == nil {
		t.Fatal("expected error for invalid magic bytes")
	}
	if !errors.Is(err, ErrInvalidMagicBytes) {
		t.Fatalf("expected ErrInvalidMagicBytes, got: %v", err)
	}
}

func TestValidateMagicBytes_TooShort(t *testing.T) {
	r := bytes.NewReader([]byte("MC"))
	err := ValidateMagicBytes(r)
	if err == nil {
		t.Fatal("expected error for short input")
	}
}

func TestSendAndValidate_Roundtrip(t *testing.T) {
	var buf bytes.Buffer
	if err := SendMagicBytes(&buf); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMagicBytes(&buf); err != nil {
		t.Fatal(err)
	}
}

// --- Config ---

func TestProductionQUICConfig(t *testing.T) {
	cfg := ProductionQUICConfig()
	if cfg.MaxIdleTimeout != DefaultIdleTimeout {
		t.Fatalf("idle timeout: got %v", cfg.MaxIdleTimeout)
	}
	if cfg.KeepAlivePeriod != DefaultKeepAlive {
		t.Fatalf("keepalive: got %v", cfg.KeepAlivePeriod)
	}
	if cfg.Allow0RTT {
		t.Fatal("0-RTT should be disabled")
	}
}

func TestSelfSignedTLSConfig(t *testing.T) {
	cfg, err := SelfSignedTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("certs: got %d", len(cfg.Certificates))
	}
	if cfg.MinVersion != 0x0304 { // tls.VersionTLS13
		t.Fatalf("min version: got %x", cfg.MinVersion)
	}
	foundMCP := false
	for _, p := range cfg.NextProtos {
		if p == ALPNProtocolMCP {
			foundMCP = true
		}
	}
	if !foundMCP {
		t.Fatalf("ALPN: mcp protocol not found in %v", cfg.NextProtos)
	}
}

func TestClientTLSConfig_Insecure(t *testing.T) {
	cfg := ClientTLSConfig(true)
	if !cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=true")
	}
	if cfg.MinVersion != 0x0304 {
		t.Fatalf("min version: got %x", cfg.MinVersion)
	}
}

func TestClientTLSConfig_Secure(t *testing.T) {
	cfg := ClientTLSConfig(false)
	if cfg.InsecureSkipVerify {
		t.Fatal("expected InsecureSkipVerify=false")
	}
}

// --- Constants ---

func TestConstants(t *testing.T) {
	if ALPNProtocolMCP != "mcp-quic-v1" {
		t.Fatalf("ALPN: got %q", ALPNProtocolMCP)
	}
	if MagicBytesMCP != "MCP1" {
		t.Fatalf("magic: got %q", MagicBytesMCP)
	}
	if MaxMessageSize != 10*1024*1024 {
		t.Fatalf("max message: got %d", MaxMessageSize)
	}
}

// --- Errors ---

func TestConnectionError(t *testing.T) {
	inner := errors.New("timeout")
	ce := &ConnectionError{
		RemoteAddr: "127.0.0.1:8443",
		Code:       ConnErrorProtocolViolation,
		Err:        inner,
	}

	msg := ce.Error()
	if !strings.Contains(msg, "127.0.0.1:8443") {
		t.Fatalf("error missing remote addr: %s", msg)
	}
	if !strings.Contains(msg, "0x03") {
		t.Fatalf("error missing code: %s", msg)
	}

	if !errors.Is(ce, inner) {
		t.Fatal("Unwrap should return inner error")
	}
}

func TestSentinelErrors(t *testing.T) {
	if ErrInvalidMagicBytes == nil {
		t.Fatal("ErrInvalidMagicBytes should not be nil")
	}
	if ErrUnsupportedALPN == nil {
		t.Fatal("ErrUnsupportedALPN should not be nil")
	}
	if ErrConnectionClosed == nil {
		t.Fatal("ErrConnectionClosed should not be nil")
	}
}

// --- Client constructor ---

func TestNewClient_DefaultTLS(t *testing.T) {
	c := NewClient("localhost:8443", nil)
	if c.addr != "localhost:8443" {
		t.Fatalf("addr: got %q", c.addr)
	}
	if c.tlsCfg == nil {
		t.Fatal("TLS config should not be nil with default")
	}
	if c.tlsCfg.InsecureSkipVerify {
		t.Fatal("default TLS should be secure (verify server cert)")
	}
}

func TestNewClient_CustomTLS(t *testing.T) {
	cfg := ClientTLSConfig(false)
	c := NewClient("srv:9000", cfg)
	if c.tlsCfg != cfg {
		t.Fatal("custom TLS config not applied")
	}
}

func TestH3TLSConfig(t *testing.T) {
	base, err := SelfSignedTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	h3 := H3TLSConfig(base)
	if len(h3.NextProtos) != 1 || h3.NextProtos[0] != "h3" {
		t.Fatalf("ALPN: got %v, want [h3]", h3.NextProtos)
	}
	if h3.MinVersion != base.MinVersion {
		t.Fatal("MinVersion should be preserved from base")
	}
	if len(h3.Certificates) != len(base.Certificates) {
		t.Fatal("Certificates should be preserved from base")
	}
	// Verify base is not mutated
	if base.NextProtos[0] == "h3" {
		t.Fatal("base config should not be mutated")
	}
}

func TestClient_NotConnected(t *testing.T) {
	c := NewClient("localhost:1234", nil)

	if _, err := c.ListTools(nil); err == nil {
		t.Fatal("expected error when not connected")
	}
	if _, err := c.CallTool(nil, "test", nil); err == nil {
		t.Fatal("expected error when not connected")
	}
	if err := c.Ping(nil); err == nil {
		t.Fatal("expected error when not connected")
	}
}
