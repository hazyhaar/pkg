package trace

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRemoteStore_FlushesToEndpoint(t *testing.T) {
	var received []*Entry

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var entries []*Entry
		if err := json.Unmarshal(body, &entries); err != nil {
			t.Errorf("unmarshal: %v", err)
			http.Error(w, "bad json", 400)
			return
		}
		received = append(received, entries...)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	rs := NewRemoteStore(srv.URL, nil)

	for i := 0; i < 5; i++ {
		rs.RecordAsync(&Entry{
			TraceID:    "trc_remote",
			Op:         "Query",
			Query:      "SELECT 1",
			DurationUs: int64(i * 10),
			Timestamp:  time.Now().UnixMicro(),
		})
	}

	// Close flushes remaining entries.
	rs.Close()

	if len(received) != 5 {
		t.Fatalf("received %d entries, want 5", len(received))
	}
	if received[0].TraceID != "trc_remote" {
		t.Fatalf("trace_id: got %q", received[0].TraceID)
	}
}

func TestRemoteStore_DropOnFull(t *testing.T) {
	// Server that never reads — doesn't matter, we test the channel.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	rs := &RemoteStore{
		url:    srv.URL,
		client: &http.Client{Timeout: time.Second},
		ch:     make(chan *Entry, 2), // tiny buffer
		done:   make(chan struct{}),
	}
	go rs.flushLoop()

	// Fill the buffer.
	rs.ch <- &Entry{Op: "a", Query: "q1", Timestamp: 1}
	rs.ch <- &Entry{Op: "b", Query: "q2", Timestamp: 2}

	// This should not block — drop silently.
	done := make(chan struct{})
	go func() {
		rs.RecordAsync(&Entry{Op: "c", Query: "q3", Timestamp: 3})
		close(done)
	}()

	select {
	case <-done:
		// ok, didn't block
	case <-time.After(time.Second):
		t.Fatal("RecordAsync blocked on full channel")
	}

	rs.Close()
}

func TestRemoteStore_Close_Flushes(t *testing.T) {
	var count int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var entries []*Entry
		json.Unmarshal(body, &entries)
		count += len(entries)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	rs := NewRemoteStore(srv.URL, nil)

	rs.RecordAsync(&Entry{Op: "Exec", Query: "INSERT 1", Timestamp: 1})
	rs.RecordAsync(&Entry{Op: "Exec", Query: "INSERT 2", Timestamp: 2})

	// Close should drain.
	rs.Close()

	if count != 2 {
		t.Fatalf("flushed %d entries on close, want 2", count)
	}
}

func TestIngestHandler_WritesToStore(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := NewStore(db)
	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	handler := IngestHandler(store)

	entries := []*Entry{
		{TraceID: "trc_1", Op: "Query", Query: "SELECT 1", DurationUs: 50, Timestamp: 1000},
		{TraceID: "trc_1", Op: "Exec", Query: "INSERT INTO t VALUES(1)", DurationUs: 120, Timestamp: 2000},
	}
	body, _ := json.Marshal(entries)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/traces", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", rec.Code)
	}

	// Close store to flush the channel.
	store.Close()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM sql_traces WHERE trace_id='trc_1'").Scan(&count)
	if count != 2 {
		t.Fatalf("stored %d entries, want 2", count)
	}
}

func TestIngestHandler_RejectsGet(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	store := NewStore(db)
	store.Init()
	defer store.Close()

	handler := IngestHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/api/internal/traces", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d, want 405", rec.Code)
	}
}

func TestIngestHandler_RejectsInvalidJSON(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	defer db.Close()
	store := NewStore(db)
	store.Init()
	defer store.Close()

	handler := IngestHandler(store)

	req := httptest.NewRequest(http.MethodPost, "/api/internal/traces", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
}
