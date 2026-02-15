package sas_ingester

import (
	"os"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "sas_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDossierCRUD(t *testing.T) {
	s := tempStore(t)

	d := &Dossier{
		ID:          "d-001",
		OwnerJWTSub: "user-abc",
		Name:        "Test Dossier",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.CreateDossier(d); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetDossier("d-001")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected dossier, got nil")
	}
	if got.Name != "Test Dossier" {
		t.Errorf("name = %q, want %q", got.Name, "Test Dossier")
	}

	// Not found
	got, err = s.GetDossier("nope")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	// Delete
	if err := s.DeleteDossier("d-001"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetDossier("d-001")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestEnsureDossier(t *testing.T) {
	s := tempStore(t)

	// Creates new dossier.
	if err := s.EnsureDossier("d-002", "owner-1"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.GetDossier("d-002")
	if d == nil {
		t.Fatal("expected dossier")
	}

	// Same owner => OK.
	if err := s.EnsureDossier("d-002", "owner-1"); err != nil {
		t.Fatal(err)
	}

	// Different owner => error.
	err := s.EnsureDossier("d-002", "owner-2")
	if err == nil {
		t.Error("expected owner mismatch error")
	}
}

func TestPieceCRUD(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "d-100", OwnerJWTSub: "u", CreatedAt: now})

	p := &Piece{
		SHA256:        "abc123",
		DossierID:     "d-100",
		State:         "received",
		SizeBytes:     1024,
		InjectionRisk: "none",
		ClamAVStatus:  "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.InsertPiece(p); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetPiece("abc123", "d-100")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected piece")
	}
	if got.State != "received" {
		t.Errorf("state = %q, want received", got.State)
	}

	// Update state.
	s.UpdatePieceState("abc123", "d-100", "ready")
	got, _ = s.GetPiece("abc123", "d-100")
	if got.State != "ready" {
		t.Errorf("state = %q, want ready", got.State)
	}

	// List pieces.
	pieces, err := s.ListPieces("d-100")
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) != 1 {
		t.Errorf("got %d pieces, want 1", len(pieces))
	}

	// Count.
	c, _ := s.PiecesCount("ready")
	if c != 1 {
		t.Errorf("count = %d, want 1", c)
	}
}

func TestChunks(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "d-200", OwnerJWTSub: "u", CreatedAt: now})
	s.InsertPiece(&Piece{SHA256: "sha1", DossierID: "d-200", State: "received", CreatedAt: now, UpdatedAt: now})

	if err := s.InsertChunk("sha1", "d-200", 0, "cshaA", true); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertChunk("sha1", "d-200", 1, "cshaB", false); err != nil {
		t.Fatal(err)
	}

	// Mark received.
	if err := s.MarkChunkReceived("sha1", "d-200", 1); err != nil {
		t.Fatal(err)
	}
}

func TestDossierRoutes(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "d-routes", OwnerJWTSub: "u", CreatedAt: now})

	// Set per-dossier routes.
	routes := []DossierRoute{
		{URL: "https://rag.internal/ingest", AuthMode: "opaque_only", Secret: "rag-key-123"},
		{URL: "https://forum.internal/notify", AuthMode: "jwt_passthru", Secret: "forum-key"},
	}
	if err := s.SetDossierRoutes("d-routes", routes); err != nil {
		t.Fatal(err)
	}

	// Retrieve and parse.
	d, err := s.GetDossier("d-routes")
	if err != nil {
		t.Fatal(err)
	}
	parsed := d.ParsedRoutes()
	if len(parsed) != 2 {
		t.Fatalf("ParsedRoutes len = %d, want 2", len(parsed))
	}
	if parsed[0].URL != "https://rag.internal/ingest" {
		t.Errorf("route[0].URL = %q", parsed[0].URL)
	}
	if parsed[0].Secret != "rag-key-123" {
		t.Errorf("route[0].Secret = %q", parsed[0].Secret)
	}
	if parsed[1].AuthMode != "jwt_passthru" {
		t.Errorf("route[1].AuthMode = %q", parsed[1].AuthMode)
	}

	// Clear routes.
	if err := s.SetDossierRoutes("d-routes", nil); err != nil {
		t.Fatal(err)
	}
	d, _ = s.GetDossier("d-routes")
	if parsed := d.ParsedRoutes(); len(parsed) != 0 {
		t.Errorf("expected empty routes after clear, got %d", len(parsed))
	}
}

func TestRoutes(t *testing.T) {
	s := tempStore(t)

	now := time.Now().UTC().Format(time.RFC3339)
	s.CreateDossier(&Dossier{ID: "d-300", OwnerJWTSub: "u", CreatedAt: now})
	s.InsertPiece(&Piece{SHA256: "sha2", DossierID: "d-300", State: "ready", CreatedAt: now, UpdatedAt: now})

	r := &RoutePending{
		PieceSHA256: "sha2",
		DossierID:   "d-300",
		Target:      "https://example.com/hook",
		AuthMode:    "opaque_only",
	}
	if err := s.InsertRoute(r); err != nil {
		t.Fatal(err)
	}

	routes, err := s.ListRoutes("sha2", "d-300")
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}

	// Retry logic.
	retryable, _ := s.ListRetryableRoutes(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if len(retryable) != 1 {
		t.Errorf("retryable = %d, want 1", len(retryable))
	}

	// Delete route.
	s.DeleteRoute("sha2", "d-300", "https://example.com/hook")
	routes, _ = s.ListRoutes("sha2", "d-300")
	if len(routes) != 0 {
		t.Errorf("routes after delete = %d, want 0", len(routes))
	}
}
