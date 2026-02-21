package dbsync

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ValidateFilterSpec checks that WHERE clauses in the spec do not contain
// dangerous SQL patterns (multi-statement, DDL, etc.). This is a defense-in-depth
// measure — FilterSpec is expected to be set by the deployer, not end users.
func ValidateFilterSpec(spec FilterSpec) error {
	for table, where := range spec.FilteredTables {
		if err := validateWhereClause(where); err != nil {
			return fmt.Errorf("dbsync: FilteredTables[%s]: %w", table, err)
		}
	}
	for table, pt := range spec.PartialTables {
		if pt.Where != "" {
			if err := validateWhereClause(pt.Where); err != nil {
				return fmt.Errorf("dbsync: PartialTables[%s]: %w", table, err)
			}
		}
	}
	return nil
}

// validateWhereClause rejects WHERE clauses that contain multi-statement, DDL,
// DML, or subquery patterns that could be used for SQL injection.
func validateWhereClause(clause string) error {
	if strings.Contains(clause, ";") {
		return fmt.Errorf("WHERE clause must not contain semicolons")
	}
	if strings.Contains(clause, "--") {
		return fmt.Errorf("WHERE clause must not contain SQL comments")
	}
	if strings.Contains(clause, "/*") {
		return fmt.Errorf("WHERE clause must not contain block comments")
	}
	upper := strings.ToUpper(clause)
	blocked := []string{
		"DROP ", "ALTER ", "CREATE ", "ATTACH ", "DETACH ",
		"INSERT ", "UPDATE ", "DELETE ", "REPLACE ",
		"UNION ", "UNION\t", "UNION\n",
		"INTO ", "EXEC ", "EXECUTE ",
		"LOAD_EXTENSION", "PRAGMA ",
	}
	for _, kw := range blocked {
		if strings.Contains(upper, kw) {
			return fmt.Errorf("WHERE clause must not contain keyword %q", strings.TrimSpace(kw))
		}
	}
	return nil
}

// ProduceSnapshot creates a filtered copy of srcDB at dstPath.
//
// Steps:
//  1. Validate WHERE clauses in the FilterSpec
//  2. VACUUM INTO a temporary file (consistent snapshot of entire DB)
//  3. Open the copy with the plain "sqlite" driver
//  4. Drop tables not in the FilterSpec whitelist
//  5. Apply WHERE clauses (FilteredTables)
//  6. Truncate non-selected columns (PartialTables)
//  7. VACUUM to compact
//  8. SHA-256 hash the result
func ProduceSnapshot(srcDB *sql.DB, dstPath string, spec FilterSpec) (*SnapshotMeta, error) {
	if err := ValidateFilterSpec(spec); err != nil {
		return nil, err
	}
	tmpPath := dstPath + ".tmp"
	defer os.Remove(tmpPath)

	// Step 1: VACUUM INTO creates a consistent snapshot.
	if _, err := srcDB.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, escapeSQLString(tmpPath))); err != nil {
		return nil, fmt.Errorf("dbsync: vacuum into: %w", err)
	}

	// Step 2: Open the copy with the plain driver (no trace recursion).
	copyDB, err := sql.Open("sqlite", tmpPath)
	if err != nil {
		return nil, fmt.Errorf("dbsync: open copy: %w", err)
	}
	defer copyDB.Close()

	// Pragmas for the temporary copy.
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=OFF",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := copyDB.Exec(p); err != nil {
			return nil, fmt.Errorf("dbsync: pragma: %w", err)
		}
	}

	// Build whitelist of allowed tables.
	allowed := buildWhitelist(spec)

	// Step 3: Drop tables not in whitelist.
	if err := dropUnlisted(copyDB, allowed); err != nil {
		return nil, fmt.Errorf("dbsync: drop unlisted: %w", err)
	}

	// Step 4: Apply WHERE clauses on FilteredTables.
	for table, where := range spec.FilteredTables {
		q := fmt.Sprintf("DELETE FROM %s WHERE NOT (%s)", quoteIdent(table), where)
		if _, err := copyDB.Exec(q); err != nil {
			return nil, fmt.Errorf("dbsync: filter %s: %w", table, err)
		}
	}

	// Step 5: Truncate non-selected columns in PartialTables.
	for table, pt := range spec.PartialTables {
		if err := truncateColumns(copyDB, table, pt); err != nil {
			return nil, fmt.Errorf("dbsync: partial %s: %w", table, err)
		}
	}

	// Step 6: VACUUM to compact the snapshot.
	if _, err := copyDB.Exec("VACUUM"); err != nil {
		return nil, fmt.Errorf("dbsync: vacuum compact: %w", err)
	}

	// Close before hashing — ensures WAL is flushed.
	copyDB.Close()

	// Step 7: Hash and rename.
	hash, size, err := hashFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("dbsync: hash: %w", err)
	}

	if err := os.Rename(tmpPath, dstPath); err != nil {
		return nil, fmt.Errorf("dbsync: rename: %w", err)
	}

	return &SnapshotMeta{
		Version:   time.Now().UnixMilli(),
		Hash:      hash,
		Size:      size,
		Timestamp: time.Now().Unix(),
	}, nil
}

// buildWhitelist returns the set of table names that should be kept.
func buildWhitelist(spec FilterSpec) map[string]bool {
	m := make(map[string]bool)
	for _, t := range spec.FullTables {
		m[t] = true
	}
	for t := range spec.FilteredTables {
		m[t] = true
	}
	for t := range spec.PartialTables {
		m[t] = true
	}
	return m
}

// dropUnlisted removes all user tables not in the whitelist.
func dropUnlisted(db *sql.DB, allowed map[string]bool) error {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return err
	}
	defer rows.Close()

	var toDrop []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if !allowed[name] {
			toDrop = append(toDrop, name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range toDrop {
		if _, err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", quoteIdent(name))); err != nil {
			return fmt.Errorf("drop %s: %w", name, err)
		}
	}
	return nil
}

// truncateColumns nullifies columns not in pt.Columns, then deletes rows
// not matching pt.Where (if set). Columns with NOT NULL constraints are set
// to their type's zero value ('', 0) instead of NULL.
func truncateColumns(db *sql.DB, table string, pt PartialTable) error {
	// First, apply WHERE filter if present.
	if pt.Where != "" {
		q := fmt.Sprintf("DELETE FROM %s WHERE NOT (%s)", quoteIdent(table), pt.Where)
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("where: %w", err)
		}
	}

	// Get all column info from the table.
	allCols, err := tableColumnInfo(db, table)
	if err != nil {
		return fmt.Errorf("columns: %w", err)
	}

	// Build set of kept columns.
	keep := make(map[string]bool)
	for _, c := range pt.Columns {
		keep[c] = true
	}

	// Nullify/zero columns not in the keep set.
	var sets []string
	for _, col := range allCols {
		if !keep[col.name] {
			if col.notNull {
				// NOT NULL column → set to zero value based on type.
				sets = append(sets, fmt.Sprintf("%s = %s", quoteIdent(col.name), zeroValue(col.colType)))
			} else {
				sets = append(sets, fmt.Sprintf("%s = NULL", quoteIdent(col.name)))
			}
		}
	}
	if len(sets) > 0 {
		q := fmt.Sprintf("UPDATE %s SET %s", quoteIdent(table), strings.Join(sets, ", "))
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("nullify: %w", err)
		}
	}

	return nil
}

// columnInfo holds metadata about a table column.
type columnInfo struct {
	name    string
	colType string
	notNull bool
}

// tableColumnInfo returns column metadata for a table.
func tableColumnInfo(db *sql.DB, table string) ([]columnInfo, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []columnInfo
	for rows.Next() {
		var cid int
		var name, ct string
		var nn int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &ct, &nn, &dflt, &pk); err != nil {
			return nil, err
		}
		cols = append(cols, columnInfo{name: name, colType: ct, notNull: nn != 0})
	}
	return cols, rows.Err()
}

// zeroValue returns the SQL zero value for a column type.
func zeroValue(colType string) string {
	upper := strings.ToUpper(colType)
	switch {
	case strings.Contains(upper, "INT"):
		return "0"
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOAT"), strings.Contains(upper, "DOUBLE"):
		return "0.0"
	default:
		return "''"
	}
}

// hashFile returns the hex-encoded SHA-256 hash and size of a file.
func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// quoteIdent wraps a SQL identifier in double quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// escapeSQLString escapes single quotes for use in SQL string literals.
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
