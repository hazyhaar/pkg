package connectivity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hazyhaar/pkg/horosafe"
)

// maxHTTPResponseBody caps the amount of response data read from remote
// HTTP endpoints to prevent memory exhaustion (10 MiB).
const maxHTTPResponseBody int64 = 10 << 20

// httpConfig is the per-route config parsed from the routes table JSON.
type httpConfig struct {
	TimeoutMs   int64  `json:"timeout_ms"`
	ContentType string `json:"content_type"`
}

// HTTPFactory creates Handlers that POST the payload to a remote HTTP
// endpoint. It supports per-route timeout and content-type from the
// config JSON column.
//
// SSRF prevention: the endpoint URL is validated against private/loopback
// addresses at factory creation time.
//
// Register it with:
//
//	router.RegisterTransport("http", connectivity.HTTPFactory())
func HTTPFactory() TransportFactory {
	return func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		// SSRF guard: reject endpoints pointing to private/loopback addresses.
		if err := horosafe.ValidateURL(endpoint); err != nil {
			return nil, nil, fmt.Errorf("connectivity/http: %w", err)
		}

		var cfg httpConfig
		if len(config) > 0 {
			_ = json.Unmarshal(config, &cfg)
		}

		timeout := 30 * time.Second
		if cfg.TimeoutMs > 0 {
			timeout = time.Duration(cfg.TimeoutMs) * time.Millisecond
		}

		contentType := "application/octet-stream"
		if cfg.ContentType != "" {
			contentType = cfg.ContentType
		}

		client := &http.Client{Timeout: timeout}

		handler := func(ctx context.Context, payload []byte) ([]byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
			if err != nil {
				return nil, fmt.Errorf("connectivity/http: create request: %w", err)
			}
			req.Header.Set("Content-Type", contentType)

			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("connectivity/http: do request: %w", err)
			}
			defer resp.Body.Close()

			body, err := horosafe.LimitedReadAll(resp.Body, maxHTTPResponseBody)
			if err != nil {
				return nil, fmt.Errorf("connectivity/http: read response: %w", err)
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return nil, fmt.Errorf("connectivity/http: status %d: %s", resp.StatusCode, body)
			}

			return body, nil
		}

		closeFn := func() {
			client.CloseIdleConnections()
		}

		return handler, closeFn, nil
	}
}
