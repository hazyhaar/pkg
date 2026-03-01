package docpipe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/pkg/connectivity"
)

func TestConn_Detect(t *testing.T) {
	pipe := New(Config{})
	router := connectivity.New()
	pipe.RegisterConnectivity(router)

	tests := []struct {
		path   string
		format string
	}{
		{"doc.docx", "docx"},
		{"doc.md", "md"},
		{"doc.pdf", "pdf"},
	}
	for _, tt := range tests {
		payload, _ := json.Marshal(map[string]any{"path": tt.path})
		resp, err := router.Call(context.Background(), "docpipe_detect", payload)
		if err != nil {
			t.Fatalf("Call(%s): %v", tt.path, err)
		}
		var result struct {
			Format string `json:"format"`
		}
		_ = json.Unmarshal(resp, &result)
		if result.Format != tt.format {
			t.Errorf("Detect(%q) = %q, want %q", tt.path, result.Format, tt.format)
		}
	}
}

func TestConn_Extract(t *testing.T) {
	pipe := New(Config{})
	router := connectivity.New()
	pipe.RegisterConnectivity(router)

	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	_ = os.WriteFile(path, []byte("Hello connectivity test"), 0644)

	payload, _ := json.Marshal(map[string]any{"path": path})
	resp, err := router.Call(context.Background(), "docpipe_extract", payload)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var doc Document
	_ = json.Unmarshal(resp, &doc)
	if doc.Format != FormatTXT {
		t.Errorf("Format = %q, want txt", doc.Format)
	}
	if doc.RawText == "" {
		t.Error("expected non-empty RawText")
	}
}

func TestConn_Detect_InvalidJSON(t *testing.T) {
	pipe := New(Config{})
	router := connectivity.New()
	pipe.RegisterConnectivity(router)

	_, err := router.Call(context.Background(), "docpipe_detect", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
