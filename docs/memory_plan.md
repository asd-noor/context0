# Memory Engine — Implementation Plan

Reference implementation: `projectcontext` (Python/FastMCP)
Target: Go, CLI + MCP daemon, SQLite (FTS5 + sqlite-vec)

---

## Overview

The Memory Engine provides long-term, persistent memory storage with hybrid
semantic + keyword search. It is scoped globally (not per-project) and is
accessed via CLI commands and MCP tools exposed by the context0 daemon.

---

## Database Schema

File: `$HOME/.context0/<transformed-project-dir>/memory.sqlite`

```sql
-- Main documents table
CREATE TABLE docs (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    category  TEXT NOT NULL,
    topic     TEXT NOT NULL,
    content   TEXT NOT NULL,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- FTS5 virtual table for keyword search (category + topic + content)
CREATE VIRTUAL TABLE docs_fts USING fts5(
    category,
    topic,
    content,
    content='docs',
    content_rowid='id'
);

-- sqlite-vec virtual table for semantic (vector) search
-- Dimension: 384 (matches embedding model output)
CREATE VIRTUAL TABLE docs_vec USING vec0(
    id        INTEGER PRIMARY KEY,
    embedding float[384]
);
```

**Triggers** (keep FTS5 in sync with docs):
```sql
CREATE TRIGGER docs_fts_insert AFTER INSERT ON docs BEGIN
    INSERT INTO docs_fts(rowid, category, topic, content)
    VALUES (new.id, new.category, new.topic, new.content);
END;

CREATE TRIGGER docs_fts_delete AFTER DELETE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, category, topic, content)
    VALUES ('delete', old.id, old.category, old.topic, old.content);
END;

CREATE TRIGGER docs_fts_update AFTER UPDATE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, category, topic, content)
    VALUES ('delete', old.id, old.category, old.topic, old.content);
    INSERT INTO docs_fts(rowid, category, topic, content)
    VALUES (new.id, new.category, new.topic, new.content);
END;
```

---

## Embedding Model

- Model: `qllama/bge-small-en-v1.5` (384-dim float32 vectors)
- Provider: Ollama or LM Studio (HTTP API, configurable endpoint)
- The daemon pre-loads / keeps the model warm; the CLI connects to the daemon
- First query: ~500ms; subsequent: <200ms

---

## Hybrid Search — Reciprocal Rank Fusion (RRF)

Both keyword (FTS5) and vector (sqlite-vec) searches are executed independently.
Results are merged with RRF:

```
score(d) = 1/(rank_fts(d) + 60) + 1/(rank_vec(d) + 60)
```

Where a document absent from one result set is assigned rank = ∞ (score 0 for
that component). Final list is sorted descending by combined score.

**Parameters:**
- `top_k` (default: 3) — number of results returned after fusion
- RRF constant: 60 (standard)

---

## Operations

### save_memory
- Input: `category string`, `topic string`, `content string`
- Generate embedding for `category + " " + topic + " " + content`
- Insert into `docs`; insert embedding into `docs_vec`; FTS5 trigger fires automatically
- Return: `{ id, topic, category }`

### query_memory
- Input: `query string`, `top_k int` (default 3)
- Run FTS5 MATCH query on `docs_fts` → ranked list
- Generate embedding for query → KNN search on `docs_vec` → ranked list
- Merge via RRF → return top-k rows from `docs` with scores
- Return: `[{ id, category, topic, content, timestamp, score }]`

### update_memory
- Input: `id int`, optional `category`, `topic`, `content`
- Update changed fields in `docs`; regenerate + update embedding if content changed
- FTS5 update trigger fires automatically
- Return: `{ id, topic, category }`

### delete_memory
- Input: `id int`
- Delete from `docs` (cascade to `docs_fts` via trigger) and `docs_vec`
- Return: `{ status }`

---

## Vector Backfill

On daemon startup, check for rows in `docs` with no corresponding row in
`docs_vec`. Generate embeddings for any missing entries and insert them.
This handles database migrations and interrupted writes.

---

## Categories (Recommended, not enforced)

| Category     | Use for                                               |
|--------------|-------------------------------------------------------|
| architecture | Tech choices, design patterns, constraints            |
| fix          | Bug root causes and solutions                         |
| feature      | Feature specifications and requirements               |
| context      | Project context, insights, development summaries      |
| keepsake     | Facts gathered from user or external sources          |

Categories are free-form strings but the above are the standard set. They are
fully searchable via FTS5.

---

## MCP Tools Exposed

| Tool            | Description                                  |
|-----------------|----------------------------------------------|
| `save_memory`   | Persist a memory (category, topic, content)  |
| `query_memory`  | Hybrid search, returns ranked results        |
| `update_memory` | Modify an existing memory by ID              |
| `delete_memory` | Remove a memory by ID                        |

---

## CLI Commands

| Command                      | Description                        |
|------------------------------|------------------------------------|
| `ctx0 memory save`           | Interactive or flag-based save     |
| `ctx0 memory query <text>`   | Search and print results           |
| `ctx0 memory update <id>`    | Update fields of a memory          |
| `ctx0 memory delete <id>`    | Delete a memory                    |

---

## Go Package Layout

```
internal/
  memory/
    db.go        # Schema init, migrations, backfill
    engine.go    # Save, Query (RRF), Update, Delete
    embed.go     # Embedding client (Ollama / LM Studio HTTP)
    rrf.go       # Reciprocal Rank Fusion merge logic
```

---

## Key Implementation Notes

1. **Parameterized queries only** — never string-interpolate SQL.
2. **FTS5 MATCH syntax** — use the virtual table; never LIKE on `docs`.
3. **Vector serialization** — store embeddings as raw `float32` little-endian
   bytes compatible with sqlite-vec's `float[384]` column.
4. **Transaction management** — wrap multi-step writes (docs + docs_vec) in a
   single transaction so partial failures are rolled back.
5. **Thread safety** — use a single shared `*sql.DB` with connection pooling;
   sqlite-vec requires `_journal_mode=WAL` for concurrent reads.
6. **Embedding endpoint** — configurable via env var or config file; default
   `http://localhost:11434` (Ollama) or `http://localhost:1234` (LM Studio).
