# sas_ingester — file ingestion pipeline

`sas_ingester` provides a complete file ingestion system with resumable uploads
(tus protocol), security scanning, metadata extraction, and webhook routing.

```
Upload (tus)
    │
    ▼
  Receive & Chunk ──► SHA-256 dedup check
    │
    ▼
  Metadata extraction (MIME, entropy, magic bytes, trailer)
    │
    ▼
  Security scan (zip bomb, polyglot, macro, ClamAV)
    │
    ▼
  Prompt injection scan (pattern-based)
    │
    ▼
  Webhook routing (opaque or jwt_passthru)
```

## Quick start

```go
cfg, _ := sas_ingester.LoadConfig("config.yaml")

ing, _ := sas_ingester.NewIngester(cfg,
    sas_ingester.WithIDGenerator(idgen.Prefixed("dos_", idgen.Default)),
    sas_ingester.WithAudit(auditLogger),
)
result, _ := ing.Ingest(fileReader, dossierID, ownerSub)
```

## Resumable uploads (tus)

```go
tus := sas_ingester.NewTusHandler(store, cfg, idgen.Default)
upload, _ := tus.Create(dossierID, ownerSub, totalSize)
tus.Patch(upload.UploadID, 0, chunkReader)
tus.Complete(upload.UploadID)
```

## Security scanning

| Check | Detection |
|-------|-----------|
| Zip bomb | > 10 PK headers in < 1 MiB |
| Polyglot | Multiple magic numbers in one file |
| Macro | OLE2/VBA signatures in Office files |
| ClamAV | INSTREAM protocol via Unix socket |
| Prompt injection | Pattern-based (jailbreak, delimiter, override) |

## Webhook routing

Two auth modes for downstream consumers:

| Mode | Identity | Use case |
|------|----------|----------|
| `opaque_only` | Dossier ID only | Privacy-preserving |
| `jwt_passthru` | Original JWT forwarded | Authenticated pipelines |

Webhooks are signed with HMAC-SHA256 (`X-Signature-256`). Failed deliveries
retry with exponential backoff (max 5 attempts).

## Schema

5 tables: `dossiers`, `pieces`, `chunks`, `routes_pending`, `tus_uploads`.

## Exported API

| Symbol | Description |
|--------|-------------|
| `Ingester` | Full pipeline orchestrator |
| `NewIngester(cfg, opts)` | Create ingester |
| `TusHandler` | Resumable upload manager |
| `Store` | SQLite persistence layer |
| `Router` | Webhook fan-out + retry |
| `Config`, `LoadConfig(path)` | YAML configuration |
| `ParseJWT(token, secret)` | JWT validation |
