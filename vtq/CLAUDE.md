# vtq

Responsabilite: Visibility Timeout Queue SQLite-native — pub/claim/ack/nack avec timeout de visibilite, batch processing concurrent, et election de leader par calibration.
Depend de: standard library uniquement (database/sql, context, log/slog, sync, time)
Dependants: aucun dans pkg/; externes: chrc/domkeeper/keeper, chrc/domkeeper/internal/schedule
Point d'entree: vtq.go
Types cles: `Q`, `Job`, `Options`, `Handler` (func(ctx, *Job) error)
Invariants:
- Le claim est atomique via `UPDATE ... WHERE id = (SELECT ... LIMIT 1) RETURNING` — pas de race condition
- Le `visible_at` est en millisecondes epoch — pas en secondes
- Un job non-acke reapparait automatiquement apres expiration du visibility timeout
- `MaxAttempts = 0` signifie illimite — les jobs sont re-delivres indefiniment
- `Purge` supprime TOUS les jobs de la queue — utiliser avec precaution
- Trois patterns par calibration: leader election (1 row, N instances), work distribution (N rows, N instances), elastic overflow (visibility < processing time)
NE PAS:
- Ne pas oublier `EnsureTable()` au demarrage — la table `vtq_jobs` n'est pas creee automatiquement
- Ne pas confondre `Run` (sequential, un job a la fois) et `RunBatch` (concurrent avec semaphore)
- Ne pas appeler `Nack` sur un job deja acke — le DELETE a deja eu lieu
- Ne pas utiliser `Extend` sans verifier que le job est toujours claim par cette instance — c'est un UPDATE aveugle
- Ne pas stocker des payloads > quelques Ko — c'est un BLOB SQLite, pas un systeme de fichiers
