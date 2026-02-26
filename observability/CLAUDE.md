# observability

Responsabilite: Monitoring SQLite-natif remplacant Prometheus/Loki/Consul — audit trail async, heartbeat workers, metriques timeseries, event logger, et retention cleanup.
Depend de: `github.com/hazyhaar/pkg/idgen`
Dependants: `sas_ingester/ingester`, `connectivity/observe`; externes: cmd/sas_ingester
Point d'entree: schema.go (Schema DDL + Init()), metrics.go (MetricsManager), audit.go (AuditLogger), heartbeat.go (HeartbeatWriter), logger.go (EventLogger)
Types cles: `AuditLogger`, `AuditEntry`, `MetricsManager`, `Metric`, `HeartbeatWriter`, `HeartbeatStatus`, `EventLogger`, `BusinessEvent`, `RetentionConfig`
Invariants:
- La DB d'observabilite doit etre separee de la DB applicative pour eviter la contention en ecriture
- La persistence est async et non-bloquante — un buffer plein drop silencieusement, jamais de backpressure sur l'app
- `AuditLogger.LogAsync` fait un fallback synchrone si le buffer est plein (pas de perte silencieuse pour l'audit)
- Les timestamps sont stockes en epoch secondes (int64), pas en RFC3339
- `Cleanup()` utilise des whitelists pour tables et colonnes — prevention injection SQL
NE PAS:
- Ne pas ouvrir la DB d'observabilite avec le driver "sqlite-trace" — utiliser "sqlite" directement pour eviter la recursion
- Ne pas oublier `Init(db)` au demarrage — toutes les tables sont creees d'un coup
- Ne pas appeler `Close()` sans avoir draine le buffer (Close le fait automatiquement)
- Ne pas utiliser `MetricsManager.Query` en boucle seree — c'est une requete SQL complete a chaque appel
