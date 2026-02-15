package connectivity

import (
	"context"
	"log/slog"
)

// WithFallback returns a HandlerMiddleware that falls back to a local handler
// when the primary (remote) handler fails. This enables graceful degradation:
// if the remote service is down, the call is retried locally.
//
// The fallback is only attempted if the local handler is non-nil. Context
// cancellation errors are NOT retried locally — they indicate the caller
// gave up, not that the remote failed.
func WithFallback(local Handler, service string, logger *slog.Logger) HandlerMiddleware {
	return func(next Handler) Handler {
		if local == nil {
			return next
		}
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			resp, err := next(ctx, payload)
			if err == nil {
				return resp, nil
			}

			// Don't fallback on context cancellation — the caller gave up.
			if ctx.Err() != nil {
				return nil, err
			}

			if logger != nil {
				logger.WarnContext(ctx, "remote failed, falling back to local",
					"service", service,
					"remote_error", err)
			}

			return local(ctx, payload)
		}
	}
}
