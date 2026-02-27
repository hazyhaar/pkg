package injection

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// DecodeROT13 applies ROT13 rotation to all ASCII letters.
func DecodeROT13(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return 'A' + (r-'A'+13)%26
		case r >= 'a' && r <= 'z':
			return 'a' + (r-'a'+13)%26
		default:
			return r
		}
	}, s)
}

// DecodeEscapes decodes common encoding escapes in text:
//   - \xHH (C-style hex escape)
//   - %HH (URL percent-encoding)
//   - &#DDD; (HTML decimal entity)
//   - &#xHH; (HTML hex entity)
func DecodeEscapes(s string) string {
	if !containsEscapePattern(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	i := 0
	for i < len(s) {
		// \xHH — C-style hex escape
		if i+3 < len(s) && s[i] == '\\' && s[i+1] == 'x' {
			if v, ok := parseHexByte(s[i+2 : i+4]); ok {
				if r := rune(v); utf8.ValidRune(r) {
					b.WriteRune(r)
					i += 4
					continue
				}
			}
		}

		// %HH — URL percent-encoding
		if i+2 < len(s) && s[i] == '%' && isHexDigit(s[i+1]) && isHexDigit(s[i+2]) {
			if v, ok := parseHexByte(s[i+1 : i+3]); ok {
				if r := rune(v); utf8.ValidRune(r) {
					b.WriteRune(r)
					i += 3
					continue
				}
			}
		}

		// &#xHH; or &#DDD; — HTML numeric character reference
		if i+3 < len(s) && s[i] == '&' && s[i+1] == '#' {
			end := strings.IndexByte(s[i:], ';')
			if end > 2 && end <= 10 {
				inner := s[i+2 : i+end]
				var cp int64
				var err error
				if len(inner) > 0 && (inner[0] == 'x' || inner[0] == 'X') {
					cp, err = strconv.ParseInt(inner[1:], 16, 32)
				} else {
					cp, err = strconv.ParseInt(inner, 10, 32)
				}
				if err == nil && cp > 0 && cp <= 0x10FFFF && utf8.ValidRune(rune(cp)) {
					b.WriteRune(rune(cp))
					i += end + 1
					continue
				}
			}
		}

		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func containsEscapePattern(s string) bool {
	return strings.Contains(s, "\\x") ||
		strings.ContainsAny(s, "%") ||
		strings.Contains(s, "&#")
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func parseHexByte(s string) (byte, bool) {
	if len(s) < 2 {
		return 0, false
	}
	v, err := strconv.ParseUint(s[:2], 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(v), true
}
