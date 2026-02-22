# horosafe — security primitives

`horosafe` provides reusable validation functions for secrets, URLs, paths, and
identifiers. Used by other packages (auth, connectivity, channels) as a shared
security baseline.

## Functions

### Secret validation

```go
err := horosafe.ValidateSecret(secret) // must be >= 32 bytes
```

### SSRF prevention

```go
err := horosafe.ValidateURL(rawURL)
```

Rejects private IPs (RFC 1918, loopback, link-local, RFC 4193), non-HTTP(S)
schemes, and performs DNS resolution to catch CNAMEs pointing to internal hosts.

### Path traversal guard

```go
safe, err := horosafe.SafePath("/data", userInput)
```

Returns an error if the resolved path escapes the base directory.

### Identifier validation

```go
err := horosafe.ValidateIdentifier(s) // alphanumeric + _-. only, max 256 chars
```

### Bounded I/O

```go
body, err := horosafe.LimitedReadAll(r, 1<<20) // max 1 MiB
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `MinSecretLen` | 32 bytes |
| `MaxResponseBody` | 1 MiB |
| `ValidateSecret(secret)` | Reject short secrets |
| `ValidateURL(rawURL)` | SSRF prevention with DNS check |
| `SafePath(base, input)` | Path traversal guard |
| `ValidateIdentifier(s)` | Reject unsafe identifiers |
| `LimitedReadAll(r, max)` | Bounded reader |
