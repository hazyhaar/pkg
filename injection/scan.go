// CLAUDE:SUMMARY Scan d'injection 3 couches : exact, fuzzy, base64 — zero regex, zero ReDoS.
// CLAUDE:DEPENDS injection/normalize.go, injection/fuzzy.go, injection/base64.go
// CLAUDE:EXPORTS Scan, Intent, Result, Match, LoadIntents, DefaultIntents
package injection

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
	"unicode"
)

//go:embed intents.json
var intentsJSON []byte

// Intent represents a canonical prompt injection pattern.
type Intent struct {
	ID        string `json:"id"`
	Canonical string `json:"canonical"` // already normalized (lowercase, no accents, no punctuation)
	Category  string `json:"category"`
	Lang      string `json:"lang"`
	Severity  string `json:"severity"` // "high", "medium", "low"
}

// Result holds the outcome of an injection scan.
type Result struct {
	Risk    string  `json:"risk"`               // "none", "medium", "high"
	Matches []Match `json:"matches,omitempty"`
}

// Match describes a single detected injection pattern.
type Match struct {
	IntentID string `json:"intent_id"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Method   string `json:"method"` // "exact", "fuzzy", "base64", "structural"
}

var (
	defaultIntentsOnce sync.Once
	defaultIntents     []Intent
)

// DefaultIntents returns the embedded intent list, loaded once.
func DefaultIntents() []Intent {
	defaultIntentsOnce.Do(func() {
		var err error
		defaultIntents, err = LoadIntents(intentsJSON)
		if err != nil {
			panic("injection: bad embedded intents.json: " + err.Error())
		}
	})
	return defaultIntents
}

// LoadIntents parses a JSON intent list from external data (for reload/feed).
func LoadIntents(data []byte) ([]Intent, error) {
	var intents []Intent
	if err := json.Unmarshal(data, &intents); err != nil {
		return nil, err
	}
	return intents, nil
}

// Scan runs the full injection detection pipeline on text:
// 1. Structural detection (zero-width clusters, homoglyph mixing) on original text
// 2. Normalize text
// 3. Exact matching (strings.Contains) against all intents
// 4. Fuzzy matching (Levenshtein) for unmatched intents
// 5. Base64 decoding and re-scan of decoded segments
//
// Scan is designed to be called on both inputs AND outputs of LLM agents.
func Scan(text string, intents []Intent) *Result {
	result := &Result{Risk: "none"}
	matched := make(map[string]bool) // intent IDs already matched

	// === 1. Structural detection on original text ===
	if hasZeroWidthCluster(text) {
		result.Matches = append(result.Matches, Match{
			IntentID: "structural.steganography",
			Category: "steganography",
			Severity: "high",
			Method:   "structural",
		})
	}
	if HasHomoglyphMixing(text) {
		result.Matches = append(result.Matches, Match{
			IntentID: "structural.homoglyph",
			Category: "homoglyph",
			Severity: "medium",
			Method:   "structural",
		})
	}
	if hasSuspiciousDelimiters(text) {
		result.Matches = append(result.Matches, Match{
			IntentID: "structural.delimiter",
			Category: "delimiter",
			Severity: "high",
			Method:   "structural",
		})
	}
	if hasDangerousMarkup(text) {
		result.Matches = append(result.Matches, Match{
			IntentID: "structural.dangerous_markup",
			Category: "rendering",
			Severity: "high",
			Method:   "structural",
		})
	}

	// === 2. Normalize ===
	normalized := Normalize(text)

	// === 3. Exact matching ===
	for _, intent := range intents {
		if strings.Contains(normalized, intent.Canonical) {
			result.Matches = append(result.Matches, Match{
				IntentID: intent.ID,
				Category: intent.Category,
				Severity: intent.Severity,
				Method:   "exact",
			})
			matched[intent.ID] = true
		}
	}

	// === 4. Fuzzy matching (only unmatched intents with multi-word canonicals) ===
	for _, intent := range intents {
		if matched[intent.ID] {
			continue
		}
		// Fuzzy only makes sense for multi-word phrases
		if len(strings.Fields(intent.Canonical)) < 2 {
			continue
		}
		if FuzzyContains(normalized, intent.Canonical, 2) {
			result.Matches = append(result.Matches, Match{
				IntentID: intent.ID,
				Category: intent.Category,
				Severity: intent.Severity,
				Method:   "fuzzy",
			})
			matched[intent.ID] = true
		}
	}

	// === 5. Zero-width word boundary alternative ===
	// When invisible chars are used as word separators (instead of spaces),
	// StripInvisible merges words. Re-scan with invisibles→spaces to catch this.
	if containsInvisible(text) {
		spaced := replaceInvisibleWithSpace(text)
		spacedNorm := Normalize(spaced)
		if spacedNorm != normalized {
			for _, intent := range intents {
				if matched[intent.ID] {
					continue
				}
				if strings.Contains(spacedNorm, intent.Canonical) {
					result.Matches = append(result.Matches, Match{
						IntentID: intent.ID,
						Category: intent.Category,
						Severity: intent.Severity,
						Method:   "exact",
					})
					matched[intent.ID] = true
					continue
				}
				if len(strings.Fields(intent.Canonical)) >= 2 {
					if FuzzyContains(spacedNorm, intent.Canonical, 2) {
						result.Matches = append(result.Matches, Match{
							IntentID: intent.ID,
							Category: intent.Category,
							Severity: intent.Severity,
							Method:   "fuzzy",
						})
						matched[intent.ID] = true
					}
				}
			}
		}
	}

	// === 6. Base64 smuggling detection ===
	decoded := DecodeBase64Segments(text)
	if decoded != text {
		decodedNorm := Normalize(decoded)
		for _, intent := range intents {
			if matched[intent.ID] {
				continue
			}
			if strings.Contains(decodedNorm, intent.Canonical) {
				result.Matches = append(result.Matches, Match{
					IntentID: intent.ID,
					Category: intent.Category,
					Severity: intent.Severity,
					Method:   "base64",
				})
				matched[intent.ID] = true
				continue
			}
			if len(strings.Fields(intent.Canonical)) >= 2 {
				if FuzzyContains(decodedNorm, intent.Canonical, 2) {
					result.Matches = append(result.Matches, Match{
						IntentID: intent.ID,
						Category: intent.Category,
						Severity: intent.Severity,
						Method:   "base64",
					})
					matched[intent.ID] = true
				}
			}
		}
	}

	// === 7. Scoring ===
	score := 0
	for _, m := range result.Matches {
		score += severityScore(m.Severity)
	}
	switch {
	case score >= 5:
		result.Risk = "high"
	case score >= 1:
		result.Risk = "medium"
	}

	return result
}

// containsInvisible reports whether s contains any character that StripInvisible would remove.
func containsInvisible(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			return true
		}
		if unicode.Is(unicode.Cc, r) && r != '\n' && r != '\t' && r != '\r' {
			return true
		}
	}
	return false
}

// replaceInvisibleWithSpace replaces invisible characters with spaces
// (instead of deleting them) to preserve word boundaries.
func replaceInvisibleWithSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return ' '
		}
		if unicode.Is(unicode.Cc, r) && r != '\n' && r != '\t' && r != '\r' {
			return ' '
		}
		return r
	}, s)
}

func severityScore(s string) int {
	switch s {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

// hasSuspiciousDelimiters detects LLM instruction boundary injection patterns
// like <|system|>, <<SYS>>, [INST] that attackers use to manipulate model behavior.
func hasSuspiciousDelimiters(s string) bool {
	lower := strings.ToLower(s)
	delimPatterns := []string{
		"<|system|>", "<|user|>", "<|assistant|>",
		"<|im_start|>", "<|im_end|>",
		"<|endoftext|>", "<|begin_of_text|>",
		"<|start_header_id|>", "<|end_header_id|>",
		"<<sys>>", "<</sys>>",
		"[inst]", "[/inst]",
	}
	for _, p := range delimPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// hasDangerousMarkup detects HTML/JS patterns that indicate rendering attacks.
// Checked before StripMarkup to catch <script>, javascript:, onerror= etc.
// Strips control characters first to prevent null-byte bypass (e.g., "<scr\x00ipt>").
func hasDangerousMarkup(s string) bool {
	// Strip Cc characters (null bytes, etc.) to prevent pattern-splitting bypass.
	cleaned := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) {
			return -1
		}
		return r
	}, s)
	lower := strings.ToLower(cleaned)
	dangerousPatterns := []string{
		"<script", "javascript:", "onerror=", "onload=",
		"onclick=", "onmouseover=", "<iframe", "<object",
		"<embed", "<svg/onload", "<img/onerror",
	}
	for _, p := range dangerousPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// hasZeroWidthCluster detects 3+ consecutive zero-width characters in text.
func hasZeroWidthCluster(s string) bool {
	count := 0
	for _, r := range s {
		if isZeroWidth(r) {
			count++
			if count >= 3 {
				return true
			}
		} else {
			count = 0
		}
	}
	return false
}

func isZeroWidth(r rune) bool {
	switch r {
	case '\u200B', '\u200C', '\u200D', '\u2060', '\uFEFF':
		return true
	}
	return false
}

// HasHomoglyphMixing detects mixed Latin/Cyrillic or Latin/Greek in single words (visual obfuscation).
func HasHomoglyphMixing(text string) bool {
	for _, word := range strings.Fields(text) {
		hasLatin, hasCyrillic, hasGreek := false, false, false
		for _, r := range word {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				hasLatin = true
			}
			if unicode.Is(unicode.Cyrillic, r) {
				hasCyrillic = true
			}
			if unicode.Is(unicode.Greek, r) {
				hasGreek = true
			}
			if hasLatin && (hasCyrillic || hasGreek) {
				return true
			}
		}
	}
	return false
}
