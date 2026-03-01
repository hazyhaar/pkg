
# feedback -- Technical Schema
# Self-contained feedback widget with submit, list, and embedded JS/CSS assets

```
╔══════════════════════════════════════════════════════════════════════════════════╗
║  feedback — Embeddable feedback widget (form + list + assets) for HOROS svcs   ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  CONFIG                                                                        ║
║  ──────                                                                        ║
║  Config {                                                                      ║
║    DB       *sql.DB       ← required (nil = error)                             ║
║    AppName  string        ← e.g. "horum", "repvow"                             ║
║    UserIDFn UserIDFunc    ← func(r) string, nil = anonymous                    ║
║  }                                                                             ║
║                                                                                ║
║       New(Config)                                                              ║
║           │                                                                    ║
║           ▼                                                                    ║
║  ┌─────────────────────┐  auto-creates feedback_comments table                 ║
║  │      Widget          │  via embedded DDL on construction                    ║
║  └────────┬─────────────┘                                                      ║
║           │                                                                    ║
║    ┌──────┴──────────────────────────────────────────┐                          ║
║    │                                                  │                        ║
║    ▼                                                  ▼                        ║
║  Handler() http.Handler              RegisterMux(mux, basePath)                ║
║  (chi: Mount + StripPrefix)          (Go 1.22+ ServeMux patterns)              ║
║                                                                                ║
╚══════════════════════════════════════════════════════════════════════════════════╝
```

## HTTP Routes

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  Method │ Path              │ Handler         │ Description                  │
├─────────┼───────────────────┼─────────────────┼──────────────────────────────┤
│  POST   │ /submit           │ handleSubmit    │ Submit a feedback comment    │
│  GET    │ /comments         │ handleListJSON  │ List comments (JSON, paged)  │
│  GET    │ /comments.html    │ handleListHTML  │ List comments (HTML page)    │
│  GET    │ /widget.js        │ handleWidgetJS  │ Embedded JS (1h cache)       │
│  GET    │ /widget.css       │ handleWidgetCSS │ Embedded CSS (1h cache)      │
└─────────┴───────────────────┴─────────────────┴──────────────────────────────┘
```

## Data Flow

```
  Browser                         Widget                        SQLite
  ───────                         ──────                        ──────

  POST /submit ──────────────────→ handleSubmit
  {text, page_url}                  │ MaxBytesReader(32KB)
  Content-Type: application/json    │ Trim + truncate text (5000 chars)
                                    │ Extract user_agent from header
                                    │ Call UserIDFn(r) if non-nil
                                    │ Generate UUID v7 via idgen.New()
                                    ▼
                              INSERT INTO feedback_comments ──→ feedback_comments
                                    │
                              ←─────┘ {id, status: "ok"}

  GET /comments?limit=N&offset=M → handleListJSON
                                    │ Default: limit=50, max=500
                                    ▼
                              SELECT ... ORDER BY created_at DESC
                              LIMIT ? OFFSET ? ←──────────────── feedback_comments
                                    │
                              ←─────┘ []Comment (JSON array)

  GET /comments.html ────────────→ handleListHTML
                                    │ Hardcoded limit=200, offset=0
                                    ▼
                              SELECT ... ←──────────────────────── feedback_comments
                                    │ Render Go html/template
                                    │ URL safety: only http/https rendered as <a>
                              ←─────┘ Full HTML page

  GET /widget.js ────────────────→ go:embed widget.js ──→ application/javascript
  GET /widget.css ───────────────→ go:embed widget.css ──→ text/css
                                  Cache-Control: public, max-age=3600
```

## Database Table

```
┌────────────────────────────────────────────────────────────────────────────┐
│  feedback_comments                                                         │
├────────────┬──────────┬────────────────────────────────────────────────────┤
│  Column    │ Type     │ Notes                                              │
├────────────┼──────────┼────────────────────────────────────────────────────┤
│  id        │ TEXT PK  │ UUID v7 via idgen.New()                            │
│  text      │ TEXT     │ NOT NULL, max 5000 chars (truncated on write)      │
│  page_url  │ TEXT     │ NOT NULL DEFAULT '', source page URL               │
│  user_agent│ TEXT     │ NOT NULL DEFAULT '', from HTTP header              │
│  user_id   │ TEXT     │ nullable, from UserIDFunc                          │
│  app_name  │ TEXT     │ NOT NULL DEFAULT '', set from Config.AppName       │
│  created_at│ INTEGER  │ NOT NULL, Unix timestamp                           │
├────────────┴──────────┴────────────────────────────────────────────────────┤
│  INDEX idx_feedback_created ON feedback_comments(created_at DESC)          │
└────────────────────────────────────────────────────────────────────────────┘
```

## Key Types

```
Widget {
    db       *sql.DB
    appName  string
    userIDFn UserIDFunc
}

Config {
    DB       *sql.DB       — required
    AppName  string        — app identifier
    UserIDFn UserIDFunc    — optional, nil = anonymous
}

Comment {
    ID        string   — UUID v7
    Text      string   — feedback text (max 5000)
    PageURL   string   — source page
    UserAgent string   — browser user-agent
    UserID    *string  — optional user identifier
    AppName   string   — originating service
    CreatedAt int64    — Unix timestamp
}

UserIDFunc = func(r *http.Request) string
```

## Dependencies

```
Internal (hazyhaar/pkg):
  idgen  — UUID v7 generation (newID → idgen.New)

External:
  database/sql       — SQLite interaction
  html/template      — HTML comment list rendering
  embed              — widget.js, widget.css embedded assets
```

## Security

```
- Body size limit:  32 KiB via http.MaxBytesReader
- Text truncation:  5000 chars (silent, not rejected)
- URL rendering:    only http:// and https:// rendered as clickable <a> (XSS prevention)
- Anonymous mode:   UserIDFn = nil accepted, user_id stored as NULL
```

## Key Function Signatures

```go
func New(cfg Config) (*Widget, error)
func (w *Widget) Handler() http.Handler
func (w *Widget) RegisterMux(mux *http.ServeMux, basePath string)
```
