package shield

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Flash reads the "flash" cookie, parses the type prefix ("success:" or "error:"),
// stores the FlashMessage in the context under FlashKey, and clears the cookie.
func Flash(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("flash")
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		http.SetCookie(w, &http.Cookie{Name: "flash", MaxAge: -1, Path: "/"})

		raw, _ := url.QueryUnescape(cookie.Value)
		flash := &FlashMessage{Type: "error", Message: raw}
		if after, ok := strings.CutPrefix(raw, "success:"); ok {
			flash.Type = "success"
			flash.Message = after
		} else if after, ok := strings.CutPrefix(raw, "error:"); ok {
			flash.Message = after
		}

		ctx := context.WithValue(r.Context(), FlashKey, flash)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SetFlash sets a flash cookie with the given type and message.
// The cookie is HttpOnly and SameSite=Lax with a 10-second TTL.
func SetFlash(w http.ResponseWriter, flashType, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    url.QueryEscape(flashType + ":" + message),
		Path:     "/",
		MaxAge:   10,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
