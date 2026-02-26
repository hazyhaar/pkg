# mail

Responsabilité : Service SMTP partagé — STARTTLS + LOGIN auth (compatible OVH).

Dépend de : stdlib (`net/smtp`, `crypto/tls`, `log/slog`)
Dépendants : `siftrag`, `repvow` (migration future)

## Types clés

- `Config` — Host, Port, Username, Password, From, AppName
- `Service` — `Enabled()`, `Send()`, `SendPasswordReset()`, `SendVerification()`
- `loginAuth` — smtp.Auth LOGIN mechanism (OVH ne supporte pas PLAIN)

## Comportement

- `Host` vide = disabled → `Send()` log warn et retourne nil (pas d'erreur)
- `AppName` injecté dans les sujets et corps des emails
- STARTTLS obligatoire, pas de fallback plaintext

## Test

```bash
go test ./mail/ -v -count=1
```

## NE PAS

- Ajouter `SendEngagementInvite` ici (reste local à repvow)
- Supprimer le LOGIN auth — OVH ne supporte pas PLAIN/CRAM-MD5
