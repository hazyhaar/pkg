// CLAUDE:SUMMARY Canonical DDL for per-dossier shard tables: rag_vectors, claim_entities, email_documents, email_attachments, email_participants.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS RAGVectorsSchema, ClaimEntitiesSchema, EmailDocumentsSchema, EmailAttachmentsSchema, EmailParticipantsSchema, ApplyAll, ApplyRAGVectors, ApplyClaimEntities, ApplyEmailDocuments, ApplyEmailAttachments, ApplyEmailParticipants, MigrateEmailDocumentsV2

// Package shardschema is the single source of truth for shard DDL.
// All services (HORAG, siftrag, mbox-cleaner) must import from here.
// Do NOT duplicate these schemas elsewhere.
package shardschema

import (
	"database/sql"
	"fmt"
	"strings"
)

// RAGVectorsSchema is the DDL for the rag_vectors table, applied per-shard.
const RAGVectorsSchema = `
CREATE TABLE IF NOT EXISTS rag_vectors (
    id            TEXT PRIMARY KEY,
    document_id   TEXT NOT NULL,
    source_id     TEXT,
    source_url    TEXT,
    source_type   TEXT,
    title         TEXT,
    chunk_index   INTEGER NOT NULL,
    chunk_text    TEXT NOT NULL,
    token_count   INTEGER,
    overlap_prev  INTEGER DEFAULT 0,
    vector        BLOB NOT NULL,
    dimension     INTEGER NOT NULL,
    model         TEXT NOT NULL,
    content_hash  TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_rag_vectors_document ON rag_vectors(document_id);
CREATE INDEX IF NOT EXISTS idx_rag_vectors_hash ON rag_vectors(content_hash);
`

// ClaimEntitiesSchema is the DDL for the claim_entities table, applied per-shard.
const ClaimEntitiesSchema = `
CREATE TABLE IF NOT EXISTS claim_entities (
    id         TEXT PRIMARY KEY,
    vector_id  TEXT NOT NULL REFERENCES rag_vectors(id),
    type       TEXT NOT NULL,
    value      TEXT NOT NULL,
    raw        TEXT,
    unit       TEXT
);
CREATE INDEX IF NOT EXISTS idx_claim_entities_type  ON claim_entities(type);
CREATE INDEX IF NOT EXISTS idx_claim_entities_value ON claim_entities(value);
CREATE INDEX IF NOT EXISTS idx_claim_entities_vid   ON claim_entities(vector_id);
`

// EmailDocumentsSchema is the DDL for the email_documents table, applied per-shard.
const EmailDocumentsSchema = `
CREATE TABLE IF NOT EXISTS email_documents (
    id              TEXT PRIMARY KEY,
    source_id       TEXT NOT NULL UNIQUE,
    from_addr       TEXT,
    to_addr         TEXT,
    cc_addr         TEXT,
    date_str        TEXT,
    subject         TEXT,
    body_preview    TEXT,
    body_html       TEXT,
    body_text       TEXT,
    in_reply_to     TEXT,
    injection_risk  TEXT NOT NULL DEFAULT 'none',
    email_type      TEXT NOT NULL DEFAULT 'normal',
    thread_id       TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_email_docs_source ON email_documents(source_id);
CREATE INDEX IF NOT EXISTS idx_email_docs_thread ON email_documents(thread_id);
`

// EmailAttachmentsSchema is the DDL for the email_attachments table, applied per-shard.
const EmailAttachmentsSchema = `
CREATE TABLE IF NOT EXISTS email_attachments (
    id           TEXT PRIMARY KEY,
    email_id     TEXT NOT NULL REFERENCES email_documents(id),
    filename     TEXT NOT NULL,
    size         INTEGER NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    is_inline    INTEGER NOT NULL DEFAULT 0,
    cid          TEXT,
    storage_path TEXT,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_email_attach_email ON email_attachments(email_id);
`

// EmailParticipantsSchema is the DDL for the email_participants table, applied per-shard.
const EmailParticipantsSchema = `
CREATE TABLE IF NOT EXISTS email_participants (
    email_id  TEXT NOT NULL REFERENCES email_documents(id),
    addr      TEXT NOT NULL,
    name      TEXT,
    role      TEXT NOT NULL CHECK(role IN ('from','to','cc','bcc')),
    PRIMARY KEY (email_id, addr, role)
);
CREATE INDEX IF NOT EXISTS idx_email_part_addr ON email_participants(addr);
`

// ApplyRAGVectors creates the rag_vectors table if it doesn't exist.
func ApplyRAGVectors(db *sql.DB) error {
	if _, err := db.Exec(RAGVectorsSchema); err != nil {
		return fmt.Errorf("shardschema: apply rag_vectors: %w", err)
	}
	return nil
}

// ApplyClaimEntities creates the claim_entities table if it doesn't exist.
func ApplyClaimEntities(db *sql.DB) error {
	if _, err := db.Exec(ClaimEntitiesSchema); err != nil {
		return fmt.Errorf("shardschema: apply claim_entities: %w", err)
	}
	return nil
}

// ApplyEmailDocuments creates the email_documents table if it doesn't exist.
func ApplyEmailDocuments(db *sql.DB) error {
	if _, err := db.Exec(EmailDocumentsSchema); err != nil {
		return fmt.Errorf("shardschema: apply email_documents: %w", err)
	}
	return nil
}

// ApplyEmailAttachments creates the email_attachments table if it doesn't exist.
func ApplyEmailAttachments(db *sql.DB) error {
	if _, err := db.Exec(EmailAttachmentsSchema); err != nil {
		return fmt.Errorf("shardschema: apply email_attachments: %w", err)
	}
	return nil
}

// ApplyEmailParticipants creates the email_participants table if it doesn't exist.
func ApplyEmailParticipants(db *sql.DB) error {
	if _, err := db.Exec(EmailParticipantsSchema); err != nil {
		return fmt.Errorf("shardschema: apply email_participants: %w", err)
	}
	return nil
}

// MigrateEmailDocumentsV2 adds new columns to an existing email_documents table.
// Safe to call on fresh or already-migrated shards (ignores "duplicate column" errors).
func MigrateEmailDocumentsV2(db *sql.DB) error {
	cols := []string{
		"body_html TEXT",
		"body_text TEXT",
		"in_reply_to TEXT",
		"injection_risk TEXT NOT NULL DEFAULT 'none'",
		"email_type TEXT NOT NULL DEFAULT 'normal'",
	}
	for _, col := range cols {
		_, err := db.Exec("ALTER TABLE email_documents ADD COLUMN " + col)
		if err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("shardschema: migrate email_documents v2 (%s): %w", col, err)
		}
	}
	return nil
}

// isDuplicateColumn returns true if the error is a "duplicate column name" SQLite error.
func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "duplicate column name")
}

// ApplyAll applies all shard schemas and runs migrations (idempotent).
func ApplyAll(db *sql.DB) error {
	for _, fn := range []func(*sql.DB) error{
		ApplyRAGVectors,
		ApplyClaimEntities,
		ApplyEmailDocuments,
		ApplyEmailAttachments,
		ApplyEmailParticipants,
		MigrateEmailDocumentsV2,
	} {
		if err := fn(db); err != nil {
			return err
		}
	}
	return nil
}
