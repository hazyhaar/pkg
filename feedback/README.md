# feedback — embeddable feedback widget

`feedback` provides a self-contained feedback system with a Go backend and
injectable JS/CSS frontend. Drop it into any HTTP service to collect user
comments.

## Quick start

```go
widget, _ := feedback.New(feedback.Config{
    DB:      db,
    AppName: "myapp",
    UserIDFn: func(r *http.Request) string {
        return auth.GetClaims(r.Context()).UserID
    },
})
widget.RegisterMux(mux, "/feedback")
```

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/submit` | Submit a comment (JSON: `{text, page_url}`) |
| GET | `/comments` | List comments as JSON (supports `limit`, `offset`) |
| GET | `/comments.html` | Rendered HTML list |
| GET | `/widget.js` | Embedded JavaScript |
| GET | `/widget.css` | Embedded CSS |

## Schema

```sql
CREATE TABLE feedback_comments (
    id         TEXT PRIMARY KEY,
    text       TEXT NOT NULL,
    page_url   TEXT,
    user_agent TEXT,
    user_id    TEXT,
    app_name   TEXT,
    created_at INTEGER NOT NULL
);
```

## Limits

- Request body: 32 KiB
- Comment text: 5 000 characters
- HTML list: 200 rows max

## Exported API

| Symbol | Description |
|--------|-------------|
| `Widget` | Feedback system (schema + handlers + assets) |
| `New(cfg)` | Create widget and apply schema |
| `Config` | DB, AppName, UserIDFn |
| `Comment` | Feedback record |
| `Handler()` | `http.Handler` serving all endpoints |
| `RegisterMux(mux, path)` | Register on a `ServeMux` |
