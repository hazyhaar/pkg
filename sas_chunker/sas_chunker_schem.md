╔═══════════════════════════════════════════════════════════════════════════════╗
║  sas_chunker — File splitting/reassembly with SHA-256 integrity manifests    ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Pure standard library. No DB. No external dependencies.                    ║
║  Splits large files into fixed-size chunks with per-chunk and global SHA-256.║
║  Produces a manifest.json for reassembly and integrity verification.        ║
║                                                                              ║
║  SPLIT FLOW (file path)                                                      ║
║  ~~~~~~~~~~~~~~~~~~~~~~                                                      ║
║                                                                              ║
║  ┌──────────────┐   os.Open    ┌───────────┐   ReadFull    ┌──────────┐     ║
║  │ Source file   │ ──────────> │ io.Reader │ ───────────> │ Chunks   │     ║
║  │ (seekable)    │  hash full  │           │  chunkSize   │          │     ║
║  │               │  file first │           │  (def 10MiB) │ chunk_   │     ║
║  └──────────────┘  then seek 0 └───────────┘              │ 00000.bin│     ║
║                                                           │ chunk_   │     ║
║  Split(srcPath, outDir, chunkSize, progress)              │ 00001.bin│     ║
║    |                                                      │ ...      │     ║
║    |-> Whole-file SHA-256 (pre-computed via full read)     └─────┬────┘     ║
║    |-> Per-chunk SHA-256 (computed on write)                     │          ║
║    |-> manifest.json                                            v          ║
║                                                           ┌──────────┐     ║
║  SPLIT FLOW (streaming)                                   │manifest. │     ║
║  ~~~~~~~~~~~~~~~~~~~~~~~                                  │json      │     ║
║                                                           └──────────┘     ║
║  ┌──────────────┐  TeeReader   ┌───────────┐  ReadFull   ┌──────────┐     ║
║  │ io.Reader    │ ──────────> │ fileHasher│ ──────────> │ Chunks   │     ║
║  │ (non-seekble)│  sha256     │ (global)  │  per-chunk  │ + manif. │     ║
║  │ e.g. HTTP    │  computed   └───────────┘  sha256     └──────────┘     ║
║  └──────────────┘  inline                                                  ║
║                                                                              ║
║  SplitReader(r, origName, outDir, chunkSize, progress)                      ║
║    |-> Global SHA-256 computed via TeeReader (no temp file)                 ║
║    |-> Total unknown during streaming; updated in final progress callback   ║
║                                                                              ║
║  ASSEMBLE FLOW                                                               ║
║  ~~~~~~~~~~~~~                                                               ║
║                                                                              ║
║  ┌──────────┐  LoadManifest  ┌──────────┐  read+verify  ┌──────────────┐   ║
║  │ chunks/  │ ─────────────> │ manifest │ ─────────────> │ Reassembled  │   ║
║  │ dir      │                │ .json    │  per-chunk     │ output file  │   ║
║  │          │                │          │  SHA-256       │              │   ║
║  │ chunk_*  │                │ sorted   │                │ final SHA-256│   ║
║  │ manifest │                │ by index │                │ verified vs  │   ║
║  └──────────┘                └──────────┘                │ manifest     │   ║
║                                                          └──────────────┘   ║
║  Assemble(chunksDir, outPath, progress)                                     ║
║    |-> validateChunkNames: reject path traversal (../, absolute paths)      ║
║    |-> Sort chunks by index                                                  ║
║    |-> Verify each chunk SHA-256 before writing                             ║
║    |-> Verify final assembled file SHA-256 vs manifest                      ║
║    |-> On hash mismatch: delete output file, return error                   ║
║                                                                              ║
║  VERIFY FLOW (no reassembly)                                                 ║
║  ~~~~~~~~~~~~~~~~~~~~~~~~~~~                                                 ║
║                                                                              ║
║  ┌──────────┐  LoadManifest  ┌──────────┐  check each  ┌──────────────┐    ║
║  │ chunks/  │ ─────────────> │ manifest │ ───────────> │ VerifyResult │    ║
║  │ dir      │                │ .json    │  - exists?   │ .TotalChunks │    ║
║  └──────────┘                └──────────┘  - SHA-256?  │ .TotalSize   │    ║
║                                            - size?     │ .Errors []   │    ║
║  Verify(chunksDir)                         - total?    │ .OK() bool   │    ║
║    |-> MISSING: chunk file not found                   └──────────────┘    ║
║    |-> CORRUPT: SHA-256 mismatch                                           ║
║    |-> BADSIZE: byte count mismatch                                        ║
║    |-> SIZE MISMATCH: sum != original_size                                 ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  MANIFEST FORMAT (manifest.json)                                             ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  {                                                                           ║
║    "original_name":   "document.pdf",                                       ║
║    "original_size":   104857600,           // bytes                         ║
║    "original_sha256": "abc123...",         // hex-encoded                   ║
║    "chunk_size":      10485760,            // 10 MiB                        ║
║    "total_chunks":    10,                                                   ║
║    "created_at":      "2026-03-01T...",    // RFC3339 UTC                   ║
║    "chunks": [                                                              ║
║      {                                                                       ║
║        "index":        0,                                                   ║
║        "file_name":    "chunk_00000.bin",                                   ║
║        "offset_bytes": 0,                                                   ║
║        "size_bytes":   10485760,                                            ║
║        "sha256":       "def456..."                                          ║
║      },                                                                      ║
║      ...                                                                     ║
║    ]                                                                         ║
║  }                                                                           ║
║                                                                              ║
║  Chunk naming: chunk_NNNNN.bin (5-digit zero-padded index)                  ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Manifest       struct  Complete description of split: orig file + chunks    ║
║  ├── OriginalName   string                                                   ║
║  ├── OriginalSize   int64                                                    ║
║  ├── OriginalSHA256 string                                                   ║
║  ├── ChunkSize      int64                                                    ║
║  ├── TotalChunks    int                                                      ║
║  ├── Chunks         []ChunkMeta                                              ║
║  └── CreatedAt      string (RFC3339)                                         ║
║                                                                              ║
║  ChunkMeta      struct  Single chunk descriptor                              ║
║  ├── Index       int                                                         ║
║  ├── FileName    string     (e.g. "chunk_00000.bin")                        ║
║  ├── OffsetBytes int64                                                       ║
║  ├── SizeBytes   int64                                                       ║
║  └── SHA256      string     (hex-encoded)                                   ║
║                                                                              ║
║  VerifyResult   struct  Integrity check outcome                              ║
║  ├── TotalChunks int                                                         ║
║  ├── TotalSize   int64                                                       ║
║  ├── Errors      []string                                                    ║
║  └── OK()        bool       (true if len(Errors) == 0)                      ║
║                                                                              ║
║  ProgressFunc   func(index, total int, bytes int64)                          ║
║                 -- called after each chunk; total=0 for streaming split      ║
║                                                                              ║
║  Constants:                                                                  ║
║  DefaultChunkSize = 10 * 1024 * 1024  (10 MiB)                             ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                               ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Split(srcPath, outDir, chunkSize, progress) (*Manifest, error)              ║
║    -- file-based: reads whole file for hash, then seeks + splits             ║
║    -- chunkSize <= 0 defaults to 10 MiB                                      ║
║                                                                              ║
║  SplitReader(r, origName, outDir, chunkSize, progress) (*Manifest, error)    ║
║    -- stream-based: uses TeeReader for inline hash, no seek needed           ║
║    -- total unknown during streaming (progress total=0 until final call)     ║
║                                                                              ║
║  Assemble(chunksDir, outPath, progress) error                                ║
║    -- reads manifest.json, sorts by index, verifies each chunk + final hash  ║
║    -- on final hash mismatch: removes output file                            ║
║                                                                              ║
║  Verify(chunksDir) (*VerifyResult, error)                                    ║
║    -- checks all chunks without assembling                                   ║
║    -- returns detailed error list: MISSING, CORRUPT, BADSIZE, SIZE MISMATCH ║
║                                                                              ║
║  LoadManifest(dir) (*Manifest, error)                                        ║
║    -- parse manifest.json from dir                                           ║
║                                                                              ║
║  FormatBytes(b int64) string                                                 ║
║    -- human-readable: "1.5 MiB", "2.30 GiB", "512 B"                       ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                                ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Standard library ONLY:                                                      ║
║    crypto/sha256, encoding/hex, encoding/json, fmt, io, os,                 ║
║    path/filepath, sort, strings, time                                        ║
║                                                                              ║
║  No internal hazyhaar/pkg dependencies. Fully self-contained.               ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  SECURITY                                                                    ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  validateChunkNames(chunksDir, manifest):                                    ║
║    - Rejects ".." in any chunk filename                                      ║
║    - Rejects absolute paths in chunk filenames                               ║
║    - Resolves absolute path and verifies it stays within chunksDir           ║
║    - Called before Assemble and Verify                                        ║
║                                                                              ║
║  Integrity chain:                                                            ║
║    Per-chunk SHA-256 -> verify on assemble                                   ║
║    Global SHA-256    -> verify after full reassembly                         ║
║    Size check        -> verify in Verify()                                   ║
║                                                                              ║
╚═══════════════════════════════════════════════════════════════════════════════╝
