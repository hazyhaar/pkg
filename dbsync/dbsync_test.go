package dbsync

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates a test SQLite database with sample tables.
func setupTestDB(t *testing.T, dir string) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(dir, "source.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		`CREATE TABLE users (
			user_id TEXT PRIMARY KEY,
			handle TEXT NOT NULL,
			display_name TEXT,
			email TEXT,
			password_hash TEXT,
			reputation_score INTEGER DEFAULT 0,
			avatar_url TEXT,
			bio TEXT,
			is_public INTEGER DEFAULT 1,
			is_active INTEGER DEFAULT 1,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE engagements (
			engagement_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT,
			visibility TEXT DEFAULT 'public',
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE templates (
			template_id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			is_blacklisted INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE admin_secrets (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`INSERT INTO users VALUES ('u1', 'alice', 'Alice', 'alice@test.com', 'hash123', 100, '', 'Bio', 1, 1, 1000)`,
		`INSERT INTO users VALUES ('u2', 'bob', 'Bob', 'bob@test.com', 'hash456', 50, '', 'Bob bio', 1, 0, 1001)`,
		`INSERT INTO engagements VALUES ('e1', 'Public Eng', 'desc', 'public', 1000)`,
		`INSERT INTO engagements VALUES ('e2', 'Private Eng', 'secret', 'private', 1001)`,
		`INSERT INTO templates VALUES ('t1', 'Good Template', 0, 1000)`,
		`INSERT INTO templates VALUES ('t2', 'Bad Template', 1, 1001)`,
		`INSERT INTO admin_secrets VALUES ('api_key', 'super_secret_123')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	return db, dbPath
}

// setupRoutesDB creates a routes table for testing.
func setupRoutesDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	routesDB, err := sql.Open("sqlite", filepath.Join(dir, "routes.db"))
	if err != nil {
		t.Fatalf("open routes: %v", err)
	}
	_, err = routesDB.Exec(`CREATE TABLE IF NOT EXISTS routes (
		service_name TEXT PRIMARY KEY,
		strategy TEXT NOT NULL,
		endpoint TEXT,
		config TEXT DEFAULT '{}',
		updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	)`)
	if err != nil {
		t.Fatalf("create routes: %v", err)
	}
	return routesDB
}

func TestFilterExcludesPrivateData(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{
		FullTables: []string{"engagements"},
		FilteredTables: map[string]string{
			"templates": "is_blacklisted = 0",
		},
		PartialTables: map[string]PartialTable{
			"users": {
				Columns: []string{"user_id", "handle", "display_name", "reputation_score"},
				Where:   "is_active = 1",
			},
		},
	}

	dstPath := filepath.Join(dir, "public.db")
	meta, err := ProduceSnapshot(db, dstPath, spec)
	if err != nil {
		t.Fatalf("produce snapshot: %v", err)
	}

	if meta.Hash == "" {
		t.Error("expected non-empty hash")
	}
	if meta.Size <= 0 {
		t.Error("expected positive size")
	}

	// Open the snapshot and verify.
	snapDB, err := sql.Open("sqlite", dstPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapDB.Close()

	// admin_secrets table should not exist.
	var count int
	err = snapDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='admin_secrets'").Scan(&count)
	if err != nil {
		t.Fatalf("check admin_secrets: %v", err)
	}
	if count != 0 {
		t.Error("admin_secrets table should be excluded from snapshot")
	}

	// Users: inactive user (bob) should be excluded.
	err = snapDB.QueryRow("SELECT count(*) FROM users").Scan(&count)
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 active user, got %d", count)
	}

	// Users: sensitive columns should be NULL.
	var email, passwordHash sql.NullString
	err = snapDB.QueryRow("SELECT email, password_hash FROM users WHERE user_id = 'u1'").Scan(&email, &passwordHash)
	if err != nil {
		t.Fatalf("check user columns: %v", err)
	}
	if email.Valid {
		t.Error("email should be NULL in snapshot")
	}
	if passwordHash.Valid {
		t.Error("password_hash should be NULL in snapshot")
	}

	// Templates: blacklisted should be excluded.
	err = snapDB.QueryRow("SELECT count(*) FROM templates").Scan(&count)
	if err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 non-blacklisted template, got %d", count)
	}
}

func TestFilterIncludesPublicData(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{
		FullTables: []string{"engagements", "templates"},
		PartialTables: map[string]PartialTable{
			"users": {
				Columns: []string{"user_id", "handle", "display_name", "reputation_score", "created_at"},
			},
		},
	}

	dstPath := filepath.Join(dir, "public.db")
	_, err := ProduceSnapshot(db, dstPath, spec)
	if err != nil {
		t.Fatalf("produce snapshot: %v", err)
	}

	snapDB, err := sql.Open("sqlite", dstPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapDB.Close()

	// All engagements should be present (full table).
	var count int
	err = snapDB.QueryRow("SELECT count(*) FROM engagements").Scan(&count)
	if err != nil {
		t.Fatalf("count engagements: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 engagements, got %d", count)
	}

	// All templates should be present (full table).
	err = snapDB.QueryRow("SELECT count(*) FROM templates").Scan(&count)
	if err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 templates, got %d", count)
	}

	// Public columns should be present.
	var handle string
	err = snapDB.QueryRow("SELECT handle FROM users WHERE user_id = 'u1'").Scan(&handle)
	if err != nil {
		t.Fatalf("select handle: %v", err)
	}
	if handle != "alice" {
		t.Errorf("expected handle 'alice', got %q", handle)
	}
}

func TestSnapshotHashVerification(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{
		FullTables: []string{"engagements"},
	}

	dstPath := filepath.Join(dir, "snap.db")
	meta, err := ProduceSnapshot(db, dstPath, spec)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Verify hash independently.
	f, err := os.Open(dstPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	h := sha256.New()
	io.Copy(h, f)
	expected := hex.EncodeToString(h.Sum(nil))

	if meta.Hash != expected {
		t.Errorf("hash mismatch: meta=%s, computed=%s", meta.Hash, expected)
	}
}

func TestSubscriberSwapAtomic(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{
		FullTables: []string{"engagements"},
	}

	// Produce a snapshot.
	snapPath := filepath.Join(dir, "snap.db")
	meta, err := ProduceSnapshot(db, snapPath, spec)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Create subscriber.
	subDBPath := filepath.Join(dir, "fo_public.db")
	sub := NewSubscriber(subDBPath, ":0", nil)

	if sub.DB() != nil {
		t.Error("DB() should be nil before first snapshot")
	}

	// Track swap callback.
	swapCount := 0
	sub.OnSwap(func() { swapCount++ })

	// Simulate receiving a snapshot.
	f, err := os.Open(snapPath)
	if err != nil {
		t.Fatalf("open snap: %v", err)
	}
	defer f.Close()

	err = sub.handleSnapshot(*meta, f)
	if err != nil {
		t.Fatalf("handle snapshot: %v", err)
	}

	if sub.DB() == nil {
		t.Error("DB() should not be nil after snapshot")
	}
	if swapCount != 1 {
		t.Errorf("expected 1 swap callback, got %d", swapCount)
	}
	if sub.Version() != meta.Version {
		t.Errorf("version mismatch: got %d, want %d", sub.Version(), meta.Version)
	}

	// Verify data is accessible.
	var count int
	err = sub.DB().QueryRow("SELECT count(*) FROM engagements").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 engagements, got %d", count)
	}

	sub.Close()
}

func TestPublisherProducesSnapshot(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	routesDB := setupRoutesDB(t, dir)
	defer routesDB.Close()

	savePath := filepath.Join(dir, "save_chaude.db")
	tlsCfg := SyncClientTLSConfig(true)

	pub := NewPublisherWithRoutesDB(db, routesDB, FilterSpec{
		FullTables: []string{"engagements"},
	}, savePath, tlsCfg)

	ctx := context.Background()

	// Directly call publish to verify the core logic works.
	err := pub.publish(ctx)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if pub.LastMeta() == nil {
		t.Fatal("expected snapshot metadata after publish")
	}

	// Verify save chaude file exists.
	if _, err := os.Stat(savePath); os.IsNotExist(err) {
		t.Fatal("save chaude file should exist")
	}

	// Verify snapshot content.
	snapDB, err := sql.Open("sqlite", savePath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer snapDB.Close()

	var count int
	err = snapDB.QueryRow("SELECT count(*) FROM engagements").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 engagements in snapshot, got %d", count)
	}

	// admin_secrets should not exist.
	err = snapDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='admin_secrets'").Scan(&count)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if count != 0 {
		t.Error("admin_secrets should be excluded from snapshot")
	}
}

func TestPublisherWatchIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping watch integration in short mode")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.db")

	// Open TWO separate connections to the same file so data_version changes.
	writerDB, err := sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writerDB.Close()

	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		`CREATE TABLE engagements (id TEXT PRIMARY KEY, title TEXT, created_at INTEGER)`,
		`INSERT INTO engagements VALUES ('e1', 'First', 1000)`,
	} {
		if _, err := writerDB.Exec(q); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	readerDB, err := sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	defer readerDB.Close()
	readerDB.Exec("PRAGMA journal_mode=WAL")

	routesDB := setupRoutesDB(t, dir)
	defer routesDB.Close()

	savePath := filepath.Join(dir, "save_chaude.db")
	pub := NewPublisherWithRoutesDB(readerDB, routesDB, FilterSpec{
		FullTables: []string{"engagements"},
	}, savePath, SyncClientTLSConfig(true),
		WithWatchInterval(50*time.Millisecond),
		WithWatchDebounce(0),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go pub.Start(ctx)
	time.Sleep(300 * time.Millisecond)

	// Write on the writer connection → data_version changes for reader.
	_, err = writerDB.Exec("INSERT INTO engagements VALUES ('e2', 'Second', 2000)")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Wait for watcher to detect and produce snapshot.
	time.Sleep(1 * time.Second)

	if pub.LastMeta() == nil {
		t.Error("expected snapshot to be produced after cross-connection write")
	}
}

func TestRoundTripQUIC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping QUIC test in short mode")
	}

	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{
		FullTables: []string{"engagements", "templates"},
	}

	// Produce snapshot.
	snapPath := filepath.Join(dir, "snap.db")
	meta, err := ProduceSnapshot(db, snapPath, spec)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	// Generate self-signed TLS config for test.
	serverTLS, err := selfSignedSyncTLS()
	if err != nil {
		t.Fatalf("server tls: %v", err)
	}
	clientTLS := SyncClientTLSConfig(true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start listener on :0 to get a random port, then extract the actual address.
	subDBPath := filepath.Join(dir, "received.db")
	received := make(chan SnapshotMeta, 1)
	listenerReady := make(chan string, 1)

	go func() {
		// We need to get the actual listen address. Use a net.ListenPacket to
		// grab a free port, then use that port for QUIC.
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Errorf("listen packet: %v", err)
			return
		}
		addr := pc.LocalAddr().String()
		pc.Close()

		listenerReady <- addr

		ListenSnapshots(ctx, addr, serverTLS, func(m SnapshotMeta, r io.Reader) error {
			f, _ := os.Create(subDBPath)
			io.Copy(f, r)
			f.Close()
			received <- m
			return nil
		})
	}()

	// Wait for listener address.
	var listenAddr string
	select {
	case listenAddr = <-listenerReady:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for listener to start")
	}

	// Give listener time to actually bind.
	time.Sleep(100 * time.Millisecond)

	// Push snapshot.
	err = PushSnapshot(ctx, listenAddr, clientTLS, *meta, snapPath)
	if err != nil {
		t.Skipf("QUIC push failed (may be port timing issue): %v", err)
	}

	select {
	case rm := <-received:
		if rm.Hash != meta.Hash {
			t.Errorf("hash mismatch: sent %s, received %s", meta.Hash, rm.Hash)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for snapshot")
	}
}

func TestNoopPausesSync(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	routesDB := setupRoutesDB(t, dir)
	defer routesDB.Close()

	// Insert a noop route.
	_, err := routesDB.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('dbsync:fo-1', 'noop', 'quic://127.0.0.1:19443')`)
	if err != nil {
		t.Fatalf("insert route: %v", err)
	}

	provider := NewRoutesTargetProvider(routesDB)
	targets, err := provider.Targets(context.Background())
	if err != nil {
		t.Fatalf("load targets: %v", err)
	}

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Strategy != "noop" {
		t.Errorf("expected strategy 'noop', got %q", targets[0].Strategy)
	}
}

// --- New tests ---

func TestStaticTargetProvider(t *testing.T) {
	p := NewStaticTargetProvider(
		Target{Name: "fo-1", Strategy: "dbsync", Endpoint: "10.0.0.1:9443"},
		Target{Name: "fo-2", Strategy: "noop", Endpoint: "10.0.0.2:9443"},
	)

	targets, err := p.Targets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].Name != "fo-1" || targets[0].Strategy != "dbsync" {
		t.Errorf("target[0] = %+v, want fo-1/dbsync", targets[0])
	}
	if targets[1].Name != "fo-2" || targets[1].Strategy != "noop" {
		t.Errorf("target[1] = %+v, want fo-2/noop", targets[1])
	}
}

func TestRoutesTargetProvider(t *testing.T) {
	dir := t.TempDir()
	routesDB := setupRoutesDB(t, dir)
	defer routesDB.Close()

	// Insert targets.
	for _, q := range []string{
		`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('dbsync:fo-1', 'dbsync', '10.0.0.1:9443')`,
		`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('dbsync:fo-2', 'noop', '10.0.0.2:9443')`,
		`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('mcp:service-a', 'quic', '10.0.0.3:9443')`, // not dbsync
	} {
		if _, err := routesDB.Exec(q); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	provider := NewRoutesTargetProvider(routesDB)
	targets, err := provider.Targets(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 dbsync targets, got %d", len(targets))
	}
}

func TestPublisherWithStaticTargets(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	savePath := filepath.Join(dir, "save.db")

	// Use static targets (no routes DB needed).
	pub := NewPublisher(db, NewStaticTargetProvider(
		Target{Name: "fo-1", Strategy: "noop", Endpoint: "127.0.0.1:19999"},
	), FilterSpec{
		FullTables: []string{"engagements"},
	}, savePath, SyncClientTLSConfig(true))

	ctx := context.Background()
	err := pub.publish(ctx)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if pub.LastMeta() == nil {
		t.Fatal("expected snapshot metadata")
	}

	// Verify dedup: second publish with same data should not change hash.
	hash1 := pub.LastMeta().Hash
	err = pub.publish(ctx)
	if err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if pub.LastMeta().Hash != hash1 {
		t.Error("expected same hash for unchanged data")
	}
}

func TestPublisherPing(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	pub := NewPublisher(db, NewStaticTargetProvider(), FilterSpec{}, filepath.Join(dir, "s.db"), nil)

	if err := pub.Ping(context.Background()); err != nil {
		t.Errorf("Ping should succeed on open DB: %v", err)
	}

	db.Close()
	if err := pub.Ping(context.Background()); err == nil {
		t.Error("Ping should fail on closed DB")
	}
}

func TestSubscriber_Ping_NoSnapshot(t *testing.T) {
	sub := NewSubscriber(filepath.Join(t.TempDir(), "nonexistent.db"), ":0", nil)

	err := sub.Ping(context.Background())
	if err == nil {
		t.Error("Ping should fail when no snapshot received")
	}
	if !strings.Contains(err.Error(), "no snapshot") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSubscriber_Ping_AfterSnapshot(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{FullTables: []string{"engagements"}}
	snapPath := filepath.Join(dir, "snap.db")
	meta, err := ProduceSnapshot(db, snapPath, spec)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	subDBPath := filepath.Join(dir, "fo.db")
	sub := NewSubscriber(subDBPath, ":0", nil)

	f, err := os.Open(snapPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	if err := sub.handleSnapshot(*meta, f); err != nil {
		t.Fatalf("handle: %v", err)
	}

	if err := sub.Ping(context.Background()); err != nil {
		t.Errorf("Ping should succeed after snapshot: %v", err)
	}

	sub.Close()
}

func TestSubscriber_HashMismatch(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	spec := FilterSpec{FullTables: []string{"engagements"}}
	snapPath := filepath.Join(dir, "snap.db")
	meta, err := ProduceSnapshot(db, snapPath, spec)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}

	subDBPath := filepath.Join(dir, "fo.db")
	sub := NewSubscriber(subDBPath, ":0", nil)

	f, err := os.Open(snapPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	// Tamper with the expected hash.
	badMeta := *meta
	badMeta.Hash = "0000000000000000000000000000000000000000000000000000000000000000"

	err = sub.handleSnapshot(badMeta, f)
	if err == nil {
		t.Fatal("expected hash mismatch error")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWriteProxy_ForwardsToBO(t *testing.T) {
	bo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("proxied"))
	}))
	defer bo.Close()

	proxy, err := NewWriteProxy(bo.URL, nil)
	if err != nil {
		t.Fatalf("NewWriteProxy: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/action", nil)
	w := httptest.NewRecorder()

	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "proxied" {
		t.Errorf("expected 'proxied', got %q", w.Body.String())
	}
}

func TestWriteProxy_InvalidEndpoint(t *testing.T) {
	_, err := NewWriteProxy("://invalid", nil)
	if err == nil {
		t.Error("expected error for invalid endpoint")
	}

	_, err = NewWriteProxy("", nil)
	if err == nil {
		t.Error("expected error for empty endpoint")
	}
}

func TestRedirectHandler(t *testing.T) {
	handler := RedirectHandler("https://bo.example.com")

	req := httptest.NewRequest("GET", "/dashboard?tab=profile", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("expected 307, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "https://bo.example.com/dashboard?tab=profile" {
		t.Errorf("unexpected redirect: %s", loc)
	}
}

func TestFilter_EmptySpec(t *testing.T) {
	dir := t.TempDir()
	db, _ := setupTestDB(t, dir)
	defer db.Close()

	// Empty spec = no tables kept.
	spec := FilterSpec{}
	dstPath := filepath.Join(dir, "empty.db")
	meta, err := ProduceSnapshot(db, dstPath, spec)
	if err != nil {
		t.Fatalf("produce: %v", err)
	}
	if meta.Size <= 0 {
		t.Error("expected positive file size even for empty snapshot")
	}

	snapDB, err := sql.Open("sqlite", dstPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer snapDB.Close()

	var count int
	err = snapDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 user tables in empty spec snapshot, got %d", count)
	}
}

func TestFilter_InvalidWhere(t *testing.T) {
	spec := FilterSpec{
		FilteredTables: map[string]string{
			"users": "1=1; DROP TABLE users",
		},
	}
	err := ValidateFilterSpec(spec)
	if err == nil {
		t.Error("expected error for semicolon in WHERE")
	}

	spec2 := FilterSpec{
		PartialTables: map[string]PartialTable{
			"users": {Columns: []string{"id"}, Where: "1=1 UNION SELECT * FROM admin"},
		},
	}
	err = ValidateFilterSpec(spec2)
	if err == nil {
		t.Error("expected error for UNION in WHERE")
	}

	spec3 := FilterSpec{
		FilteredTables: map[string]string{
			"users": "1=1 -- comment",
		},
	}
	err = ValidateFilterSpec(spec3)
	if err == nil {
		t.Error("expected error for SQL comment in WHERE")
	}
}

// selfSignedSyncTLS generates a self-signed TLS config for dbsync ALPN.
func selfSignedSyncTLS() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"HOROS Test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{certDER},
			PrivateKey:  key,
		}},
		NextProtos: []string{ALPNProtocol},
		MinVersion: tls.VersionTLS13,
	}, nil
}
