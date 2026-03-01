> **Schema technique** : voir [`injection_schem.md`](injection_schem.md) — lecture prioritaire avant tout code source.

# injection

Responsabilite: Detection d'injection de prompt multi-couche — normalisation Unicode, intent matching exact/fuzzy, decodage base64 smuggling. Zero regex, zero ReDoS.

Depend de: `golang.org/x/text/unicode/norm`
Dependants: `sas_ingester`, `siftrag`, `veille`, `horum` (via `Scan()`)
Types cles: `Intent`, `Result`, `Match`

## Architecture (2 strates + 3 couches)

- **Strate 1** : intentions semantiques canoniques (`intents.json`, go:embed, multilingue, ~35 phrases)
- **Strate 2** : `Normalize()` algorithmique — NFKD + confusables fold + leet + invisible strip + markup strip + punctuation strip + whitespace collapse

Couches de detection dans `Scan()` :
1. **Structurelle** : zero-width clusters, homoglyph mixing, dangerous HTML (avant normalisation)
2. **Exacte** : `strings.Contains` sur texte normalise — O(n*k), pas de regex
3. **Fuzzy** : Levenshtein mot par mot (seuil ≤2 edits/mot) — resistance typoglycemie
4. **Base64** : detection + decodage segments base64 inline → re-scan du contenu decode

## Fichiers

| Fichier | Role |
|---------|------|
| `normalize.go` | Pipeline normalisation (StripInvisible, StripMarkup, NFKD, FoldConfusables, FoldLeet, stripPunctuation) |
| `fuzzy.go` | Levenshtein + FuzzyContains (sliding window mot par mot) |
| `base64.go` | DecodeBase64Segments (token smuggling) |
| `scan.go` | Scan(), types, DefaultIntents, LoadIntents, HasHomoglyphMixing |
| `confusables.json` | Table Cyrillic/Greek/IPA → ASCII (~20 entrees) |
| `leet.json` | Table leet speak → ASCII (~12 entrees) |
| `intents.json` | Intentions canoniques multilingues (extensible via feed) |

## API

```go
// Scan complet
result := injection.Scan(text, injection.DefaultIntents())

// Normalisation seule
normalized := injection.Normalize(text)

// Chargement intents externes (feed Gemini, SQLite)
intents, err := injection.LoadIntents(jsonData)
result := injection.Scan(text, intents)
```

## Scan bidirectionnel (inputs ET outputs)

`Scan()` est agnostique sur la direction du texte. Les consommateurs doivent scanner :
- Documents ingeres (entree)
- Texte post-OCR (entree)
- Reponses d'agents/LLM avant stockage/affichage (sortie — defense Agent-as-a-Proxy)

## Categories d'intents (8)

`override`, `extraction`, `jailbreak`, `delimiter`, `semantic_worm`, `ai_address`, `agent_proxy`, `rendering`

## Enrichissement

Le fichier `intents.json` est prevu pour enrichissement par feed Gemini. Format stable.
`LoadIntents()` permet le rechargement sans rebuild.
Futur : table SQLite `injection_patterns` pour ajout/disable a chaud.

## Build / Test

```bash
CGO_ENABLED=0 go test ./injection/ -v -count=1
```

## Invariants

- Pas de regex — zero risque ReDoS
- Tables JSON read-only apres init — thread-safe sans mutex
- Couche fuzzy ne tourne que sur intents non matches par l'exact — cout nul pour 99% du trafic
- Couche base64 ne tourne que si segment base64 detecte
- `stripPunctuation` apres `FoldLeet` — les symboles leet ($, @, !) sont convertis avant d'etre supprimes
