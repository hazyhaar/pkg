# chunk

Responsabilite: Decoupe du texte en fragments avec overlap pour RAG et indexation FTS5, avec respect des frontieres de paragraphes.
Depend de: rien (stdlib uniquement)
Dependants: `HORAG` (pipeline vectorisation), `chrc/domkeeper/internal/ingest`
Point d'entree: `chunk.go`
Types cles: `Options` (MaxTokens, OverlapTokens, MinChunkTokens), `Chunk` (Index, Text, TokenCount, OverlapPrev)
Invariants:
- Chaque chunk ne depasse jamais `MaxTokens`
- Le premier chunk a toujours `OverlapPrev = 0`
- Les chunks trop courts (< MinChunkTokens) sont fusionnes avec le precedent
- `Split("")` retourne nil, jamais un slice vide
- Tokenization = whitespace split (approximation mot = token)
NE PAS:
- Confondre `CountTokens` (whitespace split) avec `EstimateTokens` (heuristique BPE) — le premier est utilise pour le chunking, le second pour les estimations externes
- Modifier la strategie de split sans verifier les tests de paragraph-aware splitting

## Migration depuis chrc

Ce package a ete migre depuis `chrc/chunk/` (2026-02-25). Les imports dans chrc doivent pointer vers `github.com/hazyhaar/pkg/chunk`.
