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

func TestForgotPasswordHandler_Success(t *testing.T) {
	var gotOrigin string
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/auth/forgot-password" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		gotOrigin = body["origin"]
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"flash": "Email envoyé",
		})
	})
	defer bo.Close()

	var flashMsg string
	setFlash := func(w http.ResponseWriter, kind, msg string) {
		flashMsg = msg
	}

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.ForgotPasswordHandler(setFlash)

	form := url.Values{"email": {"alice@test.com"}}
	req := httptest.NewRequest("POST", "http://fo.example.com/forgot-password", strings.NewReader(form.Encode()))
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
	if flashMsg != "Email envoyé" {
		t.Errorf("expected flash 'Email envoyé', got %q", flashMsg)
	}
	if gotOrigin != "http://fo.example.com" {
		t.Errorf("expected origin 'http://fo.example.com', got %q", gotOrigin)
	}
}

func TestForgotPasswordHandler_NeverExposesBO(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	defer bo.Close()

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.ForgotPasswordHandler(setFlashNoop)

	form := url.Values{"email": {"alice@test.com"}}
	req := httptest.NewRequest("POST", "/forgot-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, bo.URL) {
		t.Errorf("redirect URL leaks BO address: %q", loc)
	}
}

func TestResetPasswordHandler_Success(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/auth/reset-password" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"flash": "Mot de passe réinitialisé",
		})
	})
	defer bo.Close()

	var flashMsg string
	setFlash := func(w http.ResponseWriter, kind, msg string) {
		flashMsg = msg
	}

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.ResetPasswordHandler(setFlash)

	form := url.Values{
		"token":            {"reset-token-123"},
		"password":         {"newpass"},
		"password_confirm": {"newpass"},
	}
	req := httptest.NewRequest("POST", "/reset-password", strings.NewReader(form.Encode()))
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
	if flashMsg != "Mot de passe réinitialisé" {
		t.Errorf("expected flash, got %q", flashMsg)
	}
}

func TestResetPasswordHandler_Mismatch(t *testing.T) {
	proxy := NewAuthProxy("http://unused", "", false)
	handler := proxy.ResetPasswordHandler(setFlashNoop)

	form := url.Values{
		"token":            {"tok"},
		"password":         {"aaa"},
		"password_confirm": {"bbb"},
	}
	req := httptest.NewRequest("POST", "/reset-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/reset-password?token=") {
		t.Errorf("expected redirect to /reset-password with token, got %q", loc)
	}
}

func TestRequestOrigin(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		proto  string // X-Forwarded-Proto
		fwdH   string // X-Forwarded-Host
		expect string
	}{
		{
			name:   "plain HTTP",
			url:    "http://fo.example.com/forgot-password",
			expect: "http://fo.example.com",
		},
		{
			name:   "X-Forwarded-Proto HTTPS",
			url:    "http://fo.example.com/forgot-password",
			proto:  "https",
			expect: "https://fo.example.com",
		},
		{
			name:   "X-Forwarded-Host",
			url:    "http://internal:8080/forgot-password",
			fwdH:   "public.example.com",
			expect: "http://public.example.com",
		},
		{
			name:   "both forwarded headers",
			url:    "http://internal:8080/forgot-password",
			proto:  "https",
			fwdH:   "public.example.com",
			expect: "https://public.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tt.url, nil)
			if tt.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.fwdH != "" {
				req.Header.Set("X-Forwarded-Host", tt.fwdH)
			}
			got := requestOrigin(req)
			if got != tt.expect {
				t.Errorf("requestOrigin() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestResetPasswordHandler_NeverExposesBO(t *testing.T) {
	bo := mockBO(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	defer bo.Close()

	proxy := NewAuthProxy(bo.URL, "", false)
	handler := proxy.ResetPasswordHandler(setFlashNoop)

	form := url.Values{
		"token":            {"tok"},
		"password":         {"pass"},
		"password_confirm": {"pass"},
	}
	req := httptest.NewRequest("POST", "/reset-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	loc := resp.Header.Get("Location")
	if strings.Contains(loc, bo.URL) {
		t.Errorf("redirect URL leaks BO address: %q", loc)
	}
}
