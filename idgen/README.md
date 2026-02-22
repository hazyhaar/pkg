# idgen — pluggable ID generation

`idgen` provides composable ID generators. The ecosystem default is **UUIDv7**
(RFC 9562): time-sortable, globally unique, and friendly to B-Tree indexes.

## Quick start

```go
id := idgen.New()                                  // UUIDv7: "019513ab-..."
id = idgen.Prefixed("dos_", idgen.Default)()       // "dos_019513ab-..."
id = idgen.NanoID(8)()                             // "a3f8k2p1"
id = idgen.Timestamped(idgen.NanoID(6))()          // "20260221T120000Z_abc123"

parsed, err := idgen.Parse("019513ab-89ab-7cde-...")
```

## Strategies

| Generator | Output | Use case |
|-----------|--------|----------|
| `UUIDv7()` | `019513ab-89ab-7cde-8f01-...` | Default, time-sortable |
| `NanoID(n)` | `a3f8k2p1` | Short, ephemeral (session IDs) |
| `Prefixed(p, gen)` | `aud_019513ab-...` | Domain-scoped IDs |
| `Timestamped(gen)` | `20260221T120000Z_abc` | Human-readable time prefix |

## Ecosystem conventions

| Prefix | Domain |
|--------|--------|
| `aud_` | Audit entries |
| `evt_` | Business events |
| `dos_` | Dossiers |
| `req_` | HTTP requests |
| `tus_` | Resumable uploads |

## Exported API

| Symbol | Description |
|--------|-------------|
| `Generator` | `func() string` |
| `Default` | Ecosystem default (`UUIDv7()`) |
| `New()` | Generate one ID with Default |
| `UUIDv7()` | RFC 9562 generator |
| `NanoID(length)` | Base-36 random generator |
| `Prefixed(prefix, gen)` | Add prefix to any generator |
| `Timestamped(gen)` | Add ISO 8601 timestamp prefix |
| `Parse(s)` / `MustParse(s)` | UUID validation |
