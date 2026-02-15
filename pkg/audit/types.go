package audit

import "context"

// Entry records a single action for the audit trail.
type Entry struct {
	EntryID    string `json:"entry_id"`
	Timestamp  int64  `json:"timestamp"`
	Action     string `json:"action"`
	Transport  string `json:"transport"` // "http" or "mcp_quic"
	UserID     string `json:"user_id"`
	RequestID  string `json:"request_id"`
	Parameters string `json:"parameters"`
	Result     string `json:"result"`
	Error      string `json:"error_message"`
	DurationMs int64  `json:"duration_ms"`
	Status     string `json:"status"` // "success" or "error"
}

// Logger writes audit entries to storage.
type Logger interface {
	Log(ctx context.Context, entry *Entry) error
	LogAsync(entry *Entry)
	Close() error
}
