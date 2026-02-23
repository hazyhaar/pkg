// Package shield provides reusable HTTP security middleware for HOROS services.
// It consolidates security headers, rate limiting, body limits, request tracing,
// flash messages, and HEAD method handling into a single importable package.
//
// Usage:
//
//	r := chi.NewRouter()
//	r.Use(shield.SecurityHeaders(shield.DefaultHeaders()))
//	r.Use(shield.MaxFormBody(64 * 1024))
//	r.Use(shield.TraceID)
//	r.Use(shield.NewRateLimiter(db).Middleware)
//	r.Use(shield.Flash)
//	r.Use(shield.HeadToGet)
//
// Or apply the default FO stack in one call:
//
//	stack, mm := shield.DefaultFOStack(db)
//	mm.StartReloader(done)
//	for _, mw := range stack {
//	    r.Use(mw)
//	}
package shield

import (
	"context"
	"database/sql"
	"net/http"
)

type contextKey string

const (
	// LoggerKey is the context key for the per-request structured logger.
	LoggerKey contextKey = "shield_logger"

	// FlashKey is the context key for flash messages.
	FlashKey contextKey = "shield_flash"
)

// FlashMessage represents a one-time notification shown to the user.
type FlashMessage struct {
	Type    string // "success" or "error"
	Message string
}

// GetFlash retrieves the flash message from the request context.
func GetFlash(ctx context.Context) *FlashMessage {
	v, _ := ctx.Value(FlashKey).(*FlashMessage)
	return v
}

// DefaultFOStack returns the standard middleware stack for a HOROS FO service.
// Middleware is ordered: Maintenance → HeadToGet → SecurityHeaders → MaxFormBody → TraceID → RateLimiter → Flash.
// The returned MaintenanceMode handle allows callers to set a custom page
// and call StartReloader. Health checks (/healthz) bypass maintenance.
func DefaultFOStack(db *sql.DB) ([]func(http.Handler) http.Handler, *MaintenanceMode) {
	rl := NewRateLimiter(db)
	mm := NewMaintenanceMode(db, "/healthz", "/static/")
	return []func(http.Handler) http.Handler{
		mm.Middleware,
		HeadToGet,
		SecurityHeaders(DefaultHeaders()),
		MaxFormBody(64 * 1024),
		TraceID,
		rl.Middleware,
		Flash,
	}, mm
}

// DefaultBOStack returns the standard middleware stack for a HOROS BO service.
// Same as FO but without rate limiting (BO is not publicly exposed).
func DefaultBOStack() []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		HeadToGet,
		SecurityHeaders(DefaultHeaders()),
		MaxFormBody(64 * 1024),
		TraceID,
		Flash,
	}
}
