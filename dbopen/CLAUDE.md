# dbopen

Responsabilite: Helper d'ouverture SQLite appliquant les pragmas production-safe HOROS via DSN `_pragma=` (per-connection) avec `_txlock=immediate`, retry SQLITE_BUSY et chargement de schemas.
Depend de: aucune dependance interne (package feuille)
Dependants: connectivity/schema, channels/schema, repvow (database), horum (database), chrc (main, domkeeper, domregistry, vecbridge), vtq (tests), HORAG (pipeline)
Point d'entree: `dbopen.go` (Open, OpenMemory, buildDSN)
Types cles: `Option`, `Open`, `OpenMemory`, `RunTx`, `Exec`, `IsBusy`
Invariants:
- Pragmas appliques via DSN `_pragma=` (pas `db.Exec`) — chaque connexion du pool les recoit
- DSN type : `path?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)`
- `_txlock=immediate` : toutes les transactions utilisent `BEGIN IMMEDIATE` (pas DEFERRED)
- Pragmas par defaut : `foreign_keys=ON`, `journal_mode=WAL`, `busy_timeout=10000`, `synchronous=NORMAL`
- `OpenMemory(t)` force `MaxOpenConns(1)` car chaque connexion ":memory:" cree une DB separee
- `RunTx` et `Exec` retentent automatiquement 3 fois avec backoff 100/200/300ms sur SQLITE_BUSY
- `WithTrace()` est un raccourci pour `WithDriver("sqlite-trace")`
- Les schema files sont executes dans l'ordre d'enregistrement, apres les pragmas
- `db.Ping()` est appele par defaut (desactivable via `WithoutPing()`)
NE PAS:
- Utiliser `sql.Open("sqlite", ...)` directement -- toujours passer par `dbopen.Open`
- Appeler `db.Exec("PRAGMA ...")` dans le code client -- les pragmas sont dans le DSN
- Utiliser `db.Begin()` -- double violation (pas de context + DEFERRED)
- Oublier `import _ "modernc.org/sqlite"` avant d'appeler Open (le driver doit etre enregistre)
- Utiliser `OpenMemory` sans `MaxOpenConns(1)` (c'est fait automatiquement, ne pas override)
Pourquoi DSN et pas Exec:
- `database/sql` pool les connexions. `db.Exec("PRAGMA busy_timeout=10000")` ne touche qu'UNE connexion.
- Les autres connexions du pool n'ont pas le pragma → `busy_timeout=0` → SQLITE_BUSY instantane.
- `_pragma=` dans le DSN est applique par le driver modernc a chaque nouvelle connexion.
