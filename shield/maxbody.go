package shield

import "net/http"

// MaxFormBody returns middleware that limits the request body size for
// form-encoded POST requests. Other content types are passed through.
func MaxFormBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}
