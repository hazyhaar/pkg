package horos

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Wire envelope format:
//
//	+----------+----------+-------------------+
//	| format_id| checksum |     payload       |
//	| 2 bytes  | 4 bytes  |   N bytes         |
//	| uint16 LE| CRC-32C  |                   |
//	+----------+----------+-------------------+
//
// Total overhead: 6 bytes.
//
// - format_id: identifies the codec (0=raw/passthrough, 1=JSON, 2=msgpack, etc.)
// - checksum: CRC-32C (Castagnoli) of the payload, for integrity checking
// - payload: the encoded request or response
//
// A format_id of 0 means "raw passthrough" — the payload bytes are opaque to
// the type system (no codec applied). The envelope header is still present
// for unambiguous parsing.

const (
	// HeaderSize is the fixed overhead of the wire envelope.
	HeaderSize = 6 // 2 (format_id) + 4 (checksum)

	// FormatRaw is passthrough — no codec, payload is used as-is.
	FormatRaw uint16 = 0

	// FormatJSON is the JSON codec format ID.
	FormatJSON uint16 = 1
)

// crc32c is the Castagnoli CRC-32 table, chosen for its hardware acceleration
// on modern CPUs (SSE 4.2 / ARM CRC32).
var crc32c = crc32.MakeTable(crc32.Castagnoli)

// Wrap creates a wire envelope from a format ID and payload.
// The envelope is always prepended, even for FormatRaw, so that Unwrap
// can unambiguously detect the format on the receiving end.
func Wrap(formatID uint16, payload []byte) ([]byte, error) {
	buf := make([]byte, HeaderSize+len(payload))
	binary.LittleEndian.PutUint16(buf[0:2], formatID)
	checksum := crc32.Checksum(payload, crc32c)
	binary.LittleEndian.PutUint32(buf[2:6], checksum)
	copy(buf[HeaderSize:], payload)
	return buf, nil
}

// Unwrap extracts the format ID and payload from a wire envelope, verifying
// the checksum. If the data has no envelope header (too short or format_id=0),
// it is returned as raw payload with FormatRaw.
func Unwrap(data []byte) (formatID uint16, payload []byte, err error) {
	if len(data) < HeaderSize {
		// Too short for an envelope — treat as raw.
		return FormatRaw, data, nil
	}

	formatID = binary.LittleEndian.Uint16(data[0:2])
	expected := binary.LittleEndian.Uint32(data[2:6])
	payload = data[HeaderSize:]
	actual := crc32.Checksum(payload, crc32c)

	if actual != expected {
		if formatID == FormatRaw {
			// Checksum mismatch with format_id=0: this is non-enveloped data
			// whose first bytes happen to be 0x0000. Return the full original
			// data untouched rather than silently stripping 6 bytes.
			return FormatRaw, data, nil
		}
		return 0, nil, &ErrChecksum{
			Expected: expected,
			Actual:   actual,
			FormatID: formatID,
		}
	}

	return formatID, payload, nil
}

// ErrChecksum is returned when the wire envelope checksum doesn't match.
type ErrChecksum struct {
	Expected uint32
	Actual   uint32
	FormatID uint16
}

func (e *ErrChecksum) Error() string {
	return fmt.Sprintf("horos: checksum mismatch (format=%d, expected=%d, actual=%d)",
		e.FormatID, e.Expected, e.Actual)
}

// ErrUnsupportedFormat is returned when a format ID has no registered codec.
type ErrUnsupportedFormat struct {
	FormatID uint16
}

func (e *ErrUnsupportedFormat) Error() string {
	return fmt.Sprintf("horos: unsupported format: %d", e.FormatID)
}

// IsChecksumError returns true if the error is a checksum mismatch.
func IsChecksumError(err error) bool {
	var ce *ErrChecksum
	return errors.As(err, &ce)
}
