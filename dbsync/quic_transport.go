package dbsync

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/quic-go/quic-go"
)

// quicConfig returns the QUIC config used for dbsync transfers.
func quicConfig() *quic.Config {
	return &quic.Config{
		MaxStreamReceiveWindow:     MaxSnapshotSize,
		MaxConnectionReceiveWindow: MaxSnapshotSize,
		MaxIdleTimeout:             5 * time.Minute,
		KeepAlivePeriod:            30 * time.Second,
		Allow0RTT:                  false,
	}
}

// SyncTLSConfig builds a TLS config with the dbsync ALPN protocol.
// The cert is added to RootCAs so that self-signed certs are trusted
// when this config is used as a client (publisher dialing subscriber).
func SyncTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("read cert for root pool: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		NextProtos:   []string{ALPNProtocol},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// SyncClientTLSConfig returns a TLS config for connecting to a dbsync endpoint.
//
// WARNING: when insecureSkipVerify is true, the client accepts any server
// certificate. This is only appropriate for local development and testing.
// In production, use SyncClientTLSConfigWithCA to pin the CA certificate.
func SyncClientTLSConfig(insecureSkipVerify bool) *tls.Config {
	return &tls.Config{
		NextProtos:         []string{ALPNProtocol},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: insecureSkipVerify,
	}
}

// SyncClientTLSConfigWithCA returns a TLS config that trusts the given CA
// certificate file. Use this in production when the dbsync server uses a
// self-signed or internal CA certificate.
func SyncClientTLSConfigWithCA(caCertFile string) (*tls.Config, error) {
	caPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA cert from %s", caCertFile)
	}
	return &tls.Config{
		RootCAs:    pool,
		NextProtos: []string{ALPNProtocol},
		MinVersion: tls.VersionTLS13,
	}, nil
}

// SyncTLSConfigMutual builds a TLS config for the subscriber (listener) that
// requires the publisher to present a valid client certificate signed by the
// given CA. This prevents rogue clients from pushing snapshots.
//
// certFile/keyFile: the subscriber's own certificate.
// caCertFile: the CA certificate that signed the publisher's certificate.
func SyncTLSConfigMutual(certFile, keyFile, caCertFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load keypair: %w", err)
	}

	caPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA cert from %s", caCertFile)
	}

	// Also trust the CA for outbound (if this config is reused as client).
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caPool,
		RootCAs:      caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{ALPNProtocol},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// PushSnapshot sends a snapshot file to a remote subscriber over QUIC.
//
// Wire format:
//  1. Magic bytes "SYN1" (4 bytes)
//  2. Meta length (4 bytes big-endian uint32)
//  3. Meta JSON (variable)
//  4. Snapshot bytes (gzip-compressed if meta.Compressed, raw otherwise)
func PushSnapshot(ctx context.Context, endpoint string, tlsCfg *tls.Config, meta SnapshotMeta, snapshotPath string) error {
	conn, err := quic.DialAddr(ctx, endpoint, tlsCfg, quicConfig())
	if err != nil {
		return fmt.Errorf("dbsync push: dial %s: %w", endpoint, err)
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(1, "open stream failed")
		return fmt.Errorf("dbsync push: open stream: %w", err)
	}

	// Magic bytes.
	if _, err := stream.Write([]byte(MagicBytes)); err != nil {
		conn.CloseWithError(1, "write failed")
		return fmt.Errorf("dbsync push: magic: %w", err)
	}

	// Meta JSON.
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		conn.CloseWithError(1, "marshal failed")
		return fmt.Errorf("dbsync push: marshal meta: %w", err)
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(metaJSON)))
	if _, err := stream.Write(lenBuf[:]); err != nil {
		conn.CloseWithError(1, "write failed")
		return fmt.Errorf("dbsync push: meta len: %w", err)
	}
	if _, err := stream.Write(metaJSON); err != nil {
		conn.CloseWithError(1, "write failed")
		return fmt.Errorf("dbsync push: meta: %w", err)
	}

	// Snapshot data.
	f, err := os.Open(snapshotPath)
	if err != nil {
		conn.CloseWithError(1, "open snapshot failed")
		return fmt.Errorf("dbsync push: open snapshot: %w", err)
	}
	defer f.Close()

	if meta.Compressed {
		// Gzip directly into the QUIC stream.
		gw := gzip.NewWriter(stream)
		if _, err := io.Copy(gw, f); err != nil {
			conn.CloseWithError(1, "write failed")
			return fmt.Errorf("dbsync push: gzip copy: %w", err)
		}
		if err := gw.Close(); err != nil {
			conn.CloseWithError(1, "gzip close failed")
			return fmt.Errorf("dbsync push: gzip close: %w", err)
		}
	} else {
		n, err := io.Copy(stream, f)
		if err != nil {
			conn.CloseWithError(1, "write failed")
			return fmt.Errorf("dbsync push: copy data: %w", err)
		}
		if n != meta.Size {
			conn.CloseWithError(1, "size mismatch")
			return fmt.Errorf("dbsync push: size mismatch: sent %d, expected %d", n, meta.Size)
		}
	}

	// Close stream (sends FIN) before closing connection.
	// QUIC CONNECTION_CLOSE is immediate and would abort pending stream reads
	// on the peer. Give the peer time to finish reading after the stream FIN.
	stream.Close()
	time.Sleep(200 * time.Millisecond)
	conn.CloseWithError(0, "done")
	return nil
}

// ListenSnapshots accepts incoming QUIC connections carrying snapshots.
// For each connection it reads the wire format and calls handler with the
// parsed metadata and a reader for the raw database bytes.
// Blocks until ctx is cancelled.
func ListenSnapshots(ctx context.Context, addr string, tlsCfg *tls.Config, handler func(SnapshotMeta, io.Reader) error) error {
	listener, err := quic.ListenAddr(addr, tlsCfg, quicConfig())
	if err != nil {
		return fmt.Errorf("dbsync listen: %w", err)
	}
	defer listener.Close()

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		conn, err := listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("dbsync listen: accept: %w", err)
		}

		go func() {
			defer conn.CloseWithError(0, "done")
			if err := handleIncoming(ctx, conn, handler); err != nil {
				slog.Error("dbsync listen: handler error", "error", err, "remote", conn.RemoteAddr())
				conn.CloseWithError(1, err.Error())
			}
		}()
	}
}

// handleIncoming reads the wire format from a single QUIC connection.
func handleIncoming(ctx context.Context, conn *quic.Conn, handler func(SnapshotMeta, io.Reader) error) error {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		return fmt.Errorf("accept stream: %w", err)
	}
	defer stream.Close()

	// Read magic bytes.
	magic := make([]byte, len(MagicBytes))
	if _, err := io.ReadFull(stream, magic); err != nil {
		return fmt.Errorf("read magic: %w", err)
	}
	if string(magic) != MagicBytes {
		return fmt.Errorf("invalid magic: %q", magic)
	}

	// Read meta length.
	var lenBuf [4]byte
	if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
		return fmt.Errorf("read meta len: %w", err)
	}
	metaLen := binary.BigEndian.Uint32(lenBuf[:])
	if metaLen > 1024*1024 { // 1MB safety limit for meta
		return fmt.Errorf("meta too large: %d bytes", metaLen)
	}

	// Read meta JSON.
	metaBuf := make([]byte, metaLen)
	if _, err := io.ReadFull(stream, metaBuf); err != nil {
		return fmt.Errorf("read meta: %w", err)
	}

	var meta SnapshotMeta
	if err := json.Unmarshal(metaBuf, &meta); err != nil {
		return fmt.Errorf("unmarshal meta: %w", err)
	}

	if meta.Size > MaxSnapshotSize {
		return fmt.Errorf("snapshot too large: %d bytes", meta.Size)
	}

	// Build the appropriate reader based on compression.
	var reader io.Reader
	if meta.Compressed {
		gr, err := gzip.NewReader(stream)
		if err != nil {
			return fmt.Errorf("gzip reader: %w", err)
		}
		defer gr.Close()
		reader = io.LimitReader(gr, meta.Size)
	} else {
		reader = io.LimitReader(stream, meta.Size)
	}

	return handler(meta, reader)
}
