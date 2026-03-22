// Package agenda implements the Agenda Engine: per-project task management
// with FTS5 search on agendas.
package agenda

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"

	dbutil "context0/internal/db"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS agendas (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    is_active   INTEGER NOT NULL DEFAULT 1,
    title       TEXT,
    description TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tasks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    agenda_id        INTEGER NOT NULL,
    task_order       INTEGER NOT NULL DEFAULT 0,
    is_optional      INTEGER NOT NULL DEFAULT 0,
    details          TEXT    NOT NULL,
    acceptance_guard TEXT,
    is_completed     INTEGER NOT NULL DEFAULT 0,
    status           TEXT    NOT NULL DEFAULT 'pending',
    FOREIGN KEY (agenda_id) REFERENCES agendas(id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE IF NOT EXISTS agendas_fts USING fts5(
    title,
    description,
    content='agendas',
    content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS agendas_fts_insert AFTER INSERT ON agendas BEGIN
    INSERT INTO agendas_fts(rowid, title, description)
    VALUES (new.id, new.title, new.description);
END;

CREATE TRIGGER IF NOT EXISTS agendas_fts_delete AFTER DELETE ON agendas BEGIN
    INSERT INTO agendas_fts(agendas_fts, rowid, title, description)
    VALUES ('delete', old.id, old.title, old.description);
END;

CREATE TRIGGER IF NOT EXISTS agendas_fts_update AFTER UPDATE ON agendas BEGIN
    INSERT INTO agendas_fts(agendas_fts, rowid, title, description)
    VALUES ('delete', old.id, old.title, old.description);
    INSERT INTO agendas_fts(rowid, title, description)
    VALUES (new.id, new.title, new.description);
END;
`

// Open opens (or creates) the agenda SQLite database for the given project
// directory, applies the schema, runs any pending migrations, and returns
// the db handle.
func Open(projectPath string) (*sql.DB, error) {
	dbPath, err := dbutil.DBPath(projectPath, "agenda.sqlite")
	if err != nil {
		return nil, fmt.Errorf("agenda: resolve db path: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("agenda: open db: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("agenda: apply schema: %w", err)
	}

	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("agenda: migrate schema: %w", err)
	}

	return db, nil
}

// migrateSchema applies incremental schema migrations to existing databases.
// It is safe to run on both new and existing databases; each migration is
// idempotent.
func migrateSchema(db *sql.DB) error {
	// Migration 1: add `status` column to tasks (for databases created before
	// this field was introduced) and backfill from the legacy is_completed flag.
	if err := addStatusColumn(db); err != nil {
		return fmt.Errorf("migration add_status_column: %w", err)
	}
	return nil
}

// addStatusColumn adds the `status` TEXT column to the tasks table if it does
// not already exist, then backfills rows that still carry the legacy
// is_completed value but have not been assigned a status yet.
func addStatusColumn(db *sql.DB) error {
	// Check whether the column already exists via PRAGMA table_info.
	rows, err := db.Query(`PRAGMA table_info(tasks)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	hasStatus := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "status" {
			hasStatus = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !hasStatus {
		// Add column with safe default so existing rows become 'pending'.
		if _, err := db.Exec(
			`ALTER TABLE tasks ADD COLUMN status TEXT NOT NULL DEFAULT 'pending'`,
		); err != nil {
			return fmt.Errorf("alter table add status: %w", err)
		}

		// Backfill: rows that were previously completed via the boolean flag.
		if _, err := db.Exec(
			`UPDATE tasks SET status = 'completed' WHERE is_completed = 1`,
		); err != nil {
			return fmt.Errorf("backfill status from is_completed: %w", err)
		}
	}

	return nil
}
