package sas_ingester

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// scanHeaderSize is the amount of data read for structural checks.
// 8 KiB is enough for magic bytes, polyglot detection, OLE2 + VBA markers,
// and an initial ZIP local-file-header count.
const scanHeaderSize = 8 * 1024

// ScanResult holds the outcome of security scanning a file.
type ScanResult struct {
	ClamAV   string   `json:"clamav"`
	Warnings []string `json:"warnings,omitempty"`
	Blocked  bool     `json:"blocked"`
}

// ScanChunks runs security checks across multiple chunk files. It scans the
// first chunk for magic bytes, polyglot, zip bomb, and macro detection. The
// last chunk is also scanned for polyglot and macro (trailing payloads).
// ClamAV is invoked on each chunk.
func ScanChunks(chunkDir string, chunkCount int, cfg *Config) (*ScanResult, error) {
	if chunkCount == 0 {
		return &ScanResult{ClamAV: "skipped"}, nil
	}

	result := &ScanResult{ClamAV: "skipped"}

	// Scan first chunk (magic bytes, zip bomb, polyglot, macro).
	firstChunk := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", 0))
	first, err := ScanFile(firstChunk, cfg)
	if err != nil {
		return nil, fmt.Errorf("scan first chunk: %w", err)
	}
	result.Warnings = append(result.Warnings, first.Warnings...)
	result.ClamAV = first.ClamAV
	if first.Blocked {
		result.Blocked = true
	}

	// Scan last chunk if different from first (trailing payloads).
	if chunkCount > 1 {
		lastChunk := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", chunkCount-1))
		last, err := ScanFile(lastChunk, cfg)
		if err != nil {
			return nil, fmt.Errorf("scan last chunk: %w", err)
		}
		result.Warnings = append(result.Warnings, last.Warnings...)
		if last.ClamAV != "OK" && last.ClamAV != "skipped" {
			result.ClamAV = last.ClamAV
		}
		if last.Blocked {
			result.Blocked = true
		}
	}

	// If ClamAV is enabled, scan all intermediate chunks too.
	if cfg.ClamAV.Enabled && chunkCount > 2 {
		for i := 1; i < chunkCount-1; i++ {
			path := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", i))
			status, err := scanClamAV(path, cfg.ClamAV.SocketPath)
			if err != nil {
				result.ClamAV = fmt.Sprintf("error: %v", err)
			} else if status != "OK" {
				result.ClamAV = status
				result.Blocked = true
			}
		}
	}

	return result, nil
}

// ScanFile runs all security checks on a single file without loading it
// entirely into memory. Structural checks use an 8 KiB header + file size;
// ClamAV reads the file itself via its socket protocol.
func ScanFile(filePath string, cfg *Config) (*ScanResult, error) {
	result := &ScanResult{ClamAV: "skipped"}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file for scan: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file for scan: %w", err)
	}
	fileSize := stat.Size()

	header := make([]byte, scanHeaderSize)
	n, _ := f.Read(header)
	header = header[:n]

	// Check 1: zip bomb heuristic using header PK count vs file size.
	if w := checkZipBomb(header, fileSize); w != "" {
		result.Warnings = append(result.Warnings, w)
		result.Blocked = true
	}

	// Check 2: polyglot detection (multiple magic numbers in header).
	if w := checkPolyglot(header); w != "" {
		result.Warnings = append(result.Warnings, w)
		result.Blocked = true
	}

	// Check 3: macro presence in Office-like files.
	if w := checkMacro(header, filePath); w != "" {
		result.Warnings = append(result.Warnings, w)
	}

	// Check 4: ClamAV if enabled (passes file path, no RAM needed).
	if cfg.ClamAV.Enabled {
		status, err := scanClamAV(filePath, cfg.ClamAV.SocketPath)
		if err != nil {
			result.ClamAV = fmt.Sprintf("error: %v", err)
		} else {
			result.ClamAV = status
			if status != "OK" {
				result.Blocked = true
			}
		}
	}

	return result, nil
}

func checkZipBomb(header []byte, fileSize int64) string {
	// Heuristic: many ZIP local-file-header signatures packed into a tiny
	// file strongly suggests a zip bomb. We check the header only — a
	// legitimate Office doc with 100+ images will be much larger than 1 MiB.
	sig := []byte("PK\x03\x04")
	count := bytes.Count(header, sig)
	if count > 10 && fileSize < 1024*1024 {
		return fmt.Sprintf("zip_bomb_suspect: %d zip headers in first %d bytes, file only %d bytes",
			count, len(header), fileSize)
	}
	return ""
}

func checkPolyglot(header []byte) string {
	if len(header) < 16 {
		return ""
	}

	var detected []string

	// PDF
	if bytes.Contains(header[:min(1024, len(header))], []byte("%PDF")) {
		detected = append(detected, "PDF")
	}
	// ZIP/Office
	if bytes.HasPrefix(header, []byte("PK\x03\x04")) {
		detected = append(detected, "ZIP")
	}
	// ELF
	if bytes.HasPrefix(header, []byte("\x7fELF")) {
		detected = append(detected, "ELF")
	}
	// PE/MZ
	if bytes.HasPrefix(header, []byte("MZ")) {
		detected = append(detected, "PE")
	}
	// JPEG
	if bytes.HasPrefix(header, []byte("\xff\xd8\xff")) {
		detected = append(detected, "JPEG")
	}

	if len(detected) > 1 {
		return fmt.Sprintf("polyglot_suspect: %s", strings.Join(detected, "+"))
	}
	return ""
}

func checkMacro(header []byte, filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Office Open XML with macros
	if ext == ".xlsm" || ext == ".docm" || ext == ".pptm" {
		return fmt.Sprintf("macro_extension: %s", ext)
	}

	// OLE2 compound document (legacy Office)
	if bytes.HasPrefix(header, []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")) {
		// Check for VBA signatures inside OLE header area.
		if bytes.Contains(header, []byte("_VBA_PROJECT")) || bytes.Contains(header, []byte("VBAProject")) {
			return "macro_detected: OLE2+VBA"
		}
	}

	return ""
}

// scanClamAV sends file content to clamd via the INSTREAM protocol.
// This works across container boundaries (no shared filesystem needed).
// Protocol: zINSTREAM\0 + [4-byte big-endian length + data]* + \0\0\0\0
func scanClamAV(filePath, socketPath string) (string, error) {
	conn, err := net.DialTimeout("unix", socketPath, 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("connect clamav: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(60 * time.Second))

	// Send INSTREAM command.
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "", fmt.Errorf("send instream cmd: %w", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// Stream file in 8 KiB chunks with 4-byte big-endian length prefix.
	buf := make([]byte, 8192)
	lenBuf := make([]byte, 4)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			lenBuf[0] = byte(n >> 24)
			lenBuf[1] = byte(n >> 16)
			lenBuf[2] = byte(n >> 8)
			lenBuf[3] = byte(n)
			if _, err := conn.Write(lenBuf); err != nil {
				return "", fmt.Errorf("send chunk length: %w", err)
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return "", fmt.Errorf("send chunk data: %w", err)
			}
		}
		if readErr != nil {
			break
		}
	}

	// Terminate stream with zero-length chunk.
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return "", fmt.Errorf("send terminator: %w", err)
	}

	// ClamAV responses are short (< 1 KiB). Cap at 4 KiB to prevent abuse.
	resp, err := io.ReadAll(io.LimitReader(conn, 4096))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	line := strings.TrimSpace(string(resp))
	// Response format: "stream: OK" or "stream: <virus> FOUND"
	if strings.HasSuffix(line, "OK") {
		return "OK", nil
	}
	return line, nil
}
