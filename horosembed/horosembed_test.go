package horosembed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoopEmbedder(t *testing.T) {
	emb := New(Config{Dimension: 768, Model: "test-noop"})

	vec, err := emb.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 768 {
		t.Fatalf("expected 768 dims, got %d", len(vec))
	}
	if emb.Dimension() != 768 {
		t.Fatalf("expected dimension 768, got %d", emb.Dimension())
	}
	if emb.Model() != "test-noop" {
		t.Fatalf("expected model test-noop, got %q", emb.Model())
	}
}

func TestNoopEmbedBatch(t *testing.T) {
	emb := New(Config{Dimension: 128})

	vecs, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 128 {
			t.Fatalf("vec[%d] has %d dims, expected 128", i, len(v))
		}
	}
}

func TestOpenAIClient(t *testing.T) {
	// Mock OpenAI-compatible server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", 404)
			return
		}

		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		data := make([]struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}, len(req.Input))
		for i := range data {
			vec := make([]float32, 4)
			for j := range vec {
				vec[j] = float32(i+1) * 0.1 * float32(j+1)
			}
			data[i].Embedding = vec
			data[i].Index = i
		}

		json.NewEncoder(w).Encode(map[string]any{
			"data":  data,
			"model": req.Model,
		})
	}))
	defer srv.Close()

	emb := New(Config{
		Endpoint:  srv.URL,
		Model:     "test-model",
		BatchSize: 2,
	})

	// Single embed.
	vec, err := emb.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 4 {
		t.Fatalf("expected 4 dims, got %d", len(vec))
	}

	// Auto-detect dimension.
	if emb.Dimension() != 4 {
		t.Fatalf("expected auto-detected dim 4, got %d", emb.Dimension())
	}

	// Batch embed with split (batchSize=2, 3 texts → 2 calls).
	vecs, err := emb.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
}

func TestSerializeDeserializeVector(t *testing.T) {
	original := []float32{1.0, -2.5, 3.14, 0, -0.001}
	blob := SerializeVector(original)
	restored := DeserializeVector(blob)

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(restored), len(original))
	}
	for i := range original {
		if restored[i] != original[i] {
			t.Fatalf("mismatch at %d: %f vs %f", i, restored[i], original[i])
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	c := []float32{0, 1, 0}

	sim := CosineSimilarity(a, b)
	if math.Abs(sim-1.0) > 1e-6 {
		t.Fatalf("identical vectors should have similarity ~1.0, got %f", sim)
	}

	sim = CosineSimilarity(a, c)
	if math.Abs(sim) > 1e-6 {
		t.Fatalf("orthogonal vectors should have similarity ~0, got %f", sim)
	}
}

func TestCalculateNorm(t *testing.T) {
	vec := []float32{3, 4}
	norm := CalculateNorm(vec)
	if math.Abs(norm-5.0) > 1e-6 {
		t.Fatalf("expected norm 5.0, got %f", norm)
	}
}

func TestEmbedFactory(t *testing.T) {
	// EmbedFactory with empty endpoint should create a noopEmbedder.
	factory := EmbedFactory()
	cfg := `{"model":"test-factory","dimension":64,"batch_size":16}`
	handler, closeFn, err := factory("", json.RawMessage(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if closeFn != nil {
		t.Error("expected nil closeFn for noop")
	}

	// Call the handler with a batch request.
	payload, _ := json.Marshal(map[string]any{"texts": []string{"hello", "world"}})
	resp, err := handler(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}

	var result struct {
		Vectors   [][]float32 `json:"vectors"`
		Count     int         `json:"count"`
		Dimension int         `json:"dimension"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Count != 2 {
		t.Errorf("Count = %d, want 2", result.Count)
	}
	if result.Dimension != 64 {
		t.Errorf("Dimension = %d, want 64", result.Dimension)
	}
}
