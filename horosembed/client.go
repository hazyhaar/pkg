// CLAUDE:SUMMARY OpenAI-compatible HTTP client for embedding API with batching and auto-dimension detection.
// CLAUDE:EXPORTS openaiClient
package horosembed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// openaiClient implements Embedder using the OpenAI /v1/embeddings API format.
// This covers vLLM, ONNX Runtime Server, RunPod, and OpenAI itself.
type openaiClient struct {
	endpoint  string // e.g. "http://localhost:8003"
	model     string
	dim       int // 0 = auto-detect
	batchSize int
	client    *http.Client
	cfg       Config
	mu        sync.Mutex // protects dim on first call
}

func newOpenAIClient(cfg Config) *openaiClient {
	return &openaiClient{
		endpoint:  strings.TrimRight(cfg.Endpoint, "/"),
		model:     cfg.Model,
		dim:       cfg.Dimension,
		batchSize: cfg.BatchSize,
		client:    &http.Client{Timeout: cfg.Timeout},
		cfg:       cfg,
	}
}

// embedRequest is the JSON body sent to /v1/embeddings.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the JSON response from /v1/embeddings (OpenAI format).
type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *openaiClient) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (c *openaiClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Split into batches of batchSize.
	result := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += c.batchSize {
		end := start + c.batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		vecs, err := c.callAPI(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("batch [%d:%d]: %w", start, end, err)
		}
		copy(result[start:end], vecs)
	}
	return result, nil
}

func (c *openaiClient) callAPI(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(embedRequest{
		Model: c.model,
		Input: texts,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.endpoint + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, string(respBody))
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embeddings returned from %s", url)
	}

	// Auto-detect dimension on first call.
	if c.dim == 0 && len(result.Data[0].Embedding) > 0 {
		c.mu.Lock()
		if c.dim == 0 {
			c.dim = len(result.Data[0].Embedding)
			c.cfg.Logger.Info("auto-detected embedding dimension",
				"dimension", c.dim, "model", result.Model)
		}
		c.mu.Unlock()
	}

	// Reassemble in input order (OpenAI returns sorted by index).
	vecs := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index >= 0 && d.Index < len(vecs) {
			vecs[d.Index] = d.Embedding
		}
	}

	// Verify all slots are filled.
	for i, v := range vecs {
		if v == nil {
			return nil, fmt.Errorf("missing embedding for input index %d", i)
		}
	}
	return vecs, nil
}

func (c *openaiClient) Dimension() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dim
}

func (c *openaiClient) Model() string { return c.model }
