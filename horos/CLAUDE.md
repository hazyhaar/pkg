# horos

Responsabilite: Type system HOROS -- contrats de service types, format envelope wire (2B format + 4B CRC-32C + payload), erreurs structurees inter-services, et registre de formats.
Depend de: aucune dependance interne (package feuille)
Dependants: aucun service externe pour l'instant (framework pret a l'emploi)
Point d'entree: `codec.go` (Contract, Codec interface)
Types cles: `Codec[T]` (interface generique F-bounded), `Contract[Req, Resp]`, `ServiceError`, `Registry`, `FormatInfo`
Invariants:
- L'envelope wire fait 6 bytes d'overhead : uint16 LE format_id + uint32 LE CRC-32C Castagnoli
- `Unwrap` avec donnees trop courtes (<6 bytes) retourne FormatRaw sans erreur
- `Unwrap` avec format_id=0 et checksum mismatch retourne les donnees originales intactes (backward compat raw)
- `ServiceError` voyage sur le fil via le sentinel `__error` dans le JSON -- `DetectError` le detecte avant le decode normal
- `errors.Is` sur `ServiceError` compare par Code (pas par Message)
- Le Registry Go est source de verite pour le dispatch codec ; la table SQLite `horos_formats` est pour l'observabilite
- Formats built-in : 0=raw, 1=JSON, 2=msgpack
NE PAS:
- Enregistrer deux formats avec le meme ID mais des noms differents (erreur a l'enregistrement)
- Retourner une `error` Go classique depuis un handler Contract -- utiliser `ServiceError` pour que l'erreur traverse le fil
- Oublier le CRC-32C check quand on implemente un parser custom du wire format
