
# horosafe -- Technical Schema
# Security primitives: secret validation, SSRF prevention, path traversal, bounded I/O

```
╔══════════════════════════════════════════════════════════════════════════════════╗
║  horosafe — Shared security primitives for the HOROS ecosystem                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  Pure stdlib package — zero internal dependencies — leaf node                  ║
║                                                                                ║
║  ┌──────────────────────────────────────────────────────────────────────────┐  ║
║  │                       5 Security Functions                              │  ║
║  ├──────────────────────────────────────────────────────────────────────────┤  ║
║  │                                                                          │  ║
║  │  1. ValidateSecret(secret []byte) error                                  │  ║
║  │     ┌──────────┐                                                         │  ║
║  │     │ []byte   │──→ len >= 32 ? ──→ nil                                 │  ║
║  │     └──────────┘        │ no                                             │  ║
║  │                         └──→ ErrSecretTooShort                           │  ║
║  │                                                                          │  ║
║  │  2. ValidateURL(rawURL string) error                                     │  ║
║  │     ┌──────────┐    ┌────────────┐    ┌──────────┐    ┌──────────────┐  │  ║
║  │     │ raw URL  │──→ │ Parse URL  │──→ │ Check    │──→ │ DNS Resolve  │  │  ║
║  │     └──────────┘    │ scheme     │    │ literal  │    │ all addrs    │  │  ║
║  │                     │ http/https │    │ IP       │    │ check each   │  │  ║
║  │                     └────────────┘    └──────────┘    └──────────────┘  │  ║
║  │                      fail→ErrUnsafe    fail→ErrSSRF   fail→ErrSSRF     │  ║
║  │                      Scheme                                             │  ║
║  │                                                                          │  ║
║  │  3. SafePath(base, userInput string) (string, error)                     │  ║
║  │     ┌──────────┐    ┌────────────┐    ┌──────────────┐                  │  ║
║  │     │ base +   │──→ │ Reject ..  │──→ │ filepath.Join│──→ cleaned path │  ║
║  │     │ userInput│    │ in input   │    │ + HasPrefix  │   or             │  ║
║  │     └──────────┘    └────────────┘    │ check        │   ErrPathTraversal│ ║
║  │                                       └──────────────┘                  │  ║
║  │                                                                          │  ║
║  │  4. ValidateIdentifier(s string) error                                   │  ║
║  │     ┌──────────┐    ┌────────────┐    ┌──────────────┐                  │  ║
║  │     │ string   │──→ │ non-empty  │──→ │ each rune in │──→ nil           │  ║
║  │     └──────────┘    │ len <= 256 │    │ [a-zA-Z0-9_-.│                  │  ║
║  │                     └────────────┘    └──────────────┘                  │  ║
║  │                                                                          │  ║
║  │  5. LimitedReadAll(r io.Reader, maxBytes int64) ([]byte, error)          │  ║
║  │     ┌──────────┐    ┌─────────────┐    ┌────────────┐                   │  ║
║  │     │io.Reader │──→ │LimitReader  │──→ │ len > max? │──→ data or error │  ║
║  │     └──────────┘    │(max+1 bytes)│    └────────────┘                   │  ║
║  │                     └─────────────┘                                      │  ║
║  └──────────────────────────────────────────────────────────────────────────┘  ║
╚══════════════════════════════════════════════════════════════════════════════════╝
```

## Constants & Sentinel Errors

```
┌────────────────────────────────────────────────────────────────────────────┐
│  Constants                                                                 │
├────────────────────────────────────────────────────────────────────────────┤
│  MinSecretLen     = 32          (256 bits of entropy)                      │
│  MaxResponseBody  = 1 << 20    (1 MiB default cap for HTTP reads)         │
├────────────────────────────────────────────────────────────────────────────┤
│  Sentinel Errors                                                           │
├────────────────────────────────────────────────────────────────────────────┤
│  ErrSecretTooShort  — secret < 32 bytes                                   │
│  ErrPathTraversal   — path escapes base directory                         │
│  ErrSSRF            — URL targets private/loopback address                │
│  ErrUnsafeScheme    — URL scheme is not http or https                     │
└────────────────────────────────────────────────────────────────────────────┘
```

## SSRF Protection — Private IP Ranges Checked

```
┌────────────────────────────────────────────────────────────────────────────┐
│  Range              │ Type                                                 │
├─────────────────────┼────────────────────────────────────────────────────── │
│  127.0.0.0/8        │ Loopback (ip.IsLoopback)                            │
│  ::1/128            │ IPv6 Loopback                                       │
│  link-local unicast │ ip.IsLinkLocalUnicast                               │
│  link-local mcast   │ ip.IsLinkLocalMulticast                             │
│  10.0.0.0/8         │ RFC 1918 Class A                                    │
│  172.16.0.0/12      │ RFC 1918 Class B                                    │
│  192.168.0.0/16     │ RFC 1918 Class C                                    │
│  169.254.0.0/16     │ Link-local                                          │
│  fc00::/7           │ RFC 4193 Unique Local                               │
├─────────────────────┴──────────────────────────────────────────────────────┤
│  DNS resolution performed: all resolved IPs checked (anti-rebinding)      │
│  DNS failure: URL allowed through (network error at connect time)         │
└────────────────────────────────────────────────────────────────────────────┘
```

## ValidateURL Decision Tree

```
  rawURL
    │
    ▼
  url.Parse()
    │ fail → error
    ▼
  scheme == http|https ?
    │ no → ErrUnsafeScheme
    ▼
  hostname non-empty ?
    │ no → error "no host"
    ▼
  hostname is literal IP ?
    │ yes → isPrivateIP(ip) ? → ErrSSRF
    │                   no  → nil (OK)
    ▼
  net.LookupHost(hostname)
    │ fail → nil (allow, network error later)
    ▼
  any resolved IP private ?
    │ yes → ErrSSRF
    │ no  → nil (OK)
```

## SafePath Double Defense

```
  userInput
    │
    ├── contains ".." ? ──→ ErrPathTraversal (fast reject)
    │
    ▼
  filepath.Join(base, filepath.Clean("/"+userInput))
    │
    ├── HasPrefix(cleaned, base+sep) ? ── no ──→ ErrPathTraversal
    │
    ▼
  return cleaned (safe absolute path)
```

## ValidateIdentifier Allowed Characters

```
  Allowed: a-z  A-Z  0-9  _  -  .
  Max length: 256
  Must be non-empty
  Use case: SQL identifiers, filenames, URL path segments
```

## Dependencies

```
External: (none — stdlib only)
  net, net/url   — URL parsing, DNS resolution, IP classification
  path/filepath  — path joining and cleaning
  io             — LimitReader, ReadAll
  errors, fmt    — error wrapping

Internal (hazyhaar/pkg): (none — leaf package)
```

## Database Tables

```
  (none — stateless utility package)
```

## Key Function Signatures

```go
func ValidateSecret(secret []byte) error
func SafePath(base, userInput string) (string, error)
func ValidateURL(rawURL string) error
func ValidateIdentifier(s string) error
func LimitedReadAll(r io.Reader, maxBytes int64) ([]byte, error)
```

## Consumers (who imports horosafe)

```
  auth           — JWT secret validation, OAuth URL checks
  connectivity   — factory_http URL validation
  sas_ingester   — upload path safety, TUS validation
  channels       — webhook URL validation
```
