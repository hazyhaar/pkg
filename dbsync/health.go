package dbsync

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// BOHealthChecker periodically pings the back-office to track reachability.
// The cached result is used by auth proxies and health endpoints to fail fast
// instead of waiting for a 10s HTTP timeout every time.
type BOHealthChecker struct {
	boURL    string
	interval time.Duration
	client   *http.Client

	healthy    atomic.Bool
	lastCheck  atomic.Int64 // unix timestamp
	lastLatMs  atomic.Int64 // last successful latency in ms
	checkCount atomic.Int64
	failCount  atomic.Int64
}

// NewBOHealthChecker creates a health checker that pings boURL+"/health"
// every interval. Call Start to begin the check loop.
func NewBOHealthChecker(boURL string, interval time.Duration) *BOHealthChecker {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	return &BOHealthChecker{
		boURL:    boURL,
		interval: interval,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Start begins the periodic health check loop. Blocks until ctx is cancelled.
func (h *BOHealthChecker) Start(ctx context.Context) {
	// Check immediately on start.
	h.check()

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.check()
		}
	}
}

func (h *BOHealthChecker) check() {
	h.checkCount.Add(1)
	h.lastCheck.Store(time.Now().Unix())

	start := time.Now()
	resp, err := h.client.Get(h.boURL + "/health")
	latency := time.Since(start)

	if err != nil {
		h.healthy.Store(false)
		h.failCount.Add(1)
		slog.Debug("bo health check failed", "error", err, "bo_url", h.boURL)
		return
	}
	resp.Body.Close()

	ok := resp.StatusCode >= 200 && resp.StatusCode < 500
	h.healthy.Store(ok)
	if ok {
		h.lastLatMs.Store(latency.Milliseconds())
	} else {
		h.failCount.Add(1)
	}
}

// Healthy returns the cached reachability status. Returns false if no check
// has been performed yet.
func (h *BOHealthChecker) Healthy() bool { return h.healthy.Load() }

// Status returns a JSON-serializable summary.
func (h *BOHealthChecker) Status() map[string]any {
	return map[string]any{
		"reachable":   h.healthy.Load(),
		"last_check":  h.lastCheck.Load(),
		"latency_ms":  h.lastLatMs.Load(),
		"check_count": h.checkCount.Load(),
		"fail_count":  h.failCount.Load(),
	}
}
