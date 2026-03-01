
# docpipe -- Technical Schema
# Pipeline d'extraction de documents multi-format en sections structurees (pure Go)

```
╔══════════════════════════════════════════════════════════════════════════════════╗
║  docpipe — Multi-format document extraction to structured sections             ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  INPUTS                        PROCESSING                 OUTPUTS             ║
║  ------                        ----------                 -------             ║
║                                                                                ║
║  .docx file ─┐                                                                ║
║  .odt  file ─┤                 ┌─────────────────┐                            ║
║  .pdf  file ─┤──→ Detect() ──→ │    Pipeline      │──→  *Document             ║
║  .md   file ─┤    (by ext)     │                  │     {                      ║
║  .txt  file ─┤                 │  Extract(ctx,    │       Path     string      ║
║  .html file ─┘                 │    path) ────────│──→    Format   Format      ║
║                                │                  │       Title    string      ║
║  Config ──────────────────────→│  MaxFileSize     │       Sections []Section   ║
║  {                             │  Logger          │       RawText  string      ║
║    MaxFileSize int64           │                  │       Quality  *Extr.Qual. ║
║    Logger *slog.Logger         └──────┬───────────┘     }                      ║
║  }                                    │                                        ║
║  Defaults:                            │                                        ║
║   MaxFileSize = 100 MB                ▼                                        ║
║   Logger = slog.Default()    ┌────────────────────┐                            ║
║                              │  Format Dispatch    │                            ║
║                              ├────────────────────┤                            ║
║                              │ docx → extractDocx  │                           ║
║                              │ odt  → extractODT   │                           ║
║                              │ pdf  → extractPDF   │                           ║
║                              │ md   → extractMD    │                           ║
║                              │ txt  → extractText  │                           ║
║                              │ html → extractHTML   │                          ║
║                              └────────────────────┘                            ║
╚══════════════════════════════════════════════════════════════════════════════════╝
```

## Format-Specific Extractors

```
┌─────────────────────────────────────────────────────────────────┐
│ DOCX Extractor                                                  │
│   ZIP → word/document.xml → XML SAX parse                       │
│   Detects: <w:p>, <w:pStyle>, <w:t>                             │
│   Heading styles: Heading1-6, Title, Subtitle, Titre, Uberschrift│
│   XML depth limit: 256 (bomb defense)                           │
│   Output: heading/paragraph sections                            │
├─────────────────────────────────────────────────────────────────┤
│ ODT Extractor                                                   │
│   ZIP → content.xml → XML SAX parse                             │
│   Detects: <text:h outline-level=N>, <text:p>, <text:list>      │
│   XML depth limit: 256 (bomb defense)                           │
│   Output: heading/paragraph/list sections                       │
├─────────────────────────────────────────────────────────────────┤
│ PDF Extractor (via pdfcpu)                                      │
│   pdfcpu.ReadValidateAndOptimize → per-page content stream      │
│   Parses operators: Tj, TJ, ', Td, TD, T*                      │
│   Escape handling: \n, \r, \t, \\, octal                        │
│   Image detection: XObject scan + optimize fallback             │
│   Title: first non-empty line, truncated to 200 chars           │
│   Output: page sections + ExtractionQuality                     │
├─────────────────────────────────────────────────────────────────┤
│ Markdown Extractor                                              │
│   ATX headings (# to ######) → heading sections (level 1-6)    │
│   Empty lines → paragraph breaks                                │
│   Title: first heading or first section text                    │
│   Output: heading/paragraph sections                            │
├─────────────────────────────────────────────────────────────────┤
│ Text Extractor                                                  │
│   Whitespace normalization (collapse spaces, trim)              │
│   Title: first line, max 200 chars                              │
│   Output: single paragraph section                              │
├─────────────────────────────────────────────────────────────────┤
│ HTML Extractor (via golang.org/x/net/html)                      │
│   DOM tree walk, skips: script/style/noscript/nav/footer/header │
│   Hidden element detection: 5 CSS patterns (display:none, etc.) │
│   Extracts: h1-h6, p, table, ul/ol                             │
│   Fallback: all visible text as single paragraph                │
│   Title: <title> element text                                   │
│   Output: heading/paragraph/table/list sections                 │
└─────────────────────────────────────────────────────────────────┘
```

## Quality Scoring (PDF only)

```
┌──────────────────────────────────────────────────────────────┐
│  ExtractionQuality                                           │
│  ─────────────────                                           │
│  PageCount      int       — total pages in PDF               │
│  CharsPerPage   float64   — avg chars extracted per page     │
│  PrintableRatio float64   — ratio of printable chars         │
│  WordlikeRatio  float64   — ratio of 2-15 char tokens        │
│  HasImageStreams bool      — PDF contains image XObjects      │
│  VisualRefCount int       — count of "see Figure X" refs     │
│                                                              │
│  NeedsOCR() bool                                             │
│    (CharsPerPage < 50 && HasImageStreams) || PrintableRatio < 0.85 │
│                                                              │
│  HasVisualGap() bool                                         │
│    VisualRefCount > 0 && HasImageStreams                      │
│                                                              │
│  Garbage runes: PUA U+E000-F8FF, U+FFFD, control < U+0020   │
│  Visual refs: regex-based (voir/cf/see figure/table N)       │
└──────────────────────────────────────────────────────────────┘
```

## Key Types

```
Format    string    — "docx" | "odt" | "pdf" | "md" | "txt" | "html"

Section {
    Title    string            — heading text (empty for body)
    Level    int               — 1-6 for headings, 0 for body
    Text     string            — extracted text content
    Type     string            — "heading" | "paragraph" | "table" | "list" | "page"
    Metadata map[string]string — e.g. {"page": "3"} for PDF
}

Document {
    Path     string
    Format   Format
    Title    string
    Sections []Section
    RawText  string             — concatenated full text
    Quality  *ExtractionQuality — PDF only, nil for other formats
}

Pipeline {cfg Config, logger *slog.Logger}
```

## MCP Tools (RegisterMCP)

```
┌────────────────────────────────────────────────────────────────┐
│  Tool Name          │ Input          │ Output                  │
├─────────────────────┼────────────────┼─────────────────────────┤
│  docpipe_extract    │ {path: string} │ *Document (JSON)        │
│  docpipe_detect     │ {path: string} │ {format: string}        │
│  docpipe_formats    │ (none)         │ {formats: []string}     │
└─────────────────────┴────────────────┴─────────────────────────┘
```

## Connectivity Handlers (RegisterConnectivity)

```
┌────────────────────────────────────────────────────────────────┐
│  Service Name       │ Payload JSON       │ Response JSON       │
├─────────────────────┼────────────────────┼─────────────────────┤
│  docpipe_extract    │ {path: "..."}      │ Document JSON       │
│  docpipe_detect     │ {path: "..."}      │ {format: "..."}     │
└─────────────────────┴────────────────────┴─────────────────────┘
```

## Dependencies

```
External:
  github.com/pdfcpu/pdfcpu      — PDF parsing (pure Go)
  golang.org/x/net/html          — HTML DOM parsing
  github.com/modelcontextprotocol/go-sdk/mcp — MCP tool registration

Internal (hazyhaar/pkg):
  kit           — kit.RegisterMCPTool, kit.MCPDecodeResult
  connectivity  — connectivity.Router, connectivity.Handler
```

## Database Tables

```
  (none — docpipe is stateless, no DB)
```

## Key Function Signatures

```go
func New(cfg Config) *Pipeline
func (p *Pipeline) Detect(path string) (Format, error)
func (p *Pipeline) Extract(ctx context.Context, path string) (*Document, error)
func SupportedFormats() []string
func (p *Pipeline) RegisterMCP(srv *mcp.Server)
func (p *Pipeline) RegisterConnectivity(router *connectivity.Router)
func (q *ExtractionQuality) NeedsOCR() bool
func (q *ExtractionQuality) HasVisualGap() bool
```
