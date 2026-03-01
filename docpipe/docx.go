// CLAUDE:SUMMARY Extracts structured text from .docx files by parsing word/document.xml from the ZIP archive.
package docpipe

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"strings"
)

// extractDocx parses a .docx file by reading word/document.xml from the ZIP archive.
func extractDocx(path string) (string, []Section, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var docFile *zip.File
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			docFile = f
			break
		}
	}
	if docFile == nil {
		return "", nil, fmt.Errorf("word/document.xml not found in archive")
	}

	rc, err := docFile.Open()
	if err != nil {
		return "", nil, fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	const maxXMLDepth = 256

	decoder := xml.NewDecoder(rc)
	var sections []Section
	var title string
	var currentText strings.Builder
	var inParagraph bool
	var paragraphStyle string
	var depth int

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxXMLDepth {
				return "", nil, fmt.Errorf("XML nesting depth exceeds %d", maxXMLDepth)
			}
			switch {
			case t.Name.Local == "p":
				inParagraph = true
				currentText.Reset()
				paragraphStyle = ""
			case t.Name.Local == "pStyle" && inParagraph:
				for _, attr := range t.Attr {
					if attr.Name.Local == "val" {
						paragraphStyle = attr.Value
					}
				}
			case t.Name.Local == "t" && inParagraph:
				// Text run — content follows.
			}

		case xml.CharData:
			if inParagraph {
				currentText.Write(t)
			}

		case xml.EndElement:
			if depth > 0 {
				depth--
			}
			if t.Name.Local == "p" && inParagraph {
				inParagraph = false
				text := strings.TrimSpace(currentText.String())
				if text == "" {
					continue
				}

				level := docxHeadingLevel(paragraphStyle)
				if level > 0 {
					if title == "" {
						title = text
					}
					sections = append(sections, Section{
						Title: text,
						Level: level,
						Text:  text,
						Type:  "heading",
					})
				} else {
					sections = append(sections, Section{
						Text: text,
						Type: "paragraph",
					})
				}
			}
		}
	}

	return title, sections, nil
}

// docxHeadingLevel extracts the heading level from a paragraph style name.
// e.g. "Heading1" → 1, "Heading2" → 2, "Title" → 1, etc.
func docxHeadingLevel(style string) int {
	lower := strings.ToLower(style)

	if lower == "title" {
		return 1
	}
	if lower == "subtitle" {
		return 2
	}

	// "Heading1", "heading1", "Titre1", etc.
	for _, prefix := range []string{"heading", "titre", "überschrift"} {
		if strings.HasPrefix(lower, prefix) {
			rest := lower[len(prefix):]
			if len(rest) == 1 && rest[0] >= '1' && rest[0] <= '6' {
				return int(rest[0] - '0')
			}
		}
	}
	return 0
}
