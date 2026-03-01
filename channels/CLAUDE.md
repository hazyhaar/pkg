> **Schema technique** : voir [`channels_schem.md`](channels_schem.md) — lecture prioritaire avant tout code source.

# channels

Responsabilite: Framework de messagerie bidirectionnelle multi-plateforme (WhatsApp, Telegram, Discord, Signal, Matrix, webhooks) avec hot-reload SQLite et dispatching inbound/outbound.
Depend de: `github.com/hazyhaar/pkg/dbopen`
Dependants: aucun service externe pour l'instant (framework pret a l'emploi)
Point d'entree: `dispatcher.go` (Dispatcher + Reload + Watch)
Types cles: `Channel` (interface), `ChannelFactory`, `Dispatcher`, `Message`, `Admin`, `InboundHandler`
Invariants:
- Les channels actifs sont pilotes par la table SQLite `channels` -- modifier la table suffit, le Watch detecte via `PRAGMA data_version`
- Le fingerprint exclut `auth_state` : changer l'auth ne redemarre pas le channel (le SDK runtime gere sa session)
- `closeEntry` annule le context ET attend le WaitGroup du dispatch goroutine (pas de goroutine leak)
- Le lifecycle context est independant du request context -- les channels survivent au-dela d'un Reload
- `WithMaxConcurrent` limite les appels InboundHandler concurrents via semaphore channel
- Les plateformes supportees dans le schema CHECK : whatsapp, telegram, discord, signal, webhook, matrix
NE PAS:
- Passer un request context court a `Reload` en pensant qu'il controle la duree de vie des channels
- Oublier `RegisterPlatform` avant `Watch` -- les channels sans factory sont ignores silencieusement
- Modifier `auth_state` pour forcer un redemarrage -- ca ne changera pas le fingerprint
