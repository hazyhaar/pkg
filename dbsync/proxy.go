package dbsync

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// WriteProxy forwards HTTP requests from FO to the BO endpoint.
// In FO mode, write operations (POST, PUT, DELETE) are proxied to the BO
// which owns the read-write database.
type WriteProxy struct {
	boEndpoint string
	tlsCfg     *tls.Config
	logger     *slog.Logger
	proxy      *httputil.ReverseProxy
}

// NewWriteProxy creates a reverse proxy that forwards requests to boEndpoint.
// boEndpoint must be a valid HTTP(S) URL, e.g. "https://bo.internal:8443".
// Returns an error if the endpoint URL cannot be parsed.
func NewWriteProxy(boEndpoint string, tlsCfg *tls.Config) (*WriteProxy, error) {
	target, err := url.Parse(boEndpoint)
	if err != nil {
		return nil, fmt.Errorf("dbsync proxy: invalid BO endpoint %q: %w", boEndpoint, err)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("dbsync proxy: BO endpoint %q has no host", boEndpoint)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	// Custom transport with TLS config for internal QUIC/HTTP3 BO endpoint.
	if tlsCfg != nil {
		proxy.Transport = &http.Transport{
			TLSClientConfig: tlsCfg,
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Error("dbsync proxy: upstream error",
			"method", r.Method, "path", r.URL.Path, "error", err)
		http.Error(w, "Service temporarily unavailable", http.StatusBadGateway)
	}

	return &WriteProxy{
		boEndpoint: boEndpoint,
		tlsCfg:     tlsCfg,
		logger:     slog.Default(),
		proxy:      proxy,
	}, nil
}

// Handler returns an http.Handler that proxies all requests to the BO.
func (p *WriteProxy) Handler() http.Handler {
	return p.proxy
}

// RedirectHandler returns an http.HandlerFunc that redirects the client to
// the BO URL instead of proxying. This is a simpler alternative when the BO
// is directly accessible from the user's browser.
func RedirectHandler(boURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := boURL + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	}
}
