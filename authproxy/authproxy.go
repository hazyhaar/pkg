// Package authproxy provides HTTP handlers that proxy authentication requests
// from a front-office (FO) to a back-office (BO) internal API. The BO performs
// the actual credential validation, and the proxy translates JSON responses into
// cookies and redirects so that the user never sees the BO URL.
//
// This package was extracted from github.com/hazyhaar/pkg/dbsync to allow
// services that need auth proxying without importing the full dbsync package.
package authproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	horosauth "github.com/hazyhaar/pkg/auth"
)

// AuthProxy calls the BO internal auth API and translates the JSON response
// into cookies + redirects for the FO domain. The user never sees the BO URL.
type AuthProxy struct {
	boURL        string // e.g. "https://rv.docbusinessia.fr"
	cookieDomain string // e.g. "" (default to request host) or ".repvow.fr"
	secure       bool   // true for HTTPS
	logger       *slog.Logger
	client       *http.Client
}

// NewAuthProxy creates an auth proxy that calls BO internal API endpoints.
//
// Parameters:
//   - boURL: base URL of the back-office, e.g. "https://rv.docbusinessia.fr"
//   - cookieDomain: cookie Domain attribute ("" uses the request host)
//   - secure: whether to set the Secure flag on cookies (true for HTTPS)
func NewAuthProxy(boURL, cookieDomain string, secure bool) *AuthProxy {
	return &AuthProxy{
		boURL:        boURL,
		cookieDomain: cookieDomain,
		secure:       secure,
		logger:       slog.Default(),
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// authResponse mirrors the JSON returned by BO /api/internal/auth/* endpoints.
type authResponse struct {
	OK       bool   `json:"ok"`
	Token    string `json:"token,omitempty"`
	UserID   string `json:"user_id,omitempty"`
	Error    string `json:"error,omitempty"`
	Code     string `json:"code,omitempty"`
	Flash    string `json:"flash,omitempty"`
	Redirect string `json:"redirect,omitempty"`
}

// LoginHandler returns an http.HandlerFunc for POST /login on the FO.
// It reads the form, calls BO /api/internal/auth/login, sets the cookie, and redirects.
func (p *AuthProxy) LoginHandler(setFlash func(http.ResponseWriter, string, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			setFlash(w, "error", "Requête invalide")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		payload, _ := json.Marshal(map[string]string{
			"username": r.FormValue("username"),
			"password": r.FormValue("password"),
		})

		resp, err := p.callBO("/api/internal/auth/login", payload)
		if err != nil {
			p.logger.Error("auth proxy: login call failed", "error", err)
			setFlash(w, "error", "Service temporairement indisponible")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if !resp.OK {
			setFlash(w, "error", resp.Error)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Set the cookie on the FO domain.
		horosauth.SetTokenCookie(w, resp.Token, p.cookieDomain, p.secure)

		if resp.Flash != "" {
			setFlash(w, "success", resp.Flash)
		}
		redirect := resp.Redirect
		if redirect == "" {
			redirect = "/dashboard"
		}
		http.Redirect(w, r, redirect, http.StatusSeeOther)
	}
}

// RegisterHandler returns an http.HandlerFunc for POST /register on the FO.
// It reads the form, calls BO /api/internal/auth/register, and redirects.
func (p *AuthProxy) RegisterHandler(setFlash func(http.ResponseWriter, string, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			setFlash(w, "error", "Requête invalide")
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}

		payload, _ := json.Marshal(map[string]string{
			"username":     r.FormValue("username"),
			"email":        r.FormValue("email"),
			"password":     r.FormValue("password"),
			"display_name": r.FormValue("display_name"),
		})

		resp, err := p.callBO("/api/internal/auth/register", payload)
		if err != nil {
			p.logger.Error("auth proxy: register call failed", "error", err)
			setFlash(w, "error", "Service temporairement indisponible")
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}

		if !resp.OK {
			setFlash(w, "error", resp.Error)
			http.Redirect(w, r, "/register", http.StatusSeeOther)
			return
		}

		if resp.Flash != "" {
			setFlash(w, "success", resp.Flash)
		}
		redirect := resp.Redirect
		if redirect == "" {
			redirect = "/login"
		}
		http.Redirect(w, r, redirect, http.StatusSeeOther)
	}
}

// callBO sends a JSON POST to the BO internal API and decodes the response.
func (p *AuthProxy) callBO(path string, body []byte) (*authResponse, error) {
	url := p.boURL + path
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call BO %s: %w", path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var ar authResponse
	if err := json.Unmarshal(data, &ar); err != nil {
		preview := string(data)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, preview)
	}
	return &ar, nil
}
