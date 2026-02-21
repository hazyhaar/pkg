package shield

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"

	"github.com/hazyhaar/pkg/kit"
)

// TraceID generates a random trace ID for each request and injects it into
// the context, response headers, and a per-request structured logger.
// The trace ID is stored under kit.TraceIDKey and the logger under LoggerKey.
func TraceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := make([]byte, 4)
		rand.Read(id)
		traceID := hex.EncodeToString(id)

		ctx := kit.WithTraceID(r.Context(), traceID)
		w.Header().Set("X-Trace-ID", traceID)

		logger := slog.Default().With(
			"trace_id", traceID,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)
		ctx = context.WithValue(ctx, LoggerKey, logger)
		logger.Info("request")

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetLogger retrieves the per-request logger from the context.
// Returns slog.Default() if no logger was set.
func GetLogger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(LoggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
