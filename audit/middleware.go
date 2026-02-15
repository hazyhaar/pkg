package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hazyhaar/pkg/kit"
)

// Middleware wraps an Endpoint: measures duration, captures params/result/error,
// and logs asynchronously via the Logger.
func Middleware(logger Logger, actionName string) kit.Middleware {
	return func(next kit.Endpoint) kit.Endpoint {
		return func(ctx context.Context, request any) (any, error) {
			start := time.Now()

			resp, err := next(ctx, request)

			entry := &Entry{
				Action:     actionName,
				Transport:  kit.GetTransport(ctx),
				UserID:     kit.GetUserID(ctx),
				RequestID:  kit.GetRequestID(ctx),
				DurationMs: time.Since(start).Milliseconds(),
			}

			if params, e := json.Marshal(request); e == nil {
				entry.Parameters = string(params)
			}
			if err != nil {
				entry.Error = err.Error()
				entry.Status = "error"
			} else {
				entry.Status = "success"
				if result, e := json.Marshal(resp); e == nil {
					entry.Result = string(result)
				}
			}

			logger.LogAsync(entry)
			return resp, err
		}
	}
}
