# auth — JWT HS256 authentication and OAuth2

`auth` provides JWT token generation/validation, HTTP middleware, cookie
management, and Google OAuth2 for the HOROS ecosystem.

## Quick start

```go
// Generate a token.
token, _ := auth.GenerateToken(secret, &auth.HorosClaims{
    RegisteredClaims: jwt.RegisteredClaims{Subject: "user123"},
    Email: "user@example.com",
    Role:  "admin",
}, 24*time.Hour)

// Validate.
claims, _ := auth.ValidateToken(secret, token)

// HTTP middleware — extracts JWT from cookie or Authorization header.
mux.Handle("/api/", auth.Middleware(secret)(apiHandler))
```

## Security

- **HS256 only** — signing algorithm is pinned; rejects tokens using other algorithms.
- **32-byte minimum secret** — enforced via `horosafe.ValidateSecret`.
- **HttpOnly + SameSite=Strict cookies** — prevents XSS and CSRF.

## OAuth2 Google

```go
oauthCfg := auth.NewGoogleProvider(auth.OAuthConfig{
    ClientID: "...", ClientSecret: "...", RedirectURL: "...",
})
user, _ := auth.FetchGoogleUser(ctx, oauthCfg, code)
// user.Email, user.Name, user.AvatarURL
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `HorosClaims` | JWT claims with UserID, Email, Role, Handle, AvatarURL, AuthProvider |
| `GenerateToken(secret, claims, expiry)` | Create signed JWT |
| `ValidateToken(secret, tokenStr)` | Parse and verify JWT |
| `Middleware(secret)` | HTTP middleware injecting claims into context |
| `RequireAuth` | Middleware redirecting unauthenticated requests to /login |
| `GetClaims(ctx)` | Retrieve `HorosClaims` from context |
| `SetTokenCookie` / `ClearTokenCookie` | Cookie helpers |
| `NewGoogleProvider(cfg)` | Create Google OAuth2 config |
| `FetchGoogleUser(ctx, cfg, code)` | Exchange OAuth code for user profile |
