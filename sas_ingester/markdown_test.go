package sas_ingester

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hazyhaar/pkg/connectivity"
)

// --- Store markdown CRUD ---

func TestStoreMarkdown(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "d-md", OwnerJWTSub: "u", CreatedAt: now})
	s.InsertPiece(&Piece{SHA256: "sha-md", DossierID: "d-md", State: "ready", CreatedAt: now, UpdatedAt: now})

	// Store markdown.
	if err := s.StoreMarkdown("sha-md", "d-md", "# Hello\n\nWorld"); err != nil {
		t.Fatal(err)
	}

	// Get markdown.
	md, err := s.GetMarkdown("sha-md", "d-md")
	if err != nil {
		t.Fatal(err)
	}
	if md != "# Hello\n\nWorld" {
		t.Errorf("markdown = %q, want %q", md, "# Hello\n\nWorld")
	}

	// HasMarkdown.
	has, err := s.HasMarkdown("sha-md", "d-md")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("HasMarkdown should be true")
	}

	// HasMarkdown — not found.
	has, _ = s.HasMarkdown("nope", "d-md")
	if has {
		t.Error("HasMarkdown should be false for nonexistent piece")
	}

	// GetMarkdown — not found.
	md, _ = s.GetMarkdown("nope", "d-md")
	if md != "" {
		t.Errorf("expected empty, got %q", md)
	}

	// ListMarkdownByDossier.
	list, err := s.ListMarkdownByDossier("d-md")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].SHA256 != "sha-md" {
		t.Errorf("list[0].SHA256 = %q", list[0].SHA256)
	}

	// StoreMarkdown again (replace).
	if err := s.StoreMarkdown("sha-md", "d-md", "# Updated"); err != nil {
		t.Fatal(err)
	}
	md, _ = s.GetMarkdown("sha-md", "d-md")
	if md != "# Updated" {
		t.Errorf("after replace, markdown = %q", md)
	}
}

// --- Markdown cascade delete ---

func TestMarkdownCascadeDelete(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "d-cascade", OwnerJWTSub: "u", CreatedAt: now})
	s.InsertPiece(&Piece{SHA256: "sha-c", DossierID: "d-cascade", State: "ready", CreatedAt: now, UpdatedAt: now})
	s.StoreMarkdown("sha-c", "d-cascade", "# Will be deleted")

	// Delete dossier — should cascade to pieces and markdown.
	if err := s.DeleteDossier("d-cascade"); err != nil {
		t.Fatal(err)
	}

	has, _ := s.HasMarkdown("sha-c", "d-cascade")
	if has {
		t.Error("markdown should have been cascade-deleted")
	}
}

// --- WithMarkdownConverter option ---

func TestWithMarkdownConverter(t *testing.T) {
	called := false
	converter := func(ctx context.Context, filePath, mime string) (string, error) {
		called = true
		return "# Converted", nil
	}

	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	ing, err := NewIngester(cfg, WithMarkdownConverter(converter))
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	if ing.MarkdownConverter == nil {
		t.Fatal("MarkdownConverter should be set")
	}

	// We can't easily test the full pipeline without real chunks,
	// but we verify the option wiring.
	_ = called
}

// --- Connectivity: sas_create_context (authenticated) ---

func TestConnectivityCreateContext(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	ing, err := NewIngester(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	// Create context with owner_sub (service-to-service auth).
	resp, err := router.Call(context.Background(), "sas_create_context",
		[]byte(`{"owner_sub":"user_test","name":"test jetable"}`))
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatal(err)
	}

	dossierID, ok := result["dossier_id"].(string)
	if !ok || dossierID == "" {
		t.Fatal("expected dossier_id in response")
	}
	if result["name"] != "test jetable" {
		t.Errorf("name = %q, want %q", result["name"], "test jetable")
	}
	if _, ok := result["howto"]; !ok {
		t.Error("expected howto in response")
	}

	// Verify dossier exists in DB with correct owner.
	d, err := ing.Store.GetDossier(dossierID)
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("dossier should exist after sas_create_context")
	}
	if d.OwnerJWTSub != "user_test" {
		t.Errorf("owner = %q, want user_test", d.OwnerJWTSub)
	}
}

// --- Connectivity: auth required ---

func TestConnectivityCreateContextNoAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	ing, err := NewIngester(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	// No owner_sub, no horoskey — should fail.
	_, err = router.Call(context.Background(), "sas_create_context",
		[]byte(`{"name":"test"}`))
	if err == nil {
		t.Error("expected auth error")
	}
}

// --- Connectivity: sas_create_context default name ---

func TestConnectivityCreateContextDefaultName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	ing, err := NewIngester(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	resp, err := router.Call(context.Background(), "sas_create_context",
		[]byte(`{"owner_sub":"user_test"}`))
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(resp, &result)
	if result["name"] != "contexte jetable" {
		t.Errorf("default name = %q, want %q", result["name"], "contexte jetable")
	}
}

// --- Connectivity: sas_query_piece not found ---

func TestConnectivityQueryPieceNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	ing, err := NewIngester(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	_, err = router.Call(context.Background(), "sas_query_piece",
		[]byte(`{"owner_sub":"user_test","dossier_id":"none","sha256":"none"}`))
	if err == nil {
		t.Error("expected error for nonexistent piece")
	}
}

// --- Connectivity: sas_list_pieces empty ---

func TestConnectivityListPiecesEmpty(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	ing, err := NewIngester(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	resp, err := router.Call(context.Background(), "sas_list_pieces",
		[]byte(`{"owner_sub":"user_test","dossier_id":"d-empty"}`))
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(resp, &result)
	count := result["count"].(float64)
	if count != 0 {
		t.Errorf("count = %v, want 0", count)
	}
}

// --- Connectivity: horoskey auth with KeyResolver ---

func TestConnectivityHoroskeyAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/test.db"
	cfg.ChunksDir = t.TempDir()

	resolver := func(ctx context.Context, key string) (string, error) {
		if key == "hk_valid_test_key" {
			return "owner_from_key", nil
		}
		return "", fmt.Errorf("invalid key")
	}

	ing, err := NewIngester(cfg, WithKeyResolver(resolver))
	if err != nil {
		t.Fatal(err)
	}
	defer ing.Close()

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	// Create context with horoskey.
	resp, err := router.Call(context.Background(), "sas_create_context",
		[]byte(`{"horoskey":"hk_valid_test_key","name":"via key"}`))
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]any
	json.Unmarshal(resp, &result)
	dossierID := result["dossier_id"].(string)

	d, _ := ing.Store.GetDossier(dossierID)
	if d.OwnerJWTSub != "owner_from_key" {
		t.Errorf("owner = %q, want owner_from_key", d.OwnerJWTSub)
	}

	// Invalid horoskey should fail.
	_, err = router.Call(context.Background(), "sas_create_context",
		[]byte(`{"horoskey":"hk_bad_key"}`))
	if err == nil {
		t.Error("expected error for invalid horoskey")
	}
}

// --- MaxBase64Bytes constant ---

func TestMaxBase64Bytes(t *testing.T) {
	if MaxBase64Bytes != 10*1024*1024 {
		t.Errorf("MaxBase64Bytes = %d, want %d", MaxBase64Bytes, 10*1024*1024)
	}
}

// --- IngestResult has MarkdownText field ---

func TestIngestResultMarkdownField(t *testing.T) {
	result := &IngestResult{
		SHA256:       "abc",
		DossierID:    "d-1",
		State:        "ready",
		MarkdownText: "# Hello",
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	if m["markdown_text"] != "# Hello" {
		t.Errorf("markdown_text = %v", m["markdown_text"])
	}

	// Omitempty: empty string should not appear.
	result2 := &IngestResult{SHA256: "abc", DossierID: "d-1", State: "ready"}
	data2, _ := json.Marshal(result2)
	var m2 map[string]any
	json.Unmarshal(data2, &m2)
	if _, ok := m2["markdown_text"]; ok {
		t.Error("empty markdown_text should be omitted")
	}
}
