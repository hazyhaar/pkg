> **Schema technique** : voir [`docpipe_schem.md`](docpipe_schem.md) — lecture prioritaire avant tout code source.

# docpipe

Responsabilite: Pipeline d'extraction de documents multi-format (DOCX, ODT, PDF, Markdown, texte, HTML) en sections structurees, pure Go.
Migre depuis `github.com/hazyhaar/chrc/docpipe` (2026-02-28).
Depend de: `golang.org/x/net/html`, `pdfast` (PDF extraction + sanitize + tables + layout), `github.com/hazyhaar/pkg/kit`, `github.com/hazyhaar/pkg/connectivity`, `github.com/modelcontextprotocol/go-sdk/mcp`
Dependants: `sas_ingester` (cmd/sas_ingester MarkdownConverter adapter), `chrc/veille` (handler_document), `chrc/e2e`
Point d'entree: `docpipe.go`
Types cles: `Pipeline`, `Config`, `Document`, `Section`, `Format`, `ExtractionQuality`
Invariants:
- Pure Go, CGO_ENABLED=0 compatible
- MaxFileSize defaut = 100 MB
- PDF pipeline: reader.Open (security gates) → sanitize.Sanitize → text.Extract → tables.Extract → layout.DetectLayout
- PDF sans texte → NeedsOCR=true, pas d'erreur (pas de fallback pdftotext)
- SecurityRemovals dans ExtractionQuality remonte les elements dangereux supprimes (JS, OpenAction, AA, XFA)
- Sections de type "table" avec metadata (rows, columns) pour les tableaux detectes par pdfast
- Layout multi-colonnes detecte, stocke dans Section.Metadata["columns"]
- XML bomb defense : limite profondeur 256 (DOCX + ODT)
- HTML : filtrage CSS hidden text (5 patterns)
- RegisterMCP expose 3 tools, RegisterConnectivity expose 2 handlers
NE PAS:
- Utiliser une lib PDF C/CGO
- Utiliser pdftotext/poppler comme fallback (supprime depuis mars 2026)
- Confondre extractHTMLFile (fichier local) avec extract.Extract (raw bytes, package chrc/extract)
