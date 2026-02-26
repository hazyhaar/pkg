# sas_chunker

Responsabilite: Decoupage de fichiers en chunks SHA-256 verifies avec manifest JSON, reassemblage, et verification d'integrite.
Depend de: standard library uniquement (crypto/sha256, encoding/json, os, path/filepath)
Dependants: `sas_ingester/upload`, `sas_ingester/tus`; externes: cmd/sas_chunker
Point d'entree: sas_chunker.go
Types cles: `Manifest`, `ChunkMeta`, `VerifyResult`, `ProgressFunc`
Invariants:
- Chaque chunk est identifie par son SHA-256 — dedup par hash, pas par index
- Le manifest contient le hash SHA-256 global du fichier original — `Assemble` le verifie apres reassemblage
- `SplitReader` stream directement sans fichier temporaire — le hash global est calcule via `TeeReader`
- `validateChunkNames` verifie l'absence de path traversal (../) dans les noms de chunks
- Le `DefaultChunkSize` est 10 MiB — configurable mais ne doit jamais etre 0
NE PAS:
- Ne pas passer `chunkSize <= 0` sans savoir que ca sera remplace par `DefaultChunkSize` (10 MiB)
- Ne pas modifier un chunk apres le split — le hash dans le manifest ne correspondra plus
- Ne pas utiliser `Split` pour des streams non-seekable — utiliser `SplitReader` a la place
- Ne pas ignorer la valeur de retour de `Verify` — `OK()` doit etre true avant tout traitement downstream
