> **Schema technique** : voir [`auth_schem.md`](auth_schem.md) — lecture prioritaire avant tout code source.

# auth

Responsabilite: Authentification JWT (generation, validation HS256), cookies HttpOnly cross-subdomain, middleware HTTP d'extraction claims, et OAuth2 Google.
Depend de: `github.com/hazyhaar/pkg/horosafe`, `github.com/hazyhaar/pkg/kit`, `github.com/golang-jwt/jwt/v5`, `golang.org/x/oauth2`
Dependants: repvow (auth_service, auth_pages, main), horum (auth_service, auth_pages, oauth, main), chrc (main), authproxy, dbsync/auth_proxy
Point d'entree: `jwt.go` (GenerateToken, ValidateToken)
Types cles: `HorosClaims`, `OAuthConfig`, `OAuthUser`, `Middleware` (HTTP), `RequireAuth`
Invariants:
- Le signing method est TOUJOURS HS256 -- `ValidateToken` rejette tout autre algorithme (protection contre algorithm confusion)
- Le secret doit faire au moins `horosafe.MinSecretLen` (32 bytes) -- `GenerateToken` echoue sinon
- Le middleware injecte les claims dans le context via `claimsKey{}` ET `kit.WithUserID`/`kit.WithHandle`
- Cookie = source preferee, Authorization Bearer = fallback
- Un token invalide est silencieusement ignore (cookie supprime), pas de 401 -- utiliser `RequireAuth` pour forcer
NE PAS:
- Accepter un autre algorithme que HS256 (faille de securite)
- Utiliser `ValidateTokenMapClaims` dans du nouveau code -- c'est un backward-compat, preferer `ValidateToken`
- Oublier le domain dans `SetTokenCookie` pour le SSO cross-subdomain
