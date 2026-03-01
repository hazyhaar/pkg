╔═══════════════════════════════════════════════════════════════════════════════╗
║  redact — Sanitize strings for LLM: strip tokens, paths, IPs, stack traces  ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  TWO MODES:                                                                  ║
║  1. Static Redactor (code-defined rules, no DB)                             ║
║  2. Store (SQLite-backed, runtime-updatable blacklist + whitelist)           ║
║                                                                              ║
║  STATIC REDACTOR PIPELINE                                                    ║
║  ~~~~~~~~~~~~~~~~~~~~~~~~                                                    ║
║                                                                              ║
║  ┌──────────────┐    ┌───────────────────────────────────────┐  ┌─────────┐  ║
║  │ Raw string   │ ──>│ Redactor.Sanitize(s)                  │─>│ Clean   │  ║
║  │ (error msg,  │    │                                       │  │ string  │  ║
║  │  log line,   │    │  Rule 1: bearer_token                 │  │         │  ║
║  │  response)   │    │  Rule 2: api_key_param                │  │ "Bearer │  ║
║  └──────────────┘    │  Rule 3: authorization_header         │  │ [token]"│  ║
║                      │  Rule 4: unix_path                    │  │ "[path]"│  ║
║                      │  Rule 5: windows_path                 │  │ "[addr]"│  ║
║                      │  Rule 6: ipv6_addr                    │  │ "[trace]║
║                      │  Rule 7: ipv4_port                    │  │ "[enc]" │  ║
║                      │  Rule 8: go_stack_trace               │  │         │  ║
║                      │  Rule 9: base64_long (>=40 chars)     │  └─────────┘  ║
║                      │  [custom rules...]                    │               ║
║                      └───────────────────────────────────────┘               ║
║                                                                              ║
║  STORE (RUNTIME-UPDATABLE) PIPELINE                                          ║
║  ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~                                        ║
║                                                                              ║
║  ┌──────────┐                                                                ║
║  │ Input    │                                                                ║
║  │ string   │                                                                ║
║  └────┬─────┘                                                                ║
║       v                                                                      ║
║  ┌────────────────────────────────────────────────────────────────┐           ║
║  │ Step 1: Whitelist protection                                   │           ║
║  │  Find all matches of whitelist patterns -> replace with        │           ║
║  │  NULL-byte placeholders (\x00WLi_j\x00) to protect them       │           ║
║  └────────────────────┬───────────────────────────────────────────┘           ║
║       v               │                                                      ║
║  ┌────────────────────v───────────────────────────────────────────┐           ║
║  │ Step 2: Apply static rules (code-defined, e.g. Defaults())    │           ║
║  └────────────────────┬───────────────────────────────────────────┘           ║
║       v               │                                                      ║
║  ┌────────────────────v───────────────────────────────────────────┐           ║
║  │ Step 3: Apply dynamic blacklist rules (from redact_patterns)   │           ║
║  └────────────────────┬───────────────────────────────────────────┘           ║
║       v               │                                                      ║
║  ┌────────────────────v───────────────────────────────────────────┐           ║
║  │ Step 4: Restore whitelisted substrings from placeholders       │           ║
║  └────────────────────┬───────────────────────────────────────────┘           ║
║       v                                                                      ║
║  ┌──────────┐                                                                ║
║  │ Output   │                                                                ║
║  │ sanitized│                                                                ║
║  └──────────┘                                                                ║
║                                                                              ║
║  ┌───────────────────┐    Reload()     ┌──────────────────────┐              ║
║  │ redact_patterns   │ ─────────────> │ Store.blacklist      │              ║
║  │ redact_whitelist  │ ─────────────> │ Store.whitelist      │              ║
║  │ (SQLite)          │ <── AddPattern  │ (compiled regexps)   │              ║
║  │                   │ <── AddWhite..  │                      │              ║
║  └───────────────────┘                └──────────────────────┘              ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES (Store only)                                                ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  redact_patterns                          (blacklist: what to redact)        ║
║  ├── name TEXT PK                                                            ║
║  ├── pattern TEXT NOT NULL                (regex)                            ║
║  ├── replace_with TEXT DEFAULT '[redacted]'                                  ║
║  ├── is_active INTEGER DEFAULT 1                                             ║
║  ├── created_at INTEGER (epoch)                                              ║
║  └── updated_at INTEGER                                                      ║
║                                                                              ║
║  redact_whitelist                         (whitelist: what to preserve)      ║
║  ├── name TEXT PK                                                            ║
║  ├── pattern TEXT NOT NULL                (regex)                            ║
║  ├── is_active INTEGER DEFAULT 1                                             ║
║  ├── created_at INTEGER (epoch)                                              ║
║  └── updated_at INTEGER                                                      ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DEFAULT RULES (Defaults())                                                  ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  bearer_token         Bearer <token>              -> "Bearer [token]"       ║
║  api_key_param        api_key=val / secret=val    -> "${1}=[key]"           ║
║  authorization_header Authorization: <val>        -> "Authorization: [red]" ║
║  unix_path            /home/... /usr/... /var/...  -> "[path]"              ║
║  windows_path         C:\Users\...                -> "[path]"               ║
║  ipv6_addr            [fe80::1]:8080              -> "[addr]"               ║
║  ipv4_port            192.168.1.1:8080            -> "[addr]"               ║
║  go_stack_trace       goroutine N [...]\n ...     -> "[trace]"              ║
║  base64_long          >=40 char base64 strings    -> "[encoded]"            ║
║                                                                              ║
║  Extra rule set:                                                             ║
║  SQLitePaths()        *.db / *.sqlite / *.sqlite3 -> "[db]"                 ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Rule             struct  Name string, Pattern *regexp.Regexp, Replace str   ║
║  Redactor         struct  Static rule pipeline (code-only, no DB)            ║
║  Store            struct  Runtime-updatable redactor (SQLite blacklist +     ║
║                           whitelist + static rules)                          ║
║  StoreOption      func    Functional option for NewStore()                    ║
║  PatternEntry     struct  Row from redact_patterns table                     ║
║  WhitelistEntry   struct  Row from redact_whitelist table                    ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS — Redactor (static)                                           ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  New(ruleSets ...[]Rule) *Redactor                                           ║
║  Redactor.Sanitize(s) string               -- apply all rules in order       ║
║  Redactor.SanitizeLines(s) string          -- per-line sanitization           ║
║  Redactor.RedactMap(map[string]string) map  -- sanitize all map values       ║
║  Redactor.Wrap(extra ...[]Rule) *Redactor  -- layer extra rules on top       ║
║  Redactor.Rules() []Rule                   -- inspect current rules          ║
║                                                                              ║
║  Defaults() []Rule                         -- 9 standard rules               ║
║  SQLitePaths() []Rule                      -- sqlite file path rule          ║
║  Custom(name, pattern, replace) []Rule     -- single custom rule             ║
║  Merge(ruleSets ...[]Rule) []Rule          -- combine rule slices            ║
║  MustCompileRule(name, pat, repl) Rule     -- panics on bad regex            ║
║                                                                              ║
║  SanitizeError(msg) string                 -- convenience: Defaults().San()  ║
║  StripGoStackTraces(s) string              -- fast-path trace removal        ║
║  ContainsSensitive(s) bool                 -- pre-check before logging       ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS — Store (runtime)                                             ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  NewStore(db, ...StoreOption) *Store                                         ║
║  WithStaticRules(rules ...[]Rule) StoreOption                                ║
║                                                                              ║
║  Store.Init() error                        -- create tables                  ║
║  Store.Reload() error                      -- load+compile from DB           ║
║  Store.Sanitize(input) string              -- full pipeline (see above)      ║
║                                                                              ║
║  Store.AddPattern(name, pattern, replace) error   -- upsert blacklist       ║
║  Store.RemovePattern(name) error                   -- deactivate             ║
║  Store.AddWhitelist(name, pattern) error           -- upsert whitelist       ║
║  Store.RemoveWhitelist(name) error                 -- deactivate             ║
║  Store.ListPatterns() ([]PatternEntry, error)      -- all, incl. inactive   ║
║  Store.ListWhitelist() ([]WhitelistEntry, error)   -- all, incl. inactive   ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                                ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Standard library only: regexp, strings, database/sql, log/slog, sync       ║
║  No internal hazyhaar/pkg dependencies.                                      ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  USAGE PATTERNS                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Quick one-off:   redact.SanitizeError(err.Error())                         ║
║  Reusable:        r := redact.New(redact.Defaults(), redact.SQLitePaths())  ║
║                   clean := r.Sanitize(rawString)                            ║
║  Layered:         r2 := r.Wrap(redact.Custom("ssn", `\d{3}-\d{2}`, "***")) ║
║  Runtime:         store := redact.NewStore(db, WithStaticRules(Defaults())) ║
║                   store.Init(); store.Reload()                              ║
║                   store.AddPattern("custom_key", `regex`, "[hidden]")       ║
║                   store.Reload(); clean := store.Sanitize(input)            ║
║                                                                              ║
╚═══════════════════════════════════════════════════════════════════════════════╝
