package shield

import "net/http"

// HeaderConfig defines the security headers applied to every response.
type HeaderConfig struct {
	CSP                 string
	XFrameOptions       string
	XContentTypeOptions string
	ReferrerPolicy      string
	PermissionsPolicy   string
}

// DefaultHeaders returns the standard HOROS security header configuration.
func DefaultHeaders() HeaderConfig {
	return HeaderConfig{
		CSP:                 "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; frame-ancestors 'none'",
		XFrameOptions:       "DENY",
		XContentTypeOptions: "nosniff",
		ReferrerPolicy:      "strict-origin-when-cross-origin",
		PermissionsPolicy:   "camera=(), microphone=(), geolocation=()",
	}
}

// SecurityHeaders returns middleware that sets the configured security headers
// on every response. Use DefaultHeaders() for the standard HOROS configuration.
func SecurityHeaders(cfg HeaderConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.XContentTypeOptions != "" {
				w.Header().Set("X-Content-Type-Options", cfg.XContentTypeOptions)
			}
			if cfg.XFrameOptions != "" {
				w.Header().Set("X-Frame-Options", cfg.XFrameOptions)
			}
			if cfg.ReferrerPolicy != "" {
				w.Header().Set("Referrer-Policy", cfg.ReferrerPolicy)
			}
			if cfg.CSP != "" {
				w.Header().Set("Content-Security-Policy", cfg.CSP)
			}
			if cfg.PermissionsPolicy != "" {
				w.Header().Set("Permissions-Policy", cfg.PermissionsPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}
