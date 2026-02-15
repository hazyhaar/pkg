package mcpquic

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

const (
	ALPNProtocolMCP         = "mcp-quic-v1"
	MagicBytesMCP           = "MCP1"
	MaxMessageSize          = 10 * 1024 * 1024 // 10MB
	DefaultHandshakeTimeout = 10 * time.Second
	DefaultIdleTimeout      = 5 * time.Minute
	DefaultKeepAlive        = 30 * time.Second
)

func ProductionQUICConfig() *quic.Config {
	return &quic.Config{
		MaxStreamReceiveWindow:     10 * 1024 * 1024,
		MaxConnectionReceiveWindow: 50 * 1024 * 1024,
		MaxIdleTimeout:             DefaultIdleTimeout,
		KeepAlivePeriod:            DefaultKeepAlive,
		Allow0RTT:                  false,
		EnableDatagrams:            false,
	}
}

func ServerTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNProtocolMCP},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func SelfSignedTLSConfig() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"HOROS Dev"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  key,
		}},
		NextProtos: []string{ALPNProtocolMCP},
		MinVersion: tls.VersionTLS13,
	}, nil
}

func ClientTLSConfig(insecure bool) *tls.Config {
	return &tls.Config{
		NextProtos:         []string{ALPNProtocolMCP},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: insecure,
	}
}

// H3TLSConfig clones a TLS config with ALPN set to "h3" for HTTP/3 serving.
func H3TLSConfig(base *tls.Config) *tls.Config {
	cfg := base.Clone()
	cfg.NextProtos = []string{"h3"}
	return cfg
}
