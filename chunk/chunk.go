// CLAUDE:SUMMARY Splits text into overlapping chunks for RAG and FTS5 indexing with paragraph-aware boundaries.
// CLAUDE:EXPORTS Split, Options, Chunk, CountTokens, EstimateTokens

// Package chunk splits extracted text into overlapping chunks suitable for
// RAG (Retrieval-Augmented Generation) and full-text search indexing.
//
// Splitting strategy:
//  1. Split on paragraph boundaries first (double newline)
//  2. If paragraphs exceed max tokens, split on sentence boundaries
//  3. If sentences exceed max tokens, split on word boundaries
//  4. Apply configurable overlap between consecutive chunks
package chunk

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Options configures the chunking behaviour.
type Options struct {
	// MaxTokens is the maximum number of tokens per chunk. Default: 512.
	MaxTokens int
	// OverlapTokens is the number of tokens to overlap between chunks. Default: 64.
	OverlapTokens int
	// MinChunkTokens is the minimum chunk size; shorter chunks are merged. Default: 32.
	MinChunkTokens int
}

func (o *Options) defaults() {
	if o.MaxTokens <= 0 {
		o.MaxTokens = 512
	}
	if o.OverlapTokens <= 0 {
		o.OverlapTokens = 64
	}
	if o.MinChunkTokens <= 0 {
		o.MinChunkTokens = 32
	}
}

// Chunk is one text fragment with metadata.
type Chunk struct {
	Index       int    // 0-based position in the sequence
	Text        string // chunk text content
	TokenCount  int    // approximate token count
	OverlapPrev int    // how many tokens overlap with the previous chunk
}

// Split divides text into overlapping chunks.
func Split(text string, opts Options) []Chunk {
	opts.defaults()

	if text == "" {
		return nil
	}

	// Tokenize the full text.
	words := tokenize(text)
	if len(words) == 0 {
		return nil
	}

	// If the text fits in one chunk, return it as-is.
	if len(words) <= opts.MaxTokens {
		return []Chunk{{
			Index:       0,
			Text:        text,
			TokenCount:  len(words),
			OverlapPrev: 0,
		}}
	}

	// Try paragraph-aware splitting first.
	chunks := splitParagraphAware(text, words, opts)
	if len(chunks) > 0 {
		return chunks
	}

	// Fall back to simple sliding window.
	return slidingWindow(words, opts)
}

// splitParagraphAware tries to split on paragraph boundaries, keeping chunks
// under MaxTokens. Falls back to sliding window for oversized paragraphs.
func splitParagraphAware(text string, allWords []string, opts Options) []Chunk {
	paragraphs := splitOnDoubleLF(text)
	if len(paragraphs) <= 1 {
		return slidingWindow(allWords, opts)
	}

	var chunks []Chunk
	var current strings.Builder
	var currentTokens int

	flush := func(overlapPrev int) {
		t := strings.TrimSpace(current.String())
		if t == "" {
			return
		}
		tc := countTokens(t)
		if tc < opts.MinChunkTokens && len(chunks) > 0 {
			// Merge with previous chunk.
			prev := &chunks[len(chunks)-1]
			prev.Text += "\n\n" + t
			prev.TokenCount += tc
			return
		}
		chunks = append(chunks, Chunk{
			Index:       len(chunks),
			Text:        t,
			TokenCount:  tc,
			OverlapPrev: overlapPrev,
		})
	}

	for _, para := range paragraphs {
		paraTokens := countTokens(para)

		if paraTokens > opts.MaxTokens {
			// Flush current buffer then split the large paragraph.
			flush(0)
			current.Reset()
			currentTokens = 0

			paraWords := tokenize(para)
			subChunks := slidingWindow(paraWords, opts)
			for _, sc := range subChunks {
				sc.Index = len(chunks)
				chunks = append(chunks, sc)
			}
			continue
		}

		if currentTokens+paraTokens > opts.MaxTokens {
			flush(0)

			// Start new chunk with overlap from the end of the previous.
			overlap := extractOverlap(current.String(), opts.OverlapTokens)
			current.Reset()
			currentTokens = 0
			if overlap != "" {
				current.WriteString(overlap)
				currentTokens = countTokens(overlap)
			}
		}

		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(para)
		currentTokens += paraTokens
	}

	flush(0)

	// Recalculate overlap counts.
	for i := 1; i < len(chunks); i++ {
		chunks[i].OverlapPrev = computeOverlap(chunks[i-1].Text, chunks[i].Text)
	}

	return chunks
}

// slidingWindow splits words into overlapping chunks with a sliding window.
func slidingWindow(words []string, opts Options) []Chunk {
	var chunks []Chunk
	stride := opts.MaxTokens - opts.OverlapTokens
	if stride <= 0 {
		stride = opts.MaxTokens / 2
	}

	for start := 0; start < len(words); start += stride {
		end := start + opts.MaxTokens
		if end > len(words) {
			end = len(words)
		}

		text := strings.Join(words[start:end], " ")
		overlapPrev := 0
		if start > 0 {
			overlapPrev = opts.OverlapTokens
			if overlapPrev > start {
				overlapPrev = start
			}
		}

		tc := end - start
		if tc < opts.MinChunkTokens && len(chunks) > 0 {
			// Merge with previous.
			prev := &chunks[len(chunks)-1]
			prev.Text += " " + text
			prev.TokenCount += tc
			break
		}

		chunks = append(chunks, Chunk{
			Index:       len(chunks),
			Text:        text,
			TokenCount:  tc,
			OverlapPrev: overlapPrev,
		})

		if end >= len(words) {
			break
		}
	}

	return chunks
}

// tokenize splits text into word-level tokens (simple whitespace split).
func tokenize(text string) []string {
	return strings.Fields(text)
}

// countTokens approximates token count (word count).
func countTokens(text string) int {
	return len(strings.Fields(text))
}

// CountTokens is the exported version for use outside the package.
func CountTokens(text string) int {
	return countTokens(text)
}

// splitOnDoubleLF splits text on double newlines (paragraph boundaries).
func splitOnDoubleLF(text string) []string {
	var parts []string
	for _, p := range strings.Split(text, "\n\n") {
		p = strings.TrimSpace(p)
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}

// extractOverlap extracts the last N tokens from text.
func extractOverlap(text string, n int) string {
	words := tokenize(text)
	if len(words) <= n {
		return text
	}
	return strings.Join(words[len(words)-n:], " ")
}

// computeOverlap counts how many leading words of b match trailing words of a.
func computeOverlap(a, b string) int {
	wordsA := tokenize(a)
	wordsB := tokenize(b)
	maxOverlap := len(wordsA)
	if len(wordsB) < maxOverlap {
		maxOverlap = len(wordsB)
	}

	for n := maxOverlap; n > 0; n-- {
		match := true
		for i := 0; i < n; i++ {
			if wordsA[len(wordsA)-n+i] != wordsB[i] {
				match = false
				break
			}
		}
		if match {
			return n
		}
	}
	return 0
}

// EstimateTokens estimates GPT-style token count from text.
// Rough heuristic: ~0.75 words per token for English, ~4 chars per token.
func EstimateTokens(text string) int {
	// Use char count / 4 as a rough estimate closer to BPE tokenization.
	n := utf8.RuneCountInString(text)
	words := 0
	inWord := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			inWord = false
		} else if !inWord {
			inWord = true
			words++
		}
	}
	// Take the average of char-based and word-based estimates.
	charEst := n / 4
	wordEst := words * 4 / 3 // ~1.33 tokens per word
	return (charEst + wordEst) / 2
}
