// Package memory implements the Memory Engine: persistent, per-project
// document storage with hybrid FTS5 + vector (sqlite-vec) search.
package memory

import (
	"database/sql"
	"fmt"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"

	dbutil "context0/internal/db"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS docs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    category  TEXT NOT NULL,
    topic     TEXT NOT NULL,
    content   TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
    category,
    topic,
    content,
    content='docs',
    content_rowid='id'
);

CREATE VIRTUAL TABLE IF NOT EXISTS docs_vec USING vec0(
    id        INTEGER PRIMARY KEY,
    embedding float[384]
);

CREATE TRIGGER IF NOT EXISTS docs_fts_insert AFTER INSERT ON docs BEGIN
    INSERT INTO docs_fts(rowid, category, topic, content)
    VALUES (new.id, new.category, new.topic, new.content);
END;

CREATE TRIGGER IF NOT EXISTS docs_fts_delete AFTER DELETE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, category, topic, content)
    VALUES ('delete', old.id, old.category, old.topic, old.content);
END;

CREATE TRIGGER IF NOT EXISTS docs_fts_update AFTER UPDATE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, category, topic, content)
    VALUES ('delete', old.id, old.category, old.topic, old.content);
    INSERT INTO docs_fts(rowid, category, topic, content)
    VALUES (new.id, new.category, new.topic, new.content);
END;
`

func init() {
	sqlite_vec.Auto()
}

// Open opens (or creates) the memory SQLite database for the given project
// directory, applies the schema, and returns the db handle.
func Open(projectPath string) (*sql.DB, error) {
	dbPath, err := dbutil.DBPath(projectPath, "memory-ctx0.sqlite")
	if err != nil {
		return nil, fmt.Errorf("memory: resolve db path: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_foreign_keys=on&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}

	if err := applySchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func applySchema(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("memory: apply schema: %w", err)
	}
	return nil
}
