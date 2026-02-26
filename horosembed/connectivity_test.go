package horosembed

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hazyhaar/pkg/connectivity"
)

func TestConn_Embed(t *testing.T) {
	emb := New(Config{Dimension: 64, Model: "test-noop"})
	router := connectivity.New()
	RegisterConnectivity(router, emb)

	payload, _ := json.Marshal(map[string]any{"text": "Hello world"})
	resp, err := router.Call(context.Background(), "horosembed_embed", payload)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var result struct {
		Vector    []float32 `json:"vector"`
		Dimension int       `json:"dimension"`
		Model     string    `json:"model"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Dimension != 64 {
		t.Errorf("Dimension = %d, want 64", result.Dimension)
	}
	if len(result.Vector) != 64 {
		t.Errorf("vector len = %d, want 64", len(result.Vector))
	}
	if result.Model != "test-noop" {
		t.Errorf("Model = %q, want %q", result.Model, "test-noop")
	}
}

func TestConn_Batch(t *testing.T) {
	emb := New(Config{Dimension: 32, Model: "test-batch"})
	router := connectivity.New()
	RegisterConnectivity(router, emb)

	payload, _ := json.Marshal(map[string]any{"texts": []string{"a", "b"}})
	resp, err := router.Call(context.Background(), "horosembed_batch", payload)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var result struct {
		Vectors   [][]float32 `json:"vectors"`
		Count     int         `json:"count"`
		Dimension int         `json:"dimension"`
	}
	json.Unmarshal(resp, &result)
	if result.Count != 2 {
		t.Errorf("Count = %d, want 2", result.Count)
	}
	if result.Dimension != 32 {
		t.Errorf("Dimension = %d, want 32", result.Dimension)
	}
}

func TestConn_Embed_InvalidJSON(t *testing.T) {
	emb := New(Config{Dimension: 8})
	router := connectivity.New()
	RegisterConnectivity(router, emb)

	_, err := router.Call(context.Background(), "horosembed_embed", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
