package trace

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
)

// IngestHandler returns an HTTP handler that receives trace batches from
// a RemoteStore (FO side) and writes them to the local Store (BO side).
//
// Expected request: POST with application/json body containing []*Entry.
// Returns 204 on success, 405 for wrong method, 400 for bad payload.
//
// Mount on the BO:
//
//	mux.Handle("/api/internal/traces", trace.IngestHandler(store))
func IngestHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var entries []*Entry
		if err := json.Unmarshal(body, &entries); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		for _, e := range entries {
			if e != nil {
				store.RecordAsync(e)
			}
		}

		slog.Debug("trace ingest", "entries", len(entries))
		w.WriteHeader(http.StatusNoContent)
	}
}
