╔══════════════════════════════════════════════════════════════════════════════╗
║  auth — JWT HS256 auth, HttpOnly cookies, HTTP middleware, Google OAuth2   ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                            ║
║  FILE MAP                                                                  ║
║  ────────                                                                  ║
║  claims.go     HorosClaims struct definition                               ║
║  jwt.go        GenerateToken, ValidateToken, ValidateTokenMapClaims        ║
║  cookie.go     SetTokenCookie, ClearTokenCookie                            ║
║  middleware.go Middleware (extract JWT), RequireAuth, GetClaims             ║
║  oauth.go      Google OAuth2 provider + FetchGoogleUser                    ║
║                                                                            ║
║  JWT FLOW                                                                  ║
║  ────────                                                                  ║
║                                                                            ║
║  ┌──────────────┐  GenerateToken()   ┌─────────────────┐                   ║
║  │ Login handler│ ─────────────────→ │ jwt.go          │                   ║
║  │ (HorosClaims,│  secret >= 32B     │                 │                   ║
║  │  secret,     │  HS256 only        │ jwt.NewWithClaims│                  ║
║  │  expiry)     │                    │ SignedString()  │                   ║
║  └──────────────┘                    └────────┬────────┘                   ║
║                                               │ signed JWT string          ║
║                                               ▼                            ║
║                        ┌──────────────────────────────────┐                ║
║                        │ SetTokenCookie(w, token, domain, │                ║
║                        │   secure)                        │                ║
║                        │ Cookie: "token"                  │                ║
║                        │   Path=/  MaxAge=86400 (24h)     │                ║
║                        │   HttpOnly  SameSite=Strict      │                ║
║                        │   Domain=<cookieDomain> (SSO)    │                ║
║                        └──────────────────────────────────┘                ║
║                                                                            ║
║  MIDDLEWARE CHAIN                                                          ║
║  ────────────────                                                          ║
║                                                                            ║
║  ┌──────────┐    ┌─────────────────────────────────────┐   ┌─────────┐    ║
║  │ Request  │ →  │ Middleware(secret)                   │ → │ Handler │    ║
║  │          │    │                                     │   │         │    ║
║  │ Cookie:  │    │ 1. Extract token from:              │   │ uses:   │    ║
║  │  "token" │    │    a. Cookie "token" (preferred)    │   │ GetClaims│   ║
║  │   OR     │    │    b. Authorization: Bearer <tok>   │   │ (ctx)   │    ║
║  │ Bearer   │    │ 2. ValidateToken(secret, tokenStr)  │   │         │    ║
║  │ header   │    │ 3. If valid:                        │   │         │    ║
║  │          │    │    ctx += claimsKey{} → *HorosClaims │   │         │    ║
║  │          │    │    ctx += kit.UserIDKey → user_id    │   │         │    ║
║  │          │    │    ctx += kit.HandleKey → handle     │   │         │    ║
║  │          │    │ 4. If invalid: clear cookie, pass    │   │         │    ║
║  │          │    │    through (no 401)                  │   │         │    ║
║  └──────────┘    └─────────────────────────────────────┘   └─────────┘    ║
║                                                                            ║
║  ┌──────────┐    ┌────────────────────┐   ┌─────────┐                     ║
║  │ Request  │ →  │ RequireAuth        │ → │ Handler │                     ║
║  │          │    │ if GetClaims=nil:  │   │         │                     ║
║  │          │    │   redirect /login  │   │         │                     ║
║  └──────────┘    └────────────────────┘   └─────────┘                     ║
║                                                                            ║
║  GOOGLE OAUTH2 FLOW                                                        ║
║  ──────────────────                                                        ║
║                                                                            ║
║  ┌────────────┐  NewGoogleProvider()  ┌──────────────────┐                 ║
║  │ OAuthConfig│ ───────────────────→ │ *oauth2.Config   │                 ║
║  │ ClientID   │   scopes: openid,    │ (google endpoint)│                 ║
║  │ Secret     │   email, profile     └────────┬─────────┘                 ║
║  │ RedirectURL│                               │                            ║
║  └────────────┘                               ▼                            ║
║                          FetchGoogleUser(ctx, oauthCfg, code)              ║
║                                   │                                        ║
║                     1. Exchange(code) → token                              ║
║                     2. GET googleapis.com/oauth2/v2/userinfo               ║
║                     3. Decode → OAuthUser{ProviderUserID,                  ║
║                                           Email, Name, AvatarURL}          ║
║                                   │                                        ║
║                       returns (*OAuthUser, *oauth2.Token, error)           ║
║                                                                            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                            ║
║  ──────────────                                                            ║
║  HorosClaims  struct {                                                     ║
║      jwt.RegisteredClaims          -- exp, iat, iss, sub, etc.             ║
║      UserID       string           -- HOROS user ID                        ║
║      Username     string           -- login username                       ║
║      Handle       string           -- display handle (optional)            ║
║      Role         string           -- user role                            ║
║      Email        string           -- email (optional)                     ║
║      DisplayName  string           -- display name (optional)              ║
║      AvatarURL    string           -- avatar (optional)                    ║
║      AuthProvider string           -- "local", "google", "github"          ║
║  }                                                                         ║
║                                                                            ║
║  OAuthConfig   struct { ClientID, ClientSecret, RedirectURL string }       ║
║  OAuthUser     struct { ProviderUserID, Email, Name, AvatarURL string }    ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                        ║
║  ──────────────────                                                        ║
║  GenerateToken(secret []byte, claims *HorosClaims,                         ║
║                expiry time.Duration) (string, error)                       ║
║      -- secret must be >= horosafe.MinSecretLen (32 bytes)                 ║
║                                                                            ║
║  ValidateToken(secret []byte, tokenStr string) (*HorosClaims, error)       ║
║      -- pins to HS256, rejects any other alg                               ║
║                                                                            ║
║  ValidateTokenMapClaims(secret, tokenStr) (jwt.MapClaims, error)           ║
║      -- DEPRECATED backward-compat; prefer ValidateToken                   ║
║                                                                            ║
║  SetTokenCookie(w, token, domain string, secure bool)                      ║
║      -- Cookie "token", 24h, HttpOnly, Strict, optional Domain for SSO    ║
║                                                                            ║
║  ClearTokenCookie(w, domain string)                                        ║
║      -- MaxAge=-1, matching Domain for cross-subdomain clear               ║
║                                                                            ║
║  Middleware(secret []byte) func(http.Handler) http.Handler                 ║
║      -- extract JWT, inject claims + kit context; silently pass on fail    ║
║                                                                            ║
║  RequireAuth http.Handler middleware                                       ║
║      -- redirect to /login if GetClaims(ctx) == nil                        ║
║                                                                            ║
║  GetClaims(ctx) *HorosClaims                                               ║
║      -- retrieve from context, nil if absent                               ║
║                                                                            ║
║  NewGoogleProvider(cfg OAuthConfig) *oauth2.Config                         ║
║  FetchGoogleUser(ctx, cfg, code) (*OAuthUser, *oauth2.Token, error)        ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/hazyhaar/pkg/horosafe  -- ValidateSecret (min 32B),            ║
║                                       LimitedReadAll (response body cap)   ║
║  github.com/hazyhaar/pkg/kit       -- WithUserID, WithHandle context       ║
║                                       injection for interop with kit layer ║
║  DEPENDENCIES (external)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/golang-jwt/jwt/v5      -- JWT parsing, signing, claims         ║
║  golang.org/x/oauth2               -- OAuth2 config, token exchange        ║
║  golang.org/x/oauth2/google        -- Google endpoint                      ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  SECURITY INVARIANTS                                                       ║
║  ───────────────────                                                       ║
║  - HS256 ONLY — ValidateToken rejects any other signing algorithm          ║
║  - Secret >= 32 bytes enforced by horosafe.ValidateSecret                  ║
║  - Cookie source preferred over Bearer header                              ║
║  - Invalid token = cookie cleared, request passes through (no 401)         ║
║  - Use RequireAuth to enforce authentication (separate middleware)          ║
║  - Google userinfo response body capped via horosafe.LimitedReadAll        ║
╚══════════════════════════════════════════════════════════════════════════════╝
