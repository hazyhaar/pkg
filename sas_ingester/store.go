package sas_ingester

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/hazyhaar/pkg/trace" // registers "sqlite-trace" driver
)

// Store wraps an SQLite database for the Sas Ingester state machine.
type Store struct {
	db *sql.DB
}

// OpenStore opens (or creates) the SQLite database at path and runs migrations.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite-trace", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DB returns the underlying *sql.DB for sharing with audit/trace layers.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS dossiers (
    id              TEXT PRIMARY KEY,
    owner_jwt_sub   TEXT NOT NULL,
    name            TEXT,
    routes          TEXT,
    created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pieces (
    sha256          TEXT NOT NULL,
    dossier_id      TEXT NOT NULL REFERENCES dossiers(id) ON DELETE CASCADE,
    state           TEXT NOT NULL DEFAULT 'received',
    mime            TEXT,
    size_bytes      INTEGER,
    metadata        TEXT,
    injection_risk  TEXT DEFAULT 'none',
    clamav_status   TEXT DEFAULT 'pending',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    PRIMARY KEY (sha256, dossier_id)
);

CREATE TABLE IF NOT EXISTS chunks (
    piece_sha256    TEXT NOT NULL,
    dossier_id      TEXT NOT NULL,
    idx             INTEGER NOT NULL,
    chunk_sha256    TEXT NOT NULL,
    received        INTEGER DEFAULT 0,
    PRIMARY KEY (piece_sha256, dossier_id, idx),
    FOREIGN KEY (piece_sha256, dossier_id) REFERENCES pieces(sha256, dossier_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS routes_pending (
    piece_sha256    TEXT NOT NULL,
    dossier_id      TEXT NOT NULL,
    target          TEXT NOT NULL,
    auth_mode       TEXT NOT NULL,
    require_review  INTEGER DEFAULT 0,
    reviewed        INTEGER DEFAULT 0,
    attempts        INTEGER DEFAULT 0,
    last_error      TEXT,
    next_retry_at   TEXT,
    original_token  TEXT DEFAULT '',
    FOREIGN KEY (piece_sha256, dossier_id) REFERENCES pieces(sha256, dossier_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS tus_uploads (
    upload_id       TEXT PRIMARY KEY,
    dossier_id      TEXT NOT NULL,
    owner_jwt_sub   TEXT NOT NULL,
    total_size      INTEGER NOT NULL,
    offset_bytes    INTEGER NOT NULL DEFAULT 0,
    chunk_dir       TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    completed       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_pieces_state   ON pieces(state);
CREATE INDEX IF NOT EXISTS idx_routes_retry   ON routes_pending(next_retry_at);
CREATE INDEX IF NOT EXISTS idx_dossiers_owner ON dossiers(owner_jwt_sub);
CREATE INDEX IF NOT EXISTS idx_tus_dossier    ON tus_uploads(dossier_id);
`
	_, err := s.db.Exec(ddl)
	return err
}

// --- Dossiers ---

// DossierRoute defines a per-dossier webhook destination stored as JSON
// in the dossiers.routes column. When present, these override the global
// webhook config for that dossier.
type DossierRoute struct {
	URL           string `json:"url"`
	AuthMode      string `json:"auth_mode"`      // opaque_only | jwt_passthru
	Secret        string `json:"secret,omitempty"` // HMAC signing key
	RequireReview bool   `json:"require_review,omitempty"`
}

// Dossier represents a dossier row.
type Dossier struct {
	ID           string `json:"id"`
	OwnerJWTSub  string `json:"-"`
	Name         string `json:"name,omitempty"`
	Routes       string `json:"routes,omitempty"` // JSON array of DossierRoute
	CreatedAt    string `json:"created_at"`
}

// ParsedRoutes returns the per-dossier routes parsed from JSON.
// Returns nil if the column is empty or invalid.
func (d *Dossier) ParsedRoutes() []DossierRoute {
	if d.Routes == "" {
		return nil
	}
	var routes []DossierRoute
	if err := json.Unmarshal([]byte(d.Routes), &routes); err != nil {
		return nil
	}
	return routes
}

// CreateDossier inserts a new dossier.
func (s *Store) CreateDossier(d *Dossier) error {
	_, err := s.db.Exec(
		`INSERT INTO dossiers (id, owner_jwt_sub, name, routes, created_at) VALUES (?, ?, ?, ?, ?)`,
		d.ID, d.OwnerJWTSub, d.Name, d.Routes, d.CreatedAt,
	)
	return err
}

// GetDossier returns a dossier by ID. Returns nil, nil if not found.
func (s *Store) GetDossier(id string) (*Dossier, error) {
	d := &Dossier{}
	err := s.db.QueryRow(
		`SELECT id, owner_jwt_sub, name, routes, created_at FROM dossiers WHERE id = ?`, id,
	).Scan(&d.ID, &d.OwnerJWTSub, &d.Name, &d.Routes, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// EnsureDossier creates the dossier if it doesn't exist, or verifies owner match.
func (s *Store) EnsureDossier(id, ownerSub string) error {
	d, err := s.GetDossier(id)
	if err != nil {
		return err
	}
	if d == nil {
		return s.CreateDossier(&Dossier{
			ID:          id,
			OwnerJWTSub: ownerSub,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		})
	}
	if d.OwnerJWTSub != ownerSub {
		return fmt.Errorf("dossier %s: owner mismatch", id)
	}
	return nil
}

// SetDossierRoutes updates the per-dossier routes JSON. Pass nil to clear.
func (s *Store) SetDossierRoutes(id string, routes []DossierRoute) error {
	var routesJSON string
	if routes != nil {
		data, err := json.Marshal(routes)
		if err != nil {
			return fmt.Errorf("marshal routes: %w", err)
		}
		routesJSON = string(data)
	}
	_, err := s.db.Exec(`UPDATE dossiers SET routes = ? WHERE id = ?`, routesJSON, id)
	return err
}

// DeleteDossier deletes a dossier by ID.
// CASCADE on pieces → chunks + routes_pending handles cleanup.
func (s *Store) DeleteDossier(id string) error {
	_, err := s.db.Exec(`DELETE FROM dossiers WHERE id = ?`, id)
	return err
}

// --- Pieces ---

// Piece represents a piece row.
type Piece struct {
	SHA256        string `json:"sha256"`
	DossierID     string `json:"dossier_id"`
	State         string `json:"state"`
	MIME          string `json:"mime,omitempty"`
	SizeBytes     int64  `json:"size_bytes,omitempty"`
	Metadata      string `json:"metadata,omitempty"`
	InjectionRisk string `json:"injection_risk"`
	ClamAVStatus  string `json:"clamav_status"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// InsertPiece inserts a new piece row.
func (s *Store) InsertPiece(p *Piece) error {
	_, err := s.db.Exec(
		`INSERT INTO pieces (sha256, dossier_id, state, mime, size_bytes, metadata, injection_risk, clamav_status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.SHA256, p.DossierID, p.State, p.MIME, p.SizeBytes, p.Metadata,
		p.InjectionRisk, p.ClamAVStatus, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

// GetPiece returns a piece by SHA256 and dossier_id. Returns nil, nil if not found.
func (s *Store) GetPiece(sha256, dossierID string) (*Piece, error) {
	p := &Piece{}
	err := s.db.QueryRow(
		`SELECT sha256, dossier_id, state, mime, size_bytes, metadata, injection_risk, clamav_status, created_at, updated_at
		 FROM pieces WHERE sha256 = ? AND dossier_id = ?`, sha256, dossierID,
	).Scan(&p.SHA256, &p.DossierID, &p.State, &p.MIME, &p.SizeBytes, &p.Metadata,
		&p.InjectionRisk, &p.ClamAVStatus, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdatePieceState updates the state (and updated_at) of a piece.
func (s *Store) UpdatePieceState(sha256, dossierID, state string) error {
	_, err := s.db.Exec(
		`UPDATE pieces SET state = ?, updated_at = ? WHERE sha256 = ? AND dossier_id = ?`,
		state, time.Now().UTC().Format(time.RFC3339), sha256, dossierID,
	)
	return err
}

// UpdatePieceMetadata updates metadata-related fields of a piece.
func (s *Store) UpdatePieceMetadata(sha256, dossierID, mime, metadata, injectionRisk, clamavStatus, state string) error {
	_, err := s.db.Exec(
		`UPDATE pieces SET mime = ?, metadata = ?, injection_risk = ?, clamav_status = ?, state = ?, updated_at = ?
		 WHERE sha256 = ? AND dossier_id = ?`,
		mime, metadata, injectionRisk, clamavStatus, state,
		time.Now().UTC().Format(time.RFC3339), sha256, dossierID,
	)
	return err
}

// ListPieces returns all pieces for a dossier.
func (s *Store) ListPieces(dossierID string) ([]*Piece, error) {
	rows, err := s.db.Query(
		`SELECT sha256, dossier_id, state, mime, size_bytes, metadata, injection_risk, clamav_status, created_at, updated_at
		 FROM pieces WHERE dossier_id = ? ORDER BY created_at`, dossierID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pieces []*Piece
	for rows.Next() {
		p := &Piece{}
		if err := rows.Scan(&p.SHA256, &p.DossierID, &p.State, &p.MIME, &p.SizeBytes, &p.Metadata,
			&p.InjectionRisk, &p.ClamAVStatus, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		pieces = append(pieces, p)
	}
	return pieces, rows.Err()
}

// ListPiecesByState returns pieces in the given state (for crash recovery).
func (s *Store) ListPiecesByState(state string) ([]*Piece, error) {
	rows, err := s.db.Query(
		`SELECT sha256, dossier_id, state, mime, size_bytes, metadata, injection_risk, clamav_status, created_at, updated_at
		 FROM pieces WHERE state = ? ORDER BY created_at`, state,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pieces []*Piece
	for rows.Next() {
		p := &Piece{}
		if err := rows.Scan(&p.SHA256, &p.DossierID, &p.State, &p.MIME, &p.SizeBytes, &p.Metadata,
			&p.InjectionRisk, &p.ClamAVStatus, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		pieces = append(pieces, p)
	}
	return pieces, rows.Err()
}

// PiecesCount returns the number of pieces in a given state, or all if state is empty.
func (s *Store) PiecesCount(state string) (int, error) {
	var count int
	var err error
	if state == "" {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM pieces`).Scan(&count)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM pieces WHERE state = ?`, state).Scan(&count)
	}
	return count, err
}

// --- Chunks ---

// InsertChunk inserts a chunk tracking row.
func (s *Store) InsertChunk(pieceSHA256, dossierID string, idx int, chunkSHA256 string, received bool) error {
	rcv := 0
	if received {
		rcv = 1
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO chunks (piece_sha256, dossier_id, idx, chunk_sha256, received) VALUES (?, ?, ?, ?, ?)`,
		pieceSHA256, dossierID, idx, chunkSHA256, rcv,
	)
	return err
}

// MarkChunkReceived marks a chunk as received.
func (s *Store) MarkChunkReceived(pieceSHA256, dossierID string, idx int) error {
	_, err := s.db.Exec(
		`UPDATE chunks SET received = 1 WHERE piece_sha256 = ? AND dossier_id = ? AND idx = ?`,
		pieceSHA256, dossierID, idx,
	)
	return err
}

// --- Routes ---

// RoutePending represents a pending route delivery.
type RoutePending struct {
	PieceSHA256   string `json:"piece_sha256"`
	DossierID     string `json:"dossier_id"`
	Target        string `json:"target"`
	AuthMode      string `json:"auth_mode"` // opaque_only | jwt_passthru
	RequireReview bool   `json:"require_review"`
	Reviewed      bool   `json:"reviewed"`
	Attempts      int    `json:"attempts"`
	LastError     string `json:"last_error,omitempty"`
	NextRetryAt   string `json:"next_retry_at,omitempty"`
	OriginalToken string `json:"-"` // JWT for jwt_passthru; never serialized
}

// InsertRoute inserts a pending route.
func (s *Store) InsertRoute(r *RoutePending) error {
	reqReview := 0
	if r.RequireReview {
		reqReview = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO routes_pending (piece_sha256, dossier_id, target, auth_mode, require_review, reviewed, attempts, last_error, next_retry_at, original_token)
		 VALUES (?, ?, ?, ?, ?, 0, 0, '', '', ?)`,
		r.PieceSHA256, r.DossierID, r.Target, r.AuthMode, reqReview, r.OriginalToken,
	)
	return err
}

// ListRoutes returns pending routes for a piece.
func (s *Store) ListRoutes(pieceSHA256, dossierID string) ([]*RoutePending, error) {
	rows, err := s.db.Query(
		`SELECT piece_sha256, dossier_id, target, auth_mode, require_review, reviewed, attempts, last_error, next_retry_at, original_token
		 FROM routes_pending WHERE piece_sha256 = ? AND dossier_id = ?`, pieceSHA256, dossierID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var routes []*RoutePending
	for rows.Next() {
		r := &RoutePending{}
		var reqReview, reviewed int
		if err := rows.Scan(&r.PieceSHA256, &r.DossierID, &r.Target, &r.AuthMode,
			&reqReview, &reviewed, &r.Attempts, &r.LastError, &r.NextRetryAt, &r.OriginalToken); err != nil {
			return nil, err
		}
		r.RequireReview = reqReview == 1
		r.Reviewed = reviewed == 1
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// UpdateRouteAttempt updates retry state for a route.
func (s *Store) UpdateRouteAttempt(pieceSHA256, dossierID, target string, attempts int, lastError, nextRetryAt string) error {
	_, err := s.db.Exec(
		`UPDATE routes_pending SET attempts = ?, last_error = ?, next_retry_at = ?
		 WHERE piece_sha256 = ? AND dossier_id = ? AND target = ?`,
		attempts, lastError, nextRetryAt, pieceSHA256, dossierID, target,
	)
	return err
}

// MarkRouteReviewed marks a route as reviewed (approved for delivery).
func (s *Store) MarkRouteReviewed(pieceSHA256, dossierID, target string) error {
	_, err := s.db.Exec(
		`UPDATE routes_pending SET reviewed = 1 WHERE piece_sha256 = ? AND dossier_id = ? AND target = ?`,
		pieceSHA256, dossierID, target,
	)
	return err
}

// ListRetryableRoutes returns routes due for retry.
func (s *Store) ListRetryableRoutes(now string) ([]*RoutePending, error) {
	rows, err := s.db.Query(
		`SELECT piece_sha256, dossier_id, target, auth_mode, require_review, reviewed, attempts, last_error, next_retry_at, original_token
		 FROM routes_pending
		 WHERE attempts < 5
		   AND (require_review = 0 OR reviewed = 1)
		   AND (next_retry_at = '' OR next_retry_at <= ?)
		 ORDER BY next_retry_at`, now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var routes []*RoutePending
	for rows.Next() {
		r := &RoutePending{}
		var reqReview, reviewed int
		if err := rows.Scan(&r.PieceSHA256, &r.DossierID, &r.Target, &r.AuthMode,
			&reqReview, &reviewed, &r.Attempts, &r.LastError, &r.NextRetryAt, &r.OriginalToken); err != nil {
			return nil, err
		}
		r.RequireReview = reqReview == 1
		r.Reviewed = reviewed == 1
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// DeleteRoute removes a completed route.
func (s *Store) DeleteRoute(pieceSHA256, dossierID, target string) error {
	_, err := s.db.Exec(
		`DELETE FROM routes_pending WHERE piece_sha256 = ? AND dossier_id = ? AND target = ?`,
		pieceSHA256, dossierID, target,
	)
	return err
}

// --- tus uploads ---

// TusUpload represents a resumable upload in progress.
type TusUpload struct {
	UploadID    string `json:"upload_id"`
	DossierID   string `json:"dossier_id"`
	OwnerJWTSub string `json:"-"`
	TotalSize   int64  `json:"total_size"`
	OffsetBytes int64  `json:"offset_bytes"`
	ChunkDir    string `json:"chunk_dir"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Completed   bool   `json:"completed"`
}

// CreateTusUpload inserts a new tus upload record.
func (s *Store) CreateTusUpload(u *TusUpload) error {
	completed := 0
	if u.Completed {
		completed = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO tus_uploads (upload_id, dossier_id, owner_jwt_sub, total_size, offset_bytes, chunk_dir, created_at, updated_at, completed)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.UploadID, u.DossierID, u.OwnerJWTSub, u.TotalSize, u.OffsetBytes, u.ChunkDir, u.CreatedAt, u.UpdatedAt, completed,
	)
	return err
}

// GetTusUpload returns a tus upload by ID. Returns nil, nil if not found.
func (s *Store) GetTusUpload(uploadID string) (*TusUpload, error) {
	u := &TusUpload{}
	var completed int
	err := s.db.QueryRow(
		`SELECT upload_id, dossier_id, owner_jwt_sub, total_size, offset_bytes, chunk_dir, created_at, updated_at, completed
		 FROM tus_uploads WHERE upload_id = ?`, uploadID,
	).Scan(&u.UploadID, &u.DossierID, &u.OwnerJWTSub, &u.TotalSize, &u.OffsetBytes, &u.ChunkDir, &u.CreatedAt, &u.UpdatedAt, &completed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.Completed = completed == 1
	return u, nil
}

// UpdateTusOffset updates the offset and updated_at for a tus upload.
func (s *Store) UpdateTusOffset(uploadID string, offset int64) error {
	_, err := s.db.Exec(
		`UPDATE tus_uploads SET offset_bytes = ?, updated_at = ? WHERE upload_id = ?`,
		offset, time.Now().UTC().Format(time.RFC3339), uploadID,
	)
	return err
}

// CompleteTusUpload marks a tus upload as completed.
func (s *Store) CompleteTusUpload(uploadID string) error {
	_, err := s.db.Exec(
		`UPDATE tus_uploads SET completed = 1, updated_at = ? WHERE upload_id = ?`,
		time.Now().UTC().Format(time.RFC3339), uploadID,
	)
	return err
}

// DeleteTusUpload removes a tus upload record.
func (s *Store) DeleteTusUpload(uploadID string) error {
	_, err := s.db.Exec(`DELETE FROM tus_uploads WHERE upload_id = ?`, uploadID)
	return err
}
