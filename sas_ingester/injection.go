// CLAUDE:SUMMARY Prompt injection scanning for text and chunk files, delegating to the injection package for normalize and intent matching.
// CLAUDE:DEPENDS injection
// CLAUDE:EXPORTS InjectionResult, ScanInjection, ScanChunksInjection, StripZeroWidthChars, HasHomoglyphMixing
package sas_ingester

import (
	"fmt"

	"github.com/hazyhaar/pkg/injection"
)

// InjectionResult holds the outcome of prompt injection scanning.
type InjectionResult struct {
	Risk    string   `json:"risk"`
	Matches []string `json:"matches,omitempty"`
}

// ScanInjection scans text content for prompt injection patterns.
// Delegates to the injection package for normalize + intent matching.
func ScanInjection(text string) *InjectionResult {
	r := injection.Scan(text, injection.DefaultIntents())
	return &InjectionResult{
		Risk:    r.Risk,
		Matches: matchStrings(r.Matches),
	}
}

func matchStrings(matches []injection.Match) []string {
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.IntentID
	}
	return out
}

// StripZeroWidthChars removes zero-width Unicode characters used for steganographic injection.
// Delegates to injection.StripInvisible which covers a broader set of invisible characters.
func StripZeroWidthChars(text string) string {
	return injection.StripInvisible(text)
}

// HasHomoglyphMixing detects mixed Latin/Cyrillic in single words (visual obfuscation).
// Delegates to injection.HasHomoglyphMixing.
func HasHomoglyphMixing(text string) bool {
	return injection.HasHomoglyphMixing(text)
}

// ScanChunksInjection scans all chunk files for a piece and returns the worst risk.
func ScanChunksInjection(chunksDir string, chunkCount int) *InjectionResult {
	worst := &InjectionResult{Risk: "none"}

	for i := 0; i < chunkCount; i++ {
		_ = scanChunkFile(chunksDir, i, worst)
	}

	return worst
}

func scanChunkFile(chunksDir string, idx int, worst *InjectionResult) error {
	path := chunksDir + "/" + chunkFileName(idx)
	data, err := readFileSafe(path, 2*1024*1024)
	if err != nil {
		return err
	}

	r := ScanInjection(string(data))
	worst.Matches = append(worst.Matches, r.Matches...)

	if riskLevel(r.Risk) > riskLevel(worst.Risk) {
		worst.Risk = r.Risk
	}
	return nil
}

func chunkFileName(idx int) string {
	return fmt.Sprintf("chunk_%05d.bin", idx)
}

func riskLevel(r string) int {
	switch r {
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

func readFileSafe(path string, maxBytes int64) ([]byte, error) {
	f, err := openFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, _ := f.Read(buf)
	return buf[:n], nil
}
