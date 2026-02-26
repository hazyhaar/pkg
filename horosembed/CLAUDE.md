# horosembed

Responsabilite: Client embeddings transport-agnostique — convertit du texte en vecteurs float32 via n'importe quel serveur compatible OpenAI /v1/embeddings.
Depend de: `github.com/hazyhaar/pkg/kit`, `github.com/hazyhaar/pkg/connectivity`, `github.com/modelcontextprotocol/go-sdk/mcp`
Dependants: `HORAG` (pipeline vectorisation), `chrc/e2e/` (tests integration), `chrc/vecbridge/`
Point d'entree: `horosembed.go`
Types cles: `Embedder` (interface: Embed, EmbedBatch, Dimension, Model), `Config` (Endpoint, Model, Dimension, BatchSize, Timeout), `openaiClient` (implementation HTTP), `noopEmbedder` (zero vectors pour tests)
Invariants:
- Si `Endpoint` est vide, `New()` retourne un `noopEmbedder` (zero vectors, dimension configurable)
- Auto-detection de la dimension au premier appel API si `Dimension = 0`
- BatchSize par defaut = 32, Timeout par defaut = 30s
- Compatible vLLM, ONNX Runtime Server, RunPod, OpenAI
- `SerializeVector` / `DeserializeVector` : little-endian float32 blob
- `CosineSimilarity` et `CosineSimilarityOptimized` (avec normes pre-calculees)
- `RegisterMCP` expose 2 tools : `horosembed_embed`, `horosembed_batch`
- `RegisterConnectivity` expose 2 handlers : `horosembed_embed`, `horosembed_batch`
- `EmbedFactory()` retourne une `connectivity.TransportFactory` pour dispatch multi-backend
NE PAS:
- Utiliser `noopEmbedder` en production (zero vectors = ANN search inutilisable)
- Oublier que `EmbedBatch` decoupe automatiquement en sous-batches de `BatchSize`
- Confondre `Dimension()` (runtime, peut etre 0 avant le premier appel) avec `Config.Dimension` (statique)

## Migration depuis chrc

Ce package a ete migre depuis `chrc/horosembed/` (2026-02-25). Les imports dans chrc doivent pointer vers `github.com/hazyhaar/pkg/horosembed`.
