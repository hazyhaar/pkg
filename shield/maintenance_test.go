package shield

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func setupMaintenanceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE maintenance (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			active INTEGER NOT NULL DEFAULT 0,
			message TEXT NOT NULL DEFAULT 'Maintenance en cours, veuillez patienter.'
		);
		INSERT INTO maintenance (id, active, message) VALUES (1, 0, 'Maintenance en cours, veuillez patienter.');
	`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
}

func TestMaintenance_Off(t *testing.T) {
	db := setupMaintenanceDB(t)
	mm := NewMaintenanceMode(db)

	handler := mm.Middleware(okHandler())
	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when maintenance off, got %d", w.Code)
	}
	if w.Body.String() != "OK" {
		t.Errorf("expected OK body, got %q", w.Body.String())
	}
}

func TestMaintenance_On(t *testing.T) {
	db := setupMaintenanceDB(t)
	db.Exec(`UPDATE maintenance SET active = 1, message = 'On met à jour' WHERE id = 1`)

	mm := NewMaintenanceMode(db)

	handler := mm.Middleware(okHandler())
	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when maintenance on, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "On met à jour") {
		t.Errorf("expected maintenance message in body, got %q", w.Body.String())
	}
	if ra := w.Header().Get("Retry-After"); ra != "300" {
		t.Errorf("expected Retry-After: 300, got %q", ra)
	}
}

func TestMaintenance_ExcludedPath(t *testing.T) {
	db := setupMaintenanceDB(t)
	db.Exec(`UPDATE maintenance SET active = 1 WHERE id = 1`)

	mm := NewMaintenanceMode(db, "/healthz", "/static/")

	handler := mm.Middleware(okHandler())

	for _, path := range []string{"/healthz", "/static/style.css"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("path %q should bypass maintenance, got %d", path, w.Code)
		}
	}
}

func TestMaintenance_CustomPage(t *testing.T) {
	db := setupMaintenanceDB(t)
	db.Exec(`UPDATE maintenance SET active = 1 WHERE id = 1`)

	mm := NewMaintenanceMode(db)
	mm.SetPage([]byte(`<html><body>Custom maintenance</body></html>`))

	handler := mm.Middleware(okHandler())
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Custom maintenance") {
		t.Errorf("expected custom page, got %q", w.Body.String())
	}
}

func TestMaintenance_NoTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// No maintenance table — should not panic, maintenance off.
	mm := NewMaintenanceMode(db)
	if mm.Active() {
		t.Error("expected maintenance off when table missing")
	}

	handler := mm.Middleware(okHandler())
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when no table, got %d", w.Code)
	}
}

func TestMaintenance_Toggle(t *testing.T) {
	db := setupMaintenanceDB(t)
	mm := NewMaintenanceMode(db)

	if mm.Active() {
		t.Fatal("expected off initially")
	}

	// Turn on.
	db.Exec(`UPDATE maintenance SET active = 1 WHERE id = 1`)
	mm.reload()
	if !mm.Active() {
		t.Fatal("expected on after toggle")
	}

	// Turn off.
	db.Exec(`UPDATE maintenance SET active = 0 WHERE id = 1`)
	mm.reload()
	if mm.Active() {
		t.Fatal("expected off after second toggle")
	}
}

func TestMaintenance_SetDB(t *testing.T) {
	db1 := setupMaintenanceDB(t)
	mm := NewMaintenanceMode(db1)

	// db2 has maintenance on.
	db2 := setupMaintenanceDB(t)
	db2.Exec(`UPDATE maintenance SET active = 1, message = 'DB swapped' WHERE id = 1`)

	mm.SetDB(db2)

	if !mm.Active() {
		t.Error("expected active after SetDB")
	}
	if mm.Message() != "DB swapped" {
		t.Errorf("expected message 'DB swapped', got %q", mm.Message())
	}
}

func TestMaintenance_APIPath(t *testing.T) {
	db := setupMaintenanceDB(t)
	db.Exec(`UPDATE maintenance SET active = 1 WHERE id = 1`)

	mm := NewMaintenanceMode(db)
	handler := mm.Middleware(okHandler())

	req := httptest.NewRequest("GET", "/api/data", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("API paths should also get 503, got %d", w.Code)
	}
}
