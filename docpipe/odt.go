// CLAUDE:SUMMARY Extracts structured text from .odt (OpenDocument) files by parsing content.xml from the ZIP archive.
package docpipe

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// extractODT parses an .odt file by reading content.xml from the ZIP archive.
func extractODT(path string) (string, []Section, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var contentFile *zip.File
	for _, f := range r.File {
		if f.Name == "content.xml" {
			contentFile = f
			break
		}
	}
	if contentFile == nil {
		return "", nil, fmt.Errorf("content.xml not found in archive")
	}

	rc, err := contentFile.Open()
	if err != nil {
		return "", nil, fmt.Errorf("open content.xml: %w", err)
	}
	defer rc.Close()

	const maxXMLDepth = 256

	decoder := xml.NewDecoder(rc)
	var sections []Section
	var title string
	var currentText strings.Builder
	var inHeading bool
	var headingLevel int
	var inParagraph bool
	var inList bool
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
			case t.Name.Local == "h": // <text:h>
				inHeading = true
				currentText.Reset()
				headingLevel = 1
				for _, attr := range t.Attr {
					if attr.Name.Local == "outline-level" {
						if n, err := strconv.Atoi(attr.Value); err == nil {
							headingLevel = n
						}
					}
				}
			case t.Name.Local == "p": // <text:p>
				inParagraph = true
				currentText.Reset()
			case t.Name.Local == "list": // <text:list>
				inList = true
			}

		case xml.CharData:
			if inHeading || inParagraph {
				currentText.Write(t)
			}

		case xml.EndElement:
			if depth > 0 {
				depth--
			}
			switch {
			case t.Name.Local == "h" && inHeading:
				inHeading = false
				text := strings.TrimSpace(currentText.String())
				if text == "" {
					continue
				}
				if title == "" {
					title = text
				}
				sections = append(sections, Section{
					Title: text,
					Level: headingLevel,
					Text:  text,
					Type:  "heading",
				})

			case t.Name.Local == "p" && inParagraph:
				inParagraph = false
				text := strings.TrimSpace(currentText.String())
				if text == "" {
					continue
				}
				stype := "paragraph"
				if inList {
					stype = "list"
				}
				sections = append(sections, Section{
					Text: text,
					Type: stype,
				})

			case t.Name.Local == "list":
				inList = false
			}
		}
	}

	return title, sections, nil
}
