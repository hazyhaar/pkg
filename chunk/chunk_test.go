package chunk

import (
	"strings"
	"testing"
)

func TestSplit_ShortText(t *testing.T) {
	text := "Hello world this is a short text."
	chunks := Split(text, Options{MaxTokens: 512})
	if len(chunks) != 1 {
		t.Fatalf("split short: got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Text != text {
		t.Errorf("text: got %q, want %q", chunks[0].Text, text)
	}
	if chunks[0].OverlapPrev != 0 {
		t.Errorf("overlap: got %d, want 0", chunks[0].OverlapPrev)
	}
}

func TestSplit_Empty(t *testing.T) {
	chunks := Split("", Options{})
	if chunks != nil {
		t.Errorf("split empty: got %v, want nil", chunks)
	}
}

func TestSplit_LongText(t *testing.T) {
	// Generate a text longer than MaxTokens.
	words := make([]string, 200)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")

	chunks := Split(text, Options{MaxTokens: 50, OverlapTokens: 10})
	if len(chunks) < 3 {
		t.Fatalf("split long: got %d chunks, want >= 3", len(chunks))
	}

	// Check each chunk doesn't exceed max tokens.
	for i, c := range chunks {
		if c.TokenCount > 50 {
			t.Errorf("chunk[%d]: %d tokens > 50 max", i, c.TokenCount)
		}
		if c.Index != i {
			t.Errorf("chunk[%d]: index=%d, want %d", i, c.Index, i)
		}
	}

	// First chunk should have no overlap.
	if chunks[0].OverlapPrev != 0 {
		t.Errorf("chunk[0]: overlap=%d, want 0", chunks[0].OverlapPrev)
	}
}

func TestSplit_ParagraphAware(t *testing.T) {
	para1 := strings.Repeat("alpha ", 30)
	para2 := strings.Repeat("beta ", 30)
	para3 := strings.Repeat("gamma ", 30)
	text := para1 + "\n\n" + para2 + "\n\n" + para3

	chunks := Split(text, Options{MaxTokens: 50, OverlapTokens: 5})
	if len(chunks) < 2 {
		t.Fatalf("paragraph split: got %d chunks, want >= 2", len(chunks))
	}

	// Verify first chunk contains alpha.
	if !strings.Contains(chunks[0].Text, "alpha") {
		t.Errorf("chunk[0] should contain 'alpha', got: %s", chunks[0].Text[:min(len(chunks[0].Text), 50)])
	}
}

func TestCountTokens(t *testing.T) {
	text := "one two three four five"
	got := CountTokens(text)
	if got != 5 {
		t.Errorf("CountTokens: got %d, want 5", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	text := "Hello world this is a test sentence"
	est := EstimateTokens(text)
	// Should be reasonable (between 5 and 15 for this short text).
	if est < 3 || est > 20 {
		t.Errorf("EstimateTokens: got %d, expected 3-20", est)
	}
}
