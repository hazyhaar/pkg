
# horosembed -- Technical Schema
# Transport-agnostic embedding client: text to float32 vectors via OpenAI-compatible API

```
╔══════════════════════════════════════════════════════════════════════════════════╗
║  horosembed — Text → float32 vector embedding client (transport-agnostic)     ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  Config ──────→ New(Config) ────────┬──→ openaiClient  (Endpoint non-empty)   ║
║  {                                  │    (HTTP POST /v1/embeddings)            ║
║    Endpoint  string                 │                                          ║
║    Model     string                 ├──→ noopEmbedder  (Endpoint empty)       ║
║    Dimension int (0=auto)           │    (zero vectors, for testing)           ║
║    BatchSize int (def: 32)          │                                          ║
║    Timeout   time.Duration (30s)    │                                          ║
║    Logger    *slog.Logger           │                                          ║
║  }                                  ▼                                          ║
║                              ┌─────────────┐                                  ║
║                              │  Embedder    │ (interface)                      ║
║                              │  interface   │                                  ║
║  "hello world" ────────────→ │  Embed()     │──→ []float32 (e.g. 768 dims)    ║
║  ["text1","text2"] ────────→ │  EmbedBatch()│──→ [][]float32                  ║
║                              │  Dimension() │──→ int                          ║
║                              │  Model()     │──→ string                       ║
║                              └─────────────┘                                  ║
╚══════════════════════════════════════════════════════════════════════════════════╝
```

## OpenAI Client HTTP Flow

```
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │  openaiClient.EmbedBatch(ctx, texts)                                        │
  │                                                                              │
  │  texts []string                                                              │
  │    │                                                                         │
  │    ▼                                                                         │
  │  Split into sub-batches of BatchSize (default 32)                            │
  │    │                                                                         │
  │    ▼ (for each sub-batch)                                                    │
  │  callAPI(ctx, batch)                                                         │
  │    │                                                                         │
  │    ▼                                                                         │
  │  POST {endpoint}/v1/embeddings ──────────────────→  Embedding Server         │
  │  Content-Type: application/json                     (vLLM / ONNX /           │
  │  Body: {                                             RunPod / OpenAI)        │
  │    "model": "multilingual-e5-large",                                        │
  │    "input": ["text1", "text2", ...]                       │                 │
  │  }                                                        │                 │
  │                                                           ▼                 │
  │  Response ←──────────────────────────────────────── 200 OK                  │
  │  {                                                                           │
  │    "data": [                                                                 │
  │      {"embedding": [0.1, 0.2, ...], "index": 0},                            │
  │      {"embedding": [0.3, 0.4, ...], "index": 1}                             │
  │    ],                                                                        │
  │    "model": "...",                                                           │
  │    "usage": {"prompt_tokens": N, "total_tokens": M}                          │
  │  }                                                                           │
  │    │                                                                         │
  │    ▼                                                                         │
  │  Auto-detect dimension on first call (mutex-protected)                       │
  │  Reassemble vectors in input order (by index field)                          │
  │  Verify all slots filled (no missing embeddings)                             │
  │    │                                                                         │
  │    ▼                                                                         │
  │  [][]float32 (all sub-batches concatenated)                                  │
  └──────────────────────────────────────────────────────────────────────────────┘
```

## Vector Operations (vector.go)

```
  ┌─────────────────────────────────────────────────────────────────────────────┐
  │  Serialization                                                              │
  │  ─────────────                                                              │
  │  SerializeVector([]float32)   ──→ []byte  (little-endian, 4 bytes/float)   │
  │  DeserializeVector([]byte)    ──→ []float32                                │
  │                                                                             │
  │  Similarity                                                                 │
  │  ──────────                                                                 │
  │  CosineSimilarity(a, b []float32)                          ──→ float64     │
  │    dot(a,b) / (||a|| * ||b||)                                              │
  │                                                                             │
  │  CosineSimilarityOptimized(a, b []float32, normA, normB)   ──→ float64     │
  │    dot(a,b) / (normA * normB)    ← pre-computed norms                      │
  │                                                                             │
  │  CalculateNorm([]float32)                                  ──→ float64     │
  │    sqrt(sum(v[i]^2))             ← L2 norm                                │
  └─────────────────────────────────────────────────────────────────────────────┘
```

## MCP Tools (RegisterMCP)

```
  ┌────────────────────────────────────────────────────────────────────────────┐
  │  Tool Name          │ Input                    │ Output                    │
  ├─────────────────────┼──────────────────────────┼───────────────────────────┤
  │  horosembed_embed   │ {text: string}           │ {vector, dimension,       │
  │                     │                          │  model}                   │
  │  horosembed_batch   │ {texts: []string}        │ {vectors, count,          │
  │                     │                          │  dimension, model}        │
  └─────────────────────┴──────────────────────────┴───────────────────────────┘
```

## Connectivity Integration (RegisterConnectivity)

```
  ┌────────────────────────────────────────────────────────────────────────────┐
  │  Local Handlers                                                            │
  │  ──────────────                                                            │
  │  horosembed_embed  — {text} → {vector, dimension, model}                   │
  │  horosembed_batch  — {texts} → {vectors, count, dimension, model}          │
  │                                                                            │
  │  Transport Factory (EmbedFactory)                                          │
  │  ────────────────────────────────                                          │
  │                                                                            │
  │  router.RegisterTransport("embed", horosembed.EmbedFactory())              │
  │                                                                            │
  │  ┌────────────────┐     ┌───────────────────┐     ┌─────────────────┐     │
  │  │ connectivity   │────→│ EmbedFactory()    │────→│ openaiClient    │     │
  │  │ Router.Call()  │     │ TransportFactory  │     │ per endpoint    │     │
  │  │ "embed"        │     │ (endpoint,config) │     │ from config     │     │
  │  └────────────────┘     └───────────────────┘     └─────────────────┘     │
  │                                                                            │
  │  Backend switchable at runtime via SQL UPDATE on routes table              │
  └────────────────────────────────────────────────────────────────────────────┘
```

## Key Types

```
Embedder interface {
    Embed(ctx, text)           → ([]float32, error)
    EmbedBatch(ctx, texts)     → ([][]float32, error)
    Dimension()                → int           // 0 until first API call
    Model()                    → string
}

Config {
    Endpoint   string          — "" → noopEmbedder
    Model      string          — e.g. "multilingual-e5-large"
    Dimension  int             — 0 = auto-detect on first call
    BatchSize  int             — default 32
    Timeout    time.Duration   — default 30s
    Logger     *slog.Logger
}

openaiClient {
    endpoint, model, dim, batchSize string/int
    client *http.Client
    mu sync.Mutex               — protects dim auto-detect
}

noopEmbedder {
    dim int, model string       — returns zero vectors
}
```

## Dependencies

```
External:
  github.com/modelcontextprotocol/go-sdk/mcp  — MCP tool registration

Internal (hazyhaar/pkg):
  kit           — kit.RegisterMCPTool, kit.MCPDecodeResult
  connectivity  — Router, Handler, TransportFactory
```

## Database Tables

```
  (none — stateless client package)
```

## Key Function Signatures

```go
func New(cfg Config) Embedder
func RegisterMCP(srv *mcp.Server, emb Embedder)
func RegisterConnectivity(router *connectivity.Router, emb Embedder)
func EmbedFactory() connectivity.TransportFactory
func SerializeVector(vec []float32) []byte
func DeserializeVector(blob []byte) []float32
func CosineSimilarity(a, b []float32) float64
func CosineSimilarityOptimized(a, b []float32, normA, normB float64) float64
func CalculateNorm(vec []float32) float64
```
