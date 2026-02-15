package mcpquic

import (
	"errors"
	"fmt"

	"github.com/quic-go/quic-go"
)

// QUIC stream-level error codes
const (
	StreamErrorNoError           quic.StreamErrorCode = 0x00
	StreamErrorProtocolConfusion quic.StreamErrorCode = 0x02
	StreamErrorMessageTooLarge   quic.StreamErrorCode = 0x03
)

// QUIC connection-level error codes
const (
	ConnErrorNoError           quic.ApplicationErrorCode = 0x00
	ConnErrorUnsupportedALPN   quic.ApplicationErrorCode = 0x01
	ConnErrorProtocolViolation quic.ApplicationErrorCode = 0x03
)

var (
	ErrInvalidMagicBytes = errors.New("invalid magic bytes: expected MCP1")
	ErrUnsupportedALPN   = errors.New("ALPN negotiation failed: mcp-quic-v1 not selected")
	ErrConnectionClosed  = errors.New("QUIC connection closed")
)

type ConnectionError struct {
	RemoteAddr string
	Code       quic.ApplicationErrorCode
	Err        error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection %s error code 0x%02x: %v", e.RemoteAddr, e.Code, e.Err)
}

func (e *ConnectionError) Unwrap() error { return e.Err }
