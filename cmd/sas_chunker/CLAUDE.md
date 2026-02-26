# sas_chunker (CLI)

Responsabilite: CLI pour decouper, reassembler et verifier de gros fichiers via la bibliotheque `sas_chunker`.
Depend de: `github.com/hazyhaar/pkg/sas_chunker`
Dependants: aucun (entry point terminal)
Point d'entree: main.go
Types cles: aucun type exporte (`package main`)
Commandes:
- `sas_chunker split <file> [output_dir] [chunk_size_mb]` — decoupe un fichier en chunks (defaut 10 MiB)
- `sas_chunker assemble <chunks_dir> [output_file]` — reassemble les chunks en fichier original
- `sas_chunker verify <chunks_dir>` — verifie l'integrite SHA-256 de chaque chunk sans reassembler
Invariants:
- CLI pure — pas de serveur, pas de base de donnees, pas de config file
- Progress callback affiche sur stderr (pas stdout)
- Build avec `CGO_ENABLED=0`
NE PAS:
- Confondre avec `sas_ingester` (qui est un serveur HTTP)
- Utiliser stdout pour les messages de progression (reserve aux donnees de sortie)
