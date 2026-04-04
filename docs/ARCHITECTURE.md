# Architecture

## Overview

Context0 is structured as four independent engines sharing a common CLI shell and per-project data directory. Each Go-based engine has its own SQLite database and operates independently. The Python sidecar is a separate process that all three Go engines can delegate to.

```
context0 (CLI binary)
  ├── memory   → memory-ctx0.sqlite     (FTS5 + sqlite-vec)
  ├── agenda   → agenda-ctx0.sqlite     (FTS5)
  ├── codemap  → <project>-ctx0.sqlite  (relational graph)
  └── sidecar  → ~/.context0/channel.sock  (Unix Domain Socket)
```

All databases are stored under `~/.context0/<project-path>/` where the project path has separators replaced by equals signs (e.g. `/home/user/project` becomes `home=user=project`). Database filenames carry a `-ctx0` suffix to make context0 files easily identifiable on disk.

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
| Embedding inference | Python sidecar (MLX, `BAAI/bge-small-en-v1.5`, 384 dimensions) |
| LLM inference | Python sidecar (MLX, `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit`) |
| Sidecar runtime | Python ≥ 3.11, uv, mlx / mlx-lm / mlx-embeddings, huggingface-hub |

## Repository structure

```
context0/
├── main.go                     # Entry point: root command + --project flag + --daemon/--kill-daemon
├── go.mod
├── pyproject.toml              # Python sidecar dependency manifest (uv)
├── uv.lock
├── mise.toml                   # Tool versions + build tasks
├── cmd/
│   ├── memory/memory.go        # memory subcommands
│   ├── agenda/agenda.go        # agenda subcommands (plan + task sub-trees)
│   ├── codemap/codemap.go      # codemap subcommands + --src-root flag
│   ├── ask/ask.go              # ask command (delegates to sidecar)
│   ├── exec/exec.go            # exec command (delegates to sidecar)
│   ├── backup/backup.go        # backup: snapshot to ~/.context0/backup/<enc>/<ts>.tar.gz
│   ├── recover/recover.go      # recover: restore latest snapshot from ~/.context0/backup/<enc>/
│   ├── export/export.go        # export: pack databases to user-specified .tar.gz
│   └── import/import.go        # import: restore from arbitrary .tar.gz (snapshots first)
├── internal/
│   ├── db/path.go              # Per-project DB path resolution + CodeMapDBName()
│   ├── archive/archive.go      # Shared tar.gz helpers: Write, Extract, Snapshot, BackupDir
│   ├── daemon/daemon.go        # PID file management + detached process spawn
│   ├── sidecar/sidecar.go      # Sidecar lifecycle (Start/Stop/IsRunning) + UDS client
│   ├── memory/                 # Memory engine
│   │   ├── db.go               # Schema: docs, docs_fts, docs_vec, triggers
│   │   ├── embed.go            # Sidecar embed client (sends "embed" command over UDS)
│   │   ├── engine.go           # SaveMemory, QueryMemory, UpdateMemory, DeleteMemory
│   │   └── rrf.go              # Reciprocal Rank Fusion merger
│   ├── agenda/                 # Agenda engine
│   │   ├── db.go               # Schema: agendas, tasks, agendas_fts, triggers
│   │   └── engine.go           # CRUD + FTS5 search + task lifecycle + AddTask
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
├── sidecar/                    # Python sidecar server
│   ├── main.py                 # Entry point: preload models, bind socket, serve
│   ├── server.py               # Asyncio UDS server + dispatcher
│   ├── embed.py                # MLX embedding engine (bge-small-en-v1.5)
│   ├── inference.py            # MLX inference engine (Qwen2.5-Coder-3B)
│   ├── ask.py                  # Orchestration loop: plan CLI commands + compress answer
│   ├── ralph.py                # Ralph-loop: uv-run Python script with self-correction
│   ├── downloader.py           # Hugging Face Hub model cache manager
│   ├── prompts.py              # Prompt templates (ask, exec repair, discover)
│   └── protocol.py             # Command name constants
└── util/
    ├── hash.go                 # SHA256 node ID generation
    ├── uri.go                  # file:// URI <-> OS path conversion
    └── git.go                  # Git root detection
```

---

## Python Sidecar

### Role

The sidecar is an always-on background process that provides two services the Go binary cannot do natively: **local ML inference** and **orchestrated multi-step reasoning**.

- **Embedding** — every `memory save` / `memory query` call routes through the sidecar's `embed` endpoint. This replaces the previous Ollama dependency.
- **Inference** — used by `ask`, `exec`, and `discover` for planning, generation, and self-correction.

### Startup sequence (`context0 --daemon`)

```
1. Write PID  → ~/.context0/sidecar.pid
2. Load embed model  (BAAI/bge-small-en-v1.5, cached in ~/.context0/models/)
3. Load infer model  (mlx-community/Qwen2.5-Coder-3B-Instruct-4bit, cached)
4. Bind Unix socket  → ~/.context0/channel.sock
5. Serve until SIGTERM / SIGINT
```

Model downloads happen once on first startup via `huggingface-hub`; subsequent starts use the local cache.

### Protocol

Newline-delimited JSON over Unix Domain Socket. One connection per request:

```
Client → Server: {"cmd": "...", ...}\n
Server → Client: {"ok": true/false, ...}\n  (then closes connection)
```

Commands: `ping`, `embed`, `generate`, `ask`, `exec`, `discover`.

### Concurrency

The asyncio event loop handles I/O. Model calls are offloaded to a thread-pool executor. Two independent `asyncio.Lock` objects serialize the embed and inference engines separately, so an `ask` call (which acquires `infer_lock`) can still trigger a `memory query` inside its subprocess, which calls `embed` (acquires `embed_lock` independently) without deadlocking.

### Ralph-loop (`exec`, `discover`)

```
ralph_exec(script, project, inference):
  for attempt in 0..MAX_RETRIES (2):
    output, err = uv run - <<< script
    if err is None: return output
    if attempt == MAX_RETRIES: return last_error
    fixed = inference.generate(repair prompt + script + err)
    if fixed == script: abort (no improvement)
    script = fixed
```

On failure the inference model receives the traceback and attempts a fix. Handles missing imports, syntax errors, and off-by-one logic. Gives up after 2 failed repair attempts.

### `ask` orchestration

```
ask(query, project, inference, run_cmd):
  1. inference.generate(plan prompt + query)
     → list of context0 CLI commands to run
  2. For each command: run_cmd(args) → output
  3. inference.generate(compress prompt + outputs)
     → single compressed answer
```

The sidecar plans which `memory`, `codemap`, and `agenda` subcommands to call, executes them as subprocesses (re-using the `context0` binary), and compresses the results into a single answer.

### Paths (overridable via environment variables)

| Path | Env var | Default |
|---|---|---|
| Socket | `CTX0_SOCKET` | `~/.context0/channel.sock` |
| PID file | `CTX0_SIDECAR_PID` | `~/.context0/sidecar.pid` |
| Embedding model | `CTX0_EMBED_MODEL` | `BAAI/bge-small-en-v1.5` |
| Inference model | `CTX0_INFER_MODEL` | `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit` |

---

## Memory Engine

### Schema (`memory-ctx0.sqlite`)

```sql
docs     (id INTEGER PK, category TEXT, topic TEXT, content TEXT, timestamp DATETIME)
docs_fts USING fts5(category, topic, content, content='docs')
docs_vec USING vec0(id INTEGER PRIMARY KEY, embedding float[384])
```

FTS5 is synchronized with `docs` via three SQL triggers (INSERT, UPDATE, DELETE). Vector entries are managed explicitly by the engine.

### Save flow

```
SaveMemory(category, topic, content)
  ├── sidecar.Send("embed", text) -> []float32 (384-dim)
  ├── sqlite_vec.SerializeFloat32(embedding)
  ├── BEGIN
  ├── INSERT INTO docs -> trigger fires INSERT INTO docs_fts
  ├── INSERT INTO docs_vec
  └── COMMIT
```

Embedding is requested from the running sidecar before any DB write. If the sidecar is unreachable, the operation fails entirely -- no partial writes.

### Query flow

```
QueryMemory(query, topK)
  ├── sidecar.Send("embed", query) -> query vector
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

### Schema (`agenda-ctx0.sqlite`)

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
- **Task addition**: `AddTask()` appends a new task to an existing plan at any time, regardless of the plan's active state.
- **No embeddings**: search is FTS5-only. Agenda queries are keyword-oriented, making vector search unnecessary.

### CLI structure

The agenda command tree has two sub-groups:

```
context0 agenda plan   list / get / create / search / update / delete
context0 agenda task   add / start / done / reopen
```

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
- `New(ctx, rootDir, srcRoot)` -- for CLI commands (`index`, `symbol`, etc.) and `watch --foreground`; idle timeout is suppressed (no-op cancel).
- `NewWatch(ctx, cancel, rootDir, srcRoot)` -- for the background `watch` daemon; idle timeout fires `cancel()` to exit cleanly after 5 minutes of inactivity.

### Schema (`<project>-ctx0.sqlite`)

```sql
nodes (id TEXT PK, name TEXT, kind TEXT, file_path TEXT,
       line_start INT, line_end INT, col_start INT, col_end INT,
       name_line INT, name_col INT, symbol_uri TEXT)
edges (source_id TEXT, target_id TEXT, relation TEXT)
```

- **Node ID**: `SHA256(filePath:name:kind)[:16]` -- stable 32-char hex.
- **`name_line`/`name_col`**: position of the name identifier token. LSP enrichment uses these for cursor placement (not `line_start`/`col_start`, which point to the declaration keyword).
- **Edge relations**: `calls`, `implements`, `references`, `imports`.

### Database naming and `--src-root`

The codemap DB filename is derived from `--src-root` via `db.CodeMapDBName(srcRoot)`:

| `--src-root` value | Resulting DB name |
|---|---|
| *(empty)* | `codemap-ctx0.sqlite` |
| bare name (e.g. `myrepo`) | `myrepo-ctx0.sqlite` |
| absolute path (e.g. `/home/alice/myrepo/src`) | `home=alice=myrepo=src-ctx0.sqlite` |

`--src-root` defaults to `filepath.Base(projectDir)` so the DB is named after the project (e.g. `context0-ctx0.sqlite`). A bare basename is a DB-naming label only and does not override the scan directory; only a value containing a path separator changes the actual directory that is scanned.

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

## Data Management

Four commands handle database backup and portability, all sharing helpers from `internal/archive`.

### Commands

| Command   | Args            | Behavior |
|-----------|-----------------|----------|
| `backup`  | none            | Calls `archive.Snapshot(dataDir)` → writes `~/.context0/backup/<enc>/<timestamp>.tar.gz` |
| `recover` | none            | Reads latest `.tar.gz` from `~/.context0/backup/<enc>/`, snapshots current state, then extracts |
| `export`  | `--output / -o` | Packs data dir into a user-specified `.tar.gz` (defaults to CWD with auto-generated filename) |
| `import`  | `<file.tar.gz>` | Snapshots current state, then extracts from the given archive |

`backup`/`recover` are the automatic pair (managed location, no arguments).  
`export`/`import` are the manual pair (user-controlled paths, suitable for cross-machine transfer).

### `internal/archive` API

```
Write(destPath string, files []string) error
  — creates a .tar.gz storing files by base name only (portable)

Extract(dataDir, archivePath string) (int, error)
  — unpacks a .tar.gz into dataDir, skipping PID files, atomic via temp+rename

Snapshot(dataDir string) (string, error)
  — packs dataDir into ~/.context0/backup/<base(dataDir)>/<timestamp>.tar.gz
  — returns "" (no error) if dataDir is empty or doesn't exist yet

BackupDir(dataDir string) (string, error)
  — returns ~/.context0/backup/<base(dataDir)> without creating it
```

Archives store only base file names (no directory prefix) so they are portable across machines with different project paths. PID files (`*.pid`) are excluded from both reads and writes.

---

## Key design decisions

**CLI-only.** No MCP server, no HTTP API. AI agents invoke CLI commands directly via skill files. This keeps the tool simple, stateless between invocations, and easy to integrate with any agent framework.

**Per-project SQLite.** Each engine gets its own SQLite file per project. No shared databases, no external database services, no synchronization between projects.

**`-ctx0` suffix on database names.** All three engine databases carry a `-ctx0` suffix (e.g. `memory-ctx0.sqlite`) so context0 files are immediately identifiable alongside other project files on disk.

**Sidecar replaces Ollama.** The embedding client was refactored from an Ollama HTTP client to the local MLX sidecar. This removes the dependency on a separately managed Ollama daemon and keeps all inference local to the binary distribution.

**Hybrid search for Memory.** FTS5 alone fails on vocabulary mismatch. Vector search alone misses exact keyword matches. RRF fusion gives better recall than either approach alone.

**Hard fail on embed error.** If the sidecar is unreachable, `SaveMemory` writes nothing. This preserves the invariant that every doc has a corresponding vector. Silently saving without a vector would degrade search quality.

**FTS5-only for Agenda.** Agenda queries are keyword-oriented (task title, status lookup). Embeddings add no value and would create an unnecessary dependency on the sidecar for a purely task-management operation.

**Tree-sitter + LSP separation.** Tree-sitter provides fast, offline symbol extraction. LSP provides edges that require a running language server. The index partially succeeds even if LSP servers are unavailable.

**`--src-root` separates DB naming from scan root.** Previously the codemap always used a fixed `codemap.sqlite` filename. `--src-root` lets each project have a named database (e.g. `myrepo-ctx0.sqlite`) while also allowing a different directory to be scanned than the project root (useful for monorepos or sub-package indexing).

**Debounced incremental re-indexing.** 500ms debounce prevents excessive LSP calls during rapid edits. The 5-minute idle auto-stop avoids holding LSP server processes indefinitely.

**Scoped task numbering.** Task IDs shown to users are 1-based order within their agenda, not global database auto-increments. This prevents confusing gaps when agendas are deleted.

**Acceptance guards.** Each task can carry a "Done when:" condition. The skill instructions direct agents to verify the guard before marking a task complete, preventing premature completion.

**Ralph-loop self-correction.** `exec` and `discover` feed failed script output back to the inference model for repair. This makes ad-hoc Python scripting practical without requiring the agent to manually debug execution errors.
