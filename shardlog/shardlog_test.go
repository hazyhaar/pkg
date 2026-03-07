package shardlog

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/pkg/kit"
)

func TestInitAndLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shard.log")

	if err := Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer Sync()

	ctx := kit.WithTraceID(context.Background(), "trace-abc")
	ctx = kit.WithUserID(ctx, "user-123")

	Log(ctx, OpOpened, "dossier-001", slog.String("strategy", "local"))

	Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	checks := map[string]string{
		"op":         OpOpened,
		"dossier_id": "dossier-001",
		"trace_id":   "trace-abc",
		"user_id":    "user-123",
		"strategy":   "local",
	}
	for k, want := range checks {
		got, ok := entry[k].(string)
		if !ok || got != want {
			t.Errorf("field %q = %q, want %q", k, got, want)
		}
	}

	if _, ok := entry["time"]; !ok {
		t.Error("missing 'time' field")
	}
	if _, ok := entry["level"]; !ok {
		t.Error("missing 'level' field")
	}
}

func TestLogNilLogger(t *testing.T) {
	// Reset global state.
	mu.Lock()
	logger = nil
	file = nil
	mu.Unlock()

	// Should not panic.
	Log(context.Background(), OpSearch, "dossier-999")
}

func TestHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hook.log")

	if err := Init(path); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer Sync()

	hook := Hook()
	hook(OpEvicted, "dossier-002", slog.String("reason", "lru"))

	Sync()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}

	var entry map[string]any
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	if got := entry["op"]; got != OpEvicted {
		t.Errorf("op = %v, want %v", got, OpEvicted)
	}
	if got := entry["dossier_id"]; got != "dossier-002" {
		t.Errorf("dossier_id = %v, want dossier-002", got)
	}
	if got := entry["reason"]; got != "lru" {
		t.Errorf("reason = %v, want lru", got)
	}
}

func TestMultipleInit(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "first.log")
	path2 := filepath.Join(dir, "second.log")

	if err := Init(path1); err != nil {
		t.Fatalf("Init first: %v", err)
	}
	Log(context.Background(), OpCreated, "d-1")
	Sync()

	if err := Init(path2); err != nil {
		t.Fatalf("Init second: %v", err)
	}
	Log(context.Background(), OpDeleted, "d-2")
	Sync()

	// First file should have one entry.
	data1, _ := os.ReadFile(path1)
	var e1 map[string]any
	if err := json.Unmarshal(data1, &e1); err != nil {
		t.Fatalf("parse first: %v", err)
	}
	if e1["op"] != OpCreated {
		t.Errorf("first log op = %v, want %v", e1["op"], OpCreated)
	}

	// Second file should have one entry.
	data2, _ := os.ReadFile(path2)
	var e2 map[string]any
	if err := json.Unmarshal(data2, &e2); err != nil {
		t.Fatalf("parse second: %v", err)
	}
	if e2["op"] != OpDeleted {
		t.Errorf("second log op = %v, want %v", e2["op"], OpDeleted)
	}
}
