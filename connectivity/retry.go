package connectivity

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// retryConfig is parsed from the route config JSON.
type retryConfig struct {
	TimeoutMs  int64 `json:"timeout_ms"`
	MaxRetries int   `json:"max_retries"`
	BackoffMs  int64 `json:"backoff_ms"`
}

func parseRetryConfig(cfg json.RawMessage) retryConfig {
	var rc retryConfig
	if len(cfg) > 0 {
		_ = json.Unmarshal(cfg, &rc)
	}
	return rc
}

// WithTimeout returns a HandlerMiddleware that applies a per-call timeout
// derived from the route config's timeout_ms field. If timeout_ms is zero
// or absent, the provided default is used. A zero default disables the
// timeout entirely.
func WithTimeout(defaultTimeout time.Duration) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			if defaultTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
				defer cancel()
			}
			return next(ctx, payload)
		}
	}
}

// WithRetry returns a HandlerMiddleware that retries failed calls with
// exponential backoff. It respects context cancellation between retries.
//
// Parameters:
//   - maxRetries: maximum number of retry attempts (0 = no retry)
//   - baseBackoff: initial wait between retries, doubled each attempt
//   - logger: used to log retry attempts (may be nil for silent retries)
func WithRetry(maxRetries int, baseBackoff time.Duration, logger *slog.Logger) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			var lastErr error
			for attempt := 0; attempt <= maxRetries; attempt++ {
				resp, err := next(ctx, payload)
				if err == nil {
					return resp, nil
				}
				lastErr = err

				// Don't retry if context is done.
				if ctx.Err() != nil {
					return nil, lastErr
				}

				// Don't retry on circuit open — it won't help.
				if _, ok := err.(*ErrCircuitOpen); ok {
					return nil, err
				}

				if attempt < maxRetries {
					wait := baseBackoff * (1 << uint(attempt))
					if logger != nil {
						logger.WarnContext(ctx, "retrying call",
							"attempt", attempt+1,
							"max_retries", maxRetries,
							"backoff_ms", wait.Milliseconds(),
							"error", err)
					}
					select {
					case <-ctx.Done():
						return nil, lastErr
					case <-time.After(wait):
					}
				}
			}
			return nil, lastErr
		}
	}
}
