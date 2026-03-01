```
╔════════════════════════════════════════════════════════════════════════════╗
║  watch — Generic SQLite change-detect + debounce + reload loop           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  OVERVIEW                                                                ║
║  ────────                                                                ║
║                                                                          ║
║  Standardizes the reactive pattern: poll a SQLite database for changes,  ║
║  debounce rapid mutations, then fire a reload action. Used by dbsync,    ║
║  mcprt, and service hot-reload.                                          ║
║                                                                          ║
║  MAIN LOOP (OnChange)                                                    ║
║  ────────────────────                                                    ║
║                                                                          ║
║           ┌──────────────────────────────────────────────┐               ║
║           │                                              │               ║
║           │  ticker(Interval)                            │               ║
║           │       │                                      │               ║
║           │       v                                      │               ║
║           │  ┌─────────────────────┐                     │               ║
║           │  │ Detector(ctx, db)   │                     │               ║
║           │  │ -> int64 version    │                     │               ║
║           │  └──────────┬──────────┘                     │               ║
║           │             │                                │               ║
║           │    version == last?                           │               ║
║           │     YES: loop ──────────────────┐            │               ║
║           │     NO:  change detected        │            │               ║
║           │             │                   │            │               ║
║           │    Debounce == 0?               │            │               ║
║           │     YES: fire(action)           │            │               ║
║           │     NO:  start/reset timer      │            │               ║
║           │             │                   │            │               ║
║           │             v                   │            │               ║
║           │    ┌──────────────────┐         │            │               ║
║           │    │ debounce timer   │         │            │               ║
║           │    │ (resets on each  │         │            │               ║
║           │    │  new change)     │         │            │               ║
║           │    └────────┬─────────┘         │            │               ║
║           │             │ timer fires       │            │               ║
║           │             v                   │            │               ║
║           │    ┌──────────────────┐         │            │               ║
║           │    │ fire(action)     │         │            │               ║
║           │    │  action() error  │         │            │               ║
║           │    │   OK  -> advance │         │            │               ║
║           │    │         version  │         │            │               ║
║           │    │   err -> keep    │         │            │               ║
║           │    │         old ver  │         │            │               ║
║           │    │         (retry   │         │            │               ║
║           │    │         next     │         │            │               ║
║           │    │         cycle)   │         │            │               ║
║           │    └──────────────────┘         │            │               ║
║           │             │                   │            │               ║
║           │             └───────────────────┘            │               ║
║           │                                              │               ║
║           │  ctx.Done() -> stop                          │               ║
║           └──────────────────────────────────────────────┘               ║
║                                                                          ║
║  USAGE                                                                   ║
║  ─────                                                                   ║
║                                                                          ║
║  w := watch.New(db, watch.Options{                                       ║
║      Interval: 200*time.Millisecond,                                     ║
║      Debounce: 500*time.Millisecond,                                     ║
║  })                                                                      ║
║  go w.OnChange(ctx, func() error { return service.Reload() })            ║
║                                                                          ║
║  // Block until version >= 5 (useful in tests)                           ║
║  w.WaitForVersion(ctx, 5)                                                ║
║                                                                          ║
║  BUILT-IN DETECTORS                                                      ║
║  ──────────────────                                                      ║
║                                                                          ║
║  ┌─────────────────────────────────────────────────────────────────┐     ║
║  │ Detector               │ SQL                  │ Use case        │     ║
║  ├─────────────────────────┼──────────────────────┼─────────────────┤     ║
║  │ PragmaDataVersion      │ PRAGMA data_version  │ Cross-process   │     ║
║  │ (DEFAULT)              │                      │ mutation detect │     ║
║  │                        │                      │ (auto-increment)│     ║
║  ├─────────────────────────┼──────────────────────┼─────────────────┤     ║
║  │ PragmaUserVersion      │ PRAGMA user_version  │ App-controlled  │     ║
║  │                        │                      │ explicit bumps  │     ║
║  │                        │                      │ (deterministic) │     ║
║  ├─────────────────────────┼──────────────────────┼─────────────────┤     ║
║  │ MaxColumnDetector      │ SELECT COALESCE(     │ updated_at      │     ║
║  │ (table, column)        │   MAX("col"), 0)     │ timestamp polls │     ║
║  │                        │ FROM "table"         │ (needs index!)  │     ║
║  └─────────────────────────┴──────────────────────┴─────────────────┘     ║
║                                                                          ║
║  WAITFORVERSION (synchronous gate)                                       ║
║  ─────────────────────────────────                                       ║
║                                                                          ║
║  Blocks until watcher.version >= target OR ctx expires.                   ║
║  Uses sync.Cond broadcast — woken on every version advance.              ║
║  Interruptible: spawns goroutine to broadcast on ctx.Done().             ║
║  Fast path: returns immediately if version already >= target.             ║
║                                                                          ║
║  OBSERVABILITY (Stats)                                                   ║
║  ─────────────────────                                                   ║
║                                                                          ║
║  All counters are atomic (thread-safe):                                  ║
║                                                                          ║
║  ┌──────────────────┬──────────────────────────────────────┐             ║
║  │ Counter          │ Description                          │             ║
║  ├──────────────────┼──────────────────────────────────────┤             ║
║  │ Checks           │ Total detector polls                 │             ║
║  │ ChangesDetected  │ Version changes observed             │             ║
║  │ Errors           │ Detector or action failures          │             ║
║  │ Reloads          │ Successful action() calls            │             ║
║  │ AvgReloadTime    │ reloadNs / Reloads                   │             ║
║  └──────────────────┴──────────────────────────────────────┘             ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY TYPES                                                               ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Watcher          {db, opts, version atomic.Int64,                        ║
║                    versionCond *sync.Cond,                               ║
║                    checks/changes/errors/reloads/reloadNs atomic.Int64}  ║
║                                                                          ║
║  ChangeDetector   func(ctx context.Context, db *sql.DB) (int64, error)   ║
║                   Returns a version token; different = change detected    ║
║                                                                          ║
║  Options          {Interval time.Duration (default 1s),                   ║
║                    Debounce time.Duration (default 0),                    ║
║                    Detector ChangeDetector (default PragmaDataVersion),   ║
║                    Logger *slog.Logger}                                  ║
║                                                                          ║
║  Stats            {Checks, ChangesDetected, Errors, Reloads int64,       ║
║                    AvgReloadTime time.Duration}                          ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  New(db, Options) *Watcher                                               ║
║  Watcher.OnChange(ctx, action func() error)   Blocking poll loop         ║
║  Watcher.WaitForVersion(ctx, target int64) err  Block until version>=N   ║
║  Watcher.Stats() Stats                        Point-in-time counters     ║
║  Watcher.Version() int64                      Last observed version      ║
║                                                                          ║
║  PragmaDataVersion(ctx, db) (int64, error)    Default detector           ║
║  PragmaUserVersion(ctx, db) (int64, error)    App-controlled version     ║
║  MaxColumnDetector(table, col) ChangeDetector  MAX(col) on a table       ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                            ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Standard library only: database/sql, context, log/slog, sync, time      ║
║  No hazyhaar/pkg dependencies.                                           ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DEBOUNCE BEHAVIOR                                                       ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Debounce = 0 (default):                                                 ║
║    change detected -> fire immediately                                   ║
║                                                                          ║
║  Debounce = 500ms:                                                       ║
║    change A at t=0   -> start 500ms timer                                ║
║    change B at t=200 -> RESET timer to t=700                             ║
║    change C at t=400 -> RESET timer to t=900                             ║
║    no change         -> timer fires at t=900 -> action()                 ║
║                                                                          ║
║  This coalesces rapid mutations (e.g. batch INSERT) into a single        ║
║  reload, preventing reload storms.                                       ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  ERROR HANDLING                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Detector error:  version NOT advanced, errors++ , slog.Warn             ║
║  Action error:    version NOT advanced, errors++ , slog.Error            ║
║                   (action will retry on next poll cycle)                  ║
║  Action success:  version advanced, reloads++ , slog.Info                ║
║                                                                          ║
╚════════════════════════════════════════════════════════════════════════════╝
```
