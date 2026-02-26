# authproxy

Responsabilite: Proxy HTTP relayant les requetes d'authentification FO (front-office) vers l'API interne BO (back-office), avec traduction JSON -> cookies + redirects.
Depend de: `github.com/hazyhaar/pkg/auth`
Dependants: repvow (main)
Point d'entree: `authproxy.go` (AuthProxy struct + handlers)
Types cles: `AuthProxy`, `authResponse`, `LoginHandler`, `RegisterHandler`, `ForgotPasswordHandler`, `ResetPasswordHandler`
Invariants:
- Le BO URL n'est JAMAIS expose a l'utilisateur final -- toutes les interactions passent par le proxy
- `ForgotPasswordHandler` envoie le `origin` (scheme+host du FO, deduit de `r.Host`/`X-Forwarded-*`) pour que le BO construise le lien reset avec le domaine FO
- Si `HealthCheck` est configure et retourne false, les handlers echouent immediatement (circuit breaker)
- Le client HTTP a un timeout de 10s
- Les mots de passe confirm sont valides cote FO dans `ResetPasswordHandler` avant l'envoi au BO
NE PAS:
- Utiliser `dbsync.AuthProxy` (deprecated) -- ce package le remplace
- Exposer le boURL dans les redirects ou les messages d'erreur
- Oublier de configurer `HealthCheck` en production (sinon chaque requete attend 10s si le BO est down)
