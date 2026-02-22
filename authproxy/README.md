# authproxy — FO-to-BO authentication proxy

`authproxy` translates back-office (BO) internal auth API responses into
front-office (FO) cookies and redirects. The FO never exposes the BO URL to
end users.

```
Browser ──POST /login──► FO (authproxy) ──POST /api/internal/auth/login──► BO
         ◄── cookie ───  FO             ◄────── {token, user} ──────────   BO
```

## Quick start

```go
proxy := authproxy.NewAuthProxy(boURL, "example.com", true)
proxy.HealthCheck = func() bool { return bo.IsReachable() }

mux.HandleFunc("POST /login", proxy.LoginHandler(shield.SetFlash))
mux.HandleFunc("POST /register", proxy.RegisterHandler(shield.SetFlash))
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `AuthProxy` | Proxy forwarding login/register to BO |
| `NewAuthProxy(boURL, cookieDomain, secure)` | Create proxy with 10 s HTTP timeout |
| `LoginHandler(setFlash)` | POST handler: proxy login, set JWT cookie |
| `RegisterHandler(setFlash)` | POST handler: proxy registration |
| `HealthCheck` | Optional callback for circuit-breaker integration |
