╔══════════════════════════════════════════════════════════════════════════╗
║  mail — Shared SMTP service: STARTTLS + LOGIN auth (OVH compatible)     ║
╠══════════════════════════════════════════════════════════════════════════╣
║                                                                        ║
║  FLOW                                                                  ║
║  ────                                                                  ║
║                                                                        ║
║  Config{Host,Port,User,Pass,From,AppName}                              ║
║       │                                                                ║
║       ▼                                                                ║
║  NewService(cfg) ──→ Service                                           ║
║       │                                                                ║
║       │  Host == "" ?                                                  ║
║       ├──── YES ──→ Disabled mode: Send() logs warn, returns nil       ║
║       ├──── NO  ──→ Active mode                                       ║
║       │                                                                ║
║       ▼                                                                ║
║  ┌─────────────────────────────────────────────────────────────┐       ║
║  │  Send(to, subject, body)                                    │       ║
║  │                                                             │       ║
║  │  1. MIME Q-encode subject (UTF-8)                           │       ║
║  │  2. Build RFC 2822 message                                  │       ║
║  │     From: / To: / Subject: / MIME-Version: 1.0              │       ║
║  │     Content-Type: text/plain; charset=UTF-8                 │       ║
║  │                                                             │       ║
║  │  3. smtp.Dial(host:port)                                    │       ║
║  │  4. EHLO localhost                                          │       ║
║  │  5. STARTTLS (TLS config: ServerName = host)                │       ║
║  │  6. AUTH LOGIN (username, password)                          │       ║
║  │  7. MAIL FROM / RCPT TO / DATA / QUIT                       │       ║
║  └─────────────────────────────────────────────────────────────┘       ║
║       │                                                                ║
║       │  slog.Info("mail sent", to, subject)                           ║
║       ▼                                                                ║
║                                                                        ║
║  CONVENIENCE METHODS (compose subject + body, then call Send)          ║
║  ──────────────────────────────────────────────────────                 ║
║                                                                        ║
║  SendPasswordReset(to, baseURL, token)                                 ║
║  ┌─────────────────────────────────────────────────────────┐           ║
║  │ Link: {baseURL}/reset-password?token={token}            │           ║
║  │ Subject: "Reinitialisation de votre mot de passe - {App}"│           ║
║  │ Body: French template, link, 1h expiry notice           │           ║
║  └─────────────────────────────────────────────────────────┘           ║
║                                                                        ║
║  SendVerification(to, baseURL, token)                                  ║
║  ┌─────────────────────────────────────────────────────────┐           ║
║  │ Link: {baseURL}/verify-email?token={token}              │           ║
║  │ Subject: "Verification de votre email - {AppName}"      │           ║
║  │ Body: French template, link, 24h expiry notice          │           ║
║  └─────────────────────────────────────────────────────────┘           ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  SMTP AUTH: LOGIN mechanism (not PLAIN)                                ║
║  ──────────────────────────────────────                                ║
║                                                                        ║
║  loginAuth implements smtp.Auth                                        ║
║  ┌──────────────────────────────────────────────┐                      ║
║  │ Start()     -> "LOGIN", nil, nil             │                      ║
║  │ Next()      -> "Username:" -> username bytes  │                      ║
║  │             -> "Password:" -> password bytes  │                      ║
║  │             -> other       -> error           │                      ║
║  └──────────────────────────────────────────────┘                      ║
║  Required by OVH (does not support PLAIN/CRAM-MD5).                   ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                        ║
║  ──────────────                                                        ║
║                                                                        ║
║  Config {                                                              ║
║      Host     string    SMTP server hostname                           ║
║      Port     string    SMTP port (typically "587")                    ║
║      Username string    SMTP login username                            ║
║      Password string    SMTP login password                            ║
║      From     string    Sender email address                           ║
║      AppName  string    Used in subjects/bodies ("SiftRAG", "repvow")  ║
║  }                                                                     ║
║                                                                        ║
║  Service { cfg Config }                                                ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                         ║
║  ─────────────                                                         ║
║                                                                        ║
║  NewService(cfg Config) *Service                                       ║
║      Create mail service. Empty Host = disabled mode.                  ║
║                                                                        ║
║  (*Service).Enabled() bool                                             ║
║      True if Host is configured.                                       ║
║                                                                        ║
║  (*Service).Send(to, subject, body string) error                       ║
║      Core send. Plain-text, STARTTLS, LOGIN auth. No fallback.         ║
║                                                                        ║
║  (*Service).SendPasswordReset(to, baseURL, token string) error         ║
║      Password reset email with tokenized link. 1h expiry.             ║
║                                                                        ║
║  (*Service).SendVerification(to, baseURL, token string) error          ║
║      Email verification link. 24h expiry.                              ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                          ║
║  ────────────                                                          ║
║                                                                        ║
║  Stdlib only: crypto/tls, fmt, log/slog, mime, net, net/smtp, strings  ║
║  No pkg/ internal dependencies. Leaf package.                          ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES                                                       ║
║  ───────────────                                                       ║
║  None. Stateless email delivery.                                       ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                            ║
║  ──────────                                                            ║
║                                                                        ║
║  - STARTTLS mandatory — no plaintext fallback                          ║
║  - LOGIN auth only — OVH does not support PLAIN or CRAM-MD5           ║
║  - Empty Host = disabled — Send() returns nil (no error), logs warn    ║
║  - Subjects are MIME Q-encoded for UTF-8 safety                        ║
║  - Email templates are in French (ecosystem convention)                ║
║  - AppName injected into all subjects and bodies                       ║
║  - Do NOT add service-specific templates here (e.g. no repvow invite)  ║
║                                                                        ║
╚══════════════════════════════════════════════════════════════════════════╝
