> **Schema technique** : voir [`watch_schem.md`](watch_schem.md) — lecture prioritaire avant tout code source.

# watch

Responsabilite: Polling generique de changements SQLite avec detection par PRAGMA data_version, debounce, observabilite integree, et WaitForVersion synchrone.
Depend de: standard library uniquement (database/sql, context, log/slog, sync, time)
Dependants: `mcprt/registry` (RunWatcher), `mcprt/types`, `dbsync/publisher`; externes: chrc/domwatch/internal/config
Point d'entree: watch.go
Types cles: `Watcher`, `ChangeDetector` (func(ctx, db) (int64, error)), `Options`, `Stats`
Invariants:
- `PragmaDataVersion` est le detecteur par defaut — il detecte les mutations cross-process et cross-connection
- Si `action` retourne une erreur, la version n'est PAS avancee — l'action sera retentee au prochain cycle
- `WaitForVersion` bloque jusqu'a ce que la version observee soit >= target — utile pour les tests synchrones
- Le debounce reset le timer a chaque nouveau changement pendant la fenetre — pas de tempete de reloads
- Les Stats (checks, changes, errors, reloads, avg_reload_time) sont atomiques et thread-safe
NE PAS:
- Ne pas confondre `PragmaDataVersion` (auto-incrementing, cross-process) et `PragmaUserVersion` (application-controlled, explicit bump)
- Ne pas utiliser un interval trop court (<100ms) en production — chaque check est une requete PRAGMA
- Ne pas oublier que `OnChange` est bloquant — le lancer dans une goroutine
- Ne pas utiliser `MaxColumnDetector` sur une table sans index sur la colonne — ca fera un full scan a chaque poll
