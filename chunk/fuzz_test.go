package chunk

import (
	"strings"
	"testing"
)

func FuzzSplit(f *testing.F) {
	f.Add("hello world", 512, 64, 32)
	f.Add("", 512, 64, 32)
	f.Add("paragraph one\n\nparagraph two\n\nparagraph three", 50, 10, 5)
	f.Add("single", 1, 1, 1)
	f.Add(strings.Repeat("word ", 500), 100, 10, 5)

	f.Fuzz(func(t *testing.T, text string, maxTokens, overlap, minChunk int) {
		if maxTokens <= 0 || maxTokens > 10000 {
			return
		}
		if overlap < 0 || overlap >= maxTokens {
			return
		}
		if minChunk < 0 || minChunk > maxTokens {
			return
		}

		chunks := Split(text, Options{
			MaxTokens:      maxTokens,
			OverlapTokens:  overlap,
			MinChunkTokens: minChunk,
		})
		for i, c := range chunks {
			if c.Index != i {
				t.Fatalf("chunk %d has Index=%d", i, c.Index)
			}
			if c.Text == "" {
				t.Fatalf("chunk %d has empty text", i)
			}
		}
	})
}

func FuzzCountTokens(f *testing.F) {
	f.Add("hello world")
	f.Add("")
	f.Add("  multiple   spaces  ")

	f.Fuzz(func(t *testing.T, text string) {
		n := CountTokens(text)
		if n < 0 {
			t.Fatalf("CountTokens returned negative: %d", n)
		}
	})
}
