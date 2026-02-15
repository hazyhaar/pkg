// Package feedback provides a self-contained feedback widget for HOROS services.
//
// It exposes both a chi-compatible [Widget.Handler] and a standard
// [Widget.RegisterMux] so callers can pick whichever router they use.
package feedback

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/hazyhaar/pkg/idgen"
)

// UserIDFunc extracts a user identifier from the HTTP request.
// Return "" for anonymous feedback.
type UserIDFunc func(r *http.Request) string

// Config holds the settings needed to create a feedback Widget.
type Config struct {
	DB       *sql.DB
	AppName  string     // e.g. "horum" or "horostracker"
	UserIDFn UserIDFunc // nil = always anonymous
}

// Comment represents a single feedback entry.
type Comment struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	PageURL   string  `json:"page_url"`
	UserAgent string  `json:"user_agent"`
	UserID    *string `json:"user_id,omitempty"`
	AppName   string  `json:"app_name"`
	CreatedAt int64   `json:"created_at"`
}

// Widget manages the feedback system (schema, HTTP handlers, embedded assets).
type Widget struct {
	db       *sql.DB
	appName  string
	userIDFn UserIDFunc
}

const schema = `
CREATE TABLE IF NOT EXISTS feedback_comments (
    id         TEXT PRIMARY KEY,
    text       TEXT NOT NULL,
    page_url   TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    user_id    TEXT,
    app_name   TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feedback_created ON feedback_comments(created_at DESC);
`

// New creates a Widget and applies the database schema.
func New(cfg Config) (*Widget, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("feedback: DB is required")
	}
	for _, stmt := range strings.Split(schema, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := cfg.DB.Exec(stmt); err != nil {
			return nil, fmt.Errorf("feedback schema: %w", err)
		}
	}
	return &Widget{
		db:       cfg.DB,
		appName:  cfg.AppName,
		userIDFn: cfg.UserIDFn,
	}, nil
}

// Handler returns an http.Handler serving all feedback endpoints.
// The caller must strip the URL prefix before passing requests.
//
//	chi:      r.Mount("/feedback", http.StripPrefix("/feedback", w.Handler()))
//	ServeMux: w.RegisterMux(mux, "/feedback")
func (w *Widget) Handler() http.Handler {
	return http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/submit":
			w.handleSubmit(wr, r)
		case r.Method == http.MethodGet && r.URL.Path == "/comments":
			w.handleListJSON(wr, r)
		case r.Method == http.MethodGet && r.URL.Path == "/comments.html":
			w.handleListHTML(wr, r)
		case r.Method == http.MethodGet && r.URL.Path == "/widget.js":
			w.handleWidgetJS(wr, r)
		case r.Method == http.MethodGet && r.URL.Path == "/widget.css":
			w.handleWidgetCSS(wr, r)
		default:
			http.NotFound(wr, r)
		}
	})
}

// RegisterMux registers feedback routes directly on a standard ServeMux
// with explicit method+path patterns (Go 1.22+).
func (w *Widget) RegisterMux(mux *http.ServeMux, basePath string) {
	bp := strings.TrimRight(basePath, "/")
	mux.HandleFunc("POST "+bp+"/submit", w.handleSubmit)
	mux.HandleFunc("GET "+bp+"/comments", w.handleListJSON)
	mux.HandleFunc("GET "+bp+"/comments.html", w.handleListHTML)
	mux.HandleFunc("GET "+bp+"/widget.js", w.handleWidgetJS)
	mux.HandleFunc("GET "+bp+"/widget.css", w.handleWidgetCSS)
}

func newID() string {
	return idgen.New()
}
