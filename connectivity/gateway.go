// CLAUDE:SUMMARY HTTP gateway exposing local connectivity handlers — mount on any router to serve cross-process calls.
// CLAUDE:DEPENDS net/http, io, encoding/json
// CLAUDE:EXPORTS Gateway
package connectivity

import (
	"io"
	"net/http"
	"strings"
)

// maxGatewayRequestBody caps incoming request bodies (10 MiB).
const maxGatewayRequestBody int64 = 10 << 20

// Gateway returns an http.Handler that exposes local handlers over HTTP.
// Incoming POST requests are dispatched to the matching local handler:
//
//	POST /{service_name}  →  router.Call(ctx, service_name, body)
//
// Mount it on a chi router or http.ServeMux:
//
//	r.Mount("/connectivity", router.Gateway())
//
// This is the server-side counterpart of HTTPFactory: one service mounts
// the Gateway, another service calls it via HTTPFactory routes.
func (r *Router) Gateway() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract service name: strip leading slash.
		service := strings.TrimPrefix(req.URL.Path, "/")
		if service == "" {
			http.Error(w, "missing service name", http.StatusBadRequest)
			return
		}

		payload, err := io.ReadAll(io.LimitReader(req.Body, maxGatewayRequestBody))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Dispatch to local handler only (not remote — avoid loops).
		r.mu.RLock()
		h := r.localHandlers[service]
		r.mu.RUnlock()

		if h == nil {
			http.Error(w, "service not found: "+service, http.StatusNotFound)
			return
		}

		resp, err := h(req.Context(), payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if resp != nil {
			w.Write(resp)
		}
	})
}
