package dbsync

import (
	"crypto/tls"
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
// boEndpoint should be an HTTP(S) URL, e.g. "https://bo.internal:8443".
func NewWriteProxy(boEndpoint string, tlsCfg *tls.Config) *WriteProxy {
	target, err := url.Parse(boEndpoint)
	if err != nil {
		slog.Error("dbsync proxy: invalid BO endpoint", "endpoint", boEndpoint, "error", err)
		target = &url.URL{Scheme: "https", Host: "localhost:8443"}
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
	}
}

// Handler returns an http.Handler that proxies all requests to the BO.
func (p *WriteProxy) Handler() http.Handler {
	return p.proxy
}

// RedirectHandler returns an http.Handler that redirects to the BO URL
// instead of proxying. Simpler alternative when the BO is directly accessible.
func RedirectHandler(boURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := boURL + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	}
}
