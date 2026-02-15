package sas_ingester

import "os"

// openFile is a thin wrapper for testability.
func openFile(path string) (*os.File, error) {
	return os.Open(path)
}
