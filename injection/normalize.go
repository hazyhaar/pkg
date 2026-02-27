// CLAUDE:SUMMARY Normalisation Unicode multi-couche pour détection d'injection — NFKD, confusables, leet, invisible strip, markup strip.
// CLAUDE:DEPENDS golang.org/x/text/unicode/norm
// CLAUDE:EXPORTS Normalize, StripInvisible, StripMarkup, FoldConfusables, FoldLeet
package injection

import (
	_ "embed"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

//go:embed confusables.json
var confusablesJSON []byte

//go:embed leet.json
var leetJSON []byte

var confusableMap map[rune]rune
var leetMap map[rune]rune

func init() {
	confusableMap = loadRuneMap(confusablesJSON)
	leetMap = loadRuneMap(leetJSON)
}

func loadRuneMap(data []byte) map[rune]rune {
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		panic("injection: bad embedded JSON: " + err.Error())
	}
	m := make(map[rune]rune, len(raw))
	for k, v := range raw {
		kr, _ := utf8.DecodeRuneInString(k)
		vr, _ := utf8.DecodeRuneInString(v)
		if kr != utf8.RuneError && vr != utf8.RuneError {
			m[kr] = vr
		}
	}
	return m
}

// Normalize applies the full normalization pipeline to text:
// strip invisible → strip markup → NFKD → strip combining marks → fold confusables → fold leet → lower → collapse whitespace.
func Normalize(s string) string {
	s = StripInvisible(s)
	s = StripMarkup(s)
	s = norm.NFKD.String(s)
	s = stripCombiningMarks(s)
	s = FoldConfusables(s)
	s = FoldLeet(s)
	s = strings.ToLower(s)
	s = stripPunctuation(s)
	s = collapseWhitespace(s)
	return s
}

// StripInvisible removes all Unicode format (Cf) and control (Cc) characters
// except newline, tab, and carriage return.
func StripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		if unicode.Is(unicode.Cc, r) && r != '\n' && r != '\t' && r != '\r' {
			return -1
		}
		return r
	}, s)
}

// StripMarkup removes HTML/XML tags, Markdown formatting, and LaTeX commands,
// preserving the text content.
func StripMarkup(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	i := 0
	runes := []rune(s)
	n := len(runes)

	for i < n {
		r := runes[i]

		// HTML/XML tags: <...> → removed
		if r == '<' {
			j := i + 1
			for j < n && runes[j] != '>' {
				j++
			}
			if j < n {
				i = j + 1
				continue
			}
		}

		// Markdown fences: ``` lines → removed
		if r == '`' && i+2 < n && runes[i+1] == '`' && runes[i+2] == '`' {
			j := i + 3
			// skip to end of line
			for j < n && runes[j] != '\n' {
				j++
			}
			i = j
			continue
		}

		// Markdown inline code: `text` → keep text
		if r == '`' {
			i++
			continue
		}

		// Markdown bold/italic: ** __ ~~ → removed
		if (r == '*' || r == '_' || r == '~') && i+1 < n && runes[i+1] == r {
			i += 2
			continue
		}
		// Single * or _ (italic)
		if (r == '*' || r == '_') && i+1 < n && runes[i+1] != ' ' {
			// Check if this looks like formatting (not multiplication etc)
			// Only strip if preceded by space/start or followed by non-space
			if i == 0 || runes[i-1] == ' ' || runes[i-1] == '\n' {
				i++
				continue
			}
			// Closing marker
			if i+1 <= n && (i+1 == n || runes[i+1] == ' ' || runes[i+1] == '\n' || runes[i+1] == '.') {
				i++
				continue
			}
		}

		// Markdown headings: ^#{1,6}\s → removed (keep text)
		if r == '#' && (i == 0 || runes[i-1] == '\n') {
			j := i
			for j < n && runes[j] == '#' {
				j++
			}
			if j < n && runes[j] == ' ' {
				i = j + 1
				continue
			}
		}

		// Markdown links: [text](url) → keep text
		if r == '[' {
			// Find closing ]
			j := i + 1
			for j < n && runes[j] != ']' && runes[j] != '\n' {
				j++
			}
			if j < n && runes[j] == ']' && j+1 < n && runes[j+1] == '(' {
				// Write the link text
				for k := i + 1; k < j; k++ {
					b.WriteRune(runes[k])
				}
				// Skip past (url)
				k := j + 2
				for k < n && runes[k] != ')' {
					k++
				}
				if k < n {
					i = k + 1
				} else {
					i = k
				}
				continue
			}
		}

		// LaTeX commands: \command{content} → keep content
		if r == '\\' && i+1 < n && unicode.IsLetter(runes[i+1]) {
			j := i + 1
			for j < n && unicode.IsLetter(runes[j]) {
				j++
			}
			if j < n && runes[j] == '{' {
				// Skip \command{, keep content until }
				i = j + 1
				continue
			}
			// \command without braces — skip command name
			i = j
			continue
		}

		// Closing brace from LaTeX → skip
		if r == '}' {
			i++
			continue
		}

		b.WriteRune(r)
		i++
	}

	return b.String()
}

// stripCombiningMarks removes combining marks (accents, diacritics) after NFKD decomposition.
func stripCombiningMarks(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s)
}

// FoldConfusables maps homoglyph characters (Cyrillic/Greek/IPA) to their ASCII equivalents.
func FoldConfusables(s string) string {
	return strings.Map(func(r rune) rune {
		if mapped, ok := confusableMap[r]; ok {
			return mapped
		}
		return r
	}, s)
}

// FoldLeet maps leet speak characters to their ASCII letter equivalents.
func FoldLeet(s string) string {
	return strings.Map(func(r rune) rune {
		if mapped, ok := leetMap[r]; ok {
			return mapped
		}
		return r
	}, s)
}

// stripPunctuation removes all Unicode punctuation characters.
func stripPunctuation(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, s)
}

// collapseWhitespace replaces all consecutive whitespace with a single space and trims.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // trim leading
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	result := b.String()
	return strings.TrimRight(result, " ")
}
