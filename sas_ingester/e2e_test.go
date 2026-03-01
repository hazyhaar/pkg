package sas_ingester

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hazyhaar/pkg/apikey"
	"github.com/hazyhaar/pkg/connectivity"
)

// tempKeyStore creates a temporary apikey.Store for testing.
func tempKeyStore(t *testing.T) *apikey.Store {
	t.Helper()
	f, err := os.CreateTemp("", "apikey_e2e_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	s, err := apikey.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// e2eIngester creates a fully wired ingester with a real apikey.Store as KeyResolver.
func e2eIngester(t *testing.T) (*Ingester, *connectivity.Router, *apikey.Store) {
	t.Helper()

	cfg := DefaultConfig()
	cfg.DBPath = t.TempDir() + "/e2e.db"
	cfg.ChunksDir = t.TempDir()

	keyStore := tempKeyStore(t)

	resolver := func(ctx context.Context, hk string) (string, error) {
		key, err := keyStore.Resolve(hk)
		if err != nil {
			return "", err
		}
		if !key.HasService("sas_ingester") {
			return "", fmt.Errorf("key not authorized for sas_ingester")
		}
		return key.OwnerID, nil
	}

	converter := func(ctx context.Context, filePath, mime string) (string, error) {
		return "# Mock markdown\n\nConverted from " + mime, nil
	}

	ing, err := NewIngester(cfg,
		WithKeyResolver(resolver),
		WithMarkdownConverter(converter),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ing.Close() })

	router := connectivity.New()
	RegisterConnectivity(router, ing)

	return ing, router, keyStore
}

// TestE2E_FullRoundTrip tests the complete flow:
// generate API key → create context → upload piece → query piece → get markdown.
func TestE2E_FullRoundTrip(t *testing.T) {
	ing, router, keyStore := e2eIngester(t)

	// 1. Generate an API key with sas_ingester access.
	clearKey, key, err := keyStore.Generate("e2e_key_1", "user_e2e", "E2E Test Key", []string{"sas_ingester"}, 0)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	if key.OwnerID != "user_e2e" {
		t.Fatalf("key owner = %q", key.OwnerID)
	}

	// 2. Create context via connectivity with horoskey.
	resp, err := router.Call(context.Background(), "sas_create_context",
		mustJSON(map[string]any{"horoskey": clearKey, "name": "E2E dossier"}))
	if err != nil {
		t.Fatalf("sas_create_context: %v", err)
	}

	var ctxResult map[string]any
	_ = json.Unmarshal(resp, &ctxResult)
	dossierID, ok := ctxResult["dossier_id"].(string)
	if !ok || dossierID == "" {
		t.Fatalf("no dossier_id in response: %v", ctxResult)
	}

	// Verify dossier owner is the key owner.
	d, _ := ing.Store.GetDossier(dossierID)
	if d.OwnerJWTSub != "user_e2e" {
		t.Errorf("dossier owner = %q, want user_e2e", d.OwnerJWTSub)
	}

	// 3. Upload a small text file via connectivity.
	fileContent := []byte("Hello, this is a test file for the SAS ingester pipeline.")
	b64 := base64.StdEncoding.EncodeToString(fileContent)

	resp, err = router.Call(context.Background(), "sas_upload_piece",
		mustJSON(map[string]any{
			"horoskey":       clearKey,
			"dossier_id":     dossierID,
			"filename":       "test.txt",
			"content_base64": b64,
		}))
	if err != nil {
		t.Fatalf("sas_upload_piece: %v", err)
	}

	var uploadResult map[string]any
	_ = json.Unmarshal(resp, &uploadResult)
	sha256, ok := uploadResult["sha256"].(string)
	if !ok || sha256 == "" {
		t.Fatalf("no sha256 in upload result: %v", uploadResult)
	}
	state, _ := uploadResult["state"].(string)
	if state != "ready" && state != "flagged" {
		t.Errorf("unexpected state %q after upload", state)
	}

	// 4. Query piece via connectivity.
	resp, err = router.Call(context.Background(), "sas_query_piece",
		mustJSON(map[string]any{
			"horoskey":   clearKey,
			"dossier_id": dossierID,
			"sha256":     sha256,
		}))
	if err != nil {
		t.Fatalf("sas_query_piece: %v", err)
	}

	var queryResult map[string]any
	_ = json.Unmarshal(resp, &queryResult)
	hasMd, _ := queryResult["has_markdown"].(bool)
	if !hasMd {
		t.Error("expected has_markdown=true after upload with MarkdownConverter")
	}

	// 5. Get markdown via connectivity.
	resp, err = router.Call(context.Background(), "sas_get_markdown",
		mustJSON(map[string]any{
			"horoskey":   clearKey,
			"dossier_id": dossierID,
			"sha256":     sha256,
		}))
	if err != nil {
		t.Fatalf("sas_get_markdown: %v", err)
	}

	var mdResult map[string]any
	_ = json.Unmarshal(resp, &mdResult)
	md, _ := mdResult["markdown"].(string)
	if md == "" {
		t.Error("expected non-empty markdown")
	}

	// 6. List pieces.
	resp, err = router.Call(context.Background(), "sas_list_pieces",
		mustJSON(map[string]any{
			"horoskey":   clearKey,
			"dossier_id": dossierID,
		}))
	if err != nil {
		t.Fatalf("sas_list_pieces: %v", err)
	}

	var listResult map[string]any
	_ = json.Unmarshal(resp, &listResult)
	count, _ := listResult["count"].(float64)
	if count != 1 {
		t.Errorf("list count = %v, want 1", count)
	}
}

// TestE2E_AuthRejection verifies that bad/missing/revoked keys are rejected.
func TestE2E_AuthRejection(t *testing.T) {
	_, router, keyStore := e2eIngester(t)

	t.Run("no_auth", func(t *testing.T) {
		_, err := router.Call(context.Background(), "sas_create_context",
			mustJSON(map[string]any{"name": "test"}))
		if err == nil {
			t.Error("expected error without auth")
		}
	})

	t.Run("bad_key", func(t *testing.T) {
		_, err := router.Call(context.Background(), "sas_create_context",
			mustJSON(map[string]any{"horoskey": "hk_0000000000000000000000000000000000000000000000000000000000000000"}))
		if err == nil {
			t.Error("expected error with unknown key")
		}
	})

	t.Run("revoked_key", func(t *testing.T) {
		clearKey, _, _ := keyStore.Generate("e2e_revoked", "user_r", "Revoked", []string{"sas_ingester"}, 0)
		_ = keyStore.Revoke("e2e_revoked")

		_, err := router.Call(context.Background(), "sas_create_context",
			mustJSON(map[string]any{"horoskey": clearKey, "name": "test"}))
		if err == nil {
			t.Error("expected error with revoked key")
		}
	})

	t.Run("expired_key", func(t *testing.T) {
		clearKey, _, _ := keyStore.Generate("e2e_expired", "user_x", "Expired", []string{"sas_ingester"}, 0)
		past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		_ = keyStore.SetExpiry("e2e_expired", past)

		_, err := router.Call(context.Background(), "sas_create_context",
			mustJSON(map[string]any{"horoskey": clearKey, "name": "test"}))
		if err == nil {
			t.Error("expected error with expired key")
		}
	})

	t.Run("wrong_service", func(t *testing.T) {
		clearKey, _, _ := keyStore.Generate("e2e_wrong_svc", "user_w", "Wrong Svc", []string{"horum"}, 0)

		_, err := router.Call(context.Background(), "sas_create_context",
			mustJSON(map[string]any{"horoskey": clearKey, "name": "test"}))
		if err == nil {
			t.Error("expected error with key not authorized for sas_ingester")
		}
	})
}

// TestE2E_OwnerSubAuth verifies service-to-service auth via owner_sub.
func TestE2E_OwnerSubAuth(t *testing.T) {
	ing, router, _ := e2eIngester(t)

	resp, err := router.Call(context.Background(), "sas_create_context",
		mustJSON(map[string]any{"owner_sub": "service_internal", "name": "S2S dossier"}))
	if err != nil {
		t.Fatalf("sas_create_context with owner_sub: %v", err)
	}

	var result map[string]any
	_ = json.Unmarshal(resp, &result)
	dossierID := result["dossier_id"].(string)

	d, _ := ing.Store.GetDossier(dossierID)
	if d.OwnerJWTSub != "service_internal" {
		t.Errorf("owner = %q, want service_internal", d.OwnerJWTSub)
	}
}

// TestE2E_Dedup verifies that uploading the same file twice is deduplicated.
func TestE2E_Dedup(t *testing.T) {
	_, router, keyStore := e2eIngester(t)

	clearKey, _, _ := keyStore.Generate("e2e_dedup", "user_d", "Dedup", []string{"sas_ingester"}, 0)

	// Create context.
	resp, _ := router.Call(context.Background(), "sas_create_context",
		mustJSON(map[string]any{"horoskey": clearKey, "name": "dedup test"}))
	var ctx map[string]any
	_ = json.Unmarshal(resp, &ctx)
	dossierID := ctx["dossier_id"].(string)

	fileContent := []byte("duplicate content for dedup test")
	b64 := base64.StdEncoding.EncodeToString(fileContent)
	payload := mustJSON(map[string]any{
		"horoskey":       clearKey,
		"dossier_id":     dossierID,
		"content_base64": b64,
	})

	// First upload.
	resp1, err := router.Call(context.Background(), "sas_upload_piece", payload)
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}

	// Second upload — same content.
	resp2, err := router.Call(context.Background(), "sas_upload_piece", payload)
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}

	var r1, r2 map[string]any
	_ = json.Unmarshal(resp1, &r1)
	_ = json.Unmarshal(resp2, &r2)

	// Same SHA256.
	if r1["sha256"] != r2["sha256"] {
		t.Errorf("sha256 mismatch: %v vs %v", r1["sha256"], r2["sha256"])
	}

	// Second should be deduplicated.
	if dedup, _ := r2["deduplicated"].(bool); !dedup {
		t.Error("second upload should be deduplicated")
	}
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
