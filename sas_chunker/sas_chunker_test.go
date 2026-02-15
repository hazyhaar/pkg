package sas_chunker

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// --- File-based Split tests (from main) ---

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

// --- Streaming SplitReader tests (from feature branch) ---

func TestSplitReader_Basic(t *testing.T) {
	dir := t.TempDir()

	// Create 25 KiB of data with a 10 KiB chunk size → 3 chunks.
	data := make([]byte, 25*1024)
	for i := range data {
		data[i] = byte(i % 251)
	}

	manifest, err := SplitReader(bytes.NewReader(data), "test.bin", dir, 10*1024, nil)
	if err != nil {
		t.Fatal(err)
	}

	if manifest.TotalChunks != 3 {
		t.Errorf("TotalChunks = %d, want 3", manifest.TotalChunks)
	}
	if manifest.OriginalSize != int64(len(data)) {
		t.Errorf("OriginalSize = %d, want %d", manifest.OriginalSize, len(data))
	}
	if manifest.OriginalName != "test.bin" {
		t.Errorf("OriginalName = %q, want test.bin", manifest.OriginalName)
	}

	// Verify overall SHA256 matches.
	h := sha256.Sum256(data)
	expectedHash := hex.EncodeToString(h[:])
	if manifest.OriginalSHA256 != expectedHash {
		t.Errorf("OriginalSHA256 = %s, want %s", manifest.OriginalSHA256, expectedHash)
	}

	// Verify chunk files exist and manifest.json was written.
	for _, cm := range manifest.Chunks {
		path := filepath.Join(dir, cm.FileName)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("chunk file %s missing: %v", cm.FileName, err)
			continue
		}
		if info.Size() != cm.SizeBytes {
			t.Errorf("chunk %d size = %d, want %d", cm.Index, info.Size(), cm.SizeBytes)
		}
	}

	_, err = os.Stat(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Errorf("manifest.json missing: %v", err)
	}
}

func TestSplitReader_VerifyCompatible(t *testing.T) {
	dir := t.TempDir()

	data := []byte("hello, this is a test of streaming chunker compatibility")
	_, err := SplitReader(bytes.NewReader(data), "msg.txt", dir, 20, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify using the standard Verify function.
	result, err := Verify(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() {
		t.Errorf("Verify errors: %v", result.Errors)
	}
	if result.TotalSize != int64(len(data)) {
		t.Errorf("TotalSize = %d, want %d", result.TotalSize, len(data))
	}
}

func TestSplitReader_AssembleRoundtrip(t *testing.T) {
	chunksDir := t.TempDir()
	outDir := t.TempDir()

	data := make([]byte, 50*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	_, err := SplitReader(bytes.NewReader(data), "roundtrip.bin", chunksDir, 16*1024, nil)
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(outDir, "reassembled.bin")
	if err := Assemble(chunksDir, outPath, nil); err != nil {
		t.Fatal(err)
	}

	reassembled, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, reassembled) {
		t.Error("reassembled data does not match original")
	}
}

func TestSplitReader_EmptyInput(t *testing.T) {
	dir := t.TempDir()

	manifest, err := SplitReader(bytes.NewReader(nil), "empty.bin", dir, 1024, nil)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TotalChunks != 0 {
		t.Errorf("TotalChunks = %d, want 0", manifest.TotalChunks)
	}
	if manifest.OriginalSize != 0 {
		t.Errorf("OriginalSize = %d, want 0", manifest.OriginalSize)
	}
}

func TestSplitReader_Progress(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 30)

	var calls int
	progress := func(index, total int, bytesWritten int64) {
		calls++
	}

	_, err := SplitReader(bytes.NewReader(data), "p.bin", dir, 10, progress)
	if err != nil {
		t.Fatal(err)
	}
	// 3 chunks + 1 final progress update = 4 calls.
	if calls < 3 {
		t.Errorf("progress called %d times, want >= 3", calls)
	}
}

func TestSplit_MatchesSplitReader(t *testing.T) {
	// Verify that Split and SplitReader produce identical manifests.
	data := make([]byte, 32*1024)
	for i := range data {
		data[i] = byte(i % 200)
	}

	// Write temp file for Split.
	tmpFile, err := os.CreateTemp("", "split_test_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(data)
	tmpFile.Close()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	m1, err := Split(tmpFile.Name(), dir1, 10*1024, nil)
	if err != nil {
		t.Fatal(err)
	}

	m2, err := SplitReader(bytes.NewReader(data), filepath.Base(tmpFile.Name()), dir2, 10*1024, nil)
	if err != nil {
		t.Fatal(err)
	}

	if m1.OriginalSHA256 != m2.OriginalSHA256 {
		t.Errorf("SHA256 mismatch: Split=%s SplitReader=%s", m1.OriginalSHA256, m2.OriginalSHA256)
	}
	if m1.TotalChunks != m2.TotalChunks {
		t.Errorf("TotalChunks mismatch: Split=%d SplitReader=%d", m1.TotalChunks, m2.TotalChunks)
	}
	for i := range m1.Chunks {
		if m1.Chunks[i].SHA256 != m2.Chunks[i].SHA256 {
			t.Errorf("chunk %d SHA256 mismatch", i)
		}
	}
}
