# dbsync — SQLite replication over QUIC

`dbsync` replicates a filtered SQLite database from a back-office (BO) to one or
more front-office (FO) instances. The BO watches for changes, produces a filtered
snapshot, and pushes it over QUIC. Each FO validates the snapshot and swaps its
local read-only database atomically.

```
  BO (read-write)               FO (read-only)
  ┌────────────────┐            ┌────────────────┐
  │  Source DB      │            │  Public DB      │
  │  (full data)    │            │  (filtered)     │
  └───────┬────────┘            └───────▲────────┘
          │ watch + filter              │ atomic swap
          ▼                             │
  ┌────────────────┐   QUIC/TLS  ┌─────┴──────────┐
  │  Publisher      │───────────▶│  Subscriber     │
  │  (SYN1 + JSON   │            │  (verify hash,  │
  │   + raw bytes)  │            │   rename, open) │
  └────────────────┘            └────────────────┘
```

## Quick start

### Publisher (BO side)

```go
// Static targets (simplest — push to a known FO).
pub := dbsync.NewPublisher(db, dbsync.NewStaticTargetProvider(
    dbsync.Target{Name: "fo-1", Strategy: "dbsync", Endpoint: "91.134.142.134:9443"},
), myFilter(), "db/public.db", tlsCfg)

go pub.Start(ctx)
```

```go
// Dynamic targets via connectivity routes table.
pub := dbsync.NewPublisherWithRoutesDB(db, routesDB, myFilter(), "db/public.db", tlsCfg)
go pub.Start(ctx)
```

### Subscriber (FO side)

```go
sub := dbsync.NewSubscriber("db/public.db", ":9443", tlsCfg)
sub.OnSwap(func() {
    // Recreate services with sub.DB()
})
go sub.Start(ctx)
```

## FilterSpec

`FilterSpec` controls which data is included in the public snapshot.

```go
spec := dbsync.FilterSpec{
    // Copied integrally.
    FullTables: []string{"badges", "reputation_config"},

    // Only rows matching the WHERE clause.
    FilteredTables: map[string]string{
        "engagements": "visibility = 'public'",
        "templates":   "is_blacklisted = 0",
    },

    // Only listed columns kept (others set to NULL/zero). Optional WHERE.
    PartialTables: map[string]dbsync.PartialTable{
        "users": {
            Columns: []string{"user_id", "username", "display_name", "avatar_url"},
            Where:   "is_active = 1",
        },
    },
}
```

Tables not listed anywhere are **dropped** from the snapshot.

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SYNC_LISTEN` | `:9443` | Subscriber QUIC listen address |
| `SYNC_CERT` | | TLS certificate file |
| `SYNC_KEY` | | TLS private key file |
| `SYNC_SAVE_PATH` | `db/public.db` | Publisher local snapshot path |
| `ROUTES_DB_PATH` | | Connectivity routes DB (for RoutesTargetProvider) |
| `BO_URL` | | Back-office URL (for FO auth proxy + redirects) |

## Wire format

Each QUIC stream carries one snapshot:

```
┌──────────┬───────────────┬──────────────┬──────────────────┐
│ "SYN1"   │ meta_len (4B) │ meta JSON    │ raw DB bytes     │
│ (4 bytes)│ big-endian    │ (variable)   │ (meta.Size bytes)│
└──────────┴───────────────┴──────────────┴──────────────────┘
```

Meta JSON (`SnapshotMeta`):
```json
{"version": 1708012345000, "hash": "sha256hex...", "size": 131072, "timestamp": 1708012345}
```

## TLS configuration

**Production** (self-signed CA):
```go
// Server (subscriber):
tlsCfg, _ := dbsync.SyncTLSConfig("cert.pem", "key.pem")

// Client (publisher) — pin CA cert:
tlsCfg, _ := dbsync.SyncClientTLSConfigWithCA("ca.pem")
```

**Development**:
```go
tlsCfg := dbsync.SyncClientTLSConfig(true) // insecureSkipVerify
```
