# Architecture

## Overview

Context0 is structured as three independent engines sharing a common CLI shell and per-project data directory. Each engine has its own SQLite database and operates independently -- you can use Memory without Agenda, or CodeMap without either.

```
context0 (CLI binary)
  ├── memory   → memory.sqlite    (FTS5 + sqlite-vec)
  ├── agenda   → agenda.sqlite    (FTS5)
  └── codemap  → codemap.sqlite   (relational graph)
```

All databases are stored under `~/.context0/<project-path>/` where the project path has separators replaced by equals signs (e.g. `/home/user/project` becomes `home=user=project`).

## Tech stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| CLI framework | cobra |
| Storage | SQLite (WAL mode) via `mattn/go-sqlite3` (CGo) |
| Full-text search | SQLite FTS5 |
| Vector search | sqlite-vec via `asg017/sqlite-vec-go-bindings` (CGo) |
| AST parsing | Tree-sitter via `tree-sitter/go-tree-sitter` (CGo, official bindings) |
| LSP integration | JSON-RPC 2.0 over stdio (custom client) |
| File watching | fsnotify |
| Embedding inference | Ollama HTTP API (`/v1/embeddings`, OpenAI-compatible) |
| Embedding model | `qllama/bge-small-en-v1.5` (384 dimensions) |

## Repository structure

```
context0/
├── main.go                     # Entry point: root command + --project flag
├── go.mod
├── mise.toml                   # Tool versions + build tasks
├── cmd/
│   ├── memory/memory.go        # memory subcommands
│   ├── agenda/agenda.go        # agenda subcommands
│   └── codemap/codemap.go      # codemap subcommands
├── internal/
│   ├── db/path.go              # Per-project DB path resolution
│   ├── daemon/daemon.go        # PID file management + detached process spawn
│   ├── memory/                 # Memory engine
│   │   ├── db.go               # Schema: docs, docs_fts, docs_vec, triggers
│   │   ├── embed.go            # HTTP embedding client (Ollama / LM Studio)
│   │   ├── engine.go           # SaveMemory, QueryMemory, UpdateMemory, DeleteMemory
│   │   └── rrf.go              # Reciprocal Rank Fusion merger
│   ├── agenda/                 # Agenda engine
│   │   ├── db.go               # Schema: agendas, tasks, agendas_fts, triggers
│   │   └── engine.go           # CRUD + FTS5 search + task lifecycle
│   ├── graph/                  # Semantic code graph
│   │   ├── types.go            # Node, Edge, Relation constants
│   │   └── store.go            # SQLite graph store + ErrNotIndexed + OpenReadOnly
│   ├── scanner/                # Tree-sitter AST scanner
│   │   ├── queries.go          # S-expression queries per language
│   │   └── scanner.go          # Directory walker -> graph nodes
│   ├── lsp/                    # LSP client
│   │   ├── types.go            # JSON-RPC + LSP message types
│   │   ├── transport.go        # Content-Length framing (read/write)
│   │   └── lsp.go              # Client subprocess pool + Enrich()
│   ├── watcher/watcher.go      # fsnotify -> incremental re-index + idle auto-stop
│   ├── codemapserver/          # Code exploration engine wiring
│   │   ├── server.go           # Index lifecycle; New() and NewWatch() constructors
│   │   └── query.go            # Shared CLI helpers (NodeWithSource, etc.)
│   └── pkgmgr/                 # LSP binary resolver
│       ├── manager.go          # ResolveBinary: PATH -> cache -> download
│       ├── metadata.go         # Per-binary install metadata + download URLs
│       └── upgrade.go          # Background version check + silent reinstall
└── util/
    ├── hash.go                 # SHA256 node ID generation
    ├── uri.go                  # file:// URI <-> OS path conversion
    └── git.go                  # Git root detection
```

---

## Memory Engine

### Schema (`memory.sqlite`)

```sql
docs     (id INTEGER PK, category TEXT, topic TEXT, content TEXT, timestamp DATETIME)
docs_fts USING fts5(category, topic, content, content='docs')
docs_vec USING vec0(id INTEGER PRIMARY KEY, embedding float[384])
```

FTS5 is synchronized with `docs` via three SQL triggers (INSERT, UPDATE, DELETE). Vector entries are managed explicitly by the engine.

### Save flow

```
SaveMemory(category, topic, content)
  ├── Embed(category + " " + topic + " " + content)
  │    └── POST /v1/embeddings -> []float32 (384-dim)
  ├── sqlite_vec.SerializeFloat32(embedding)
  ├── BEGIN
  ├── INSERT INTO docs -> trigger fires INSERT INTO docs_fts
  ├── INSERT INTO docs_vec
  └── COMMIT
```

Embedding happens before any DB write. If Ollama is unreachable, the operation fails entirely -- no partial writes.

### Query flow

```
QueryMemory(query, topK)
  ├── Embed(query) -> query vector
  ├── FTS5 leg: SELECT FROM docs_fts MATCH ? ORDER BY rank (BM25) LIMIT topK*5
  ├── Vector leg: SELECT FROM docs_vec WHERE embedding MATCH ? AND k=topK*5 (KNN)
  ├── MergeRRF(ftsIDs, vecIDs)
  │    └── score = sum(1/(rank + 60)) per list, sorted descending
  └── Fetch top-K docs by fused score
```

Both search legs over-fetch by 5x before fusion to reduce rank cutoff bias. The RRF constant of 60 follows the original Cormack et al. recommendation.

### Update and delete

- **Update**: fetches current values, merges non-empty fields, re-embeds, updates doc + vector in a transaction.
- **Delete**: removes vector first, then doc (trigger cleans up FTS5).

---

## Agenda Engine

### Schema (`agenda.sqlite`)

```sql
agendas     (id INTEGER PK, is_active BOOL, title TEXT, description TEXT, created_at DATETIME)
tasks       (id INTEGER PK, agenda_id FK, task_order INT, is_optional BOOL,
             details TEXT, acceptance_guard TEXT, is_completed INT, status TEXT)
agendas_fts USING fts5(title, description, content='agendas')
```

`status` is the canonical task state: `pending` | `in_progress` | `completed`. The legacy `is_completed` integer column is kept for backwards compatibility and is kept in sync with `status` on every write (`completed` → 1, everything else → 0).

**Schema migration**: On open, `Open()` runs `migrateSchema()` which detects absence of the `status` column via `PRAGMA table_info(tasks)` and adds it, backfilling `completed` from rows where `is_completed=1`. This is safe to run on both new and existing databases.

Tasks cascade on agenda delete. FTS5 is trigger-maintained.

### Key behaviours

- **Auto-deactivation**: when a task status is updated, the engine checks if all non-optional tasks for the agenda have `status = 'completed'`. If so, `is_active` is set to false. Tasks with status `in_progress` or `pending` keep the agenda active.
- **Active guard on delete**: active agendas cannot be deleted.
- **Scoped task numbering**: tasks are displayed and addressed by 1-based order within their agenda (e.g. `task done 5 2` = agenda 5, task #2), not by global database ID.
- **Acceptance guards**: each task can have a "Done when:" condition. Agents should verify this condition before marking the task complete.
- **Task lifecycle**: `pending` → `in_progress` → `completed` (and back via `reopen`). Any transition between states is permitted.
- **No embeddings**: search is FTS5-only. Agenda queries are keyword-oriented, making vector search unnecessary.

---

## Code Exploration Engine

### Component architecture

```
codemapserver.Server
  ├── scanner.Scanner      Tree-sitter: source files -> Nodes (symbols)
  ├── lsp.Service          LSP clients: Nodes -> Edges (cross-references)
  ├── graph.Store          SQLite: stores Nodes + Edges
  └── watcher.Watcher      fsnotify: drives incremental re-index on file changes
```

Two constructors:
- `New(ctx, rootDir)` -- for CLI commands (`index`, `symbol`, etc.) and `watch --foreground`; idle timeout is suppressed (no-op cancel).
- `NewWatch(ctx, cancel, rootDir)` -- for the background `watch` daemon; idle timeout fires `cancel()` to exit cleanly after 5 minutes of inactivity.

### Schema (`codemap.sqlite`)

```sql
nodes (id TEXT PK, name TEXT, kind TEXT, file_path TEXT,
       line_start INT, line_end INT, col_start INT, col_end INT,
       name_line INT, name_col INT, symbol_uri TEXT)
edges (source_id TEXT, target_id TEXT, relation TEXT)
```

- **Node ID**: `SHA256(filePath:name:kind)[:16]` -- stable 32-char hex.
- **`name_line`/`name_col`**: position of the name identifier token. LSP enrichment uses these for cursor placement (not `line_start`/`col_start`, which point to the declaration keyword).
- **Edge relations**: `calls`, `implements`, `references`, `imports`.

### Index lifecycle

```
Full index (runIndex -> doIndex):
  1. store.Clear()
  2. scanner.Scan(ctx, root)        -> []Node
  3. store.BulkUpsertNodes(nodes)
  4. lsp.Enrich(ctx, nodes, store)  -> []Edge
  5. store.BulkUpsertEdges(edges)

Status transitions: Idle -> InProgress -> Ready | Failed
```

### Tree-sitter scanning

The scanner walks the project directory, parses each supported file with Tree-sitter, and runs language-specific S-expression queries to extract symbol nodes.

Supported languages: Go, Python, JavaScript, TypeScript, Lua, Zig.

Skipped: `vendor/`, `node_modules/`, `__pycache__/`, `.git/`, `.venv/`, `zig-cache/`, `zig-out/`, generated files. Respects `.gitignore`.

### LSP enrichment

After scanning, the LSP service enriches the graph with cross-reference edges:

```
lsp.Service.Enrich(ctx, nodes, store)
  ├── Group nodes by language
  └── Per language: fan out 10 workers
       enrichNode(ctx, client, langID, node, store):
         ├── didOpen(uri)
         ├── textDocument/references(uri, name_line, name_col)
         │    -> for each location: store.FindNode -> Edge{calls}
         ├── textDocument/implementation(uri, ...)  [interfaces/types only]
         │    -> Edge{implements}
         └── didClose(uri)
```

### LSP binary management

Binaries are resolved in order: **PATH** -> **cache** (`~/.context0/bin/`) -> **auto-download**.

After resolution, a background goroutine checks for a newer version via the upstream registry (GitHub releases, npm, pip, Go module proxy) and silently reinstalls if one is found. Checked once per binary per process lifetime via `sync.Map`.

Supported servers: `gopls`, `pylsp`, `typescript-language-server`, `lua-language-server`, `zls`.

### Watcher and daemon

```
watcher.Run(ctx, cancel)
  ├── fsnotify events -> filter gitignore, debounce 500ms
  ├── Flush: per changed file:
  │    ├── Delete old nodes for file
  │    ├── scanner.ScanFile -> new nodes
  │    ├── lsp.Enrich -> new edges
  │    └── Upsert to store
  └── 5-minute idle timer -> cancel() -> daemon exits
```

`codemap watch` (background) spawns a detached background process via `daemon.Spawn()`. The child re-executes itself with a hidden `--daemon` flag, writes a PID file, and detaches from the parent session. The idle timer is active: the process self-terminates after 5 minutes of file inactivity.

`codemap watch --foreground` runs the watcher in the calling process. It uses `codemapserver.New()` (no-op cancel) so the idle timer never fires. The process blocks until SIGINT or SIGTERM is received, at which point it cleans up the PID file and exits. Intended for process supervisors that manage the process lifetime externally.

---

## Key design decisions

**CLI-only.** No MCP server, no HTTP API. AI agents invoke CLI commands directly via skill files. This keeps the tool simple, stateless between invocations, and easy to integrate with any agent framework.

**Per-project SQLite.** Each engine gets its own SQLite file per project. No shared databases, no external database services, no synchronization between projects.

**Hybrid search for Memory.** FTS5 alone fails on vocabulary mismatch. Vector search alone misses exact keyword matches. RRF fusion gives better recall than either approach alone.

**Hard fail on embed error.** If Ollama is unreachable, `SaveMemory` writes nothing. This preserves the invariant that every doc has a corresponding vector. Silently saving without a vector would degrade search quality.

**FTS5-only for Agenda.** Agenda queries are keyword-oriented (task title, status lookup). Embeddings add no value and would create an unnecessary dependency on the embedding server.

**Tree-sitter + LSP separation.** Tree-sitter provides fast, offline symbol extraction. LSP provides edges that require a running language server. The index partially succeeds even if LSP servers are unavailable.

**Debounced incremental re-indexing.** 500ms debounce prevents excessive LSP calls during rapid edits. The 5-minute idle auto-stop avoids holding LSP server processes indefinitely.

**Scoped task numbering.** Task IDs shown to users are 1-based order within their agenda, not global database auto-increments. This prevents confusing gaps when agendas are deleted.

**Acceptance guards.** Each task can carry a "Done when:" condition. The skill instructions direct agents to verify the guard before marking a task complete, preventing premature completion.
