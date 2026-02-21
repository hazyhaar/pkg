package shield

import "net/http"

// HeadToGet converts HEAD requests to GET so that route handlers registered
// with r.Get() respond with 200 instead of 405 (Method Not Allowed).
// Go's net/http automatically strips the body for HEAD responses.
func HeadToGet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			r.Method = http.MethodGet
		}
		next.ServeHTTP(w, r)
	})
}
