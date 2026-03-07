# shardschema — Technical Schema

**Source de verite DDL pour les tables par-dossier (shards usertenant).**

Module: `github.com/hazyhaar/pkg/shardschema`
Go: 1.25 | CGO_ENABLED=0
Library-only, pas de cmd/

## Arborescence

```
shardschema/
  schema.go          DDL canoniques + fonctions Apply*
```

## Schema de donnees — 3 tables DDL par shard

### rag_vectors

```
┌──────────────┬─────────┬──────────────────────────────┐
│ Colonne      │ Type    │ Contraintes                  │
├──────────────┼─────────┼──────────────────────────────┤
│ id           │ TEXT    │ PRIMARY KEY                  │
│ document_id  │ TEXT    │ NOT NULL                     │
│ source_id    │ TEXT    │                              │
│ source_url   │ TEXT    │                              │
│ source_type  │ TEXT    │                              │
│ title        │ TEXT    │                              │
│ chunk_index  │ INTEGER │ NOT NULL                     │
│ chunk_text   │ TEXT    │ NOT NULL                     │
│ token_count  │ INTEGER │                              │
│ overlap_prev │ INTEGER │ DEFAULT 0                    │
│ vector       │ BLOB    │ NOT NULL                     │
│ dimension    │ INTEGER │ NOT NULL                     │
│ model        │ TEXT    │ NOT NULL                     │
│ content_hash │ TEXT    │                              │
│ created_at   │ TEXT    │ NOT NULL DEFAULT now()       │
└──────────────┴─────────┴──────────────────────────────┘
 idx_rag_vectors_document(document_id)
 idx_rag_vectors_hash(content_hash)
```

### claim_entities

```
┌───────────┬──────┬──────────────────────────────────┐
│ Colonne   │ Type │ Contraintes                      │
├───────────┼──────┼──────────────────────────────────┤
│ id        │ TEXT │ PRIMARY KEY                      │
│ vector_id │ TEXT │ NOT NULL FK → rag_vectors(id)    │
│ type      │ TEXT │ NOT NULL                         │
│ value     │ TEXT │ NOT NULL                         │
│ raw       │ TEXT │                                  │
│ unit      │ TEXT │                                  │
└───────────┴──────┴──────────────────────────────────┘
 idx_claim_entities_type(type)
 idx_claim_entities_value(value)
 idx_claim_entities_vid(vector_id)
```

### email_documents

```
┌──────────────┬──────┬──────────────────────────────┐
│ Colonne      │ Type │ Contraintes                  │
├──────────────┼──────┼──────────────────────────────┤
│ id           │ TEXT │ PRIMARY KEY                  │
│ source_id    │ TEXT │ NOT NULL UNIQUE              │
│ from_addr    │ TEXT │                              │
│ to_addr      │ TEXT │                              │
│ cc_addr      │ TEXT │                              │
│ date_str     │ TEXT │                              │
│ subject      │ TEXT │                              │
│ body_preview │ TEXT │                              │
│ thread_id    │ TEXT │                              │
│ created_at   │ TEXT │ NOT NULL DEFAULT now()       │
└──────────────┴──────┴──────────────────────────────┘
 idx_email_docs_source(source_id)
 idx_email_docs_thread(thread_id)
```

## Types publics exportes

```
╔══════════════════════════════════════════════════════╗
║  Constants (string DDL)                              ║
╠══════════════════════════════════════════════════════╣
║  RAGVectorsSchema      DDL rag_vectors + indexes     ║
║  ClaimEntitiesSchema   DDL claim_entities + indexes  ║
║  EmailDocumentsSchema  DDL email_documents + indexes ║
╚══════════════════════════════════════════════════════╝

╔══════════════════════════════════════════════════════╗
║  Functions                                           ║
╠══════════════════════════════════════════════════════╣
║  ApplyRAGVectors(db *sql.DB) error                   ║
║  ApplyClaimEntities(db *sql.DB) error                ║
║  ApplyEmailDocuments(db *sql.DB) error               ║
║  ApplyAll(db *sql.DB) error                          ║
╚══════════════════════════════════════════════════════╝
```

## Consommateurs

```
╔══════════════════════════════════════════════════════╗
║  HORAG/internal/store/schema.go                      ║
║    ApplyRAGVectors, ApplyClaimEntities               ║
║    Applique DDL sur chaque shard ouverte             ║
╠══════════════════════════════════════════════════════╣
║  siftrag/internal/services/shard_schema.go           ║
║    ApplyRAGVectors                                   ║
║    Initialise la table vecteurs au premier acces     ║
╠══════════════════════════════════════════════════════╣
║  siftrag/internal/services/search_test.go            ║
║    ApplyRAGVectors                                   ║
║    Setup shard de test pour recherche semantique     ║
╠══════════════════════════════════════════════════════╣
║  scripts/mbox-cleaner/schema.go                      ║
║    ApplyAll                                          ║
║    Cree toutes les tables (email + vecteurs + claims)║
╚══════════════════════════════════════════════════════╝
```
