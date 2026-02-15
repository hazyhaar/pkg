package sas_ingester

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// FileMetadata holds extracted metadata for a piece.
type FileMetadata struct {
	MIME        string         `json:"mime"`
	Extension   string         `json:"extension,omitempty"`
	Entropy     float64        `json:"entropy"`
	IsText      bool           `json:"is_text"`
	IsBinary    bool           `json:"is_binary"`
	MagicHeader string         `json:"magic_header,omitempty"`
	Trailer     *TrailerInfo   `json:"trailer,omitempty"`
}

// TrailerInfo holds structural information extracted from the end of a file.
type TrailerInfo struct {
	HasPDFEOF      bool   `json:"has_pdf_eof,omitempty"`
	PDFStartXRef   string `json:"pdf_startxref,omitempty"`
	HasZIPEOCD     bool   `json:"has_zip_eocd,omitempty"`
	ZIPComment     string `json:"zip_comment,omitempty"`
	TrailerEntropy float64 `json:"trailer_entropy"`
}

// ExtractMetadata reads the first bytes of a file and extracts structural metadata.
func ExtractMetadata(filePath string) (*FileMetadata, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open for metadata: %w", err)
	}
	defer f.Close()

	// Read first 512 bytes for MIME detection.
	header := make([]byte, 512)
	n, _ := f.Read(header)
	header = header[:n]

	mime := http.DetectContentType(header)
	ext := strings.ToLower(filepath.Ext(filePath))

	// Compute entropy on the header.
	entropy := shannonEntropy(header)

	isText := strings.HasPrefix(mime, "text/") || mime == "application/json" || mime == "application/xml"
	isBinary := !isText

	magic := identifyMagic(header)

	return &FileMetadata{
		MIME:        mime,
		Extension:   ext,
		Entropy:     math.Round(entropy*1000) / 1000,
		IsText:      isText,
		IsBinary:    isBinary,
		MagicHeader: magic,
	}, nil
}

// ExtractFullMetadata reads the first chunk for header metadata and the last
// chunk for trailer analysis (PDF %%EOF/startxref, ZIP EOCD). If chunkDir
// has only one chunk, it is used for both header and trailer. Entropy is
// computed across all chunks for a more accurate measurement.
func ExtractFullMetadata(chunkDir string, chunkCount int) (*FileMetadata, error) {
	if chunkCount == 0 {
		return &FileMetadata{MIME: "application/octet-stream", MagicHeader: "unknown"}, nil
	}

	// Header from first chunk.
	firstChunk := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", 0))
	meta, err := ExtractMetadata(firstChunk)
	if err != nil {
		return nil, err
	}

	// Trailer from last chunk.
	lastChunk := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", chunkCount-1))
	trailer, err := extractTrailer(lastChunk)
	if err == nil {
		meta.Trailer = trailer
	}

	// Compute entropy across all chunks (sampling up to 64 KiB per chunk).
	fullEntropy := computeMultiChunkEntropy(chunkDir, chunkCount)
	if fullEntropy > 0 {
		meta.Entropy = math.Round(fullEntropy*1000) / 1000
	}

	return meta, nil
}

// extractTrailer reads the tail of a chunk file and looks for PDF/ZIP trailer
// structures. It reads the last 8 KiB which is enough for PDF startxref and
// ZIP end-of-central-directory records.
func extractTrailer(filePath string) (*TrailerInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	// Read up to 8 KiB from the end.
	tailSize := int64(8 * 1024)
	if stat.Size() < tailSize {
		tailSize = stat.Size()
	}
	if tailSize == 0 {
		return &TrailerInfo{}, nil
	}

	if _, err := f.Seek(-tailSize, 2); err != nil {
		return nil, err
	}
	tail := make([]byte, tailSize)
	n, _ := f.Read(tail)
	tail = tail[:n]

	info := &TrailerInfo{
		TrailerEntropy: math.Round(shannonEntropy(tail)*1000) / 1000,
	}

	// PDF trailer: look for %%EOF and startxref.
	tailStr := string(tail)
	if idx := strings.LastIndex(tailStr, "%%EOF"); idx >= 0 {
		info.HasPDFEOF = true
		// Look for startxref above %%EOF.
		before := tailStr[:idx]
		if sxIdx := strings.LastIndex(before, "startxref"); sxIdx >= 0 {
			// Extract the offset value between startxref and %%EOF.
			between := strings.TrimSpace(before[sxIdx+len("startxref"):])
			lines := strings.SplitN(between, "\n", 2)
			if len(lines) > 0 {
				info.PDFStartXRef = strings.TrimSpace(lines[0])
			}
		}
	}

	// ZIP EOCD: PK\x05\x06 signature.
	eocdSig := []byte{0x50, 0x4b, 0x05, 0x06}
	if idx := bytes.LastIndex(tail, eocdSig); idx >= 0 {
		info.HasZIPEOCD = true
		// ZIP comment is at EOCD+20 (2-byte length) then the comment bytes.
		commentStart := idx + 22
		if commentStart+2 <= len(tail) {
			commentLen := int(tail[idx+20]) | int(tail[idx+21])<<8
			if commentStart+commentLen <= len(tail) && commentLen > 0 {
				info.ZIPComment = string(tail[commentStart : commentStart+commentLen])
			}
		}
	}

	return info, nil
}

// computeMultiChunkEntropy samples each chunk (up to 64 KiB per chunk) and
// returns the overall Shannon entropy. This is more representative than
// computing entropy on just the first 512 bytes.
func computeMultiChunkEntropy(chunkDir string, chunkCount int) float64 {
	var freq [256]float64
	var total float64
	const sampleSize = 64 * 1024

	for i := 0; i < chunkCount; i++ {
		path := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", i))
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		buf := make([]byte, sampleSize)
		n, _ := f.Read(buf)
		f.Close()

		for _, b := range buf[:n] {
			freq[b]++
		}
		total += float64(n)
	}

	if total == 0 {
		return 0
	}

	var entropy float64
	for _, f := range freq {
		if f > 0 {
			p := f / total
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// MetadataJSON returns metadata as a JSON string for storage.
func MetadataJSON(m *FileMetadata) string {
	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func shannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var freq [256]float64
	for _, b := range data {
		freq[b]++
	}
	total := float64(len(data))
	var entropy float64
	for _, f := range freq {
		if f > 0 {
			p := f / total
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

func identifyMagic(header []byte) string {
	if len(header) < 4 {
		return "unknown"
	}

	switch {
	case string(header[:4]) == "%PDF":
		return "PDF"
	case header[0] == 'P' && header[1] == 'K' && header[2] == 3 && header[3] == 4:
		return "ZIP"
	case header[0] == 0x7f && string(header[1:4]) == "ELF":
		return "ELF"
	case header[0] == 'M' && header[1] == 'Z':
		return "PE"
	case header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff:
		return "JPEG"
	case string(header[:3]) == "GIF":
		return "GIF"
	case header[0] == 0x89 && string(header[1:4]) == "PNG":
		return "PNG"
	case header[0] == 0xd0 && header[1] == 0xcf && header[2] == 0x11 && header[3] == 0xe0:
		return "OLE2"
	case string(header[:5]) == "<?xml":
		return "XML"
	case header[0] == '{' || header[0] == '[':
		return "JSON"
	default:
		return "unknown"
	}
}
