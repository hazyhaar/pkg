package mcprt

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Sanitizer cleans tool descriptions and names before exposing them to an LLM
// via tools/list. Protects against prompt injection via tool metadata from
// downstream MCP servers.
type Sanitizer struct {
	maxDescLen       int
	stripHTML        bool
	stripInjection   bool
	normalizeUnicode bool
	customFilters    []func(string) string
}

// SanitizerOption configures a Sanitizer.
type SanitizerOption func(*Sanitizer)

// WithMaxDescriptionLength sets the maximum description length (default 1024).
func WithMaxDescriptionLength(n int) SanitizerOption {
	return func(s *Sanitizer) { s.maxDescLen = n }
}

// WithCustomFilter adds a custom string filter applied after built-in filters.
func WithCustomFilter(fn func(string) string) SanitizerOption {
	return func(s *Sanitizer) { s.customFilters = append(s.customFilters, fn) }
}

// DefaultSanitizer creates a Sanitizer with all protections enabled.
func DefaultSanitizer(opts ...SanitizerOption) *Sanitizer {
	s := &Sanitizer{
		maxDescLen:       1024,
		stripHTML:        true,
		stripInjection:   true,
		normalizeUnicode: true,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SanitizeName cleans a tool name: strips control characters and normalizes unicode.
func (s *Sanitizer) SanitizeName(name string) string {
	if s.normalizeUnicode {
		name = normalizeText(name)
	}
	name = stripControlChars(name)
	return name
}

// SanitizeDescription cleans a tool description applying all configured filters.
func (s *Sanitizer) SanitizeDescription(desc string) string {
	if s.normalizeUnicode {
		desc = normalizeText(desc)
	}
	desc = stripControlChars(desc)
	if s.stripHTML {
		desc = stripHTMLTags(desc)
	}
	if s.stripInjection {
		desc = stripInjectionPatterns(desc)
	}
	for _, fn := range s.customFilters {
		desc = fn(desc)
	}
	if s.maxDescLen > 0 && len(desc) > s.maxDescLen {
		desc = desc[:s.maxDescLen]
	}
	return strings.TrimSpace(desc)
}

// SanitizeTool applies sanitization to a DynamicTool's Name and Description in place.
func (s *Sanitizer) SanitizeTool(t *DynamicTool) {
	t.Name = s.SanitizeName(t.Name)
	t.Description = s.SanitizeDescription(t.Description)
}

// --- Filters ---

// htmlTagPattern matches HTML/XML tags.
var htmlTagPattern = regexp.MustCompile(`<[^>]+>`)

// stripHTMLTags removes all HTML/XML tags from the string.
func stripHTMLTags(s string) string {
	return htmlTagPattern.ReplaceAllString(s, "")
}

// injectionPatterns detects common prompt injection phrases.
// These are matched case-insensitively.
var injectionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bignore\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|context)\b`),
	regexp.MustCompile(`(?i)\bforget\s+(all\s+)?(previous|prior|above|earlier)\s+(instructions?|prompts?|context)\b`),
	regexp.MustCompile(`(?i)\bsystem\s+prompt\b`),
	regexp.MustCompile(`(?i)\byou\s+are\s+(now\s+)?(a|an)\b`),
	regexp.MustCompile(`(?i)\b(disregard|override)\s+(all\s+)?(previous|prior|above|earlier)\b`),
	regexp.MustCompile(`(?i)\bnew\s+instructions?\s*:`),
	regexp.MustCompile(`(?i)\bdo\s+not\s+follow\s+(previous|prior|earlier)\b`),
	regexp.MustCompile(`(?i)\bact\s+as\s+(a|an|if)\b`),
	regexp.MustCompile(`(?i)\bpretend\s+(you\s+are|to\s+be)\b`),
}

// stripInjectionPatterns removes known prompt injection phrases.
func stripInjectionPatterns(s string) string {
	for _, p := range injectionPatterns {
		s = p.ReplaceAllString(s, "")
	}
	// Clean up double spaces left by removals.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// normalizeText applies NFC normalization and strips zero-width characters.
func normalizeText(s string) string {
	s = norm.NFC.String(s)
	s = stripZeroWidth(s)
	return s
}

// zeroWidthChars are unicode characters with zero width commonly used for
// homoglyph attacks and invisible text injection.
var zeroWidthChars = []rune{
	'\u200B', // zero-width space
	'\u200C', // zero-width non-joiner
	'\u200D', // zero-width joiner
	'\u200E', // left-to-right mark
	'\u200F', // right-to-left mark
	'\u2060', // word joiner
	'\uFEFF', // zero-width no-break space (BOM)
}

func stripZeroWidth(s string) string {
	return strings.Map(func(r rune) rune {
		for _, zw := range zeroWidthChars {
			if r == zw {
				return -1
			}
		}
		return r
	}, s)
}

// stripControlChars removes ASCII control characters except newline and tab.
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

// WithSanitizer returns a BridgeOption that applies a Sanitizer to all tools
// before registering them with the MCP server.
func WithSanitizer(san *Sanitizer) BridgeOption {
	return func(c *bridgeConfig) { c.sanitizer = san }
}
