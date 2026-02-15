package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/hazyhaar/pkg/sas_chunker"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "split":
		cmdSplit(os.Args[2:])
	case "assemble":
		cmdAssemble(os.Args[2:])
	case "verify":
		cmdVerify(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `sas_chunker — split and reassemble large files

usage:
  sas_chunker split    <file> [output_dir] [chunk_size_mb]
  sas_chunker assemble <chunks_dir> [output_file]
  sas_chunker verify   <chunks_dir>

split     Splits <file> into chunks (default 10 MiB each).
assemble  Reassembles chunks into the original file.
verify    Checks every chunk hash without assembling.
`)
}

func progress(index, total int, bytes int64) {
	fmt.Fprintf(os.Stderr, "\r  chunk %d/%d  (%s)", index+1, total, sas_chunker.FormatBytes(bytes))
}

func cmdSplit(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "split requires a file path")
		os.Exit(1)
	}

	srcPath := args[0]

	outDir := srcPath + ".chunks"
	if len(args) >= 2 {
		outDir = args[1]
	}

	var chunkSize int64
	if len(args) >= 3 {
		mb, err := strconv.ParseInt(args[2], 10, 64)
		if err != nil || mb <= 0 {
			fmt.Fprintln(os.Stderr, "chunk_size_mb must be a positive integer")
			os.Exit(1)
		}
		chunkSize = mb * 1024 * 1024
	}

	manifest, err := sas_chunker.Split(srcPath, outDir, chunkSize, progress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nsplit failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "done: %d chunks in %s\n", manifest.TotalChunks, outDir)
	fmt.Fprintf(os.Stderr, "  sha256: %s\n", manifest.OriginalSHA256)
}

func cmdAssemble(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "assemble requires a chunks directory")
		os.Exit(1)
	}

	chunksDir := args[0]

	manifest, err := sas_chunker.LoadManifest(chunksDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}

	outPath := manifest.OriginalName
	if len(args) >= 2 {
		outPath = args[1]
	}

	if err := sas_chunker.Assemble(chunksDir, outPath, progress); err != nil {
		fmt.Fprintf(os.Stderr, "\nassemble failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "done: %s (%s)\n", outPath, sas_chunker.FormatBytes(manifest.OriginalSize))
}

func cmdVerify(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "verify requires a chunks directory")
		os.Exit(1)
	}

	result, err := sas_chunker.Verify(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify failed: %v\n", err)
		os.Exit(1)
	}

	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "  %s\n", e)
	}

	if !result.OK() {
		fmt.Fprintf(os.Stderr, "verification FAILED: %d error(s)\n", len(result.Errors))
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "verification OK: %d chunks, %s total\n", result.TotalChunks, sas_chunker.FormatBytes(result.TotalSize))
}
