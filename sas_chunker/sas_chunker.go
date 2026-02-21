package sas_chunker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultChunkSize int64 = 10 * 1024 * 1024 // 10 MiB

// ChunkMeta describes a single chunk within a manifest.
type ChunkMeta struct {
	Index       int    `json:"index"`
	FileName    string `json:"file_name"`
	OffsetBytes int64  `json:"offset_bytes"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

// Manifest describes the original file and all its chunks.
type Manifest struct {
	OriginalName   string      `json:"original_name"`
	OriginalSize   int64       `json:"original_size"`
	OriginalSHA256 string      `json:"original_sha256"`
	ChunkSize      int64       `json:"chunk_size"`
	TotalChunks    int         `json:"total_chunks"`
	Chunks         []ChunkMeta `json:"chunks"`
	CreatedAt      string      `json:"created_at"`
}

// ProgressFunc is called after each chunk is processed.
// index is zero-based, total is the expected chunk count, bytes is cumulative.
type ProgressFunc func(index, total int, bytes int64)

// SplitReader reads from r and writes chunk files plus a manifest.json into outDir.
// It streams directly without creating a temp file, computing the overall SHA-256
// while writing each chunk. chunkSize <= 0 defaults to DefaultChunkSize.
// originalName is used in the manifest; progress may be nil.
func SplitReader(r io.Reader, originalName, outDir string, chunkSize int64, progress ProgressFunc) (*Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	fileHasher := sha256.New()
	tee := io.TeeReader(r, fileHasher)

	manifest := &Manifest{
		OriginalName: originalName,
		ChunkSize:    chunkSize,
		Chunks:       make([]ChunkMeta, 0),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	buf := make([]byte, chunkSize)
	var offset int64
	var idx int

	for {
		n, readErr := io.ReadFull(tee, buf)
		if n == 0 {
			break
		}
		data := buf[:n]

		chunkHasher := sha256.New()
		chunkHasher.Write(data)
		chunkHash := hex.EncodeToString(chunkHasher.Sum(nil))

		fileName := fmt.Sprintf("chunk_%05d.bin", idx)
		chunkPath := filepath.Join(outDir, fileName)
		if err := os.WriteFile(chunkPath, data, 0644); err != nil {
			return nil, fmt.Errorf("write chunk %d: %w", idx, err)
		}

		manifest.Chunks = append(manifest.Chunks, ChunkMeta{
			Index:       idx,
			FileName:    fileName,
			OffsetBytes: offset,
			SizeBytes:   int64(n),
			SHA256:      chunkHash,
		})

		offset += int64(n)
		idx++

		if progress != nil {
			progress(idx-1, 0, offset) // total unknown during streaming
		}

		if readErr != nil {
			break
		}
	}

	manifest.OriginalSize = offset
	manifest.OriginalSHA256 = hex.EncodeToString(fileHasher.Sum(nil))
	manifest.TotalChunks = idx

	// Update progress callback with final total.
	if progress != nil && idx > 0 {
		progress(idx-1, idx, offset)
	}

	// Write manifest.
	manifestPath := filepath.Join(outDir, "manifest.json")
	mData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, mData, 0644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	return manifest, nil
}

// Split reads srcPath and writes chunk files plus a manifest.json into outDir.
// chunkSize <= 0 defaults to DefaultChunkSize.
// progress may be nil.
func Split(srcPath, outDir string, chunkSize int64, progress ProgressFunc) (*Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	stat, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat source: %w", err)
	}
	fileSize := stat.Size()

	// Hash the whole file.
	fileHasher := sha256.New()
	if _, err := io.Copy(fileHasher, src); err != nil {
		return nil, fmt.Errorf("hash source: %w", err)
	}
	fileHash := hex.EncodeToString(fileHasher.Sum(nil))

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek source: %w", err)
	}

	totalChunks := int(fileSize / chunkSize)
	if fileSize%chunkSize != 0 {
		totalChunks++
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	manifest := &Manifest{
		OriginalName:   filepath.Base(srcPath),
		OriginalSize:   fileSize,
		OriginalSHA256: fileHash,
		ChunkSize:      chunkSize,
		TotalChunks:    totalChunks,
		Chunks:         make([]ChunkMeta, 0, totalChunks),
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}

	buf := make([]byte, chunkSize)
	var offset int64

	for i := 0; i < totalChunks; i++ {
		n, err := io.ReadFull(src, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("read chunk %d: %w", i, err)
		}
		data := buf[:n]

		chunkHasher := sha256.New()
		chunkHasher.Write(data)
		chunkHash := hex.EncodeToString(chunkHasher.Sum(nil))

		fileName := fmt.Sprintf("chunk_%05d.bin", i)
		chunkPath := filepath.Join(outDir, fileName)
		if err := os.WriteFile(chunkPath, data, 0644); err != nil {
			return nil, fmt.Errorf("write chunk %d: %w", i, err)
		}

		manifest.Chunks = append(manifest.Chunks, ChunkMeta{
			Index:       i,
			FileName:    fileName,
			OffsetBytes: offset,
			SizeBytes:   int64(n),
			SHA256:      chunkHash,
		})

		offset += int64(n)

		if progress != nil {
			progress(i, totalChunks, offset)
		}
	}

	// Write manifest.
	manifestPath := filepath.Join(outDir, "manifest.json")
	mData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, mData, 0644); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}

	return manifest, nil
}

// Assemble reads chunks from chunksDir using its manifest.json,
// verifies each chunk hash, writes the reassembled file to outPath,
// and validates the final SHA-256 against the manifest.
// progress may be nil.
func Assemble(chunksDir, outPath string, progress ProgressFunc) error {
	manifest, err := LoadManifest(chunksDir)
	if err != nil {
		return err
	}
	if err := validateChunkNames(chunksDir, manifest); err != nil {
		return err
	}

	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	fileHasher := sha256.New()
	writer := io.MultiWriter(out, fileHasher)

	sorted := make([]ChunkMeta, len(manifest.Chunks))
	copy(sorted, manifest.Chunks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Index < sorted[j].Index })

	var written int64
	for _, cm := range sorted {
		chunkPath := filepath.Join(chunksDir, cm.FileName)
		data, err := os.ReadFile(chunkPath)
		if err != nil {
			return fmt.Errorf("read chunk %d: %w", cm.Index, err)
		}

		h := sha256.New()
		h.Write(data)
		actual := hex.EncodeToString(h.Sum(nil))
		if actual != cm.SHA256 {
			return fmt.Errorf("chunk %d hash mismatch: expected %s, got %s", cm.Index, cm.SHA256, actual)
		}

		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("write chunk %d: %w", cm.Index, err)
		}

		written += int64(len(data))

		if progress != nil {
			progress(cm.Index, manifest.TotalChunks, written)
		}
	}

	finalHash := hex.EncodeToString(fileHasher.Sum(nil))
	if finalHash != manifest.OriginalSHA256 {
		out.Close()
		os.Remove(outPath)
		return fmt.Errorf("assembled file hash mismatch: expected %s, got %s", manifest.OriginalSHA256, finalHash)
	}

	return nil
}

// VerifyResult holds the outcome of a Verify call.
type VerifyResult struct {
	TotalChunks int
	TotalSize   int64
	Errors      []string
}

// OK returns true when no errors were found.
func (v *VerifyResult) OK() bool { return len(v.Errors) == 0 }

// Verify checks every chunk in chunksDir against its manifest without assembling.
func Verify(chunksDir string) (*VerifyResult, error) {
	manifest, err := LoadManifest(chunksDir)
	if err != nil {
		return nil, err
	}
	if err := validateChunkNames(chunksDir, manifest); err != nil {
		return nil, err
	}

	result := &VerifyResult{TotalChunks: manifest.TotalChunks}
	var totalSize int64

	for _, cm := range manifest.Chunks {
		chunkPath := filepath.Join(chunksDir, cm.FileName)

		data, err := os.ReadFile(chunkPath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("MISSING chunk %d (%s)", cm.Index, cm.FileName))
			continue
		}

		h := sha256.New()
		h.Write(data)
		actual := hex.EncodeToString(h.Sum(nil))

		if actual != cm.SHA256 {
			result.Errors = append(result.Errors, fmt.Sprintf("CORRUPT chunk %d (%s)", cm.Index, cm.FileName))
			continue
		}

		if int64(len(data)) != cm.SizeBytes {
			result.Errors = append(result.Errors, fmt.Sprintf("BADSIZE chunk %d: expected %d, got %d", cm.Index, cm.SizeBytes, len(data)))
			continue
		}

		totalSize += int64(len(data))
	}

	result.TotalSize = totalSize

	if totalSize != manifest.OriginalSize {
		result.Errors = append(result.Errors, fmt.Sprintf("SIZE MISMATCH: chunks total %d, expected %d", totalSize, manifest.OriginalSize))
	}

	return result, nil
}

// validateChunkNames ensures no chunk filename contains path traversal components.
func validateChunkNames(chunksDir string, m *Manifest) error {
	absDir, err := filepath.Abs(chunksDir)
	if err != nil {
		return fmt.Errorf("resolve chunks dir: %w", err)
	}
	for _, cm := range m.Chunks {
		if strings.Contains(cm.FileName, "..") || filepath.IsAbs(cm.FileName) {
			return fmt.Errorf("invalid chunk filename %q: path traversal detected", cm.FileName)
		}
		absChunk, err := filepath.Abs(filepath.Join(chunksDir, cm.FileName))
		if err != nil {
			return fmt.Errorf("resolve chunk path %q: %w", cm.FileName, err)
		}
		if !strings.HasPrefix(absChunk, absDir+string(filepath.Separator)) {
			return fmt.Errorf("chunk %q resolves outside chunks directory", cm.FileName)
		}
	}
	return nil
}

// LoadManifest reads and parses manifest.json from a chunks directory.
func LoadManifest(dir string) (*Manifest, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest.json: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest.json: %w", err)
	}
	return &m, nil
}

// FormatBytes returns a human-readable size string.
func FormatBytes(b int64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)

	switch {
	case b >= GiB:
		return fmt.Sprintf("%.2f GiB", float64(b)/float64(GiB))
	case b >= MiB:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(MiB))
	case b >= KiB:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
