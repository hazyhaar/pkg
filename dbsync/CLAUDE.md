> **Schema technique** : voir [`dbsync_schem.md`](dbsync_schem.md) — lecture prioritaire avant tout code source.

# dbsync

Responsabilite: Replication SQLite BO-vers-FO via QUIC -- le BO produit des snapshots filtres, les pousse aux FO qui les valident (SHA-256) et font un swap atomique.
Depend de: `github.com/hazyhaar/pkg/watch`, `github.com/hazyhaar/pkg/auth`, `github.com/hazyhaar/pkg/connectivity`, `github.com/quic-go/quic-go`
Dependants: repvow (main, health)
Point d'entree: `publisher.go` (Publisher.Start) et `subscriber.go` (Subscriber.Start)
Types cles: `Publisher`, `Subscriber`, `FilterSpec`, `SnapshotMeta`, `TargetProvider`, `WriteProxy`, `BOHealthChecker`
Invariants:
- Le Publisher ne pousse que si le hash du snapshot a change (dedup par hash SHA-256)
- Le Subscriber verifie taille + hash avant le swap atomique (rename tmp -> target)
- Wire format QUIC : magic "SYN1" + 4 bytes meta length + meta JSON + payload (optionnellement gzip)
- ALPN protocol = "horos-dbsync-v1" (distinct de MCP)
- TLS 1.3 minimum, support mutual TLS (SyncTLSConfigMutual)
- Le snapshot fait `VACUUM INTO` + drop tables non-whitelistees + WHERE filters + column truncation + VACUUM compact
- `WithMaxAge` protege contre les attaques replay (snapshots trop vieux rejetes)
- `ValidateFilterSpec` rejette les WHERE contenant des injections SQL (defense en profondeur)
- `AuthProxy` dans ce package est **DEPRECATED** -- utiliser `authproxy.NewAuthProxy`
NE PAS:
- Utiliser `dbsync.AuthProxy` -- c'est deprecated, utiliser `authproxy.NewAuthProxy`
- Ouvrir le snapshot temporaire avec le driver "sqlite-trace" (recursion, le filter.go utilise "sqlite" brut)
- Oublier `WithDriverName("sqlite-trace")` sur le Subscriber si on veut le tracing SQL cote FO
- Mettre des sous-requetes ou UNION dans les WHERE de FilterSpec (valide et rejete par ValidateFilterSpec)
