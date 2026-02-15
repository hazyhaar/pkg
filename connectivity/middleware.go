package connectivity

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"
)

// HandlerMiddleware wraps a Handler, adding cross-cutting behaviour
// (logging, timeout, recovery, metrics) without changing the signature.
type HandlerMiddleware func(next Handler) Handler

// Chain composes middlewares left-to-right: the first middleware in the
// slice is the outermost wrapper (executed first on the request path).
//
//	chain := Chain(logging, timeout, recovery)
//	wrapped := chain(baseHandler)
func Chain(mws ...HandlerMiddleware) HandlerMiddleware {
	return func(next Handler) Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}

// Logging returns a middleware that logs every call with its duration.
func Logging(logger *slog.Logger) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			start := time.Now()
			resp, err := next(ctx, payload)
			dur := time.Since(start)

			if err != nil {
				logger.ErrorContext(ctx, "call failed",
					"duration_ms", dur.Milliseconds(),
					"payload_bytes", len(payload),
					"error", err)
			} else {
				logger.DebugContext(ctx, "call ok",
					"duration_ms", dur.Milliseconds(),
					"payload_bytes", len(payload),
					"response_bytes", len(resp))
			}
			return resp, err
		}
	}
}

// Timeout returns a middleware that enforces a maximum call duration.
// If the context deadline is exceeded, the handler's goroutine keeps
// running (Go has no goroutine cancellation), but the caller gets an
// immediate context.DeadlineExceeded error.
func Timeout(d time.Duration) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx, payload)
		}
	}
}

// Recovery returns a middleware that catches panics in downstream handlers
// and converts them into errors instead of crashing the process.
func Recovery(logger *slog.Logger) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) (resp []byte, err error) {
			defer func() {
				if r := recover(); r != nil {
					stack := debug.Stack()
					logger.ErrorContext(ctx, "handler panic recovered",
						"panic", r,
						"stack", string(stack))
					err = &ErrPanic{Value: r}
				}
			}()
			return next(ctx, payload)
		}
	}
}

// ErrPanic wraps a recovered panic value as an error.
type ErrPanic struct {
	Value any
}

func (e *ErrPanic) Error() string {
	return "connectivity: handler panicked"
}
