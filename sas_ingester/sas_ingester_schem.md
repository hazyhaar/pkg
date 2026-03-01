```
╔═══════════════════════════════════════════════════════════════════════════════════════╗
║  sas_ingester — Full file ingestion pipeline: receive, chunk, scan, dedup, markdown  ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  PIPELINE (7 steps + identity cutoff)                                                 ║
║  ─────────────────────────────────────                                                ║
║                                                                                       ║
║  ┌─────────────────┐   ┌──────────────────────────────────────────────────────────┐   ║
║  │ io.Reader (file) │──>│ Ingester.Ingest(r, dossierID, ownerSub)                │   ║
║  │ or base64 data   │   │                                                        │   ║
║  │ or TUS partial   │   │  PRE-CUTOFF: ownerSub available                        │   ║
║  └─────────────────┘   │    EnsureDossier(dossierID, ownerSub)                   │   ║
║                         │    AuditLog("sas.upload.received", ownerSub)            │   ║
║                         │                                                        │   ║
║                         │  ════════ IDENTITY CUTOFF: ownerSub erased ════════    │   ║
║                         │                                                        │   ║
║  Step 1: ReceiveFile ───│──> sas_chunker.SplitReader ──> chunks on disk          │   ║
║          (hash+dedup)   │    hash = SHA-256(whole file)                           │   ║
║          if dedup: exit │    dedup check: (sha256, dossier_id)                    │   ║
║                         │                                                        │   ║
║  Step 2: ExtractFull ───│──> header magic + trailer (PDF/ZIP) + Shannon entropy  │   ║
║          Metadata       │    across all chunks (64 KiB/chunk sampling)            │   ║
║                         │                                                        │   ║
║  Step 3: ScanChunks ────│──> zip bomb (PK count vs size)                         │   ║
║          (security)     │    polyglot (multiple magic numbers)                    │   ║
║                         │    macro detection (OLE2+VBA, .xlsm/.docm/.pptm)       │   ║
║                         │    ClamAV INSTREAM (Unix socket, all chunks)            │   ║
║                         │    if blocked: state="blocked", exit                    │   ║
║                         │                                                        │   ║
║  Step 4: ScanChunks ────│──> injection.Scan() on binary chunks                   │   ║
║          Injection      │    normalize + intent match (exact/fuzzy/base64)        │   ║
║                         │                                                        │   ║
║  Step 5: UpdatePiece ───│──> state = "ready" or "flagged" (if injection=high)    │   ║
║          Metadata       │    store MIME, metadata JSON, clamav status             │   ║
║                         │                                                        │   ║
║  Step 5.5: convertTo ──>│──> assemble chunks -> tmpfile -> MarkdownConverter()   │   ║
║            Markdown     │    store result in pieces_markdown                      │   ║
║                         │                                                        │   ║
║  Step 5.5b: re-scan ───│──> ScanInjection(extracted text)                        │   ║
║             injection   │    upgrade to "flagged" if risk=high                    │   ║
║                         │                                                        │   ║
║  Step 5.5c: Buffer ────│──> BufferWriter.Write() -> HORAG buffer dir             │   ║
║             Writer      │    .md with YAML frontmatter (atomic tmp->rename)       │   ║
║                         │                                                        │   ║
║  Step 6: Enqueue ───────│──> Router.EnqueueRoutesWithToken(piece, token)          │   ║
║          Routes         │    per-dossier routes OR global webhooks                │   ║
║                         └────────────────────────────────────────────────────────┘   ║
║                                          │                                           ║
║                                          v                                           ║
║                                   ┌──────────────┐                                   ║
║                                   │ IngestResult  │                                   ║
║                                   │  .SHA256      │                                   ║
║                                   │  .SizeBytes   │                                   ║
║                                   │  .DossierID   │                                   ║
║                                   │  .State       │                                   ║
║                                   │  .MIME        │                                   ║
║                                   │  .Scan        │                                   ║
║                                   │  .Injection   │                                   ║
║                                   │  .MarkdownText│                                   ║
║                                   └──────────────┘                                   ║
║                                                                                       ║
║  UPLOAD PATHS                                                                         ║
║  ─────────────                                                                        ║
║                                                                                       ║
║  [base64 <=10MB] ──> Ingest(bytes.Reader, ...)                                       ║
║  [TUS >10MB]     ──> TusHandler.Create/Patch/Complete ──> IngestFromUpload(...)       ║
║  [direct stream] ──> ReceiveFile(io.Reader, ...)                                     ║
║                                                                                       ║
║  TUS PROTOCOL (resumable)                                                             ║
║  ┌────────┐  POST /uploads   ┌───────────┐  PATCH /uploads/:id  ┌───────────────┐   ║
║  │ Client │ ────────────────>│ Create()   │ ───────────────────> │ Patch()       │   ║
║  │        │  Upload-Length   │ -> uploadID│  Upload-Offset      │ -> newOffset  │   ║
║  │        │                  └───────────┘  append to partial   └───────────────┘   ║
║  │        │  HEAD /uploads/:id -> GetOffset()                                       ║
║  │        │  offset == totalSize -> Complete() -> hash + SplitReader -> IngestFrom  ║
║  └────────┘                                                                          ║
║                                                                                       ║
║  WEBHOOK ROUTING                                                                      ║
║  ───────────────                                                                      ║
║                                                                                       ║
║  ┌──────────┐         ┌─────────────────────────────────────────────┐                ║
║  │ Router   │ ──────> │ per-dossier routes (dossiers.routes JSON)   │                ║
║  │          │         │ OR global webhooks (Config.Webhooks)        │                ║
║  │ .Deliver │         └──────────────┬──────────────────────────────┘                ║
║  │ .Process │                        │                                                ║
║  │  Retries │                        v                                                ║
║  └──────────┘         ┌──────────────────────────────┐                                ║
║                       │ Auth Mode:                    │                                ║
║                       │  opaque_only: no JWT, HMAC    │                                ║
║                       │  jwt_passthru: Bearer + HMAC  │                                ║
║                       │ Retry: exp backoff, max 5     │                                ║
║                       │ Payload: OpaquePayload or     │                                ║
║                       │          PassthruPayload      │                                ║
║                       └──────────────────────────────┘                                ║
║                                                                                       ║
║  AUTHENTICATION (dual-auth)                                                           ║
║  ──────────────────────────                                                           ║
║                                                                                       ║
║  resolveOwner(ctx, ownerSub, horoskey):                                               ║
║    1. ownerSub non-empty? -> return (service-to-service trust)                        ║
║    2. horoskey non-empty? -> KeyResolver(ctx, hk) -> ownerSub                         ║
║    3. neither? -> error "authentication required"                                     ║
║                                                                                       ║
║  CONNECTIVITY (6 services registered on connectivity.Router)                          ║
║  ──────────────────────────────────────────────────────────                            ║
║                                                                                       ║
║  sas_create_context  {owner_sub|horoskey, name?}              -> {dossier_id}         ║
║  sas_upload_piece    {owner_sub|horoskey, dossier_id, b64}    -> IngestResult         ║
║  sas_query_piece     {owner_sub|horoskey, dossier_id, sha256} -> {piece, has_md}      ║
║  sas_list_pieces     {owner_sub|horoskey, dossier_id, state?} -> {pieces, count}      ║
║  sas_get_markdown    {owner_sub|horoskey, dossier_id, sha256} -> {markdown}           ║
║  sas_retry_routes    {owner_sub|horoskey, dossier_id, sha256} -> {retried: N}         ║
║                                                                                       ║
║  MCP (6 tools via kit.RegisterMCPTool, same services, horoskey only)                 ║
║  ──────────────────────────────────────────────────────────────                        ║
║  sas_create_context, sas_upload_piece, sas_query_piece,                               ║
║  sas_list_pieces, sas_get_markdown, sas_retry_routes                                  ║
║                                                                                       ║
║  BUFFER WRITER (HORAG integration)                                                    ║
║  ─────────────────────────────────                                                    ║
║                                                                                       ║
║  BufferWriter.Write(ctx, dossierID, sha256, title, markdown)                          ║
║    -> {bufferDir}/{sha256}.md with YAML frontmatter:                                  ║
║       ---                                                                             ║
║       id: "<sha256>"                                                                  ║
║       dossier_id: "<dossier_id>"                                                      ║
║       content_hash: "sha256:<hash>"                                                   ║
║       source_type: "document"                                                         ║
║       title: "<title>"                                                                ║
║       ---                                                                             ║
║       <markdown body>                                                                 ║
║    Atomic: .md.tmp -> os.Rename -> .md                                                ║
║    Nil-safe: no-op if *BufferWriter is nil                                            ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES (SQLite, driver: "sqlite-trace")                                     ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  dossiers                                                                             ║
║  ┌────────────────┬──────┬────────────────────────────────────────────┐               ║
║  │ id             │ TEXT │ PK, UUID v7 prefixed "dos_"                │               ║
║  │ owner_jwt_sub  │ TEXT │ NOT NULL, identity owner                   │               ║
║  │ name           │ TEXT │ optional human-readable name               │               ║
║  │ routes         │ TEXT │ JSON array of DossierRoute (per-dossier)   │               ║
║  │ created_at     │ TEXT │ NOT NULL, RFC3339                          │               ║
║  └────────────────┴──────┴────────────────────────────────────────────┘               ║
║  IDX: idx_dossiers_owner(owner_jwt_sub)                                               ║
║                                                                                       ║
║  pieces                                                                               ║
║  ┌────────────────┬─────────┬──────────────────────────────────────────┐              ║
║  │ sha256         │ TEXT    │ PK1, hex SHA-256 of original file        │              ║
║  │ dossier_id     │ TEXT    │ PK2, FK -> dossiers(id) CASCADE          │              ║
║  │ state          │ TEXT    │ received|scanned|ready|flagged|blocked   │              ║
║  │ mime           │ TEXT    │ detected MIME type                       │              ║
║  │ size_bytes     │ INTEGER │ original file size                       │              ║
║  │ metadata       │ TEXT    │ JSON FileMetadata                        │              ║
║  │ injection_risk │ TEXT    │ none|low|medium|high                     │              ║
║  │ clamav_status  │ TEXT    │ pending|OK|<virus>|error                 │              ║
║  │ created_at     │ TEXT    │ RFC3339                                  │              ║
║  │ updated_at     │ TEXT    │ RFC3339                                  │              ║
║  └────────────────┴─────────┴──────────────────────────────────────────┘              ║
║  IDX: idx_pieces_state(state)                                                         ║
║                                                                                       ║
║  chunks                                                                               ║
║  ┌────────────────┬─────────┬──────────────────────────────────────────┐              ║
║  │ piece_sha256   │ TEXT    │ PK1, FK -> pieces(sha256, dossier_id)    │              ║
║  │ dossier_id     │ TEXT    │ PK2                                      │              ║
║  │ idx            │ INTEGER │ PK3, chunk index                         │              ║
║  │ chunk_sha256   │ TEXT    │ per-chunk hash                           │              ║
║  │ received       │ INTEGER │ 0|1                                      │              ║
║  └────────────────┴─────────┴──────────────────────────────────────────┘              ║
║                                                                                       ║
║  routes_pending                                                                       ║
║  ┌────────────────┬─────────┬──────────────────────────────────────────┐              ║
║  │ piece_sha256   │ TEXT    │ FK -> pieces                             │              ║
║  │ dossier_id     │ TEXT    │ FK -> pieces                             │              ║
║  │ target         │ TEXT    │ webhook URL                               │              ║
║  │ auth_mode      │ TEXT    │ opaque_only | jwt_passthru               │              ║
║  │ require_review │ INTEGER │ 0|1                                      │              ║
║  │ reviewed       │ INTEGER │ 0|1                                      │              ║
║  │ attempts       │ INTEGER │ retry count, max 5                       │              ║
║  │ last_error     │ TEXT    │ last failure message                      │              ║
║  │ next_retry_at  │ TEXT    │ RFC3339, exponential backoff              │              ║
║  │ original_token │ TEXT    │ JWT for passthru (never serialized)       │              ║
║  └────────────────┴─────────┴──────────────────────────────────────────┘              ║
║  IDX: idx_routes_retry(next_retry_at)                                                 ║
║                                                                                       ║
║  tus_uploads                                                                          ║
║  ┌────────────────┬─────────┬──────────────────────────────────────────┐              ║
║  │ upload_id      │ TEXT    │ PK                                       │              ║
║  │ dossier_id     │ TEXT    │ target dossier                            │              ║
║  │ owner_jwt_sub  │ TEXT    │ uploader identity                         │              ║
║  │ total_size     │ INTEGER │ expected total bytes                      │              ║
║  │ offset_bytes   │ INTEGER │ bytes received so far                     │              ║
║  │ chunk_dir      │ TEXT    │ staging directory path                    │              ║
║  │ created_at     │ TEXT    │ RFC3339                                   │              ║
║  │ updated_at     │ TEXT    │ RFC3339                                   │              ║
║  │ completed      │ INTEGER │ 0|1                                      │              ║
║  └────────────────┴─────────┴──────────────────────────────────────────┘              ║
║  IDX: idx_tus_dossier(dossier_id)                                                     ║
║                                                                                       ║
║  pieces_markdown                                                                      ║
║  ┌────────────────┬──────┬─────────────────────────────────────────────┐              ║
║  │ sha256         │ TEXT │ PK1, FK -> pieces(sha256, dossier_id)       │              ║
║  │ dossier_id     │ TEXT │ PK2                                         │              ║
║  │ markdown       │ TEXT │ converted markdown text                     │              ║
║  │ created_at     │ TEXT │ RFC3339                                     │              ║
║  └────────────────┴──────┴─────────────────────────────────────────────┘              ║
║  IDX: idx_markdown_dossier(dossier_id)                                                ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  KEY TYPES                                                                            ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  Ingester           Main orchestrator (Store, Config, Router, Audit, Metrics, ...)     ║
║  Store              SQLite state machine wrapper (*sql.DB)                             ║
║  Router             Webhook fan-out + retry (exp backoff, max 5 attempts)              ║
║  Config             YAML config (listen, db_path, chunks_dir, max_file_mb, ...)       ║
║  TusHandler         TUS resumable upload (Create/Patch/Complete)                       ║
║  TusUpload          Resumable upload state row                                         ║
║  Piece              File piece row (sha256+dossier_id composite PK)                    ║
║  Dossier            Dossier row (id, owner, routes)                                    ║
║  DossierRoute       Per-dossier webhook target (url, auth_mode, secret)                ║
║  RoutePending       Pending webhook delivery row                                       ║
║  IngestResult       Pipeline output (sha256, state, scan, injection, markdown)         ║
║  UploadResult       Receive result (sha256, size, chunk_count, dedup flag)              ║
║  ScanResult         Security scan output (clamav status, warnings[], blocked flag)      ║
║  InjectionResult    Injection scan output (risk level, match IDs)                      ║
║  FileMetadata       Structural metadata (MIME, entropy, magic, trailer)                 ║
║  TrailerInfo        PDF/ZIP trailer analysis (%%EOF, startxref, EOCD)                  ║
║  JWTClaims          Decoded JWT (sub, dossier_id, exp, iat)                            ║
║  OpaquePayload      Webhook body for opaque_only (no identity)                         ║
║  PassthruPayload    Webhook body for jwt_passthru (with JWT)                           ║
║  PieceMarkdown      Markdown storage row                                               ║
║  BufferWriter       Writes .md to HORAG buffer dir (nil-safe)                          ║
║  MarkdownConverter  Callback: (ctx, filePath, mime) -> markdown (set by binary)        ║
║  KeyResolver        Callback: (ctx, horoskey) -> ownerSub (set by binary)             ║
║  ShardCatalog       Interface: EnsureShard(ctx, dossierID, ownerID, name)              ║
║  IngesterOption     Functional option: func(*Ingester)                                 ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS (simplified signatures)                                                ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  NewIngester(cfg, ...IngesterOption) (*Ingester, error)                                ║
║  Ingester.Ingest(io.Reader, dossierID, ownerSub) (*IngestResult, error)               ║
║  Ingester.IngestWithToken(io.Reader, dossierID, ownerSub, token) (*IngestResult, err) ║
║  Ingester.IngestFromUpload(*UploadResult, dossierID, ownerSub) (*IngestResult, err)   ║
║  Ingester.RecoverStalePieces()                                                        ║
║  Ingester.Close() error                                                               ║
║  ReceiveFile(io.Reader, dossierID, *Config, *Store) (*UploadResult, error)            ║
║  ExtractMetadata(filePath) (*FileMetadata, error)                                     ║
║  ExtractFullMetadata(chunkDir, chunkCount) (*FileMetadata, error)                     ║
║  ScanFile(filePath, *Config) (*ScanResult, error)                                     ║
║  ScanChunks(chunkDir, chunkCount, *Config) (*ScanResult, error)                       ║
║  ScanInjection(text) *InjectionResult                                                 ║
║  ScanChunksInjection(chunksDir, chunkCount) *InjectionResult                          ║
║  ParseJWT(tokenStr, secret) (*JWTClaims, error)                                       ║
║  RegisterMCP(*mcp.Server, *Ingester)                                                  ║
║  RegisterConnectivity(*connectivity.Router, *Ingester)                                ║
║  NewTusHandler(*Store, *Config, newID) *TusHandler                                    ║
║  TusHandler.Create(dossierID, ownerSub, totalSize) (*TusUpload, error)                ║
║  TusHandler.Patch(uploadID, clientOffset, io.Reader) (int64, error)                   ║
║  TusHandler.Complete(uploadID) (*UploadResult, error)                                 ║
║  NewRouter(*Store, *Config) *Router                                                   ║
║  Router.EnqueueRoutesWithToken(*Piece, token) error                                   ║
║  Router.Deliver(*RoutePending, *Piece) bool                                           ║
║  Router.ProcessRetries()                                                              ║
║  OpenStore(path) (*Store, error)                                                      ║
║  LoadConfig(path) (*Config, error)                                                    ║
║  NewBufferWriter(bufferDir) *BufferWriter                                             ║
║  BufferWriter.Write(ctx, dossierID, sha256, title, markdown) error                    ║
║  WithMarkdownConverter(MarkdownConverter) IngesterOption                               ║
║  WithKeyResolver(KeyResolver) IngesterOption                                          ║
║  WithBufferWriter(*BufferWriter) IngesterOption                                       ║
║  WithShardCatalog(ShardCatalog) IngesterOption                                        ║
║  WithAudit(*observability.AuditLogger) IngesterOption                                 ║
║  WithMetrics(*observability.MetricsManager) IngesterOption                             ║
║  WithEvents(*observability.EventLogger) IngesterOption                                ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (hazyhaar/pkg/*)                                                        ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  sas_chunker    SplitReader, Assemble (chunking + manifest)                           ║
║  horosafe       ValidateIdentifier (path traversal guard on dossierID)                ║
║  injection      Scan, StripInvisible, HasHomoglyphMixing (prompt injection)           ║
║  trace          "sqlite-trace" driver (import side-effect)                            ║
║  idgen          Prefixed UUID v7 generator                                            ║
║  observability  AuditLogger, MetricsManager, EventLogger                              ║
║  connectivity   Router, Handler (service mesh)                                        ║
║  kit            RegisterMCPTool, MCPDecodeResult, TraceID context                     ║
║                                                                                       ║
║  External: gopkg.in/yaml.v3, modelcontextprotocol/go-sdk/mcp                         ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  CONFIG (YAML)                                                                        ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  listen:       ":8081"                                                                ║
║  db_path:      "sas_ingester.db"                                                      ║
║  chunks_dir:   "chunks"                                                               ║
║  max_file_mb:  500                                                                    ║
║  chunk_size_mb: 10                                                                    ║
║  buffer_dir:   "" (empty=disabled, path=HORAG buffer)                                 ║
║  jwt_secret:   "<HS256 secret>"                                                       ║
║  clamav:                                                                              ║
║    enabled:     false                                                                 ║
║    socket_path: "/var/run/clamav/clamd.ctl"                                           ║
║  webhooks:                                                                            ║
║    - name: "downstream"                                                               ║
║      url: "https://..."                                                               ║
║      auth_mode: "opaque_only" | "jwt_passthru"                                        ║
║      secret: "<HMAC key>"                                                             ║
║      require_review: false                                                            ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  PIECE STATE MACHINE                                                                  ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  received ──> scanned ──> ready ──> (delivered via routes)                             ║
║     │                       │                                                         ║
║     │                       └──> flagged (injection risk = high)                      ║
║     │                                                                                 ║
║     └──> blocked (ClamAV virus / zip bomb / polyglot)                                 ║
║                                                                                       ║
║  deduplicated (exit immediately, no state change)                                     ║
║                                                                                       ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║  FILE LAYOUT ON DISK                                                                  ║
╠═══════════════════════════════════════════════════════════════════════════════════════╣
║                                                                                       ║
║  {chunks_dir}/                                                                        ║
║    {dossier_id}/                                                                      ║
║      {sha256}/                                                                        ║
║        chunk_00000.bin                                                                ║
║        chunk_00001.bin                                                                ║
║        ...                                                                            ║
║        manifest.json         (from sas_chunker)                                       ║
║        assembled.{ext}       (temp, deleted after markdown conversion)                ║
║      _tus_{uploadID}/        (staging, deleted after Complete)                         ║
║        partial.bin                                                                    ║
║      incoming-{random}/      (temp, renamed to {sha256}/ on success)                  ║
║                                                                                       ║
║  {buffer_dir}/               (HORAG integration)                                      ║
║    {sha256}.md               (YAML frontmatter + markdown body)                       ║
║    {sha256}.md.tmp           (atomic write staging)                                   ║
║                                                                                       ║
╚═══════════════════════════════════════════════════════════════════════════════════════╝
```
