# feedback

Responsabilite: Widget feedback integrable fournissant formulaire de soumission, liste JSON/HTML des commentaires, et assets JS/CSS embarques.
Depend de: `github.com/hazyhaar/pkg/idgen`
Dependants: repvow (main), horum (main, feedback e2e tests), horostracker (main)
Point d'entree: `feedback.go` (New, Widget.Handler, Widget.RegisterMux)
Types cles: `Widget`, `Config`, `Comment`, `UserIDFunc`
Invariants:
- Le schema est applique automatiquement dans `New()` -- pas besoin d'appel Init separe
- Le body POST est limite a 32 KiB, le texte a 5000 caracteres (tronque, pas rejete)
- Les IDs sont generes via `idgen.New()` (UUID v7)
- Les assets `widget.js` et `widget.css` sont embarques via `//go:embed` et servis avec cache 1h
- La pagination par defaut : limit=50, max=500
- `UserIDFunc` peut etre nil (feedback anonyme)
- Compatible chi (via Handler + StripPrefix) et ServeMux (via RegisterMux, Go 1.22+)
NE PAS:
- Appeler `New()` avec DB=nil -- retourne une erreur
- Oublier de stripper le prefix URL quand on monte via chi (`http.StripPrefix`)
- S'attendre a ce que les URL page_url soient rendues comme liens cliquables si elles ne commencent pas par http/https (securite XSS)
