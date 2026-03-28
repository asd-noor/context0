# Agenda Engine — Implementation Plan

Reference implementation: `projectcontext` (Python/FastMCP)
Target: Go, CLI + MCP daemon, SQLite (FTS5)

---

## Overview

The Agenda Engine provides project-scoped, structured task management.
An **agenda** is a named plan with an ordered list of **tasks**. It is designed
for multi-step workflows that span multiple sessions. The engine exposes CRUD
operations via CLI and MCP tools.

Agendas are stored per-project (keyed by the git root / CWD), separate from
the global Memory Engine.

---

## Database Schema

File: `$HOME/.context0/<transformed-project-dir>/agenda.sqlite`

```sql
-- Agendas (plans / todo lists)
CREATE TABLE agendas (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    is_active   INTEGER NOT NULL DEFAULT 1,
    title       TEXT,
    description TEXT,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Tasks belonging to an agenda
CREATE TABLE tasks (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    agenda_id        INTEGER NOT NULL,
    task_order       INTEGER NOT NULL DEFAULT 0,
    is_optional      INTEGER NOT NULL DEFAULT 0,
    details          TEXT    NOT NULL,
    acceptance_guard TEXT,
    is_completed     INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (agenda_id) REFERENCES agendas(id) ON DELETE CASCADE
);

-- FTS5 on agenda title + description for fast text search
CREATE VIRTUAL TABLE agendas_fts USING fts5(
    title,
    description,
    content='agendas',
    content_rowid='id'
);
```

**Triggers** (keep FTS5 in sync):
```sql
CREATE TRIGGER agendas_fts_insert AFTER INSERT ON agendas BEGIN
    INSERT INTO agendas_fts(rowid, title, description)
    VALUES (new.id, new.title, new.description);
END;

CREATE TRIGGER agendas_fts_delete AFTER DELETE ON agendas BEGIN
    INSERT INTO agendas_fts(agendas_fts, rowid, title, description)
    VALUES ('delete', old.id, old.title, old.description);
END;

CREATE TRIGGER agendas_fts_update AFTER UPDATE ON agendas BEGIN
    INSERT INTO agendas_fts(agendas_fts, rowid, title, description)
    VALUES ('delete', old.id, old.title, old.description);
    INSERT INTO agendas_fts(rowid, title, description)
    VALUES (new.id, new.title, new.description);
END;
```

**Auto-deactivation rule:** After any `update_task` call, if all non-optional
tasks for that agenda are completed, set `agendas.is_active = 0` automatically.

---

## Data Model

### Agenda

| Field       | Type    | Notes                                         |
|-------------|---------|-----------------------------------------------|
| id          | int     | Auto-generated primary key                    |
| is_active   | bool    | `true` until all required tasks are done      |
| title       | string  | Short name (e.g. "Refactor Auth System")      |
| description | string  | Detailed goal; indexed by FTS5                |
| created_at  | datetime|                                               |

### Task

| Field            | Type   | Notes                                             |
|------------------|--------|---------------------------------------------------|
| id               | int    | Auto-generated primary key                        |
| agenda_id        | int    | FK → agendas.id                                   |
| task_order       | int    | 0-based ordering within the agenda                |
| is_optional      | bool   | If true, does not block auto-deactivation         |
| details          | string | Specific instruction for this step                |
| acceptance_guard | string | Definition of Done (e.g. "tests pass + coverage > 80%") |
| is_completed     | bool   | Set to true when the step is finished             |

---

## Operations

### create_agenda
- Input: `title string` (opt), `description string` (opt), `tasks []TaskInput`
- `TaskInput`: `{ details, is_optional bool, acceptance_guard string (opt) }`
- Insert agenda row; insert tasks with sequential `task_order` values
- Return: `{ agenda_id }`

### list_agendas
- Input: `active_only bool` (default: true)
- Return: `[{ id, is_active, title, description, created_at }]`

### get_agenda
- Input: `agenda_id int`
- Return full agenda row + all tasks ordered by `task_order`

### search_agendas
- Input: `query string`, `limit int` (default: 10)
- FTS5 MATCH on `agendas_fts` (title + description)
- Return: `[{ id, title, description }]`

### update_task
- Input: `task_id int`, `is_completed bool`
- Update `tasks.is_completed`
- After update: check auto-deactivation condition for parent agenda
- Return: `{ status }`

### update_agenda
- Input: `agenda_id int`, optional `title`, `description`, `is_active bool`,
  `new_tasks []TaskInput`
- Update metadata fields if provided
- If `new_tasks` provided: append with `task_order` continuing from last existing
- Note: setting `is_active = false` is irreversible
- FTS5 update trigger fires automatically for title/description changes
- Return: `{ status }`

### delete_agenda
- Input: `agenda_id int`
- Precondition: `is_active` must be `false`; reject with error otherwise
- Delete agenda (cascades to tasks and FTS5 via trigger)
- Return: `{ status }`

---

## MCP Tools Exposed

| Tool             | Description                                          |
|------------------|------------------------------------------------------|
| `create_agenda`  | Create a new plan with tasks                         |
| `list_agendas`   | List all (or active-only) agendas                    |
| `get_agenda`     | Get full detail of one agenda including tasks        |
| `search_agendas` | FTS5 search by title/description                     |
| `update_task`    | Mark a task completed or pending                     |
| `update_agenda`  | Edit metadata, add tasks, or deactivate an agenda    |
| `delete_agenda`  | Delete an inactive agenda (irreversible)             |

---

## CLI Commands

| Command                          | Description                          |
|----------------------------------|--------------------------------------|
| `ctx0 agenda create`             | Create agenda (interactive/flags)    |
| `ctx0 agenda list`               | List active agendas                  |
| `ctx0 agenda list --all`         | List all agendas including inactive  |
| `ctx0 agenda get <id>`           | Show full agenda with tasks          |
| `ctx0 agenda search <query>`     | Search by title/description          |
| `ctx0 agenda task done <id>`     | Mark task as completed               |
| `ctx0 agenda task reopen <id>`   | Mark task as pending                 |
| `ctx0 agenda update <id>`        | Edit agenda or add tasks             |
| `ctx0 agenda delete <id>`        | Delete an inactive agenda            |

---

## Go Package Layout

```
internal/
  agenda/
    db.go      # Schema init, migrations
    engine.go  # Create, List, Get, Search, UpdateTask, UpdateAgenda, Delete
```

No embedding model is needed for the Agenda Engine — it uses FTS5 only.

---

## Key Implementation Notes

1. **No vector search** — agenda search is FTS5-only (title + description).
   This keeps the engine lightweight and avoids embedding latency.
2. **Auto-deactivation** — implemented in `update_task` as a post-update check:
   `SELECT COUNT(*) FROM tasks WHERE agenda_id=? AND is_optional=0 AND is_completed=0`.
   If count is 0, set `is_active=0` on the agenda.
3. **task_order** — always assigned at insert time; new tasks appended via
   `SELECT COALESCE(MAX(task_order), -1) + 1 FROM tasks WHERE agenda_id=?`.
4. **Delete guard** — `delete_agenda` must verify `is_active = 0` before
   proceeding; return a descriptive error if the agenda is still active.
5. **Cascade delete** — `ON DELETE CASCADE` on `tasks.agenda_id` ensures tasks
   are removed when an agenda is deleted.
6. **Parameterized queries only** — never string-interpolate SQL.
7. **WAL mode** — enable `PRAGMA journal_mode=WAL` for concurrent read safety
   (shared with memory.sqlite if they are separate files).
