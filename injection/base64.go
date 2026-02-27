// CLAUDE:SUMMARY Détection et décodage de segments Base64 embarqués dans du texte brut (token smuggling).
// CLAUDE:EXPORTS DecodeBase64Segments
package injection

import (
	"encoding/base64"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DecodeBase64Segments scans text for base64-encoded tokens and decodes them in-place.
// Only tokens >= 16 characters that decode to valid, mostly-printable UTF-8 are replaced.
func DecodeBase64Segments(s string) string {
	tokens := strings.Fields(s)
	changed := false

	for i, tok := range tokens {
		if len(tok) < 16 {
			continue
		}
		if !isBase64Token(tok) {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(tok)
		if err != nil {
			// Try with padding
			padded := tok
			if rem := len(tok) % 4; rem != 0 {
				padded += strings.Repeat("=", 4-rem)
			}
			decoded, err = base64.StdEncoding.DecodeString(padded)
			if err != nil {
				continue
			}
		}
		if !utf8.Valid(decoded) {
			continue
		}
		if !isMostlyPrintable(decoded) {
			continue
		}
		tokens[i] = string(decoded)
		changed = true
	}

	if !changed {
		return s
	}
	return strings.Join(tokens, " ")
}

// isBase64Token checks if a string contains only base64 alphabet characters.
func isBase64Token(s string) bool {
	for _, r := range s {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=') {
			return false
		}
	}
	return true
}

// isMostlyPrintable checks that > 80% of bytes are printable Unicode.
func isMostlyPrintable(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	printable := 0
	total := 0
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		total++
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			printable++
		}
		i += size
	}
	return float64(printable)/float64(total) > 0.8
}
