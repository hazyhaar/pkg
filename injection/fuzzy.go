// CLAUDE:SUMMARY Fuzzy string matching par Levenshtein pour résistance à la typoglycémie.
// CLAUDE:EXPORTS FuzzyContains
package injection

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// FuzzyContains checks if text contains a fuzzy match for phrase using
// Levenshtein distance per word with a sliding window approach.
// Returns true only if every word in phrase matches within maxEditPerWord edits
// AND the total distance is > 0 (exact matches are handled by strings.Contains).
func FuzzyContains(text string, phrase string, maxEditPerWord int) bool {
	words := strings.Fields(text)
	patternWords := strings.Fields(phrase)
	if len(patternWords) == 0 {
		return false
	}

	for i := 0; i <= len(words)-len(patternWords); i++ {
		totalDist := 0
		match := true
		for j, pw := range patternWords {
			d := levenshtein(words[i+j], pw)
			if d > maxEditPerWord {
				match = false
				break
			}
			totalDist += d
		}
		if match && totalDist > 0 {
			return true
		}
	}
	return false
}

// levenshtein computes the edit distance between two strings.
// Space-optimized: O(min(n,m)) space using two-row sliding window.
func levenshtein(a, b string) int {
	la := utf8.RuneCountInString(a)
	lb := utf8.RuneCountInString(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	ra := []rune(a)
	rb := []rune(b)

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// ReorderMatch checks if text contains a window of words that, when sorted
// alphabetically, match the sorted words of phrase. Catches word-reordered
// injections like "instructions previous ignore all".
// Only returns true when words are actually reordered (not in original order).
func ReorderMatch(text string, phrase string) bool {
	words := strings.Fields(text)
	patternWords := strings.Fields(phrase)
	pLen := len(patternWords)
	if pLen < 2 || len(words) < pLen {
		return false
	}

	sortedPattern := make([]string, pLen)
	copy(sortedPattern, patternWords)
	sort.Strings(sortedPattern)
	target := strings.Join(sortedPattern, "\x00")

	window := make([]string, pLen)
	for i := 0; i <= len(words)-pLen; i++ {
		copy(window, words[i:i+pLen])

		// Skip if already in correct order (caught by exact/fuzzy)
		inOrder := true
		for j := range window {
			if window[j] != patternWords[j] {
				inOrder = false
				break
			}
		}
		if inOrder {
			continue
		}

		sort.Strings(window)
		if strings.Join(window, "\x00") == target {
			return true
		}
	}
	return false
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
