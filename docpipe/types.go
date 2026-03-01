// CLAUDE:SUMMARY Defines Format, Section, and Document types for the docpipe extraction pipeline.
package docpipe

// Format identifies a document type.
type Format string

const (
	FormatDocx Format = "docx"
	FormatODT  Format = "odt"
	FormatPDF  Format = "pdf"
	FormatMD   Format = "md"
	FormatTXT  Format = "txt"
	FormatHTML Format = "html"
)

// Section is a structural unit of a document.
type Section struct {
	Title    string            `json:"title,omitempty"`
	Level    int               `json:"level"`              // heading level 1-6, 0 for body
	Text     string            `json:"text"`               // extracted text content
	Type     string            `json:"type"`               // heading, paragraph, table, list
	Metadata map[string]string `json:"metadata,omitempty"` // extra attributes
}

// Document is the result of extracting content from a file.
type Document struct {
	Path     string    `json:"path"`
	Format   Format    `json:"format"`
	Title    string    `json:"title"`
	Sections []Section `json:"sections"`
	RawText  string              `json:"raw_text"`           // concatenated full text
	Quality  *ExtractionQuality  `json:"quality,omitempty"`  // PDF extraction quality metrics
}
