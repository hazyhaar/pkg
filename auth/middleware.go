package auth

import (
	"context"
	"net/http"

	"github.com/hazyhaar/pkg/kit"
)

type claimsKey struct{}

// Middleware returns an http.Handler middleware that extracts a JWT from the
// "token" cookie (preferred) or the Authorization Bearer header. If valid,
// the parsed HorosClaims are injected into the request context along with
// kit.UserIDKey and kit.HandleKey for interoperability with the kit layer.
// Invalid or missing tokens are silently ignored — use RequireAuth to enforce.
func Middleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var tokenStr string

			// 1. Cookie "token"
			if c, err := r.Cookie("token"); err == nil && c.Value != "" {
				tokenStr = c.Value
			}

			// 2. Authorization: Bearer <token> (overrides cookie)
			if tokenStr == "" {
				if h := r.Header.Get("Authorization"); len(h) > 7 && h[:7] == "Bearer " {
					tokenStr = h[7:]
				}
			}

			if tokenStr == "" {
				next.ServeHTTP(w, r)
				return
			}

			claims, err := ValidateToken(secret, tokenStr)
			if err != nil {
				// Clear invalid cookie
				http.SetCookie(w, &http.Cookie{Name: "token", MaxAge: -1, Path: "/"})
				next.ServeHTTP(w, r)
				return
			}

			ctx := r.Context()
			ctx = context.WithValue(ctx, claimsKey{}, claims)
			ctx = kit.WithUserID(ctx, claims.UserID)
			if claims.Handle != "" {
				ctx = kit.WithHandle(ctx, claims.Handle)
			} else if claims.Username != "" {
				ctx = kit.WithHandle(ctx, claims.Username)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClaims retrieves the HorosClaims from the context, or nil if absent.
func GetClaims(ctx context.Context) *HorosClaims {
	c, _ := ctx.Value(claimsKey{}).(*HorosClaims)
	return c
}

// RequireAuth is an http.Handler middleware that redirects unauthenticated
// requests to /login. It checks for the presence of HorosClaims in context.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if GetClaims(r.Context()) == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
