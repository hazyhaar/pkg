> **Schema technique** : voir [`sas_ingester_schem.md`](sas_ingester_schem.md) — lecture prioritaire avant tout code source.

# sas_ingester

Responsabilite: Pipeline d'ingestion SAS complet — reception fichier, chunking, metadata, scan securite (ClamAV, zip bomb, polyglot, macro, injection prompt), dedup SHA-256, conversion markdown, buffer HORAG (.md), routing webhook, TUS resumable upload, et JWT identity avec cutoff.
Depend de: `github.com/hazyhaar/pkg/idgen`, `github.com/hazyhaar/pkg/observability`, `github.com/hazyhaar/pkg/sas_chunker`, `github.com/hazyhaar/pkg/trace`, `github.com/hazyhaar/pkg/horosafe`, `github.com/hazyhaar/pkg/connectivity`, `github.com/hazyhaar/pkg/kit`, `github.com/hazyhaar/pkg/injection`, `github.com/modelcontextprotocol/go-sdk/mcp`, `gopkg.in/yaml.v3`
Dependants: aucun dans pkg/ (package terminal); externes: cmd/sas_ingester
Point d'entree: ingester.go (Ingester, pipeline orchestrator)
Types cles: `Ingester`, `Store`, `Router`, `Config`, `Piece`, `Dossier`, `TusHandler`, `TusUpload`, `IngestResult`, `ScanResult`, `InjectionResult`, `FileMetadata`, `JWTClaims`, `UploadResult`, `OpaquePayload`, `PassthruPayload`, `RoutePending`, `MarkdownConverter`, `PieceMarkdown`, `BufferWriter`

## Pipeline (7 étapes)

```
Ingest(io.Reader, dossierID, ownerSub)
  ├─ Pre-cutoff: EnsureDossier(dossierID, ownerSub)
  ├─ Step 1: ReceiveFile → chunk + hash + dedup
  ├─ ═══ Identity cutoff: ownerSub erased ═══
  ├─ Step 2: ExtractFullMetadata
  ├─ Step 3: ScanChunks (ClamAV + structural)
  ├─ Step 4: ScanChunksInjection
  ├─ Step 5: UpdatePieceMetadata → finalState
  ├─ Step 5.5: convertToMarkdown (if MarkdownConverter set, state=ready)
  ├─ Step 5.5b: ScanInjection on extracted text (re-scan after format extraction)
  ├─ Step 5.5c: BufferWriter.Write (if configured, state=ready, markdown non-empty)
  ├─ Step 6: EnqueueRoutesWithToken
  └─ Return IngestResult (with MarkdownText if converted)
```

## Authentification (double auth)

Tous les handlers (connectivity + MCP) exigent l'authentification :

- **`owner_sub`** — identité pré-authentifiée (service-to-service via JWT/middleware)
- **`horoskey`** — clé API (`hk_xxx`), résolue via `KeyResolver` callback

`resolveOwner(ctx, ownerSub, horoskey)` dans `ingester.go` :
1. Si `owner_sub` non vide → retour direct (confiance service interne)
2. Si `horoskey` non vide → appel `KeyResolver(ctx, horoskey)` → `ownerSub`
3. Sinon → erreur `"authentication required"`

Le `KeyResolver` est configuré via `WithKeyResolver(fn)`. Le binaire fournit l'implémentation
(ex: `apikey.Store.Resolve` → `key.OwnerID`). Sans `KeyResolver`, les requêtes avec `horoskey`
sont rejetées.

Voir `pkg/apikey` pour le package de gestion des clés API.

## Connectivity (6 services)

`RegisterConnectivity(router, ing)` enregistre :

| Service | Input | Output |
|---------|-------|--------|
| `sas_create_context` | `{owner_sub\|horoskey, name?}` | `{dossier_id, howto}` — crée un dossier sous le compte de l'appelant |
| `sas_upload_piece` | `{owner_sub\|horoskey, dossier_id, filename?, content_base64}` | `IngestResult` — base64 max 10 Mo, message howto TUS si trop gros |
| `sas_query_piece` | `{owner_sub\|horoskey, dossier_id, sha256}` | `{piece, has_markdown}` |
| `sas_list_pieces` | `{owner_sub\|horoskey, dossier_id, state?}` | `{pieces, count}` |
| `sas_get_markdown` | `{owner_sub\|horoskey, dossier_id, sha256}` | `{markdown}` |
| `sas_retry_routes` | `{owner_sub\|horoskey, dossier_id, sha256}` | `{retried: N}` |

## MCP (6 outils)

`RegisterMCP(srv, ing)` enregistre les memes 6 outils MCP via `kit.RegisterMCPTool`.
Tous exigent `horoskey` comme champ requis (les LLM n'ont pas de JWT, ils utilisent des clés API).
Description et schema JSON calqués sur les services connectivity.

## Markdown converter (callback pattern)

```go
type MarkdownConverter func(ctx context.Context, filePath string, mime string) (string, error)
```

- Défini dans `markdown.go`, implémenté côté binaire (ex: docpipe dans chrc/)
- Evite l'import circulaire chrc → hazyhaar_pkg
- Configuré via `WithMarkdownConverter(fn)`
- Si nil, step 5.5 est silencieusement ignoré
- Résultat stocké dans `pieces_markdown` (table SQLite, FK cascade sur pieces)
- Assemble les chunks en fichier temporaire avant d'appeler le converter

## Contexte jetable (sas_create_context)

Un usager authentifié (par JWT ou horoskey) peut créer un dossier pour uploader des pièces et récupérer du markdown. Le dossier est lié au compte de l'appelant (pour facturation).
- `owner_jwt_sub` = identité résolue (jamais anonyme)
- Le dossier est un contexte jetable — le caller reçoit un `dossier_id` et peut immédiatement uploader des pièces

## Invariants

- **Identity cutoff** : `ownerSub` est efface apres `EnsureDossier` — le pipeline ne passe que le `dossierID` opaque en aval
- Routes `opaque_only` ne doivent JAMAIS avoir de header `Authorization` — assertion explicite dans `Deliver()`
- `originalToken` pour `jwt_passthru` est forward puis efface — jamais persiste dans les logs
- Le Store utilise le driver "sqlite-trace" — la DB de trace doit etre ouverte avec "sqlite" brut
- `ReceiveFile` stream directement via `SplitReader` — pas de fichier temporaire intermediaire
- La dedup est basee sur `(sha256, dossier_id)` — meme hash dans des dossiers differents = pieces differentes
- Scan ClamAV via INSTREAM (socket Unix) — pas besoin de filesystem partage
- **Base64 upload max 10 Mo** — au-dela, retour JSON avec message howto TUS
- **MarkdownConverter nil = skip** — pas de panic, pas d'erreur
- **Step 5.5b** : re-scan injection sur texte extrait (pas juste chunks binaires) — si risk upgraye a "high", piece passe en "flagged"
- **Step 5.5c** : BufferWriter ecrit .md avec frontmatter YAML dans HORAG buffer dir — atomic write (tmp→rename), nil-safe
- **BufferWriter** : configure via `WithBufferWriter(NewBufferWriter(bufferDir))`, `buffer_dir` dans config YAML (vide = desactive)
- **Injection** : `ScanInjection()` delegue a `injection.Scan()` (package `hazyhaar/pkg/injection`). Normalisation multi-couche (NFKD, confusables, leet, invisible strip, markup strip) + intent matching exact/fuzzy/base64. Zero regex.
- **`StripZeroWidthChars()`** : delegue a `injection.StripInvisible()` (couvre tous les unicode Cf/Cc)
- **`HasHomoglyphMixing()`** : delegue a `injection.HasHomoglyphMixing()`
- **Invariant** : tout texte extrait est scanne AVANT stockage/routing

NE PAS:
- Ne pas passer `ownerSub` apres le cutoff d'identite dans `processPipeline` — c'est un invariant de securite
- Ne pas oublier `RecoverStalePieces()` au boot — les pieces en etat "received"/"scanned" d'un crash precedent doivent etre re-traitees
- Ne pas logger le `originalToken` — il contient le JWT utilisateur
- Ne pas desactiver `horosafe.ValidateIdentifier(dossierID)` — c'est la garde anti path-traversal
- Ne pas utiliser le Store pour autre chose que l'etat de la pipeline — l'observabilite va dans une DB separee
- Ne pas retourner `error` dans un ToolHandler MCP — utiliser `result.SetError()` (kit.RegisterMCPTool le fait automatiquement)
