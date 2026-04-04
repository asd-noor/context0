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
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    is_active        INTEGER  NOT NULL DEFAULT 1,
    git_branch       TEXT     NOT NULL DEFAULT '',
    priority         TEXT     NOT NULL DEFAULT 'normal',
    title            TEXT,
    description      TEXT,
    acceptance_guard TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at     DATETIME,
    deleted_at       DATETIME
);

CREATE TABLE IF NOT EXISTS tasks (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    agenda_id INTEGER NOT NULL,
    details   TEXT    NOT NULL,
    notes     TEXT    NOT NULL DEFAULT '',
    status    TEXT    NOT NULL DEFAULT 'pending',
    FOREIGN KEY (agenda_id) REFERENCES agendas(id) ON DELETE CASCADE
);

CREATE VIRTUAL TABLE IF NOT EXISTS agendas_fts USING fts5(
    title,
    description,
    acceptance_guard,
    content='agendas',
    content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS agendas_fts_insert AFTER INSERT ON agendas BEGIN
    INSERT INTO agendas_fts(rowid, title, description, acceptance_guard)
    VALUES (new.id, new.title, new.description, new.acceptance_guard);
END;

CREATE TRIGGER IF NOT EXISTS agendas_fts_delete AFTER DELETE ON agendas BEGIN
    INSERT INTO agendas_fts(agendas_fts, rowid, title, description, acceptance_guard)
    VALUES ('delete', old.id, old.title, old.description, old.acceptance_guard);
END;

CREATE TRIGGER IF NOT EXISTS agendas_fts_update AFTER UPDATE ON agendas BEGIN
    INSERT INTO agendas_fts(agendas_fts, rowid, title, description, acceptance_guard)
    VALUES ('delete', old.id, old.title, old.description, old.acceptance_guard);
    INSERT INTO agendas_fts(rowid, title, description, acceptance_guard)
    VALUES (new.id, new.title, new.description, new.acceptance_guard);
END;

-- tasks_fts indexes task details and notes against the tasks content table.
-- agenda_id is resolved via the tasks table (tasks_fts.rowid = tasks.id).
CREATE VIRTUAL TABLE IF NOT EXISTS tasks_fts USING fts5(
    details,
    notes,
    content='tasks',
    content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS tasks_fts_insert AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, details, notes)
    VALUES (new.id, new.details, new.notes);
END;

CREATE TRIGGER IF NOT EXISTS tasks_fts_delete AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, details, notes)
    VALUES ('delete', old.id, old.details, old.notes);
END;

CREATE TRIGGER IF NOT EXISTS tasks_fts_update AFTER UPDATE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, details, notes)
    VALUES ('delete', old.id, old.details, old.notes);
    INSERT INTO tasks_fts(rowid, details, notes)
    VALUES (new.id, new.details, new.notes);
END;
`

// Open opens (or creates) the agenda SQLite database for the given project
// directory, applies the schema, and returns the db handle.
func Open(projectPath string) (*sql.DB, error) {
	dbPath, err := dbutil.DBPath(projectPath, "agenda-ctx0.sqlite")
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

	return db, nil
}
