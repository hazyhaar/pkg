// CLAUDE:SUMMARY Defines the Embedder interface, Config, factory New(), and noop implementation.
// CLAUDE:DEPENDS (none — stdlib only for this file)
// CLAUDE:EXPORTS Embedder, Config, New

// Package horosembed provides a transport-agnostic embedding client that
// converts text to float32 vectors via any OpenAI-compatible embedding server.
//
// It decouples embedding generation from storage/indexing so any HOROS component
// can convert text to vectors without knowing the backend (CPU ONNX, GPU vLLM,
// or RunPod serverless).
//
// Usage:
//
//	emb := horosembed.New(horosembed.Config{
//	    Endpoint: "http://localhost:8003",
//	    Model:    "multilingual-e5-large",
//	})
//	vec, err := emb.Embed(ctx, "What is photosynthesis?")
//	// vec is []float32 of dimension 768 (or whatever the model produces)
package horosembed

import (
	"context"
	"log/slog"
	"time"
)

// Embedder converts text to vectors.
type Embedder interface {
	// Embed returns the embedding vector for a single text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns embeddings for multiple texts in one HTTP call.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimension returns the vector dimension (768, 1536, etc).
	// Returns 0 if not yet detected (first call not made).
	Dimension() int

	// Model returns the model name.
	Model() string
}

// Config configures the embedding client.
type Config struct {
	// Endpoint is the base URL of the embedding server (e.g. "http://localhost:8003").
	// If empty, a NoopEmbedder is returned.
	Endpoint string `json:"endpoint" yaml:"endpoint"`

	// Model is the model name sent in the request (e.g. "multilingual-e5-large").
	Model string `json:"model" yaml:"model"`

	// Dimension is the expected vector dimension. 0 means auto-detect on first call.
	Dimension int `json:"dimension" yaml:"dimension"`

	// BatchSize is the maximum number of texts per HTTP request. Default: 32.
	BatchSize int `json:"batch_size" yaml:"batch_size"`

	// Timeout per HTTP request. Default: 30s.
	Timeout time.Duration `json:"timeout" yaml:"timeout"`

	// Logger for debug/error messages. Defaults to slog.Default().
	Logger *slog.Logger `json:"-" yaml:"-"`
}

func (c *Config) defaults() {
	if c.BatchSize <= 0 {
		c.BatchSize = 32
	}
	if c.Timeout <= 0 {
		c.Timeout = 30 * time.Second
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// New creates an Embedder from config. If Endpoint is empty, returns a
// NoopEmbedder that produces zero vectors of the configured dimension.
func New(cfg Config) Embedder {
	cfg.defaults()
	if cfg.Endpoint == "" {
		dim := cfg.Dimension
		if dim <= 0 {
			dim = 768
		}
		return &noopEmbedder{dim: dim, model: cfg.Model}
	}
	return newOpenAIClient(cfg)
}

// noopEmbedder returns zero vectors — useful for testing without a server.
type noopEmbedder struct {
	dim   int
	model string
}

func (n *noopEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, n.dim), nil
}

func (n *noopEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, n.dim)
	}
	return out, nil
}

func (n *noopEmbedder) Dimension() int { return n.dim }
func (n *noopEmbedder) Model() string  { return n.model }
