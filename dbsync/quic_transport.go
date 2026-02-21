package dbsync

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
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
func SyncTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{ALPNProtocol},
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// SyncClientTLSConfig returns a TLS config for connecting to a dbsync endpoint.
func SyncClientTLSConfig(insecure bool) *tls.Config {
	return &tls.Config{
		NextProtos:         []string{ALPNProtocol},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: insecure,
	}
}

// PushSnapshot sends a snapshot file to a remote subscriber over QUIC.
//
// Wire format:
//  1. Magic bytes "SYN1" (4 bytes)
//  2. Meta length (4 bytes big-endian uint32)
//  3. Meta JSON (variable)
//  4. Raw snapshot bytes (meta.Size bytes)
func PushSnapshot(ctx context.Context, endpoint string, tlsCfg *tls.Config, meta SnapshotMeta, snapshotPath string) error {
	conn, err := quic.DialAddr(ctx, endpoint, tlsCfg, quicConfig())
	if err != nil {
		return fmt.Errorf("dbsync push: dial %s: %w", endpoint, err)
	}
	defer conn.CloseWithError(0, "done")

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("dbsync push: open stream: %w", err)
	}
	defer stream.Close()

	// Magic bytes.
	if _, err := stream.Write([]byte(MagicBytes)); err != nil {
		return fmt.Errorf("dbsync push: magic: %w", err)
	}

	// Meta JSON.
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("dbsync push: marshal meta: %w", err)
	}

	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(metaJSON)))
	if _, err := stream.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("dbsync push: meta len: %w", err)
	}
	if _, err := stream.Write(metaJSON); err != nil {
		return fmt.Errorf("dbsync push: meta: %w", err)
	}

	// Snapshot data.
	f, err := os.Open(snapshotPath)
	if err != nil {
		return fmt.Errorf("dbsync push: open snapshot: %w", err)
	}
	defer f.Close()

	n, err := io.Copy(stream, f)
	if err != nil {
		return fmt.Errorf("dbsync push: copy data: %w", err)
	}
	if n != meta.Size {
		return fmt.Errorf("dbsync push: size mismatch: sent %d, expected %d", n, meta.Size)
	}

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

	// Pass limited reader to handler.
	reader := io.LimitReader(stream, meta.Size)
	return handler(meta, reader)
}
