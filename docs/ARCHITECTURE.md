# Architecture

## Overview

Context0 is structured as four independent engines sharing a common CLI shell and per-project data directory. Each Go-based engine has its own SQLite database and operates independently. The Python sidecar is a separate process that all three Go engines can delegate to.

```
context0 (CLI binary)
  ├── memory   → memory-ctx0.sqlite   (FTS5 + sqlite-vec)
  ├── agenda   → agenda-ctx0.sqlite   (FTS5)
  ├── codemap  → codemap-ctx0.sqlite  (relational graph)
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
| Embedding inference | Python sidecar (MLX, `mlx-community/bge-small-en-v1.5-4bit`, 384 dimensions) |
| LLM inference | Python sidecar (MLX, `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit`) |
| Sidecar runtime | Python ≥ 3.11, uv, mlx / mlx-lm / mlx-embeddings, huggingface-hub |

## Repository structure

```
context0/
├── main.go                     # Entry point: root command + --project flag + --start-sidecar/--stop-sidecar
├── go.mod
├── pyproject.toml              # Python sidecar dependency manifest (uv)
├── uv.lock
├── mise.toml                   # Tool versions + build tasks
├── cmd/
│   ├── memory/memory.go        # memory subcommands
│   ├── agenda/agenda.go        # agenda subcommands (plan + task sub-trees)
│   ├── codemap/codemap.go      # codemap subcommands
│   ├── ask/ask.go              # ask command (delegates to sidecar)
│   ├── exec/exec.go            # exec command (delegates to sidecar)
│   ├── docs-lib/docslib.go     # docs-lib command: resolve + fetch Context7 docs
│   ├── backup/backup.go        # backup: snapshot to ~/.context0/backup/<enc>/<ts>.tar.gz
│   ├── recover/recover.go      # recover: restore latest snapshot from ~/.context0/backup/<enc>/
│   ├── export/export.go        # export: pack databases to user-specified .tar.gz
│   └── import/import.go        # import: restore from arbitrary .tar.gz (snapshots first)
├── internal/
│   ├── db/path.go              # Per-project DB path resolution
│   ├── archive/archive.go      # Shared tar.gz helpers: Write, Extract, Snapshot, BackupDir
│   ├── daemon/daemon.go        # PID file management + detached process spawn
│   ├── sidecar/sidecar.go      # Sidecar lifecycle (Start/Stop/IsRunning) + UDS client
│   ├── memory/                 # Memory engine
│   │   ├── db.go               # Schema: docs, docs_fts, docs_vec, triggers
│   │   ├── embed.go            # Sidecar embed client (sends "embed" command over UDS)
│   │   ├── engine.go           # SaveMemory, QueryMemory, UpdateMemory, DeleteMemory
│   │   └── rrf.go              # Reciprocal Rank Fusion merger
│   ├── agenda/                 # Agenda engine
│   │   ├── db.go               # Schema: agendas, tasks, agendas_fts, tasks_fts, triggers
│   │   └── engine.go           # CRUD + FTS5 search + task lifecycle + AddTask
│   ├── graph/                  # Semantic code graph
│   │   ├── types.go            # Node, Edge, Relation constants
│   │   ├── hash.go             # SHA256 node/diagnostic ID generation
│   │   └── store.go            # SQLite graph store + ErrNotIndexed + OpenReadOnly
│   ├── scanner/                # Tree-sitter AST scanner
│   │   ├── queries.go          # S-expression queries per language
│   │   └── scanner.go          # Directory walker -> graph nodes
│   ├── lsp/                    # LSP client
│   │   ├── types.go            # JSON-RPC + LSP message types
│   │   ├── transport.go        # Content-Length framing (read/write)
│   │   ├── uri.go              # file:// URI <-> OS path conversion
│   │   ├── langroot.go         # Per-language workspace root detection (detectLangRoot)
│   │   └── lsp.go              # Client per language; concurrent Enrich() + Prewarm()
│   ├── watcher/watcher.go      # fsnotify -> incremental re-index + idle auto-stop
│   ├── codemapserver/          # Code exploration engine wiring
│   │   ├── server.go           # Index lifecycle; New() and NewWatch() constructors
│   │   ├── git.go              # Git root detection
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
│   ├── ralph.py                # Ralph-loop: uv-run script + triage + context7 doc-fetch + repair
│   ├── context7.py             # Context7 MCP client: resolve_library + get_docs (stdlib-only)
│   ├── downloader.py           # Hugging Face Hub model cache manager
│   ├── prompts.py              # Prompt templates (ask, exec repair + triage, discover)
│   └── protocol.py             # Command name constants
├── tests/
│   └── sidecar/                # Python unit tests (no model or sidecar required)
│       ├── test_ask.py         # _plan and ask orchestration
│       ├── test_context7.py    # _jsonrpc, _unwrap, _extract_sse_data, _extract_library_id
│       ├── test_ralph.py       # _strip_fences, ralph_exec, _fetch_repair_docs
│       └── test_server.py      # SidecarServer._dispatch (ping, embed, context7, unknown)
```

The `tests/` directory is intentionally outside `sidecar/` so the `//go:embed all:sidecar` directive in `main.go` does not bundle test files into the binary.

---

## Python Sidecar

The sidecar is an always-on background process that provides two services the Go binary cannot do natively: **local ML inference** and **orchestrated multi-step reasoning**.

- **Embedding** — every `memory save` / `memory query` call routes through the sidecar's `embed` endpoint. This replaces the previous Ollama dependency.
- **Inference** — used by `ask`, `exec`, and `discover` for planning, generation, and self-correction.

### Startup sequence (`context0 --start-sidecar`)

```
1. Write PID  → ~/.context0/sidecar.pid
2. Load embed model  (mlx-community/bge-small-en-v1.5-4bit, cached in ~/.context0/models/)
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

Commands: `ping`, `embed`, `generate`, `ask`, `exec`, `discover`, `context7`.

### Concurrency

The asyncio event loop handles I/O. Model calls are offloaded to a thread-pool executor. Two independent `asyncio.Lock` objects serialize the embed and inference engines separately, so an `ask` call (which acquires `infer_lock`) can still trigger a `memory query` inside its subprocess, which calls `embed` (acquires `embed_lock` independently) without deadlocking.

### Ralph-loop (`exec`, `discover`)

```
ralph_exec(script, project, inference):
  for attempt in 0..MAX_RETRIES (2):
    output, err = uv run - <<< script
    if err is None: return output
    if attempt == MAX_RETRIES: return last_error
    docs = _fetch_repair_docs(script, err, inference)  # may return None
    fixed = inference.generate(repair prompt + script + err [+ docs])
    if fixed == script: abort (no improvement)
    script = fixed
```

On failure the inference model receives the traceback and attempts a fix. Handles missing imports, syntax errors, and off-by-one logic. Gives up after 2 failed repair attempts.

Before each repair the loop runs a **triage step** (`_fetch_repair_docs`) to decide whether library docs would help and, if so, fetches them from Context7. See the section below.

### Repair triage and Context7 doc-fetch

```
_fetch_repair_docs(script, err, inference) -> str | None:
  1. inference.generate(REPAIR_TRIAGE_SYSTEM + triage prompt, max_tokens=128, temp=0.1)
     → JSON {"library": "...", "query": "..."} or null
  2. If null (or non-JSON, or any exception): return None  # graceful degrade
  3. context7.resolve_library(library, query) → library_id
  4. context7.get_docs(library_id, query, tokens=2000) → markdown docs
  5. Return docs string (or None on any Context7Error / exception)
```

The triage prompt is deliberately constrained: the model must output only a JSON object or the literal word `null`, at low temperature (0.1) and a small token budget (128). This keeps the overhead tiny.

When docs are returned they are injected into `EXEC_REPAIR_USER` as a `LIBRARY DOCUMENTATION` section between the "Common causes" list and the script block. When `docs=None` the section is an empty string so the prompt is byte-for-byte identical to the pre-triage behaviour.

**Graceful degradation is unconditional**: every failure path in `_fetch_repair_docs` — triage inference error, JSON parse failure, network error, Context7 protocol error — is caught, logged at `DEBUG`, and returns `None`. The ralph-loop never fails because of this step.

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
| Embedding model | `CTX0_EMBED_MODEL` | `mlx-community/bge-small-en-v1.5-4bit` |
| Inference model | `CTX0_INFER_MODEL` | `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit` |
| Context7 API key | `CONTEXT7_API_KEY` | *(none — unauthenticated, low rate limits)* |

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
agendas     (id INTEGER PK, is_active BOOL, git_branch TEXT, priority TEXT,
             title TEXT, description TEXT, acceptance_guard TEXT,
             created_at DATETIME, completed_at DATETIME, deleted_at DATETIME)
tasks       (id INTEGER PK, agenda_id FK, details TEXT, notes TEXT, status TEXT)
agendas_fts USING fts5(title, description, acceptance_guard, content='agendas')
tasks_fts   USING fts5(details, notes, content='tasks', content_rowid='id')
```

`status` is the canonical task state: `pending` | `in_progress` | `completed` | `blocked`.

`priority` controls sort order in list and search output: `high` (0) → `normal` (1) → `low` (2).

`completed_at` is set on both auto-deactivation and manual `--deactivate`. `deleted_at` enables soft-delete; agendas must be deactivated before they can be deleted.

Tasks cascade on agenda delete. Both FTS5 tables are trigger-maintained (INSERT / UPDATE / DELETE).

### Key behaviours

- **Auto-deactivation**: when a task status is updated, the engine checks if all tasks for the agenda have `status = 'completed'`. If so, `is_active` is set to false and `completed_at` is recorded. Tasks with `pending`, `in_progress`, or `blocked` status keep the agenda open.
- **Memory snapshot on auto-close**: when a plan auto-deactivates (all tasks completed), a fire-and-forget memory save is triggered with the full plan details. This only fires on auto-deactivation, not on manual `--deactivate`.
- **Active guard on delete**: agendas must be deactivated before they can be soft-deleted.
- **Soft delete / restore**: `DeleteAgenda` sets `deleted_at`; `RestoreAgenda` clears it. Deleted agendas are excluded from list/search by default and shown with `--deleted`.
- **Scoped task numbering**: tasks are displayed and addressed by 1-based order within their agenda (e.g. `task done 5 2` = agenda 5, task #2), not by global database ID.
- **Acceptance guards**: each agenda can have a "Completion Condition:" field. Agents should verify this condition before marking the agenda as inactive.
- **Task lifecycle**: `pending` → `in_progress` → `completed` (and back via `reopen`). `block` transitions any task to `blocked`. `reopen` resets to `pending` and is also how blocked tasks are unblocked.
- **Task notes**: each task carries an optional `notes` field updated via `task done --notes`, `task start --notes`, or `task block --notes`. Passing an empty string leaves existing notes unchanged.
- **Task addition**: `AddTask()` appends a new task to an existing plan at any time, regardless of the plan's active state.
- **No embeddings**: search is FTS5-only. Agenda queries are keyword-oriented, making vector search unnecessary.

### CLI structure

The agenda command tree has two sub-groups:

```
context0 agenda plan   list / get / create / search / update / delete / restore
context0 agenda task   add / start / done / block / reopen
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
- `New(ctx, rootDir)` -- for CLI commands (`index`, `symbol`, etc.) and `watch --foreground`; idle timeout is suppressed (no-op cancel).
- `NewWatch(ctx, cancel, rootDir)` -- for the background `watch` daemon; idle timeout fires `cancel()` to exit cleanly after 5 minutes of inactivity.

### Schema (`codemap-ctx0.sqlite`)

```sql
nodes (id TEXT PK, name TEXT, kind TEXT, language TEXT,
       file_path TEXT,
       line_start INT, line_end INT, col_start INT, col_end INT,
       name_line INT, name_col INT, symbol_uri TEXT)
edges (source_id TEXT, target_id TEXT, relation TEXT)
```

- **Node ID**: `SHA256(filePath:name:kind)[:16]` -- stable 32-char hex.
- **`language`**: LSP language ID (`"go"`, `"python"`, etc.), set by the scanner from the file extension. Stored explicitly so language-filtered queries (`codemap find --lang go`) use an indexed equality check rather than a suffix `LIKE` scan on `file_path`.
- **`name_line`/`name_col`**: position of the name identifier token. LSP enrichment uses these for cursor placement (not `line_start`/`col_start`, which point to the declaration keyword).
- **Edge relations**: `implements`, `references`.
- **Indexes**: `(file_path)` for `outline` and `FindNode`; composite `(name, language)` for `find` — covers both the unfiltered `WHERE name = ?` path and the filtered `WHERE name = ? AND language = ?` path via the leftmost-prefix rule.

### Per-language workspace roots

Before starting an LSP client, `detectLangRoot` (in `internal/lsp/langroot.go`) searches for the innermost directory under the git root that contains a language-specific manifest file. It performs a BFS up to depth 3, skipping `vendor/`, `node_modules/`, `.git/`, and hidden directories.

| Language | Manifest files |
|---|---|
| go | `go.mod` |
| python | `pyproject.toml`, `setup.py`, `setup.cfg` |
| javascript / typescript | `package.json`, `tsconfig.json` |
| lua | `.luarc.json` |
| zig | `build.zig` |

If no manifest is found the git root itself is used as the workspace root. This ensures each language server is initialised in the most relevant sub-directory (e.g. a TypeScript package nested inside a Go monorepo).

### Index lifecycle

```
Full index (runIndex -> doIndex):
  1. store.Clear()
  2. sc.DetectLanguages(root) -> []langID
     go lsp.Prewarm(ctx, langIDs)     -- starts all LSP clients concurrently in background
  3. scanner.Scan(ctx, root)          -- concurrent worker pool (runtime.NumCPU workers)
       -> []Node
  4. store.BulkUpsertNodes(nodes)
  5. lsp.Enrich(ctx, nodes, store)    -- all languages enriched concurrently
       -> []Edge
  6. store.BulkUpsertEdges(edges)

Status transitions: Idle -> InProgress -> Ready | Failed
```

### Tree-sitter scanning

The scanner walks the project directory, parses each supported file with Tree-sitter, and runs language-specific S-expression queries to extract symbol nodes.

Scanning is two-phase: a sequential `filepath.WalkDir` collects candidate file paths (walk itself cannot be parallelised because `SkipDir` must be returned synchronously), then a worker pool of `runtime.NumCPU()` goroutines parses all files concurrently. Each `ScanFile` call creates its own `sitter.Parser` instance, so workers share no mutable state.

Supported languages: Go, Python, JavaScript, TypeScript, Lua, Zig.

Skipped: `vendor/`, `node_modules/`, `__pycache__/`, `.git/`, `.venv/`, `zig-cache/`, `zig-out/`, generated files. Respects `.gitignore`.

### LSP enrichment

Before scanning, `DetectLanguages` does a fast extension-only walk and `Prewarm` starts all required LSP clients concurrently in a background goroutine. By the time `Enrich` runs, most or all warmup periods have already elapsed.

`Enrich` groups nodes by language and processes all languages concurrently — each language gets its own goroutine that calls `warmupWait()` (a no-op if `Prewarm` already elapsed it) and then fans out across a pool of 10 node-workers:

```
lsp.Service.Enrich(ctx, nodes, store)
  ├── Group nodes by language
  └── All languages concurrently (one goroutine per language):
       warmupWait()  -- no-op if Prewarm already elapsed the warmup period
       Fan out 10 workers per language:
         enrichNode(ctx, client, langID, node, store):
           ├── didOpen(uri)
           ├── textDocument/references(uri, name_line, name_col)
           │    -> for each location: store.FindNode -> Edge{references}
           ├── textDocument/implementation(uri, ...)  [interfaces/types only]
           │    -> Edge{implements}
           └── didClose(uri)
```

### LSP binary management

Binaries are resolved in order: **PATH** -> **cache** (`~/.context0/bin/`) -> **auto-download**.

After resolution, a background goroutine checks for a newer version via the upstream registry (GitHub releases, npm, pip, Go module proxy) and silently reinstalls if one is found. Checked once per binary per process lifetime via `sync.Map`.

Supported servers: `gopls`, `pyright-langserver`, `typescript-language-server`, `lua-language-server`, `zls`.

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

**FTS5-only for Agenda.** Agenda queries are keyword-oriented (task title, status lookup). Embeddings add no value and would create an unnecessary dependency on the sidecar for a purely task-management operation. Two FTS5 tables cover the full search surface: `agendas_fts` (title, description, acceptance_guard) and `tasks_fts` (details, notes). `plan search` queries both via a UNION and deduplicates by agenda ID.

**Concurrent scanning and enrichment.** Tree-sitter file parsing uses a `runtime.NumCPU()` worker pool because each `ScanFile` call creates an independent `sitter.Parser` — no shared mutable state. LSP enrichment runs all language servers in parallel (`Enrich` launches one goroutine per language, each with its own 10-worker pool) because each language has a dedicated `Client` subprocess with no cross-language locking. LSP pre-warming (`Prewarm`) is fired as a background goroutine before the Tree-sitter scan begins, so server startup and tree-sitter parsing overlap in time.

**Tree-sitter + LSP separation.** Tree-sitter provides fast, offline symbol extraction. LSP provides edges that require a running language server. The index partially succeeds even if LSP servers are unavailable.

**Per-language workspace roots.** Each LSP client is initialised against the innermost directory containing a language-specific manifest (`go.mod`, `pyproject.toml`, `package.json`, etc.), found via BFS up to depth 3 from the git root. This ensures language servers receive the correct workspace root in monorepos where multiple language ecosystems coexist. Falls back to the git root if no manifest is found.

**Debounced incremental re-indexing.** 500ms debounce prevents excessive LSP calls during rapid edits. The 5-minute idle auto-stop avoids holding LSP server processes indefinitely.

**Scoped task numbering.** Task IDs shown to users are 1-based order within their agenda, not global database auto-increments. This prevents confusing gaps when agendas are deleted.

**Acceptance guards.** Each agenda can carry a "Completion Condition:" condition. The skill instructions direct agents to verify the guard before marking the agenda as inactive, preventing premature completion.

**Ralph-loop self-correction.** `exec` and `discover` feed failed script output back to the inference model for repair. This makes ad-hoc Python scripting practical without requiring the agent to manually debug execution errors.

**Triage-before-repair for Context7 docs.** Before each repair attempt a tiny inference call decides whether the error is library-API-related. If yes, docs are fetched from Context7 and injected into the repair prompt. This avoids unconditionally fetching docs (expensive, noisy) while still giving the model up-to-date API information when it actually matters. Every failure path degrades silently so the ralph-loop is never broken by a network issue.
