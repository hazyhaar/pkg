╔══════════════════════════════════════════════════════════════════════════╗
║  idgen — Pluggable ID generation (UUIDv7, NanoID, prefixed, timestamped)║
╠══════════════════════════════════════════════════════════════════════════╣
║                                                                        ║
║  ARCHITECTURE                                                          ║
║  ────────────                                                          ║
║                                                                        ║
║  All constructors across pkg/ accept a Generator (func() string).      ║
║  ID strategy is a startup-time decision, not compile-time.             ║
║                                                                        ║
║  ┌──────────────────────── Generators ───────────────────────────┐     ║
║  │                                                               │     ║
║  │  UUIDv7()  ──→ "019538a1-..."   (RFC 9562, time-sortable)    │     ║
║  │  NanoID(n) ──→ "k3mx9a..."      (base-36, crypto/rand)       │     ║
║  │                                                               │     ║
║  └────────────────────────┬──────────────────────────────────────┘     ║
║                           │                                            ║
║           ┌───────────────▼───────────────────┐                        ║
║           │         Decorators (composable)    │                        ║
║           │                                    │                        ║
║           │  Prefixed("aud_", gen)             │                        ║
║           │    └─→ "aud_019538a1-..."          │                        ║
║           │                                    │                        ║
║           │  Timestamped(gen)                  │                        ║
║           │    └─→ "20260301T120000Z_k3mx9a"   │                        ║
║           │                                    │                        ║
║           │  Prefixed + Timestamped composable │                        ║
║           └───────────────┬───────────────────┘                        ║
║                           │                                            ║
║                    ┌──────▼──────┐                                     ║
║                    │  string ID  │                                     ║
║                    └─────────────┘                                     ║
║                                                                        ║
║  Default var ──→ UUIDv7() (ecosystem convention)                       ║
║  New()       ──→ Default()                                             ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                        ║
║  ──────────────                                                        ║
║                                                                        ║
║  Generator  func() string                                              ║
║  Default    var Generator = UUIDv7()                                   ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                         ║
║  ─────────────                                                         ║
║                                                                        ║
║  UUIDv7() Generator                                                    ║
║      RFC 9562 UUID v7 — time-sortable, globally unique.                ║
║      Ecosystem default per CLAUDE.md.                                  ║
║                                                                        ║
║  NanoID(length int) Generator                                          ║
║      Base-36 random IDs (0-9a-z). Uses crypto/rand.                    ║
║      Short, URL-safe. Only where UUIDv7 too verbose.                   ║
║                                                                        ║
║  Prefixed(prefix string, gen Generator) Generator                      ║
║      Prepends fixed prefix: "aud_", "sess_", "trc_".                  ║
║                                                                        ║
║  Timestamped(gen Generator) Generator                                  ║
║      Format: "20060102T150405Z_<suffix>".                              ║
║                                                                        ║
║  New() string                                                          ║
║      Produces ID using Default generator. Convenience shortcut.        ║
║                                                                        ║
║  MustParse(s string) string                                            ║
║      Validates UUID string, panics on invalid.                         ║
║                                                                        ║
║  Parse(s string) (string, error)                                       ║
║      Validates UUID string, returns error on invalid.                  ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                          ║
║  ────────────                                                          ║
║                                                                        ║
║  External: github.com/google/uuid                                      ║
║  Stdlib : crypto/rand, fmt, time                                       ║
║  No pkg/ internal dependencies. Leaf package.                          ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DEPENDANTS (within pkg/)                                              ║
║  ────────────────────────                                              ║
║                                                                        ║
║  mcpquic/server, mcprt/registry, mcprt/handlers, mcprt/types,          ║
║  observability/audit, observability/logger, sas_ingester/ingester,     ║
║  audit/logger, feedback/feedback                                       ║
║  External: horostracker, chrc (domkeeper, domwatch, veille)            ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES                                                       ║
║  ───────────────                                                       ║
║  None. Pure computation, no storage.                                   ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                            ║
║  ──────────                                                            ║
║                                                                        ║
║  - Default is always UUIDv7 — never replace with NanoID globally       ║
║  - NanoID uses crypto/rand — never math/rand                           ║
║  - In tests, inject a deterministic Generator — never use New()        ║
║  - Parse/MustParse validate only — they do not generate                ║
║  - NanoID is NOT time-sortable — use UUIDv7 for chronological order    ║
║                                                                        ║
╚══════════════════════════════════════════════════════════════════════════╝
