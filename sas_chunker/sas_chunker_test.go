package sas_chunker

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func createTestFile(t *testing.T, dir string, size int) string {
	t.Helper()
	path := filepath.Join(dir, "testfile.bin")
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSplit_And_Assemble(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 1024*25) // 25 KiB
	chunksDir := filepath.Join(tmpDir, "chunks")

	manifest, err := Split(srcPath, chunksDir, 1024*10, nil) // 10 KiB chunks
	if err != nil {
		t.Fatal(err)
	}

	if manifest.TotalChunks != 3 {
		t.Fatalf("chunks: got %d, want 3", manifest.TotalChunks)
	}
	if manifest.OriginalSize != 1024*25 {
		t.Fatalf("size: got %d", manifest.OriginalSize)
	}
	if manifest.OriginalName != "testfile.bin" {
		t.Fatalf("name: got %q", manifest.OriginalName)
	}

	// Assemble
	outPath := filepath.Join(tmpDir, "reassembled.bin")
	if err := Assemble(chunksDir, outPath, nil); err != nil {
		t.Fatal(err)
	}

	// Compare
	original, _ := os.ReadFile(srcPath)
	reassembled, _ := os.ReadFile(outPath)
	if len(original) != len(reassembled) {
		t.Fatalf("size mismatch: %d vs %d", len(original), len(reassembled))
	}
	for i := range original {
		if original[i] != reassembled[i] {
			t.Fatalf("byte mismatch at offset %d", i)
		}
	}
}

func TestSplit_DefaultChunkSize(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 100)
	chunksDir := filepath.Join(tmpDir, "chunks")

	manifest, err := Split(srcPath, chunksDir, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TotalChunks != 1 {
		t.Fatalf("chunks: got %d, want 1", manifest.TotalChunks)
	}
	if manifest.ChunkSize != DefaultChunkSize {
		t.Fatalf("chunk size: got %d, want %d", manifest.ChunkSize, DefaultChunkSize)
	}
}

func TestSplit_Progress(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 300)
	chunksDir := filepath.Join(tmpDir, "chunks")

	var calls int
	progress := func(index, total int, bytes int64) {
		calls++
	}

	_, err := Split(srcPath, chunksDir, 100, progress)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("progress calls: got %d, want 3", calls)
	}
}

func TestVerify_OK(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 500)
	chunksDir := filepath.Join(tmpDir, "chunks")

	if _, err := Split(srcPath, chunksDir, 200, nil); err != nil {
		t.Fatal(err)
	}

	result, err := Verify(chunksDir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() {
		t.Fatalf("verify errors: %v", result.Errors)
	}
	if result.TotalSize != 500 {
		t.Fatalf("total size: got %d", result.TotalSize)
	}
}

func TestVerify_CorruptChunk(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 500)
	chunksDir := filepath.Join(tmpDir, "chunks")

	if _, err := Split(srcPath, chunksDir, 200, nil); err != nil {
		t.Fatal(err)
	}

	// Corrupt first chunk
	chunkPath := filepath.Join(chunksDir, "chunk_00000.bin")
	os.WriteFile(chunkPath, []byte("corrupted"), 0644)

	result, err := Verify(chunksDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK() {
		t.Fatal("expected verification errors")
	}
}

func TestVerify_MissingChunk(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 500)
	chunksDir := filepath.Join(tmpDir, "chunks")

	if _, err := Split(srcPath, chunksDir, 200, nil); err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(chunksDir, "chunk_00001.bin"))

	result, err := Verify(chunksDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK() {
		t.Fatal("expected MISSING error")
	}
}

func TestAssemble_HashMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	srcPath := createTestFile(t, tmpDir, 500)
	chunksDir := filepath.Join(tmpDir, "chunks")

	if _, err := Split(srcPath, chunksDir, 200, nil); err != nil {
		t.Fatal(err)
	}

	// Corrupt a chunk
	chunkPath := filepath.Join(chunksDir, "chunk_00000.bin")
	os.WriteFile(chunkPath, []byte("bad data here!!"), 0644)

	outPath := filepath.Join(tmpDir, "out.bin")
	err := Assemble(chunksDir, outPath, nil)
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

func TestLoadManifest_Missing(t *testing.T) {
	_, err := LoadManifest(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.00 GiB"},
		{1536, "1.5 KiB"},
	}
	for _, tt := range tests {
		got := FormatBytes(tt.input)
		if got != tt.expected {
			t.Fatalf("FormatBytes(%d): got %q, want %q", tt.input, got, tt.expected)
		}
	}
}
