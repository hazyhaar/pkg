
# horos -- Technical Schema
# HOROS type system: typed contracts, wire envelope, structured errors, format registry

```
╔══════════════════════════════════════════════════════════════════════════════════╗
║  horos — Type system for inter-service communication                          ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  Leaf package — zero internal dependencies — foundation layer                  ║
║                                                                                ║
║  4 Subsystems:                                                                 ║
║    1. Codec[T] + Contract[Req,Resp] — typed service calls                     ║
║    2. Envelope (Wrap/Unwrap) — wire format with CRC-32C                       ║
║    3. ServiceError — structured errors that travel on the wire                ║
║    4. Registry — format ID ↔ name mapping + SQLite persistence                ║
║                                                                                ║
╚══════════════════════════════════════════════════════════════════════════════════╝
```

## 1. Codec & Contract — Typed Service Calls

```
  ┌────────────────────────────────────────────────────────────────────────────────┐
  │                                                                                │
  │  Codec[T] interface {              F-bounded polymorphism (Go 1.18+)          │
  │      Encode() ([]byte, error)      Self-codec: T serializes itself            │
  │      Decode([]byte) (T, error)     Returns concrete T, not interface          │
  │  }                                                                             │
  │                                                                                │
  │  Encoder interface {               Write-only side of Codec                   │
  │      Encode() ([]byte, error)                                                  │
  │  }                                                                             │
  │                                                                                │
  │  Contract[Req Codec[Req], Resp Codec[Resp]] {                                 │
  │      Service  string    — routing name for connectivity.Router                │
  │      FormatID uint16    — wire format (0=default uses registry)               │
  │  }                                                                             │
  │                                                                                │
  │  CLIENT SIDE (Call):                                                           │
  │                                                                                │
  │  Req ──→ Req.Encode() ──→ Wrap(formatID, payload) ──→ caller(svc, envelope)   │
  │                                                           │                    │
  │                                                           ▼                    │
  │  Resp ←── Resp.Decode() ←── Unwrap(raw) ←──────── response bytes              │
  │              │                    │                                             │
  │              │            DetectError(payload) ──→ *ServiceError if __error    │
  │              ▼                                                                 │
  │          typed Resp                                                            │
  │                                                                                │
  │  SERVER SIDE (Handler):                                                        │
  │                                                                                │
  │  payload ──→ Unwrap ──→ Req.Decode() ──→ fn(ctx, req) ──→ Resp.Encode()       │
  │                                              │                                 │
  │                                         error? → ToServiceError → Wrap         │
  │                                         ok?    → Resp.Encode → Wrap            │
  └────────────────────────────────────────────────────────────────────────────────┘
```

## 2. Wire Envelope Format

```
  ┌──────────────────────────────────────────────────────────────┐
  │  Wire Envelope (6 bytes overhead)                            │
  │                                                              │
  │  Byte offset:  0    1    2    3    4    5    6 ... 6+N       │
  │               ├────┼────┼────┼────┼────┼────┼─────────┤     │
  │               │ format_id │   CRC-32C       │ payload  │     │
  │               │ uint16 LE │   Castagnoli    │ N bytes  │     │
  │               │ (2 bytes) │   uint32 LE     │          │     │
  │               ├───────────┼─────────────────┼──────────┤     │
  │                                                              │
  │  Format IDs:                                                 │
  │    0 = FormatRaw     — passthrough, no codec                 │
  │    1 = FormatJSON    — JSON (canonical, human-readable)      │
  │    2 = FormatMsgp    — MessagePack (Go-to-Go optimized)      │
  │                                                              │
  │  Wrap(formatID, payload) → envelope bytes                    │
  │  Unwrap(data) → (formatID, payload, error)                   │
  │                                                              │
  │  Edge cases:                                                 │
  │    data < 6 bytes     → FormatRaw, full data, nil error      │
  │    format_id=0 +      → FormatRaw, full original data        │
  │     CRC mismatch        (non-enveloped backward compat)      │
  │    CRC mismatch       → ErrChecksum{Expected, Actual,        │
  │     (format_id != 0)     FormatID}                           │
  │                                                              │
  │  CRC-32C (Castagnoli) — hardware-accelerated on modern CPUs  │
  │  (SSE 4.2 / ARM CRC32)                                      │
  └──────────────────────────────────────────────────────────────┘
```

## 3. ServiceError — Structured Wire Errors

```
  ┌─────────────────────────────────────────────────────────────────────────────┐
  │  ServiceError {                                                             │
  │      Code    string           — machine-readable: "NOT_FOUND", "INTERNAL"  │
  │      Message string           — human-readable description                 │
  │      Details json.RawMessage  — optional structured data (retry-after etc) │
  │      Service string           — originating service name                   │
  │  }                                                                          │
  │                                                                             │
  │  Implements: error, Codec[ServiceError], errors.Is (matches by Code)       │
  │                                                                             │
  │  Wire format (JSON):                                                        │
  │    {"__error": {"code":"NOT_FOUND","message":"...","service":"svc"}}        │
  │     ^^^^^^^^                                                                │
  │     sentinel key for DetectError()                                          │
  │                                                                             │
  │  DetectError(payload) → (*ServiceError, bool)                              │
  │    Quick check: len < 12 → false                                           │
  │    Unmarshal, check Code non-empty → true                                  │
  │                                                                             │
  │  ToServiceError(err) → *ServiceError                                       │
  │    Already *ServiceError? → return as-is                                   │
  │    Plain error? → ServiceError{Code:"INTERNAL", Message: err.Error()}      │
  │                                                                             │
  │  Pre-built sentinel errors (for errors.Is matching):                       │
  │    ErrNotFound    — Code: "NOT_FOUND"                                      │
  │    ErrBadRequest  — Code: "BAD_REQUEST"                                    │
  │    ErrInternal    — Code: "INTERNAL"                                       │
  │    ErrRateLimited — Code: "RATE_LIMITED"                                   │
  │    ErrForbidden   — Code: "FORBIDDEN"                                      │
  │    ErrConflict    — Code: "CONFLICT"                                       │
  │                                                                             │
  │  Builder methods:                                                           │
  │    NewServiceError(code, message) → *ServiceError                          │
  │    .WithDetails(v any)            → (*ServiceError, error)                 │
  │    .WithService(service)          → *ServiceError                          │
  │                                                                             │
  │  Codec errors (non-ServiceError, wrapping):                                │
  │    ErrEncode{Service, Cause}  — encode failure                             │
  │    ErrDecode{Service, Cause}  — decode failure                             │
  └─────────────────────────────────────────────────────────────────────────────┘
```

## 4. Registry — Format ID Management

```
  ┌─────────────────────────────────────────────────────────────────────────────┐
  │  Registry {                                                                 │
  │      mu      sync.RWMutex          — concurrent-safe                       │
  │      formats map[uint16]FormatInfo                                         │
  │  }                                                                          │
  │                                                                             │
  │  FormatInfo {                                                               │
  │      ID   uint16     — wire format identifier                              │
  │      Name string     — "json", "msgpack", "protobuf"                       │
  │      MIME string     — "application/json", etc.                            │
  │  }                                                                          │
  │                                                                             │
  │  NewRegistry() → pre-seeded with:                                          │
  │    0 → {raw, application/octet-stream}                                     │
  │    1 → {json, application/json}                                            │
  │    2 → {msgpack, application/msgpack}                                      │
  │                                                                             │
  │  Register(info)      — error if ID collision with different name            │
  │  Lookup(id)          → (FormatInfo, bool)                                  │
  │  All()               → []FormatInfo snapshot                               │
  │                                                                             │
  │  SQLite persistence:                                                        │
  │  InitDB(db)   — CREATE TABLE + seed built-in formats                       │
  │  SyncToDB(db)  — upsert all registered formats                             │
  └─────────────────────────────────────────────────────────────────────────────┘
```

## Database Table

```
  ┌────────────────────────────────────────────────────────────────────────────┐
  │  horos_formats                                                             │
  ├──────────┬──────────────────┬───────────────────────────────────────────── ┤
  │  Column  │ Type             │ Notes                                       │
  ├──────────┼──────────────────┼─────────────────────────────────────────────┤
  │  id      │ INTEGER PK       │ Format ID (0, 1, 2, ...)                   │
  │  name    │ TEXT NOT NULL UQ  │ Human name ("json", "msgpack")             │
  │  mime    │ TEXT NOT NULL     │ Default: 'application/octet-stream'        │
  ├──────────┴──────────────────┴─────────────────────────────────────────────┤
  │  Purpose: observability + admin UI (Go Registry is source of truth)       │
  └────────────────────────────────────────────────────────────────────────────┘
```

## Full Call Flow (Client → Server)

```
  Client                                                      Server
  ──────                                                      ──────

  contract.Call(ctx, router.Call, req)
    │
    ├── req.Encode()           → payload bytes
    ├── Wrap(formatID, payload)→ [2B fmt | 4B CRC | payload]
    ├── caller(ctx, svc, envelope)
    │         │
    │         ▼
    │   ┌─────────────────────┐
    │   │ connectivity.Router │ ──→ network/local dispatch
    │   └─────────────────────┘
    │                                    │
    │                              contract.Handler(fn)
    │                                    │
    │                              ├── Unwrap(payload) → (fmtID, reqPayload)
    │                              ├── req.Decode(reqPayload) → typed Req
    │                              ├── fn(ctx, req) → (Resp, error)
    │                              │     │
    │                              │     ├── error? → ToServiceError → Encode
    │                              │     │            → Wrap → response
    │                              │     │
    │                              │     └── ok? → resp.Encode → Wrap → response
    │                              │
    │                         ←────┘ response envelope
    │
    ├── Unwrap(raw)            → (fmtID, respPayload)
    ├── DetectError(respPayload) → ServiceError? abort
    └── resp.Decode(respPayload) → typed Resp
```

## Dependencies

```
External: (none — stdlib only)
  encoding/binary  — wire envelope LE encoding
  encoding/json    — ServiceError codec
  hash/crc32       — CRC-32C Castagnoli
  database/sql     — Registry SQLite persistence
  sync             — RWMutex for Registry

Internal (hazyhaar/pkg): (none — leaf package)
```

## Key Function Signatures

```go
// Codec system
func NewContract[Req Codec[Req], Resp Codec[Resp]](service string) Contract[Req, Resp]
func (c Contract[Req,Resp]) Call(ctx, caller, req) (Resp, error)
func (c Contract[Req,Resp]) Handler(fn) func(ctx, []byte) ([]byte, error)
func (c Contract[Req,Resp]) WithFormat(id uint16) Contract[Req, Resp]

// Envelope
func Wrap(formatID uint16, payload []byte) ([]byte, error)
func Unwrap(data []byte) (formatID uint16, payload []byte, err error)
func IsChecksumError(err error) bool

// Errors
func NewServiceError(code, message string) *ServiceError
func ToServiceError(err error) *ServiceError
func DetectError(payload []byte) (*ServiceError, bool)
func (e *ServiceError) WithDetails(v any) (*ServiceError, error)
func (e *ServiceError) WithService(service string) *ServiceError

// Registry
func NewRegistry() *Registry
func (r *Registry) Register(info FormatInfo) error
func (r *Registry) Lookup(id uint16) (FormatInfo, bool)
func (r *Registry) All() []FormatInfo
func (r *Registry) InitDB(db *sql.DB) error
func (r *Registry) SyncToDB(db *sql.DB) error
```
