package mcpquic

import (
	"bytes"
	"fmt"
	"io"
)

// ValidateMagicBytes reads and validates "MCP1" from a reader.
// Defense-in-depth against ALPN confusion attacks.
func ValidateMagicBytes(r io.Reader) error {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return fmt.Errorf("failed to read magic bytes: %w", err)
	}
	if !bytes.Equal(magic, []byte(MagicBytesMCP)) {
		return fmt.Errorf("%w: got %q", ErrInvalidMagicBytes, string(magic))
	}
	return nil
}

// SendMagicBytes writes "MCP1" to a writer.
// Client MUST send magic bytes immediately after opening the QUIC stream.
func SendMagicBytes(w io.Writer) error {
	if _, err := w.Write([]byte(MagicBytesMCP)); err != nil {
		return fmt.Errorf("failed to write magic bytes: %w", err)
	}
	return nil
}
