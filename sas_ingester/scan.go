package sas_ingester

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
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

// ScanFile runs all security checks on a file without loading it entirely
// into memory. Structural checks use an 8 KiB header + file size; ClamAV
// reads the file itself via its socket protocol.
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

func scanClamAV(filePath, socketPath string) (string, error) {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return "", fmt.Errorf("connect clamav: %w", err)
	}
	defer conn.Close()

	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}

	// Use SCAN command (file path scanning).
	cmd := fmt.Sprintf("SCAN %s\n", abs)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("send command: %w", err)
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	line := strings.TrimSpace(string(resp))
	if strings.HasSuffix(line, "OK") {
		return "OK", nil
	}
	return line, nil
}
