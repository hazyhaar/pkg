package sas_ingester

import (
	"fmt"
	"regexp"
	"strings"
)

// InjectionResult holds the outcome of prompt injection scanning.
type InjectionResult struct {
	Risk     string   `json:"risk"`
	Matches  []string `json:"matches,omitempty"`
}

var injectionPatterns = []*regexp.Regexp{
	// Direct instruction override attempts
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+(instructions?|prompts?|rules?)`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)\s+(instructions?|prompts?|rules?)`),
	regexp.MustCompile(`(?i)forget\s+(all\s+)?(previous|prior|above)\s+(instructions?|prompts?|rules?)`),

	// System prompt extraction
	regexp.MustCompile(`(?i)(reveal|show|print|output|display|repeat)\s+(your\s+)?(system\s+)?(prompt|instructions?|rules?|config)`),
	regexp.MustCompile(`(?i)what\s+(are|is)\s+your\s+(system\s+)?(prompt|instructions?|rules?)`),

	// Role-play / jailbreak patterns
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(DAN|evil|unrestricted|unfiltered|jailbroken)`),
	regexp.MustCompile(`(?i)(pretend|act)\s+(like\s+)?(you\s+are|to\s+be)\s+.{0,30}(without|no)\s+(restrictions?|limits?|rules?|filters?)`),
	regexp.MustCompile(`(?i)enter\s+(DAN|developer|god|sudo|admin)\s+mode`),

	// Delimiter injection
	regexp.MustCompile(`(?i)<\|?(system|endof(text|turn)|im_start|im_end)\|?>`),
	regexp.MustCompile(`(?i)\[INST\]|\[/INST\]|\[SYS(TEM)?\]`),

	// Markdown/HTML injection for rendering attacks
	regexp.MustCompile(`(?i)<script[^>]*>|javascript\s*:`),
	regexp.MustCompile(`(?i)on(load|error|click|mouseover)\s*=`),
}

// ScanInjection scans text content for prompt injection patterns.
func ScanInjection(text string) *InjectionResult {
	result := &InjectionResult{Risk: "none"}

	for _, pat := range injectionPatterns {
		matches := pat.FindAllString(text, 3)
		for _, m := range matches {
			result.Matches = append(result.Matches, strings.TrimSpace(m))
		}
	}

	switch {
	case len(result.Matches) >= 3:
		result.Risk = "high"
	case len(result.Matches) >= 1:
		result.Risk = "medium"
	}

	return result
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
	// Read chunk as text — binary files will produce few matches.
	path := chunksDir + "/" + chunkFileName(idx)
	data, err := readFileSafe(path, 2*1024*1024) // cap at 2MB text scan
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
