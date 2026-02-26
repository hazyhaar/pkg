# dbopen — production-safe SQLite open

`dbopen` ouvre une base SQLite avec les pragmas HOROS (WAL, foreign_keys, busy_timeout, synchronous) et du retry automatique sur SQLITE_BUSY.

## Quick start

```go
db, err := dbopen.Open("data/app.db",
    dbopen.WithMkdirAll(),
    dbopen.WithSchema(schema),
)
defer db.Close()

// In tests:
db := dbopen.OpenMemory(t)
```

## Pragmas par défaut

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 10000;
PRAGMA synchronous = NORMAL;
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `Open(path, opts)` | Ouvre SQLite avec pragmas + schemas |
| `OpenMemory(t, opts)` | In-memory pour tests (MaxOpenConns=1 auto) |
| `RunTx(ctx, db, fn)` | Transaction avec retry 3x sur SQLITE_BUSY |
| `Exec(ctx, db, query, args)` | Exec avec retry 3x sur SQLITE_BUSY |
| `IsBusy(err)` | Détecte SQLITE_BUSY/SQLITE_LOCKED |
| `WithDriver(name)` | Driver alternatif (ex: "sqlite-trace") |
| `WithTrace()` | Raccourci pour `WithDriver("sqlite-trace")` |
| `WithSchema(sql)` | Applique un schema DDL après ouverture |
| `WithSchemaFile(path)` | Schema depuis un fichier |
| `WithMkdirAll()` | Crée les répertoires parents si absents |
| `WithBusyTimeout(ms)` | Override du busy_timeout |
| `WithCacheSize(pages)` | Override du cache_size |
| `WithSynchronous(mode)` | Override du synchronous |
| `WithoutForeignKeys()` | Désactive foreign_keys |
| `WithoutPing()` | Skip le ping initial |

## Quand utiliser

**Toujours.** Jamais `sql.Open("sqlite", ...)` directement. `dbopen.Open` garantit les pragmas et le retry.

## Anti-patterns

| Ne pas faire | Faire |
|---|---|
| `sql.Open("sqlite", path)` | `dbopen.Open(path)` |
| Appliquer les pragmas manuellement | `dbopen.Open` le fait |
| `OpenMemory` sans `MaxOpenConns(1)` | C'est fait automatiquement |
| Oublier `import _ "modernc.org/sqlite"` | Le driver doit être enregistré |
