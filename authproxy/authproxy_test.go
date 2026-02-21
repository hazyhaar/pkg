package authproxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// mockBO creates a test server that mimics the BO /api/internal/auth/* endpoints.
func mockBO(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func setFlashNoop(w http.ResponseWriter, kind, msg string) {}

func TestLoginHandler_Success(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/auth/login" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"token": "jwt-test-token",
		})
	})
	defer bo.Close()

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.LoginHandler(setFlashNoop)

	form := url.Values{"username": {"alice"}, "password": {"secret"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/dashboard" {
		t.Errorf("expected redirect to /dashboard, got %q", loc)
	}

	// Check cookie was set.
	cookies := resp.Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "token" && c.Value == "jwt-test-token" {
			found = true
		}
	}
	if !found {
		t.Error("expected token cookie to be set")
	}
}

func TestLoginHandler_InvalidCredentials(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "invalid credentials",
		})
	})
	defer bo.Close()

	var flashKind, flashMsg string
	setFlash := func(w http.ResponseWriter, kind, msg string) {
		flashKind = kind
		flashMsg = msg
	}

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.LoginHandler(setFlash)

	form := url.Values{"username": {"alice"}, "password": {"wrong"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
	if flashKind != "error" || flashMsg != "invalid credentials" {
		t.Errorf("expected error flash, got kind=%q msg=%q", flashKind, flashMsg)
	}
}

func TestLoginHandler_BODown(t *testing.T) {
	// Use a server that immediately closes.
	bo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	bo.Close()

	var flashKind string
	setFlash := func(w http.ResponseWriter, kind, msg string) {
		flashKind = kind
	}

	proxy := NewAuthProxy(bo.URL, "", false)
	proxy.client.Timeout = 100 * time.Millisecond
	handler := proxy.LoginHandler(setFlash)

	form := url.Values{"username": {"alice"}, "password": {"secret"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	if flashKind != "error" {
		t.Errorf("expected error flash when BO is down, got %q", flashKind)
	}
}

func TestRegisterHandler_Success(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/auth/register" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"flash": "Compte créé",
		})
	})
	defer bo.Close()

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.RegisterHandler(setFlashNoop)

	form := url.Values{
		"username":     {"bob"},
		"email":        {"bob@test.com"},
		"password":     {"secret123"},
		"display_name": {"Bob"},
	}
	req := httptest.NewRequest("POST", "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
}

func TestRegisterHandler_Duplicate(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": "user already exists",
			"code":  "user_exists",
		})
	})
	defer bo.Close()

	var flashMsg string
	setFlash := func(w http.ResponseWriter, kind, msg string) {
		flashMsg = msg
	}

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.RegisterHandler(setFlash)

	form := url.Values{
		"username": {"bob"},
		"email":    {"bob@test.com"},
		"password": {"secret123"},
	}
	req := httptest.NewRequest("POST", "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	loc := resp.Header.Get("Location")
	if loc != "/register" {
		t.Errorf("expected redirect to /register, got %q", loc)
	}
	if flashMsg != "user already exists" {
		t.Errorf("expected 'user already exists' flash, got %q", flashMsg)
	}
}
