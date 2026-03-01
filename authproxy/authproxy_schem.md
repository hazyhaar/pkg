╔══════════════════════════════════════════════════════════════════════════════╗
║  authproxy — HTTP proxy: FO auth requests → BO internal API → cookies      ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                            ║
║  ARCHITECTURE                                                              ║
║  ────────────                                                              ║
║                                                                            ║
║  ┌──────────┐     ┌──────────────────────┐     ┌───────────────────┐       ║
║  │  Browser  │     │  Front-Office (FO)    │     │  Back-Office (BO) │       ║
║  │  (user)   │     │  AuthProxy handlers   │     │  /api/internal/   │       ║
║  │           │     │                      │     │  auth/*           │       ║
║  │  POST     │     │                      │     │                   │       ║
║  │  /login   │ ──→ │ LoginHandler         │ ──→ │ /auth/login       │       ║
║  │  /register│ ──→ │ RegisterHandler      │ ──→ │ /auth/register    │       ║
║  │  /forgot  │ ──→ │ ForgotPasswordHandler│ ──→ │ /auth/forgot-pw   │       ║
║  │  /reset   │ ──→ │ ResetPasswordHandler │ ──→ │ /auth/reset-pw    │       ║
║  │           │     │                      │     │                   │       ║
║  │  ◄────────│─────│── cookie + redirect  │ ◄── │── JSON response   │       ║
║  │  (never   │     │  (BO URL hidden)     │     │  {ok,token,error, │       ║
║  │  sees BO) │     │                      │     │   flash,redirect} │       ║
║  └──────────┘     └──────────────────────┘     └───────────────────┘       ║
║                                                                            ║
║  HANDLER DETAIL                                                            ║
║  ──────────────                                                            ║
║                                                                            ║
║  LoginHandler(setFlash):                                                   ║
║  ┌──────────────────────────────────────────────────────────┐              ║
║  │ 1. HealthCheck? → fail fast if BO down                  │              ║
║  │ 2. ParseForm → {username, password}                     │              ║
║  │ 3. POST JSON → BO /api/internal/auth/login              │              ║
║  │ 4. if resp.OK:                                          │              ║
║  │      auth.SetTokenCookie(w, resp.Token, domain, secure) │              ║
║  │      redirect → resp.Redirect or "/dashboard"           │              ║
║  │ 5. if !resp.OK:                                         │              ║
║  │      setFlash("error", resp.Error)                      │              ║
║  │      redirect → /login                                  │              ║
║  └──────────────────────────────────────────────────────────┘              ║
║                                                                            ║
║  RegisterHandler(setFlash):                                                ║
║  ┌──────────────────────────────────────────────────────────┐              ║
║  │ 1. HealthCheck? → fail fast                             │              ║
║  │ 2. ParseForm → {username, email, password, display_name}│              ║
║  │ 3. POST JSON → BO /api/internal/auth/register           │              ║
║  │ 4. redirect → resp.Redirect or "/login"                 │              ║
║  └──────────────────────────────────────────────────────────┘              ║
║                                                                            ║
║  ForgotPasswordHandler(setFlash):                                          ║
║  ┌──────────────────────────────────────────────────────────┐              ║
║  │ 1. HealthCheck? → fail fast                             │              ║
║  │ 2. ParseForm → {email}                                  │              ║
║  │ 3. Compute FO origin: scheme://host (from TLS state,    │              ║
║  │    X-Forwarded-Proto, X-Forwarded-Host, r.Host)         │              ║
║  │ 4. POST JSON → BO /api/internal/auth/forgot-password    │              ║
║  │    body: {email, origin}                                │              ║
║  │    BO uses origin for reset link (user clicks FO URL)   │              ║
║  │ 5. redirect → resp.Redirect or "/login"                 │              ║
║  └──────────────────────────────────────────────────────────┘              ║
║                                                                            ║
║  ResetPasswordHandler(setFlash):                                           ║
║  ┌──────────────────────────────────────────────────────────┐              ║
║  │ 1. HealthCheck? → fail fast                             │              ║
║  │ 2. ParseForm → {token, password, password_confirm}      │              ║
║  │ 3. FO-side validation: password == password_confirm     │              ║
║  │ 4. POST JSON → BO /api/internal/auth/reset-password     │              ║
║  │    body: {token, password}                              │              ║
║  │ 5. redirect → resp.Redirect or "/login"                 │              ║
║  └──────────────────────────────────────────────────────────┘              ║
║                                                                            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  BO JSON RESPONSE FORMAT (authResponse)                                    ║
║  ──────────────────────────────────────                                     ║
║  {                                                                         ║
║    "ok":       bool,            -- success/failure                          ║
║    "token":    "jwt...",        -- JWT (login success only)                 ║
║    "user_id":  "uuid",          -- user ID (optional)                      ║
║    "error":    "message",       -- error text (on failure)                  ║
║    "code":     "ERR_CODE",      -- machine-readable code (optional)        ║
║    "flash":    "success msg",   -- flash message for UI                    ║
║    "redirect": "/path"          -- suggested redirect path                 ║
║  }                                                                         ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                            ║
║  ──────────────                                                            ║
║  AuthProxy  struct {                                                       ║
║      boURL        string          -- BO base URL, never exposed            ║
║      cookieDomain string          -- "" or ".repvow.fr" for SSO            ║
║      secure       bool            -- cookie Secure flag                    ║
║      logger       *slog.Logger                                             ║
║      client       *http.Client    -- timeout: 10s                          ║
║      HealthCheck  func() bool     -- optional circuit breaker              ║
║  }                                                                         ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                        ║
║  ──────────────────                                                        ║
║  NewAuthProxy(boURL, cookieDomain string, secure bool) *AuthProxy          ║
║                                                                            ║
║  (p *AuthProxy) LoginHandler(setFlash) http.HandlerFunc                    ║
║  (p *AuthProxy) RegisterHandler(setFlash) http.HandlerFunc                 ║
║  (p *AuthProxy) ForgotPasswordHandler(setFlash) http.HandlerFunc           ║
║  (p *AuthProxy) ResetPasswordHandler(setFlash) http.HandlerFunc            ║
║                                                                            ║
║  setFlash signature: func(http.ResponseWriter, string, string)             ║
║      -- (w, level, message) where level = "error" | "success"              ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  BO ENDPOINTS CALLED                                                       ║
║  ───────────────────                                                       ║
║  POST /api/internal/auth/login           {username, password}              ║
║  POST /api/internal/auth/register        {username, email, password,       ║
║                                           display_name}                    ║
║  POST /api/internal/auth/forgot-password {email, origin}                   ║
║  POST /api/internal/auth/reset-password  {token, password}                 ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/hazyhaar/pkg/auth  -- SetTokenCookie (cookie management)       ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                ║
║  ──────────                                                                ║
║  - BO URL is NEVER exposed to the end user (not in redirects or errors)    ║
║  - ForgotPassword sends FO origin so BO builds reset link with FO domain   ║
║  - requestOrigin deduces scheme from TLS/X-Forwarded-Proto, host from      ║
║    X-Forwarded-Host/r.Host                                                 ║
║  - Password confirm validation done FO-side (ResetPasswordHandler)         ║
║  - HealthCheck callback = circuit breaker; if set and returns false,       ║
║    handlers fail immediately (no 10s timeout wait)                          ║
║  - HTTP client timeout: 10 seconds                                         ║
║  - Response body read capped at 64 KiB                                     ║
║  - dbsync.AuthProxy is DEPRECATED — use this package instead               ║
╚══════════════════════════════════════════════════════════════════════════════╝
