// CLAUDE:SUMMARY SQLite-backed idempotency guard storing SHA-256 keyed results to prevent duplicate execution.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Schema, Guard, New

// Package idempotent provides a SQLite-backed idempotency guard.
// It stores a SHA-256 hash of the caller-provided key and the result of the
// first execution. Subsequent calls with the same key return the cached result
// without re-executing the function.
package idempotent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Schema creates the idempotent_log table.
const Schema = `
CREATE TABLE IF NOT EXISTS idempotent_log (
	key_hash TEXT PRIMARY KEY,
	original_key TEXT NOT NULL,
	result BLOB,
	error_msg TEXT,
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_idempotent_created ON idempotent_log(created_at);
`

// Guard provides idempotent execution backed by SQLite.
type Guard struct {
	db *sql.DB
}

// New creates a Guard backed by the given database.
// Call Init() to create the table.
func New(db *sql.DB) *Guard {
	return &Guard{db: db}
}

// Init creates the idempotent_log table. Idempotent.
func (g *Guard) Init() error {
	_, err := g.db.Exec(Schema)
	return err
}

// Once executes fn only if key has not been seen before.
// If key was already processed, returns the cached result.
// The key is hashed with SHA-256 before storage.
//
// If fn returns an error, the error message is stored and returned on
// subsequent calls (the function is NOT retried).
func (g *Guard) Once(ctx context.Context, key string, fn func() ([]byte, error)) ([]byte, error) {
	hash := hashKey(key)

	// Check if already processed.
	var result sql.NullString
	var errMsg sql.NullString
	err := g.db.QueryRowContext(ctx,
		`SELECT result, error_msg FROM idempotent_log WHERE key_hash = ?`, hash,
	).Scan(&result, &errMsg)

	if err == nil {
		// Already processed.
		if errMsg.Valid && errMsg.String != "" {
			return nil, fmt.Errorf("idempotent: previous execution failed: %s", errMsg.String)
		}
		if result.Valid {
			return []byte(result.String), nil
		}
		return nil, nil
	}

	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("idempotent: check: %w", err)
	}

	// Execute the function.
	data, fnErr := fn()

	// Store result.
	var errStr *string
	if fnErr != nil {
		s := fnErr.Error()
		errStr = &s
	}

	_, insertErr := g.db.ExecContext(ctx,
		`INSERT INTO idempotent_log (key_hash, original_key, result, error_msg)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(key_hash) DO NOTHING`,
		hash, key, data, errStr)
	if insertErr != nil {
		// If insertion failed but fn succeeded, still return the result
		// (we lose idempotency guarantee but don't fail the operation).
		if fnErr == nil {
			return data, nil
		}
		return nil, fnErr
	}

	return data, fnErr
}

// Seen checks whether a key has been processed before.
func (g *Guard) Seen(ctx context.Context, key string) (bool, error) {
	hash := hashKey(key)
	var count int
	err := g.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM idempotent_log WHERE key_hash = ?`, hash,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("idempotent: seen: %w", err)
	}
	return count > 0, nil
}

// Prune deletes entries older than the given duration.
// Returns the number of deleted rows.
func (g *Guard) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	res, err := g.db.ExecContext(ctx,
		`DELETE FROM idempotent_log WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("idempotent: prune: %w", err)
	}
	return res.RowsAffected()
}

// Delete removes a specific key from the log, allowing re-execution.
func (g *Guard) Delete(ctx context.Context, key string) error {
	hash := hashKey(key)
	_, err := g.db.ExecContext(ctx,
		`DELETE FROM idempotent_log WHERE key_hash = ?`, hash)
	return err
}

// Count returns the number of entries in the log.
func (g *Guard) Count(ctx context.Context) (int64, error) {
	var count int64
	err := g.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM idempotent_log`).Scan(&count)
	return count, err
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}
