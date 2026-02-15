package sas_ingester

import (
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
	MIME        string  `json:"mime"`
	Extension   string  `json:"extension,omitempty"`
	Entropy     float64 `json:"entropy"`
	IsText      bool    `json:"is_text"`
	IsBinary    bool    `json:"is_binary"`
	MagicHeader string  `json:"magic_header,omitempty"`
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
