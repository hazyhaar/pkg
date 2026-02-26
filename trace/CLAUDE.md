# trace

Responsabilite: Tracing SQL transparent via un driver "sqlite-trace" wrappant modernc.org/sqlite — interception Exec/Query, logging slog adaptatif, persistence async locale (Store) ou distante (RemoteStore FO->BO).
Depend de: `modernc.org/sqlite`, `github.com/hazyhaar/pkg/kit`
Dependants: `sas_ingester/store` (import side-effect), `dbopen/dbopen`; externes: repvow/cmd/repvow, horum/cmd/horum, horostracker/main
Point d'entree: trace.go (Entry, Recorder, SetStore, init driver registration), driver.go (TracingDriver, tracingConn, tracingStmt), store.go (Store local), remote.go (RemoteStore), ingest.go (IngestHandler)
Types cles: `Entry`, `Recorder` (interface), `Store`, `RemoteStore`, `TracingDriver`, `IngestHandler`
Invariants:
- Le driver "sqlite-trace" est enregistre dans `init()` — un simple `import _ "github.com/hazyhaar/pkg/trace"` suffit
- Le `Store` DOIT utiliser le driver "sqlite" brut pour sa propre DB — jamais "sqlite-trace" (recursion infinie)
- Les requetes `PRAGMA` sont filtrees sauf si lentes (>10ms) ou en erreur — reduction de 99.5% du bruit dbsync watcher
- `RemoteStore` et `Store` utilisent le meme pattern async: channel 1024, batch 64, flush chaque seconde
- `RecordAsync` est non-bloquant — drop silencieux si le buffer est plein
- Les niveaux slog sont adaptatifs: Debug normal, Warn >100ms, Error si erreur
NE PAS:
- Ne pas ouvrir la DB de traces avec "sqlite-trace" — utiliser "sqlite" brut pour eviter la recursion infinie
- Ne pas appeler `SetStore(nil)` sans raison — ca desactive la persistence, seul le slog reste
- Ne pas oublier `store.Init()` apres `NewStore()` — il cree la table `sql_traces`
- Ne pas utiliser `IngestHandler` sans authentification sur le BO — c'est un endpoint interne mais doit etre protege
