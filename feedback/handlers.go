package feedback

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (w *Widget) handleSubmit(wr http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(wr, r.Body, 32*1024)

	var req struct {
		Text    string `json:"text"`
		PageURL string `json:"page_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(wr, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		jsonErr(wr, "text is required", http.StatusBadRequest)
		return
	}
	if len(req.Text) > 5000 {
		req.Text = req.Text[:5000]
	}

	id := newID()
	now := time.Now().Unix()
	ua := r.UserAgent()

	var userID *string
	if w.userIDFn != nil {
		if uid := w.userIDFn(r); uid != "" {
			userID = &uid
		}
	}

	_, err := w.db.Exec(
		`INSERT INTO feedback_comments (id, text, page_url, user_agent, user_id, app_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, req.Text, req.PageURL, ua, userID, w.appName, now,
	)
	if err != nil {
		jsonErr(wr, "internal error", http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(map[string]string{"id": id, "status": "ok"})
}

func (w *Widget) handleListJSON(wr http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	comments, err := w.listComments(limit, offset)
	if err != nil {
		jsonErr(wr, "internal error", http.StatusInternalServerError)
		return
	}

	wr.Header().Set("Content-Type", "application/json")
	json.NewEncoder(wr).Encode(comments)
}

// commentView is the template-friendly projection of a Comment.
type commentView struct {
	Text      string
	UserID    string
	CreatedAt string
	PageURL   string
	SafeURL   bool
}

var listHTMLTmpl = template.Must(template.New("list").Parse(`<!DOCTYPE html>
<html lang="fr"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Commentaires — {{.AppName}}</title>
<style>
body{font-family:system-ui,sans-serif;max-width:800px;margin:2rem auto;padding:0 1rem;color:#222;background:#fafafa}
h1{font-size:1.4rem;border-bottom:2px solid #e0e0e0;padding-bottom:.5rem}
.comment{background:#fff;border:1px solid #e0e0e0;border-radius:6px;padding:1rem;margin-bottom:1rem}
.meta{font-size:.8rem;color:#666;margin-top:.5rem}
.empty{color:#999;font-style:italic}
</style></head><body>
<h1>Commentaires — {{.AppName}} ({{.Count}})</h1>
{{- if eq .Count 0}}
<p class="empty">Aucun commentaire pour le moment.</p>
{{- end}}
{{- range .Comments}}
<div class="comment"><p>{{.Text}}</p><div class="meta">{{.UserID}} &mdash; {{.CreatedAt}}
{{- if and .PageURL .SafeURL}} &mdash; <a href="{{.PageURL}}">{{.PageURL}}</a>
{{- else if .PageURL}} &mdash; {{.PageURL}}
{{- end}}</div></div>
{{- end}}
</body></html>`))

func (w *Widget) handleListHTML(wr http.ResponseWriter, r *http.Request) {
	comments, err := w.listComments(200, 0)
	if err != nil {
		http.Error(wr, "internal error", http.StatusInternalServerError)
		return
	}

	views := make([]commentView, len(comments))
	for i, c := range comments {
		uid := "anonyme"
		if c.UserID != nil {
			uid = *c.UserID
		}
		views[i] = commentView{
			Text:      c.Text,
			UserID:    uid,
			CreatedAt: time.Unix(c.CreatedAt, 0).Format("2006-01-02 15:04"),
			PageURL:   c.PageURL,
			SafeURL:   c.PageURL != "" && isSafeURL(c.PageURL),
		}
	}

	wr.Header().Set("Content-Type", "text/html; charset=utf-8")
	listHTMLTmpl.Execute(wr, struct {
		AppName  string
		Count    int
		Comments []commentView
	}{
		AppName:  w.appName,
		Count:    len(comments),
		Comments: views,
	})
}

func (w *Widget) listComments(limit, offset int) ([]Comment, error) {
	rows, err := w.db.Query(
		`SELECT id, text, page_url, user_agent, user_id, app_name, created_at
		 FROM feedback_comments ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		var uid sql.NullString
		if err := rows.Scan(&c.ID, &c.Text, &c.PageURL, &c.UserAgent, &uid, &c.AppName, &c.CreatedAt); err != nil {
			continue
		}
		if uid.Valid {
			c.UserID = &uid.String
		}
		comments = append(comments, c)
	}
	if comments == nil {
		comments = []Comment{}
	}
	return comments, nil
}

// isSafeURL returns true if the URL uses http or https scheme.
func isSafeURL(u string) bool {
	lower := strings.ToLower(u)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
