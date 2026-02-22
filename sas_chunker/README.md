# sas_chunker — file splitting with SHA-256 verification

`sas_chunker` splits large files into chunks with a JSON manifest and SHA-256
hashes. Supports streaming via `io.Reader` and progress callbacks.

## Quick start

```go
// Split a file into 10 MiB chunks.
manifest, _ := sas_chunker.Split("large.bin", "/tmp/chunks", 10*1024*1024, nil)

// Verify integrity without reassembling.
result, _ := sas_chunker.Verify("/tmp/chunks")

// Reassemble.
err := sas_chunker.Assemble("/tmp/chunks", "output.bin", func(i, total int, bytes int64) {
    fmt.Printf("chunk %d/%d\n", i+1, total)
})
```

## Manifest format

```json
{
  "original_name": "large.bin",
  "original_size": 52428800,
  "original_sha256": "abc123...",
  "chunk_size": 10485760,
  "total_chunks": 5,
  "chunks": [
    {"index": 0, "file_name": "chunk_00000.bin", "offset_bytes": 0, "size_bytes": 10485760, "sha256": "..."}
  ]
}
```

## Streaming

```go
manifest, _ := sas_chunker.SplitReader(reader, "upload.bin", outDir, chunkSize, progressFn)
```

`SplitReader` consumes an `io.Reader` without buffering the entire file.

## Exported API

| Symbol | Description |
|--------|-------------|
| `Split(path, outDir, chunkSize, progress)` | Split file into chunks |
| `SplitReader(r, name, outDir, chunkSize, progress)` | Streaming split |
| `Assemble(chunkDir, outPath, progress)` | Reassemble with verification |
| `Verify(chunkDir)` | Check all hashes |
| `LoadManifest(dir)` | Parse manifest.json |
| `Manifest`, `ChunkMeta` | Data types |
| `DefaultChunkSize` | 10 MiB |
| `FormatBytes(n)` | Human-readable size |
