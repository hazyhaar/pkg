package sas_ingester

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// WebhookPayload is the JSON body sent to webhook targets.
type WebhookPayload struct {
	Event     string      `json:"event"`
	DossierID string      `json:"dossier_id"`
	Piece     *Piece      `json:"piece,omitempty"`
	Metadata  interface{} `json:"metadata,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// Router manages webhook fan-out and retries.
type Router struct {
	store  *Store
	cfg    *Config
	client *http.Client
}

// NewRouter creates a new webhook router.
func NewRouter(store *Store, cfg *Config) *Router {
	return &Router{
		store:  store,
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// EnqueueRoutes creates pending routes for all configured webhooks for a piece.
func (rt *Router) EnqueueRoutes(piece *Piece) error {
	for _, wh := range rt.cfg.Webhooks {
		if err := rt.store.InsertRoute(&RoutePending{
			PieceSHA256:   piece.SHA256,
			DossierID:     piece.DossierID,
			Target:        wh.URL,
			AuthMode:      wh.AuthMode,
			RequireReview: wh.RequireReview,
		}); err != nil {
			return fmt.Errorf("enqueue route %s: %w", wh.URL, err)
		}
	}
	return nil
}

// Deliver attempts to deliver a single route. Returns true if successful.
func (rt *Router) Deliver(route *RoutePending, piece *Piece) bool {
	payload := &WebhookPayload{
		Event:     "piece.ready",
		DossierID: route.DossierID,
		Piece:     piece,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[router] marshal payload: %v", err)
		return false
	}

	req, err := http.NewRequest("POST", route.Target, bytes.NewReader(body))
	if err != nil {
		log.Printf("[router] create request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")

	if route.AuthMode == "bearer" && rt.cfg.JWTSecret != "" {
		req.Header.Set("Authorization", "Bearer "+rt.cfg.JWTSecret)
	}

	resp, err := rt.client.Do(req)
	if err != nil {
		rt.recordFailure(route, fmt.Sprintf("http error: %v", err))
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := rt.store.DeleteRoute(route.PieceSHA256, route.DossierID, route.Target); err != nil {
			log.Printf("[router] delete route: %v", err)
		}
		return true
	}

	rt.recordFailure(route, fmt.Sprintf("http %d", resp.StatusCode))
	return false
}

func (rt *Router) recordFailure(route *RoutePending, errMsg string) {
	attempts := route.Attempts + 1
	backoff := time.Duration(1<<uint(attempts)) * time.Second
	if backoff > 5*time.Minute {
		backoff = 5 * time.Minute
	}
	nextRetry := time.Now().UTC().Add(backoff).Format(time.RFC3339)

	if err := rt.store.UpdateRouteAttempt(
		route.PieceSHA256, route.DossierID, route.Target,
		attempts, errMsg, nextRetry,
	); err != nil {
		log.Printf("[router] record failure: %v", err)
	}
}

// ProcessRetries processes all routes due for retry.
func (rt *Router) ProcessRetries() {
	now := time.Now().UTC().Format(time.RFC3339)
	routes, err := rt.store.ListRetryableRoutes(now)
	if err != nil {
		log.Printf("[router] list retryable: %v", err)
		return
	}

	for _, route := range routes {
		piece, err := rt.store.GetPiece(route.PieceSHA256, route.DossierID)
		if err != nil || piece == nil {
			continue
		}
		rt.Deliver(route, piece)
	}
}
