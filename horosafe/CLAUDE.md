> **Schema technique** : voir [`horosafe_schem.md`](horosafe_schem.md) — lecture prioritaire avant tout code source.

# horosafe

Responsabilite: Primitives de securite partagees -- validation secrets (longueur min), prevention SSRF (URL vers IP privees), protection path traversal, I/O borne, et validation identifiants.
Depend de: aucune dependance interne (package feuille, stdlib uniquement)
Dependants: auth (jwt.go, oauth.go), connectivity/factory_http, sas_ingester (upload, tus), channels/webhook
Point d'entree: `horosafe.go` (ValidateSecret, ValidateURL, SafePath, LimitedReadAll)
Types cles: `ValidateSecret`, `ValidateURL`, `SafePath`, `ValidateIdentifier`, `LimitedReadAll`
Invariants:
- `MinSecretLen` = 32 bytes (256 bits) -- tout secret plus court est rejete
- `ValidateURL` resout le DNS et verifie que TOUTES les IPs resolues ne sont pas privees/loopback (anti-SSRF avec anti-rebinding)
- Si la resolution DNS echoue, l'URL est acceptee (l'erreur reseau viendra au moment de la connexion)
- `SafePath` rejette les chemins contenant `..` AVANT le filepath.Clean (double protection)
- `LimitedReadAll` lit maxBytes+1 pour detecter le depassement, puis retourne une erreur si depasse
- `ValidateIdentifier` autorise uniquement : alphanumerique, underscore, hyphen, point ; max 256 chars
- Ranges IP privees verifiees : loopback, link-local, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7, 169.254.0.0/16, ::1/128
NE PAS:
- Utiliser ce package pour du rate limiting (c'est dans `shield`)
- Faire confiance a `ValidateURL` seul pour la securite SSRF en environnement hostile (c'est une couche de defense, pas une garantie absolue)
- Contourner `ValidateSecret` pour accepter des secrets courts (faille cryptographique)
