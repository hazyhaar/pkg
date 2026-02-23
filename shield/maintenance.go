package shield

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// MaintenanceMode provides a middleware that returns a 503 Service Unavailable
// page when maintenance mode is active. The flag is stored in a SQLite table
// (replicated to FO instances by dbsync) and cached in memory.
//
// Expected schema:
//
//	CREATE TABLE IF NOT EXISTS maintenance (
//	    id INTEGER PRIMARY KEY CHECK (id = 1),
//	    active INTEGER NOT NULL DEFAULT 0,
//	    message TEXT NOT NULL DEFAULT 'Maintenance en cours, veuillez patienter.'
//	);
//
// Only one row (id=1) is expected. If the table does not exist or is empty,
// maintenance mode is off.
type MaintenanceMode struct {
	db      *sql.DB
	active  atomic.Bool
	message atomic.Value // string
	exclude []string     // path prefixes that bypass maintenance (e.g. /healthz)
	page    []byte       // static HTML served during maintenance
}

// NewMaintenanceMode creates a maintenance mode checker. Paths matching any of
// excludePrefixes are never blocked (useful for health checks, static assets).
func NewMaintenanceMode(db *sql.DB, excludePrefixes ...string) *MaintenanceMode {
	m := &MaintenanceMode{
		db:      db,
		exclude: excludePrefixes,
	}
	m.message.Store("Maintenance en cours, veuillez patienter.")
	m.reload()
	return m
}

// SetDB replaces the database connection and reloads the flag.
// Used in FO mode when the dbsync subscriber swaps the database.
func (m *MaintenanceMode) SetDB(db *sql.DB) {
	m.db = db
	m.reload()
}

// Active reports whether maintenance mode is currently on.
func (m *MaintenanceMode) Active() bool {
	return m.active.Load()
}

// Message returns the current maintenance message.
func (m *MaintenanceMode) Message() string {
	s, _ := m.message.Load().(string)
	return s
}

// SetPage sets custom HTML to serve during maintenance. If not set, a minimal
// default page is used. The HTML is served as-is with Content-Type text/html.
func (m *MaintenanceMode) SetPage(html []byte) {
	m.page = html
}

// StartReloader starts a background goroutine that reloads the maintenance
// flag every 5 seconds. Stops when done is closed.
func (m *MaintenanceMode) StartReloader(done <-chan struct{}) {
	tick := time.NewTicker(5 * time.Second)
	go func() {
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				m.reload()
			}
		}
	}()
}

func (m *MaintenanceMode) reload() {
	var active int
	var message string
	err := m.db.QueryRow(`SELECT active, message FROM maintenance WHERE id = 1`).Scan(&active, &message)
	if err != nil {
		// Table missing or empty → maintenance off (normal state).
		if m.active.Load() {
			slog.Info("maintenance: flag cleared (table missing or empty)")
		}
		m.active.Store(false)
		return
	}

	was := m.active.Load()
	m.active.Store(active == 1)
	if message != "" {
		m.message.Store(message)
	}

	if active == 1 && !was {
		slog.Warn("maintenance: mode ENABLED", "message", message)
	} else if active != 1 && was {
		slog.Info("maintenance: mode DISABLED")
	}
}

// Middleware returns an HTTP middleware that blocks requests with a 503 page
// when maintenance mode is active. Excluded prefixes pass through.
func (m *MaintenanceMode) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.active.Load() {
			next.ServeHTTP(w, r)
			return
		}

		// Let excluded paths through (healthz, static, etc.).
		for _, prefix := range m.exclude {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(http.StatusServiceUnavailable)

		if len(m.page) > 0 {
			w.Write(m.page)
			return
		}

		msg := m.Message()
		w.Write([]byte(defaultMaintenancePage(msg)))
	})
}

func defaultMaintenancePage(message string) string {
	return `<!DOCTYPE html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Maintenance</title>
<style>
  body { font-family: system-ui, sans-serif; display: flex; align-items: center;
         justify-content: center; min-height: 100vh; margin: 0; background: #f8f9fa; color: #333; }
  .box { text-align: center; max-width: 480px; padding: 2rem; }
  h1 { font-size: 1.5rem; margin-bottom: .5rem; }
  p  { color: #666; }
</style>
</head>
<body>
<div class="box">
  <h1>Maintenance</h1>
  <p>` + message + `</p>
</div>
</body>
</html>`
}
