// CLAUDE:SUMMARY One-shot migration: registers existing siftrag dossiers in usertenant catalog.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hazyhaar/pkg/dbopen"

	_ "modernc.org/sqlite"
)

func main() {
	siftragPath := flag.String("siftrag", "", "path to siftrag.db")
	catalogPath := flag.String("catalog", "", "path to catalog.db (usertenant)")
	flag.Parse()

	if *siftragPath == "" || *catalogPath == "" {
		fmt.Fprintf(os.Stderr, "Usage: migrate-shards -siftrag /path/to/siftrag.db -catalog /path/to/catalog.db\n")
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	siftragDB, err := dbopen.Open(*siftragPath)
	if err != nil {
		slog.Error("open siftrag db", "error", err)
		os.Exit(1)
	}

	catalogDB, err := dbopen.Open(*catalogPath)
	if err != nil {
		siftragDB.Close()
		slog.Error("open catalog db", "error", err)
		os.Exit(1)
	}
	defer siftragDB.Close()
	defer catalogDB.Close()

	ctx := context.Background()

	// Read all dossiers from siftrag.
	rows, err := siftragDB.QueryContext(ctx,
		`SELECT dossier_id, user_id, name FROM user_dossiers WHERE archived = 0`)
	if err != nil {
		slog.Error("query siftrag dossiers", "error", err)
		os.Exit(1)
	}
	defer rows.Close()

	var created, skipped int
	now := time.Now().UnixMilli()

	for rows.Next() {
		var dossierID, userID, name string
		if err := rows.Scan(&dossierID, &userID, &name); err != nil {
			slog.Error("scan row", "error", err)
			continue
		}

		res, err := catalogDB.ExecContext(ctx,
			`INSERT OR IGNORE INTO shards (id, owner_id, name, strategy, endpoint, config, status, size_bytes, created_at, updated_at)
			 VALUES (?, ?, ?, 'local', '', '{}', 'active', 0, ?, ?)`,
			dossierID, userID, name, now, now)
		if err != nil {
			slog.Error("insert shard", "dossier_id", dossierID, "error", err)
			continue
		}

		n, _ := res.RowsAffected()
		if n > 0 {
			created++
			slog.Info("shard created", "dossier_id", dossierID, "name", name)
		} else {
			skipped++
			slog.Info("shard already exists", "dossier_id", dossierID)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Error("iterate rows", "error", err)
	}

	slog.Info("migration complete", "created", created, "skipped", skipped, "total", created+skipped)
}
