package dbopen

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const maxRetries = 3

// IsBusy reports whether err indicates an SQLite BUSY condition.
// It checks for SQLITE_BUSY, "database is locked", and "database table is locked".
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked")
}

// RunTx executes fn inside a transaction with automatic retry on SQLITE_BUSY.
// It retries up to 3 times with 100/200/300 ms backoff.
func RunTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	for i := range maxRetries {
		err := runOnce(ctx, db, fn)
		if err == nil {
			return nil
		}
		if !IsBusy(err) || i == maxRetries-1 {
			return err
		}
		if err := sleepCtx(ctx, time.Duration(100*(i+1))*time.Millisecond); err != nil {
			return fmt.Errorf("dbopen: context cancelled during retry: %w", err)
		}
	}
	return fmt.Errorf("dbopen: RunTx: max retries exceeded")
}

func runOnce(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("dbopen: begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("dbopen: commit: %w", err)
	}
	return nil
}

// Exec executes a statement with automatic retry on SQLITE_BUSY.
// It retries up to 3 times with 100/200/300 ms backoff.
func Exec(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error) {
	for i := range maxRetries {
		result, err := db.ExecContext(ctx, query, args...)
		if err == nil {
			return result, nil
		}
		if !IsBusy(err) || i == maxRetries-1 {
			return nil, err
		}
		if err := sleepCtx(ctx, time.Duration(100*(i+1))*time.Millisecond); err != nil {
			return nil, fmt.Errorf("dbopen: context cancelled during retry: %w", err)
		}
	}
	return nil, fmt.Errorf("dbopen: Exec: max retries exceeded")
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
