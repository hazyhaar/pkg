package feedback

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNew_NilDB(t *testing.T) {
	_, err := New(Config{DB: nil, AppName: "test"})
	if err == nil {
		t.Fatal("expected error for nil DB")
	}
	if !strings.Contains(err.Error(), "DB is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubmitAndList(t *testing.T) {
	db := openTestDB(t)
	w, err := New(Config{DB: db, AppName: "testapp"})
	if err != nil {
		t.Fatal(err)
	}

	handler := w.Handler()

	// Submit a comment.
	body := `{"text":"hello world","page_url":"https://example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("submit: got status %d, body: %s", rec.Code, rec.Body.String())
	}
	var submitResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&submitResp); err != nil {
		t.Fatal(err)
	}
	if submitResp["status"] != "ok" {
		t.Fatalf("submit: unexpected status %q", submitResp["status"])
	}
	if submitResp["id"] == "" {
		t.Fatal("submit: empty id")
	}

	// List JSON.
	req = httptest.NewRequest(http.MethodGet, "/comments", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list: got status %d", rec.Code)
	}
	var comments []Comment
	if err := json.NewDecoder(rec.Body).Decode(&comments); err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].Text != "hello world" {
		t.Fatalf("unexpected text: %q", comments[0].Text)
	}
	if comments[0].AppName != "testapp" {
		t.Fatalf("unexpected app_name: %q", comments[0].AppName)
	}
}

func TestSubmitTruncation(t *testing.T) {
	db := openTestDB(t)
	w, err := New(Config{DB: db, AppName: "trunc"})
	if err != nil {
		t.Fatal(err)
	}

	handler := w.Handler()

	longText := strings.Repeat("a", 6000)
	body, _ := json.Marshal(map[string]string{"text": longText})
	req := httptest.NewRequest(http.MethodPost, "/submit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("submit: got status %d, body: %s", rec.Code, rec.Body.String())
	}

	// Verify stored length.
	req = httptest.NewRequest(http.MethodGet, "/comments", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var comments []Comment
	json.NewDecoder(rec.Body).Decode(&comments)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if len(comments[0].Text) != 5000 {
		t.Fatalf("expected text length 5000, got %d", len(comments[0].Text))
	}
}

func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		url  string
		safe bool
	}{
		{"https://example.com", true},
		{"http://example.com", true},
		{"HTTP://EXAMPLE.COM", true},
		{"javascript:alert(1)", false},
		{"data:text/html,<h1>hi</h1>", false},
		{"ftp://example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isSafeURL(tt.url); got != tt.safe {
			t.Errorf("isSafeURL(%q) = %v, want %v", tt.url, got, tt.safe)
		}
	}
}
