package sqlitedb

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ExecuteWithRetry runs a transactional function with automatic retry
// on SQLITE_BUSY errors. The function receives a transaction and should
// perform its work within it — do NOT call tx.Commit() or tx.Rollback(),
// as ExecuteWithRetry manages the transaction lifecycle.
//
// Retry uses exponential backoff starting at 100ms, doubling each attempt.
func ExecuteWithRetry(db *sql.DB, maxRetries int, fn func(tx *sql.Tx) error) error {
	for i := 0; i <= maxRetries; i++ {
		tx, err := db.Begin()
		if err != nil {
			if isBusy(err) && i < maxRetries {
				time.Sleep(backoff(i))
				continue
			}
			return fmt.Errorf("sqlitedb: begin tx: %w", err)
		}

		err = fn(tx)
		if err == nil {
			if commitErr := tx.Commit(); commitErr != nil {
				if isBusy(commitErr) && i < maxRetries {
					time.Sleep(backoff(i))
					continue
				}
				return fmt.Errorf("sqlitedb: commit: %w", commitErr)
			}
			return nil
		}

		tx.Rollback()

		if isBusy(err) && i < maxRetries {
			time.Sleep(backoff(i))
			continue
		}
		return err
	}
	return fmt.Errorf("sqlitedb: max retries (%d) exceeded", maxRetries)
}

func isBusy(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLITE_BUSY")
}

func backoff(attempt int) time.Duration {
	return 100 * time.Millisecond * (1 << uint(attempt))
}
