package auth

import "github.com/golang-jwt/jwt/v5"

// HorosClaims defines the unified JWT claims structure for all HOROS services.
// It embeds jwt.RegisteredClaims for standard fields (exp, iat, etc.) and adds
// HOROS-specific fields for user identity and auth provider tracking.
type HorosClaims struct {
	jwt.RegisteredClaims
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	Handle       string `json:"handle,omitempty"`
	Role         string `json:"role"`
	Email        string `json:"email,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	AvatarURL    string `json:"avatar_url,omitempty"`
	AuthProvider string `json:"auth_provider,omitempty"` // "local", "google", "github"
}
