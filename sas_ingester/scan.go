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

// ScanResult holds the outcome of security scanning a file.
type ScanResult struct {
	ClamAV    string   `json:"clamav"`
	Warnings  []string `json:"warnings,omitempty"`
	Blocked   bool     `json:"blocked"`
}

// ScanFile runs all security checks on a file.
func ScanFile(filePath string, cfg *Config) (*ScanResult, error) {
	result := &ScanResult{ClamAV: "skipped"}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file for scan: %w", err)
	}

	// Check 1: zip bomb (compressed-to-decompressed ratio via nested zips, large repetition).
	if w := checkZipBomb(data); w != "" {
		result.Warnings = append(result.Warnings, w)
		result.Blocked = true
	}

	// Check 2: polyglot detection (multiple magic numbers).
	if w := checkPolyglot(data); w != "" {
		result.Warnings = append(result.Warnings, w)
		result.Blocked = true
	}

	// Check 3: macro presence in Office-like files.
	if w := checkMacro(data, filePath); w != "" {
		result.Warnings = append(result.Warnings, w)
	}

	// Check 4: ClamAV if enabled.
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

func checkZipBomb(data []byte) string {
	// Detect nested ZIP signatures: PK\x03\x04 appearing multiple times at suspicious offsets.
	sig := []byte("PK\x03\x04")
	count := bytes.Count(data, sig)
	if count > 100 && len(data) < 1024*1024 {
		return fmt.Sprintf("zip_bomb_suspect: %d zip entries in %d bytes", count, len(data))
	}
	return ""
}

func checkPolyglot(data []byte) string {
	if len(data) < 16 {
		return ""
	}

	var detected []string

	// PDF
	if bytes.Contains(data[:min(1024, len(data))], []byte("%PDF")) {
		detected = append(detected, "PDF")
	}
	// ZIP/Office
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		detected = append(detected, "ZIP")
	}
	// ELF
	if bytes.HasPrefix(data, []byte("\x7fELF")) {
		detected = append(detected, "ELF")
	}
	// PE/MZ
	if bytes.HasPrefix(data, []byte("MZ")) {
		detected = append(detected, "PE")
	}
	// JPEG
	if bytes.HasPrefix(data, []byte("\xff\xd8\xff")) {
		detected = append(detected, "JPEG")
	}

	if len(detected) > 1 {
		return fmt.Sprintf("polyglot_suspect: %s", strings.Join(detected, "+"))
	}
	return ""
}

func checkMacro(data []byte, filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))

	// Office Open XML with macros
	if ext == ".xlsm" || ext == ".docm" || ext == ".pptm" {
		return fmt.Sprintf("macro_extension: %s", ext)
	}

	// OLE2 compound document (legacy Office)
	if bytes.HasPrefix(data, []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1")) {
		// Check for VBA signatures inside OLE
		if bytes.Contains(data, []byte("_VBA_PROJECT")) || bytes.Contains(data, []byte("VBAProject")) {
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
