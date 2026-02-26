package connectivity

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGateway(t *testing.T) {
	r := New()
	r.RegisterLocal("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	})

	gw := r.Gateway()

	t.Run("POST dispatches to local handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader("hello"))
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
		body, _ := io.ReadAll(w.Result().Body)
		if string(body) != "hello" {
			t.Fatalf("expected 'hello', got %q", body)
		}
	})

	t.Run("GET rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/echo", nil)
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", w.Code)
		}
	})

	t.Run("unknown service returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/unknown", strings.NewReader("x"))
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	t.Run("empty path returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x"))
		w := httptest.NewRecorder()
		gw.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

func TestHTTPFactory_AllowInternal(t *testing.T) {
	// Start a local test server to act as gateway
	r := New()
	r.RegisterLocal("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		return []byte("pong"), nil
	})
	srv := httptest.NewServer(r.Gateway())
	defer srv.Close()

	// Without AllowInternal, localhost should be rejected by SSRF guard
	factory := HTTPFactory()
	_, _, err := factory(srv.URL+"/ping", nil)
	if err == nil {
		t.Fatal("expected SSRF rejection for localhost without AllowInternal")
	}

	// With AllowInternal, localhost should work
	factory2 := HTTPFactory(AllowInternal())
	handler, closeFn, err := factory2(srv.URL+"/ping", nil)
	if err != nil {
		t.Fatalf("expected no error with AllowInternal, got: %v", err)
	}
	defer closeFn()

	resp, err := handler(context.Background(), []byte("test"))
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if string(resp) != "pong" {
		t.Fatalf("expected 'pong', got %q", resp)
	}
}
