# audit

Responsabilite: Logger d'audit SQLite asynchrone avec batch writes -- enregistre qui a fait quoi, quand, via quelle couche de transport.
Depend de: `github.com/hazyhaar/pkg/idgen`, `github.com/hazyhaar/pkg/kit`
Dependants: repvow (actions, main), horum (actions, main, e2e tests), horostracker (mcp/server, main)
Point d'entree: `logger.go` (SQLiteLogger + flushLoop)
Types cles: `Entry`, `Logger` (interface), `SQLiteLogger`, `Option`, `Middleware` (kit endpoint wrapper)
Invariants:
- Le channel async a un buffer de 256 ; les entries sont droppees silencieusement si le buffer est plein (warn log)
- `fillDefaults` genere un EntryID via idgen si vide, infere Status depuis Error
- `flushLoop` flush toutes les 500ms OU quand le batch atteint 32 entries
- `Close()` draine le channel puis attend la fin du flushLoop via `done` chan
- Le Schema doit etre applique via `Init()` avant toute ecriture
NE PAS:
- Appeler `LogAsync` apres `Close()` -- le channel est ferme, panic possible
- Oublier d'appeler `Init()` -- les INSERTs echoueront sans la table audit_log
- Utiliser `Log()` en hot path -- preferer `LogAsync()` qui ne bloque pas
