// CLAUDE:SUMMARY Thin wrapper around os.Open for testability of file operations.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS none
package sas_ingester

import "os"

// openFile is a thin wrapper for testability.
func openFile(path string) (*os.File, error) {
	return os.Open(path)
}
