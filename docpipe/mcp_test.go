package docpipe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var testMCPImpl = &mcp.Implementation{Name: "docpipe-test", Version: "0.1.0"}

func mcpSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	pipe := New(Config{})
	srv := mcp.NewServer(testMCPImpl, nil)
	pipe.RegisterMCP(srv)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	go func() { _ = srv.Run(ctx, serverT) }()

	client := mcp.NewClient(testMCPImpl, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func mcpCallTool(t *testing.T, session *mcp.ClientSession, name string, args any) string {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if err := result.GetError(); err != nil {
		t.Fatalf("CallTool(%s) tool error: %v", name, err)
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): expected TextContent", name)
	}
	return tc.Text
}

// --- docpipe_formats ---

func TestMCP_Formats(t *testing.T) {
	session := mcpSession(t)

	text := mcpCallTool(t, session, "docpipe_formats", map[string]any{})

	var resp struct {
		Formats []string `json:"formats"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Formats) != 6 {
		t.Errorf("expected 6 formats, got %d: %v", len(resp.Formats), resp.Formats)
	}
	// Must include all known formats.
	expected := map[string]bool{"docx": true, "odt": true, "pdf": true, "md": true, "txt": true, "html": true}
	for _, f := range resp.Formats {
		if !expected[f] {
			t.Errorf("unexpected format: %q", f)
		}
		delete(expected, f)
	}
	for f := range expected {
		t.Errorf("missing format: %q", f)
	}
}

// --- docpipe_detect ---

func TestMCP_Detect(t *testing.T) {
	session := mcpSession(t)

	tests := []struct {
		path   string
		format string
	}{
		{"report.docx", "docx"},
		{"readme.md", "md"},
		{"data.txt", "txt"},
		{"page.html", "html"},
		{"manual.pdf", "pdf"},
		{"document.odt", "odt"},
	}
	for _, tt := range tests {
		text := mcpCallTool(t, session, "docpipe_detect", map[string]any{"path": tt.path})
		var resp struct {
			Format string `json:"format"`
		}
		_ = json.Unmarshal([]byte(text), &resp)
		if resp.Format != tt.format {
			t.Errorf("Detect(%q) = %q, want %q", tt.path, resp.Format, tt.format)
		}
	}
}

// --- docpipe_extract ---

func TestMCP_Extract_Text(t *testing.T) {
	session := mcpSession(t)

	// Create a temp .txt file.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	_ = os.WriteFile(path, []byte("Hello World\nSecond line"), 0644)

	text := mcpCallTool(t, session, "docpipe_extract", map[string]any{"path": path})

	var doc Document
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Format != FormatTXT {
		t.Errorf("Format = %q, want %q", doc.Format, FormatTXT)
	}
	if doc.RawText == "" {
		t.Error("expected non-empty RawText")
	}
}

func TestMCP_Extract_Markdown(t *testing.T) {
	session := mcpSession(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "readme.md")
	_ = os.WriteFile(path, []byte("# Title\n\nParagraph text here.\n\n## Section\n\nMore content."), 0644)

	text := mcpCallTool(t, session, "docpipe_extract", map[string]any{"path": path})

	var doc Document
	_ = json.Unmarshal([]byte(text), &doc)
	if doc.Format != FormatMD {
		t.Errorf("Format = %q, want %q", doc.Format, FormatMD)
	}
	if doc.Title == "" {
		t.Error("expected non-empty Title from markdown heading")
	}
	if len(doc.Sections) == 0 {
		t.Error("expected sections")
	}
}
