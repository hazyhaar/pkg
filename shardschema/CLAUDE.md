> **Schema technique** : voir [`shardschema_schem.md`](shardschema_schem.md) — lecture prioritaire avant tout code source.

# shardschema

Responsabilite: Source de verite DDL pour les tables per-dossier (shards usertenant) — rag_vectors, claim_entities, email_documents.
Depend de: aucune dependance interne (package feuille, stdlib only)
Dependants: HORAG/internal/store, siftrag/internal/services, scripts/mbox-cleaner
Point d'entree: `schema.go`
Types cles: constantes DDL (RAGVectorsSchema, ClaimEntitiesSchema, EmailDocumentsSchema)
Exports: ApplyAll, ApplyRAGVectors, ApplyClaimEntities, ApplyEmailDocuments

## Build / Test

```bash
go test ./shardschema/...   # pas de test propre — valide via horoscheck
```

## Invariants

- Toute modification de schema DDL se fait ICI, pas dans les consumers
- Les fonctions Apply* sont idempotentes (CREATE TABLE IF NOT EXISTS)
- Pas de dependance au-dela de `database/sql`

## NE PAS

- Dupliquer du DDL dans HORAG, siftrag ou mbox-cleaner — deleguer ici
- Ajouter des dependances non-stdlib
