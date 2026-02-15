package connectivity

import (
	"context"
	"log/slog"
	"time"

	"github.com/hazyhaar/pkg/observability"
)

// WithObservability returns a HandlerMiddleware that records call duration
// as a metric and logs errors via the observability package.
//
// It emits a "connectivity.call.duration_ms" metric for every call and a
// "connectivity.call.error" metric on failures. Labels include the service
// name and strategy.
func WithObservability(mm *observability.MetricsManager, service, strategy string) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			start := time.Now()
			resp, err := next(ctx, payload)
			dur := time.Since(start)

			labels := map[string]string{
				"service":  service,
				"strategy": strategy,
			}

			mm.Record(&observability.Metric{
				Name:      "connectivity.call.duration_ms",
				Timestamp: start,
				Value:     float64(dur.Milliseconds()),
				Labels:    labels,
				Unit:      "milliseconds",
			})

			if err != nil {
				errLabels := map[string]string{
					"service":  service,
					"strategy": strategy,
				}
				mm.Record(&observability.Metric{
					Name:      "connectivity.call.error",
					Timestamp: start,
					Value:     1,
					Labels:    errLabels,
					Unit:      "count",
				})
			}

			return resp, err
		}
	}
}

// WithCallLogging returns a HandlerMiddleware that uses slog for structured
// call logging with duration, payload size and error details.
func WithCallLogging(logger *slog.Logger, service string) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			start := time.Now()
			resp, err := next(ctx, payload)
			dur := time.Since(start)

			if err != nil {
				logger.ErrorContext(ctx, "connectivity call failed",
					"service", service,
					"duration_ms", dur.Milliseconds(),
					"payload_bytes", len(payload),
					"error", err)
			} else {
				logger.DebugContext(ctx, "connectivity call ok",
					"service", service,
					"duration_ms", dur.Milliseconds(),
					"payload_bytes", len(payload),
					"response_bytes", len(resp))
			}
			return resp, err
		}
	}
}
