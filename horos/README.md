# horos — type system et contrats de service

`horos` fournit des contrats de service typés au-dessus de `connectivity.Handler`, un format envelope wire (2B format + 4B CRC-32C + payload), et des erreurs structurées inter-services.

## Quick start

```go
// Définir un contrat typé.
var SearchContract = horos.Contract[SearchReq, SearchResp]{
    Service: "domkeeper_search", FormatID: 1,
}

// Appel typé (remplace json.Marshal + router.Call + json.Unmarshal).
resp, err := SearchContract.Call(ctx, router, req)

// Handler typé (remplace func(ctx, []byte) ([]byte, error)).
router.RegisterLocal("domkeeper_search", SearchContract.Handler(svc.search))
```

## Wire format

```
┌──────────────┬──────────────┬───────────┐
│ format_id    │ CRC-32C      │ payload   │
│ (2B uint16LE)│ (4B uint32LE)│ (variable)│
└──────────────┴──────────────┴───────────┘
```

Formats built-in : 0=raw, 1=JSON, 2=msgpack.

## Exported API

| Symbol | Description |
|--------|-------------|
| `Codec[T]` | Interface générique F-bounded (Encode/Decode) |
| `Contract[Req, Resp]` | Contrat typé : Call + Handler |
| `ServiceError` | Erreur structurée traversant le fil (`__error` sentinel) |
| `Registry` | Registre de formats (source de vérité Go) |
| `Wrap(formatID, data)` | Emballe avec envelope |
| `Unwrap(data)` | Déballe + vérifie CRC |

## Quand utiliser

Couche optionnelle au-dessus de `connectivity.Handler` pour éliminer le JSON manuel. Utile quand un service a beaucoup de handlers ou quand on veut du type-safety compile-time.

## Anti-patterns

| Ne pas faire | Faire |
|---|---|
| Retourner `error` Go depuis un handler Contract | `ServiceError` pour traverser le fil |
| Ignorer le CRC-32C dans un parser custom | Toujours vérifier |
| Deux formats avec le même ID | Erreur à l'enregistrement |
