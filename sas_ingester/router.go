package sas_ingester

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// OpaquePayload is the JSON body sent to opaque_only webhook targets.
// It never contains user identity information — only the opaque dossier_id.
type OpaquePayload struct {
	Event     string         `json:"event"`
	DossierID string         `json:"dossier_id"`
	SHA256    string         `json:"sha256"`
	SizeBytes int64          `json:"size_bytes"`
	State     string         `json:"state"`
	MIME      string         `json:"mime,omitempty"`
	Metadata  *FileMetadata  `json:"metadata,omitempty"`
	Timestamp string         `json:"timestamp"`
}

// PassthruPayload is the JSON body sent to jwt_passthru webhook targets.
// It includes the opaque dossier_id and the piece details.
type PassthruPayload struct {
	Event     string         `json:"event"`
	DossierID string         `json:"dossier_id"`
	SHA256    string         `json:"sha256"`
	SizeBytes int64          `json:"size_bytes"`
	State     string         `json:"state"`
	MIME      string         `json:"mime,omitempty"`
	Metadata  *FileMetadata  `json:"metadata,omitempty"`
	Timestamp string         `json:"timestamp"`
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

// EnqueueRoutes creates pending routes for a piece (without JWT passthru).
func (rt *Router) EnqueueRoutes(piece *Piece) error {
	return rt.EnqueueRoutesWithToken(piece, "")
}

// EnqueueRoutesWithToken creates pending routes for a piece. If the dossier has
// per-dossier routes (dossiers.routes JSON), those are used exclusively.
// Otherwise, the global webhook config is used as a fallback.
// originalToken is stored for jwt_passthru routes; empty for opaque_only.
func (rt *Router) EnqueueRoutesWithToken(piece *Piece, originalToken string) error {
	// Check for per-dossier routing first.
	dossier, err := rt.store.GetDossier(piece.DossierID)
	if err != nil {
		return fmt.Errorf("get dossier for routing: %w", err)
	}

	if dossier != nil {
		if dossierRoutes := dossier.ParsedRoutes(); len(dossierRoutes) > 0 {
			for _, dr := range dossierRoutes {
				token := ""
				if dr.AuthMode == "jwt_passthru" {
					token = originalToken
				}
				if err := rt.store.InsertRoute(&RoutePending{
					PieceSHA256:   piece.SHA256,
					DossierID:     piece.DossierID,
					Target:        dr.URL,
					AuthMode:      dr.AuthMode,
					RequireReview: dr.RequireReview,
					OriginalToken: token,
				}); err != nil {
					return fmt.Errorf("enqueue dossier route %s: %w", dr.URL, err)
				}
			}
			return nil
		}
	}

	// Fallback to global webhooks.
	for _, wh := range rt.cfg.Webhooks {
		token := ""
		if wh.AuthMode == "jwt_passthru" {
			token = originalToken
		}
		if err := rt.store.InsertRoute(&RoutePending{
			PieceSHA256:   piece.SHA256,
			DossierID:     piece.DossierID,
			Target:        wh.URL,
			AuthMode:      wh.AuthMode,
			RequireReview: wh.RequireReview,
			OriginalToken: token,
		}); err != nil {
			return fmt.Errorf("enqueue route %s: %w", wh.URL, err)
		}
	}
	return nil
}

// Deliver attempts to deliver a single route. Returns true if successful.
func (rt *Router) Deliver(route *RoutePending, piece *Piece) bool {
	now := time.Now().UTC().Format(time.RFC3339)

	// Build auth-mode-appropriate payload.
	// opaque_only: never contains user identity — assert no JWT header sent.
	// jwt_passthru: includes the original JWT for identified downstream services.
	var body []byte
	var err error

	switch route.AuthMode {
	case "jwt_passthru":
		body, err = json.Marshal(&PassthruPayload{
			Event:     "piece.ready",
			DossierID: route.DossierID,
			SHA256:    piece.SHA256,
			SizeBytes: piece.SizeBytes,
			State:     piece.State,
			MIME:      piece.MIME,
			Timestamp: now,
		})
	default: // opaque_only or empty
		body, err = json.Marshal(&OpaquePayload{
			Event:     "piece.ready",
			DossierID: route.DossierID,
			SHA256:    piece.SHA256,
			SizeBytes: piece.SizeBytes,
			State:     piece.State,
			MIME:      piece.MIME,
			Timestamp: now,
		})
	}
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

	// Apply auth based on route mode.
	secret := rt.resolveSecret(route.DossierID, route.Target)
	switch route.AuthMode {
	case "jwt_passthru":
		// Forward the original JWT stored in the route's LastError field
		// (repurposed as token storage for passthru routes).
		// Also sign with HMAC if a secret is configured.
		if route.OriginalToken != "" {
			req.Header.Set("Authorization", "Bearer "+route.OriginalToken)
		}
		if secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			sig := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Signature-256", "sha256="+sig)
		}
	default: // opaque_only
		// ASSERT: no Authorization header for opaque routes.
		// Sign with HMAC if a secret is configured.
		if secret != "" {
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write(body)
			sig := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Signature-256", "sha256="+sig)
		}
		// Safety check: ensure no Authorization header leaked.
		if req.Header.Get("Authorization") != "" {
			log.Printf("[router] ALERT: opaque_only route %s has Authorization header — removing", route.Target)
			req.Header.Del("Authorization")
		}
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

// resolveSecret looks up the per-webhook secret for a target URL.
// It checks per-dossier routes first, then falls back to global config.
func (rt *Router) resolveSecret(dossierID, targetURL string) string {
	// Check per-dossier routes.
	if dossier, err := rt.store.GetDossier(dossierID); err == nil && dossier != nil {
		for _, dr := range dossier.ParsedRoutes() {
			if dr.URL == targetURL {
				return dr.Secret
			}
		}
	}
	// Fallback to global config.
	for i := range rt.cfg.Webhooks {
		if rt.cfg.Webhooks[i].URL == targetURL {
			return rt.cfg.Webhooks[i].Secret
		}
	}
	return ""
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
