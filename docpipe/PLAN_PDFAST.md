# Plan : Intégration pdfast dans docpipe

## Contexte

docpipe est le package partagé d'extraction multi-format (PDF, DOCX, ODT, HTML, TXT, MD).
L'extraction PDF repose actuellement sur **pdfcpu** (niveau 1) avec fallback **pdftotext** (subprocess, niveau 2).

pdfast est la réécriture pure Go de pdfcpu — CMap/ToUnicode, fonts composites, LZW, security scanning.
L'intégration dans docpipe est le **point central** : tous les consommateurs (veille, HORAG, siftrag) en bénéficient.

## Problèmes actuels (pdfcpu)

- Pas d'extraction CIDFont/ToUnicode (browser print-to-PDF = texte vide)
- Fallback pdftotext = dépendance binaire externe (poppler-utils), incompatible CGO_ENABLED=0
- Pas de scanning sécurité PDF (injection, polyglot, zip bomb)
- Pas d'audit trail

## Objectif

Remplacer pdfcpu par pdfast dans `pdf.go` comme extracteur principal.
Garder pdftotext comme ultime fallback (dégradé gracieux).

## Fichiers impactés

| Fichier | Action |
|---------|--------|
| `pdf.go` | Réécrire `extractPDF()` avec pdfast |
| `pdf_test.go` | Adapter tests + ajouter cas CIDFont/ToUnicode |
| `quality.go` | Alimenter `ExtractionQuality` depuis pdfast |
| `docpipe.go` | Aucun changement (dispatch par format inchangé) |
| `go.mod` (hazyhaar_pkg) | Ajouter pdfast, retirer pdfcpu |

## Plan d'implémentation

### Etape 1 : API pdfast pour docpipe

pdfast doit exposer une API simple consommable par docpipe.

```go
// pdfast/ops/text/text.go — existe déjà
func Extract(s *store.ObjectStore) ([]PageText, error)

// pdfast/pkg/pdf/reader.go — existe déjà
func Open(r io.ReaderAt, size int64, opts ...Option) (*Document, error)
```

**Manque** : helper haut niveau fichier → texte (docpipe passe un path).

```go
// A ajouter dans pdfast — helper convenience
func ExtractFile(path string) ([]PageText, error) {
    f, err := os.Open(path)
    // ...
    doc, err := pdf.Open(f, stat.Size())
    return text.Extract(doc.Store())
}
```

### Etape 2 : Réécrire extractPDF()

```go
// pdf.go — nouvelle implémentation
func extractPDF(path string) (string, []Section, *ExtractionQuality, error) {
    // 1. pdfast extraction
    pages, err := pdftext.ExtractFile(path)
    if err != nil {
        // Fallback pdftotext si pdfast échoue
        return extractPDFViaPdftotext(path)
    }

    // 2. Construire sections (1 section par page)
    var sections []Section
    for _, page := range pages {
        sections = append(sections, Section{
            Title: fmt.Sprintf("Page %d", page.PageNum),
            Type:  "page",
            Text:  page.Text,
            Metadata: map[string]string{"page": strconv.Itoa(page.PageNum)},
        })
    }

    // 3. Construire ExtractionQuality
    quality := buildQualityFromPages(pages)

    // 4. Fallback pdftotext si 0 sections
    if len(sections) == 0 {
        return extractPDFViaPdftotext(path)
    }

    title := extractTitleFromSections(sections)
    return title, sections, quality, nil
}
```

### Etape 3 : ExtractionQuality depuis pdfast

pdfast fournit des TextBlocks avec position et font — on peut calculer :

| Métrique | Source pdfast |
|----------|-------------|
| `PageCount` | `len(pages)` |
| `CharsPerPage` | `totalChars / pageCount` |
| `PrintableRatio` | `computePrintableRatio(rawText)` (inchangé) |
| `WordlikeRatio` | `computeWordlikeRatio(rawText)` (inchangé) |
| `HasImageStreams` | pdfast store : scan XObject streams |
| `VisualRefCount` | `countVisualRefs(rawText)` (inchangé) |

`NeedsOCR()` et `HasVisualGap()` fonctionnent sans changement (basés sur les métriques ci-dessus).

### Etape 4 : Tests

| Test | Validation |
|------|-----------|
| `TestExtractPDF_Simple` | Adapter pour pdfast (même assertions) |
| `TestExtractPDF_CIDFont` | NOUVEAU : pdfast doit réussir sans fallback pdftotext |
| `TestExtractPDF_ToUnicode` | NOUVEAU : browser print-to-PDF avec CMap compressé |
| `TestExtractPDF_LZW` | NOUVEAU : PDF avec streams LZW (TIFF variant) |
| `TestExtractPDF_ImageOnly` | Inchangé (NeedsOCR = true) |
| `TestExtractPDF_Fallback` | NOUVEAU : PDF corrompu → fallback pdftotext |

### Etape 5 : Nettoyage

- Retirer `github.com/pdfcpu/pdfcpu` de go.mod
- Supprimer l'ancien code pdfcpu dans pdf.go
- Garder `tryPdftotext()` comme fallback

## Contrat de sortie

- `go test -race -count=1 ./docpipe/...` passe
- CIDFont/ToUnicode extrait sans fallback pdftotext
- `ExtractionQuality` identique ou meilleure
- pdfcpu retiré des dépendances
- Zéro régression sur les autres formats (DOCX, ODT, etc.)

## Dépendances

- pdfast doit être publiable (go module) ou accessible via `replace`
- pdfast helper `ExtractFile()` doit exister

## Risques

- **Dedup hash** : si pdfast produit un texte légèrement différent de pdfcpu, les hash SHA-256 changent → re-extraction de documents déjà indexés. Acceptable (one-shot).
- **Performance** : pdfast est plus rapide que pdfcpu sur les benchmarks actuels (145 pages en 98ms).
