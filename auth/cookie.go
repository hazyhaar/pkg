package auth

import "net/http"

// SetTokenCookie writes the JWT token as an HttpOnly cookie.
// When domain is non-empty, the cookie is set with that Domain attribute,
// enabling cross-subdomain SSO (e.g. Domain=".docbusinessia.fr").
func SetTokenCookie(w http.ResponseWriter, token, domain string, secure bool) {
	c := &http.Cookie{
		Name:     "token",
		Value:    token,
		Path:     "/",
		MaxAge:   86400, // 24h
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	}
	if domain != "" {
		c.Domain = domain
	}
	http.SetCookie(w, c)
}

// ClearTokenCookie removes the JWT cookie, matching the same Domain attribute
// so that cross-subdomain cookies are properly cleared.
func ClearTokenCookie(w http.ResponseWriter, domain string) {
	c := &http.Cookie{
		Name:     "token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	}
	if domain != "" {
		c.Domain = domain
	}
	http.SetCookie(w, c)
}
