package sas_ingester

import (
	"bytes"
	"testing"
	"time"
)

func TestTusUploadCRUD(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	u := &TusUpload{
		UploadID:    "tus_001",
		DossierID:   "dos_001",
		OwnerJWTSub: "user-a",
		TotalSize:   1024,
		OffsetBytes: 0,
		ChunkDir:    "/tmp/chunks/tus_001",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.CreateTusUpload(u); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTusUpload("tus_001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected tus upload")
	}
	if got.TotalSize != 1024 {
		t.Errorf("TotalSize = %d, want 1024", got.TotalSize)
	}
	if got.Completed {
		t.Error("expected Completed=false")
	}

	// Update offset.
	if err := s.UpdateTusOffset("tus_001", 512); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTusUpload("tus_001")
	if got.OffsetBytes != 512 {
		t.Errorf("OffsetBytes = %d, want 512", got.OffsetBytes)
	}

	// Complete.
	if err := s.CompleteTusUpload("tus_001"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTusUpload("tus_001")
	if !got.Completed {
		t.Error("expected Completed=true")
	}

	// Not found.
	got, err = s.GetTusUpload("nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	// Delete.
	if err := s.DeleteTusUpload("tus_001"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTusUpload("tus_001")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestTusHandler_CreateAndPatch(t *testing.T) {
	s := tempStore(t)
	cfg := DefaultConfig()
	cfg.ChunksDir = t.TempDir()

	// Ensure dossier exists.
	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "dos_tus", OwnerJWTSub: "u", CreatedAt: now})

	seq := 0
	gen := func() string {
		seq++
		return "tus_test_" + string(rune('0'+seq))
	}

	h := NewTusHandler(s, cfg, gen)

	// Create upload for 30 bytes.
	u, err := h.Create("dos_tus", "u", 30)
	if err != nil {
		t.Fatal(err)
	}
	if u.OffsetBytes != 0 {
		t.Errorf("initial offset = %d", u.OffsetBytes)
	}

	// Patch: send first 15 bytes.
	data1 := bytes.Repeat([]byte("A"), 15)
	off, err := h.Patch(u.UploadID, 0, bytes.NewReader(data1))
	if err != nil {
		t.Fatal(err)
	}
	if off != 15 {
		t.Errorf("offset after first patch = %d, want 15", off)
	}

	// HEAD: check offset.
	u2, err := h.GetOffset(u.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if u2.OffsetBytes != 15 {
		t.Errorf("stored offset = %d, want 15", u2.OffsetBytes)
	}

	// Patch: send remaining 15 bytes.
	data2 := bytes.Repeat([]byte("B"), 15)
	off, err = h.Patch(u.UploadID, 15, bytes.NewReader(data2))
	if err != nil {
		t.Fatal(err)
	}
	if off != 30 {
		t.Errorf("offset after second patch = %d, want 30", off)
	}

	// Complete.
	result, err := h.Complete(u.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA256 == "" {
		t.Error("expected non-empty SHA256")
	}
	if result.SizeBytes != 30 {
		t.Errorf("SizeBytes = %d, want 30", result.SizeBytes)
	}
	if result.ChunkCount < 1 {
		t.Errorf("ChunkCount = %d, want >= 1", result.ChunkCount)
	}
}

func TestTusHandler_OffsetMismatch(t *testing.T) {
	s := tempStore(t)
	cfg := DefaultConfig()
	cfg.ChunksDir = t.TempDir()

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "dos_tus2", OwnerJWTSub: "u", CreatedAt: now})

	h := NewTusHandler(s, cfg, func() string { return "tus_mismatch" })

	u, err := h.Create("dos_tus2", "u", 100)
	if err != nil {
		t.Fatal(err)
	}

	// Try to patch with wrong offset.
	_, err = h.Patch(u.UploadID, 50, bytes.NewReader([]byte("data")))
	if err == nil {
		t.Error("expected offset mismatch error")
	}
}

func TestTusHandler_SizeExceeded(t *testing.T) {
	s := tempStore(t)
	cfg := DefaultConfig()
	cfg.MaxFileMB = 1 // 1 MB max

	h := NewTusHandler(s, cfg, func() string { return "tus_size" })

	// Try to create upload larger than max.
	_, err := h.Create("dos_big", "u", 2*1024*1024)
	if err == nil {
		t.Error("expected size exceeded error")
	}
}
