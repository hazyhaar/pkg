// CLAUDE:SUMMARY Extracts content from plain text and Markdown files with heading detection and whitespace normalization.
package docpipe

import (
	"os"
	"strings"
	"unicode"
)

// extractText extracts content from a plain text file.
func extractText(path string) (string, []Section, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	text := normalizeWhitespace(string(data))
	if text == "" {
		return "", nil, nil
	}

	title := firstLine(text)

	return title, []Section{{
		Text: text,
		Type: "paragraph",
	}}, nil
}

// extractMarkdown extracts structured sections from a Markdown file.
// Detects headings (# lines) and splits content into sections.
func extractMarkdown(path string) (string, []Section, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}

	lines := strings.Split(string(data), "\n")
	var sections []Section
	var title string
	var currentText strings.Builder

	flushParagraph := func() {
		text := strings.TrimSpace(currentText.String())
		if text != "" {
			sections = append(sections, Section{
				Text: text,
				Type: "paragraph",
			})
		}
		currentText.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect ATX headings: # heading, ## heading, etc.
		if strings.HasPrefix(trimmed, "#") {
			flushParagraph()

			level := 0
			for _, ch := range trimmed {
				if ch == '#' {
					level++
				} else {
					break
				}
			}
			if level > 6 {
				level = 6
			}

			headingText := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			headingText = strings.TrimRight(headingText, "#")
			headingText = strings.TrimSpace(headingText)

			if headingText != "" {
				if title == "" {
					title = headingText
				}
				sections = append(sections, Section{
					Title: headingText,
					Level: level,
					Text:  headingText,
					Type:  "heading",
				})
			}
			continue
		}

		// Empty line = paragraph break.
		if trimmed == "" {
			flushParagraph()
			continue
		}

		if currentText.Len() > 0 {
			currentText.WriteByte(' ')
		}
		currentText.WriteString(trimmed)
	}
	flushParagraph()

	if title == "" && len(sections) > 0 {
		title = firstLine(sections[0].Text)
	}

	return title, sections, nil
}

func normalizeWhitespace(text string) string {
	var sb strings.Builder
	prevSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevSpace && sb.Len() > 0 {
				sb.WriteByte(' ')
				prevSpace = true
			}
		} else {
			sb.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(sb.String())
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	text = strings.TrimSpace(text)
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}
