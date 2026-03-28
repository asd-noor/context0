# Context0 — Project Report

## Overview

Context0 (`context0`) is a CLI tool that acts as a persistent knowledge layer for AI coding agents. It provides three independent engines — Memory, Agenda, and Code Exploration — each backed by a per-project SQLite database stored under `~/.context0/<project-dir>/`.

The tool is exposed as a CLI only:

```
context0 memory ...
context0 agenda ...
context0 codemap ...
```

All commands accept a root-level `--project / -p` flag (defaults to CWD) that controls which project's databases are used. This flag is inherited by every subcommand.

---

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| CLI framework | cobra |
| Storage | SQLite (WAL mode) via `mattn/go-sqlite3` (CGo) |
| Full-text search | SQLite FTS5 |
| Vector search | `sqlite-vec` via `asg017/sqlite-vec-go-bindings` (CGo) |
| AST parsing | Tree-sitter via `tree-sitter/go-tree-sitter` (CGo, official bindings) |
| LSP integration | JSON-RPC 2.0 over stdio (custom client) |
| File watching | `fsnotify` |
| Embedding inference | Ollama HTTP API (`/v1/embeddings`, OpenAI-compatible) |
| Embedding model | `qllama/bge-small-en-v1.5` (384 dimensions) |
| Build tooling | mise |

---

## Repository Structure

```
context0/
├── main.go                     # Binary entry point — defines root `context0` command + --project flag
├── go.mod
├── mise.toml                   # Tool versions + build tasks
├── cmd/
│   ├── memory/memory.go        # context0 memory subcommands
│   ├── agenda/agenda.go        # context0 agenda subcommands
│   └── codemap/codemap.go      # context0 codemap subcommands
├── internal/
│   ├── db/path.go              # Per-project DB path resolution + PIDPath helper
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
│   │   └── store.go            # SQLite-backed graph store + ErrNotIndexed + OpenReadOnly
│   ├── scanner/                # Tree-sitter AST scanner
│   │   ├── queries.go          # S-expression queries per language
│   │   └── scanner.go          # Directory walker → graph nodes
│   ├── lsp/                    # LSP client
│   │   ├── types.go            # JSON-RPC + LSP message types
│   │   ├── transport.go        # Content-Length framing (read/write)
│   │   └── lsp.go              # Client subprocess pool + Enrich()
│   ├── watcher/watcher.go      # fsnotify → incremental re-index; idle auto-stop after 5 min
│   ├── codemapserver/          # Code Exploration engine wiring
│   │   ├── server.go           # Index lifecycle; New() and NewWatch() constructors
│   │   └── query.go            # Shared CLI helpers (NodeWithSource, GetSymbolWithSource, …)
│   └── pkgmgr/                 # LSP binary resolver
│       ├── manager.go
│       ├── metadata.go
│       └── upgrade.go          # Background version-check + silent reinstall
└── util/
    ├── hash.go                 # SHA256 node ID generation
    ├── uri.go                  # file:// URI ↔ OS path conversion
    └── git.go                  # Git root detection (walk up for .git)
```

---

## Data Storage

All databases are stored at:

```
~/.context0/<transformed-project-path>/
    memory.sqlite
    agenda.sqlite
    codemap.sqlite
    codemap.pid        # written by `context0 codemap watch`; removed on exit
```

The project path is transformed by replacing path separators with `=`:

```
/Users/noor/myproject  →  ~/.context0/Users=noor=myproject/
```

---

## Feature 1: Memory Engine

### Purpose

Persistent, per-project knowledge store. Designed for AI agents to save and retrieve project decisions, architecture notes, bug fixes, checkpoints, and other contextual information.

### Storage Schema (`memory.sqlite`)

```sql
docs (id, category, topic, content, timestamp)
docs_fts  USING fts5(category, topic, content, content='docs')
docs_vec  USING vec0(id INTEGER PRIMARY KEY, embedding float[384])
```

FTS5 is kept in sync with `docs` via three SQL triggers (`INSERT`, `UPDATE`, `DELETE`). Vector entries in `docs_vec` are managed explicitly by the engine.

### Embedding

Text embedding is performed by calling Ollama's OpenAI-compatible API:

```
POST http://localhost:11434/v1/embeddings
model: qllama/bge-small-en-v1.5 (384 dimensions)
```

Configuration is overridable via environment variables:
- `CTX0_EMBED_ENDPOINT` — defaults to `http://localhost:11434`
- `CTX0_EMBED_MODEL` — defaults to `qllama/bge-small-en-v1.5`

### Save Flow (`memory save`)

```
NewEmbedClient()                         embed.go:33
  └─ reads env vars, constructs http.Client (30s timeout)

SaveMemory(category, topic, content)     engine.go:49
  ├─ Embed(category + " " + topic + " " + content)
  │    └─ POST /v1/embeddings → []float32 (384-dim)
  ├─ sqlite_vec.SerializeFloat32(embedding)
  ├─ db.Begin()
  ├─ INSERT INTO docs (category, topic, content)
  │    └─ trigger fires: INSERT INTO docs_fts
  ├─ INSERT INTO docs_vec (id, embedding)
  └─ tx.Commit()
```

The embed call happens before any database write. If Ollama is unreachable, the operation fails entirely — no partial writes occur.

### Query Flow (`memory query`)

```
QueryMemory(query, topK)                 engine.go:93
  ├─ Embed(query) → query vector
  ├─ queryFTS(query, topK*5)
  │    └─ SELECT id FROM docs_fts WHERE docs_fts MATCH ?
  │       ORDER BY rank  (BM25)
  ├─ queryVec(blob, topK*5)
  │    └─ SELECT id FROM docs_vec
  │       WHERE embedding MATCH ? AND k=?  (KNN)
  ├─ MergeRRF(ftsIDs, vecIDs)
  │    └─ score = Σ 1/(rank + 60) per list
  │       sorted descending by score
  └─ getDoc(id) × topK  →  []QueryResult
```

Hybrid search combines BM25 keyword matching (FTS5) and approximate nearest-neighbour vector search (sqlite-vec), fused via Reciprocal Rank Fusion (RRF). Both legs run on `topK×5` candidates before fusion to reduce rank cutoff bias.

### Update Flow (`memory update <id>`)

```
UpdateMemory(id, category, topic, content)   engine.go:155
  ├─ getDoc(id) — fetch current values
  ├─ merge partial fields (empty → keep existing)
  ├─ Embed(merged category + topic + content)
  ├─ db.Begin()
  ├─ UPDATE docs SET ...
  │    └─ trigger fires: delete + re-insert docs_fts
  ├─ INSERT OR REPLACE INTO docs_vec (id, embedding)
  └─ tx.Commit()
```

### Delete Flow (`memory delete <id>`)

```
DeleteMemory(id)                             engine.go:196
  ├─ db.Begin()
  ├─ DELETE FROM docs_vec WHERE id=?
  ├─ DELETE FROM docs WHERE id=?
  │    └─ trigger fires: delete from docs_fts
  └─ tx.Commit()
```

### CLI Commands

```
context0 memory save    --category <c> --topic <t> --content <C>
context0 memory query   <text> [--top <k>] [--minimal]
context0 memory update  <id> [--category] [--topic] [--content]
context0 memory delete  <id>
```

---

## Feature 2: Agenda Engine

### Purpose

Structured task and plan management scoped per project. Supports multi-task agendas with optional tasks, acceptance guards, and automatic deactivation when all required tasks complete. Keyword search via FTS5.

### Storage Schema (`agenda.sqlite`)

```sql
agendas (id, is_active, title, description, created_at)
tasks   (id, agenda_id, task_order, is_optional, details,
         acceptance_guard, is_completed)
agendas_fts USING fts5(title, description, content='agendas')
```

FTS5 is maintained by triggers. Tasks are a child table with `ON DELETE CASCADE`.

### Key Behaviours

- **Auto-deactivation**: when `UpdateTask` marks a task complete, the engine checks if all non-optional tasks for the agenda are now complete. If so, `is_active` is set to `false` automatically.
- **Active guard on delete**: `DeleteAgenda` rejects deletion of active agendas.
- **Partial update**: `UpdateAgenda` builds a dynamic `SET` clause from non-empty fields and optionally appends new tasks.
- **Search**: `SearchAgendas` uses FTS5 `MATCH` with escaped query terms.

### Embedding

The Agenda engine does **not** use text embeddings. All search is FTS5-based. This is appropriate because agenda queries are keyword-oriented (task status, title lookup) rather than conceptual.

### CLI Commands

```
context0 agenda create  --title <t> --description <d> [--task <t>]... [--task-guard <g>]...
context0 agenda list    [--all]
context0 agenda get     <id>
context0 agenda search  <query> [--limit <n>]
context0 agenda task    done   <agenda-id> <task-number>
context0 agenda task    reopen <agenda-id> <task-number>
context0 agenda update  <id> [--title] [--description] [--deactivate] [--tasks <json>]
context0 agenda delete  <id>
```

---

## Feature 3: Code Exploration Engine

### Purpose

Builds and maintains a real-time semantic graph of the codebase. Combines Tree-sitter AST parsing for symbol extraction with LSP-based cross-reference enrichment. Exposed via CLI commands for symbol lookup, definition navigation, and change impact analysis.

### Architecture

```
codemapserver.Server
  ├─ scanner.Scanner      (Tree-sitter: extracts Nodes from source files)
  ├─ lsp.Service          (LSP clients: extracts Edges from cross-references)
  ├─ graph.Store          (SQLite: stores Nodes + Edges)
  └─ watcher.Watcher      (fsnotify: drives incremental re-index on file changes)
```

Two constructors serve different use cases:
- `codemapserver.New(ctx, rootDir)` — used by `index`, `symbol`, and other CLI commands; idle-timeout from the watcher is suppressed (process lifetime governs shutdown).
- `codemapserver.NewWatch(ctx, cancel, rootDir)` — used by `watch`; passes the real cancel so the watcher's idle-timeout fires `cancel()` and the daemon exits cleanly.

### Storage Schema (`codemap.sqlite`)

```sql
nodes (id TEXT PK, name, kind, file_path, line_start, line_end,
       col_start, col_end, name_line, name_col, symbol_uri)
edges (source_id, target_id, relation)
```

Node IDs are stable 32-character hex strings: `SHA256(filePath:name:kind)[:16]`.

Edge relations: `calls`, `implements`, `references`, `imports`.

Indexes on `file_path`, `name`, `source_id`, `target_id` for fast lookup.

`name_line` and `name_col` store the position of the name identifier token (as opposed to `line_start`/`col_start` which store the declaration keyword position). LSP enrichment uses `name_line`/`name_col` to place the cursor correctly for `textDocument/references` queries.

`graph.OpenReadOnly()` opens the database with `mode=ro` and returns `ErrNotIndexed` if the file does not exist yet, preventing accidental DB creation by read-only commands.

### Index Lifecycle

```
codemapserver.New(ctx, rootDir)
  ├─ util.FindGitRoot(rootDir)     — anchor to repo root
  ├─ graph.Open(rootDir)           — open/create codemap.sqlite
  ├─ pkgmgr.New(rootDir)           — resolve LSP binaries
  ├─ scanner.New(root)
  ├─ lsp.NewService(rootDir, pm)
  └─ go runIndex(ctx)              — initial full index
     go watcher.Run(ctx, cancel)   — incremental updates + idle auto-stop

runIndex → doIndex:
  ├─ store.Clear()
  ├─ scanner.Scan(ctx, root)       → []Node
  ├─ store.BulkUpsertNodes(nodes)
  ├─ lsp.Enrich(ctx, nodes, store) → []Edge
  └─ store.BulkUpsertEdges(edges)
```

Index status transitions: `Idle → InProgress → Ready | Failed`.

### AST Scanning (Tree-sitter)

Supported languages and extracted symbol kinds:

| Language | Extensions | Kinds |
|---|---|---|
| Go | `.go` | `function`, `method`, `type` |
| Python | `.py` | `function`, `class` |
| JavaScript | `.js`, `.jsx` | `function`, `method`, `class` |
| TypeScript | `.ts`, `.tsx` | `function`, `method`, `class`, `interface`, `type` |
| Lua | `.lua` | `function`, `method` |
| Zig | `.zig` | `function` |

Skipped: `vendor/`, `node_modules/`, `__pycache__/`, `.git/`, `zig-cache/`, `zig-out/`, generated files (`_templ.go`, `.sql.go`, `_string.go`). Respects `.gitignore`.

### LSP Enrichment

```
lsp.Service.Enrich(ctx, nodes, store)
  ├─ group nodes by language
  └─ per language: fan out 10 workers
       enrichNode(ctx, client, langID, node, store):
         ├─ client.didOpen(uri)
         ├─ client.references(ctx, uri, position)
         │    └─ textDocument/references → []Location
         │         → store.FindNode per location → Edge{calls/references}
         ├─ client.implementations(ctx, uri, position)  [for interface/type]
         │    └─ textDocument/implementation → []Location
         │         → Edge{implements}
         └─ client.didClose(uri)
```

Language server binaries are resolved by `pkgmgr` in order: `PATH` → cache (`~/.context0/bin/`) → auto-download. After a binary is located, `maybeUpgrade` is called in a background goroutine to silently check for a newer version and reinstall if one is found (once per process lifetime per binary, tracked via `sync.Map`).

Supported servers: `gopls`, `pylsp`, `typescript-language-server`, `lua-language-server`, `zls`.

#### Binary Version Management (`internal/pkgmgr/upgrade.go`)

```
maybeUpgrade(name, binaryPath)          — called by ResolveBinary after binary is found
  └─ silentUpgrade(name, binaryPath)    — runs in goroutine; skips if already checked
       ├─ versionFromArgs(binaryPath)   — runs binary with --version / version
       ├─ latestXxx(name)               — fetches latest from GitHub/npm/pip/Go module
       └─ if latest > installed:
            manualInstall(name)         — re-downloads and replaces binary in cache
```

`latestGitHubRelease` is also called by `manualInstall` at install time so that fresh installs always download the current latest version (falls back to a hard-coded version if the GitHub API is unreachable).

### Incremental Re-indexing (Watcher)

```
watcher.Run(ctx, cancel)
  ├─ fsnotify events (Create/Write/Remove/Rename)
  │    └─ filter gitignore, watch new dirs, add to pending map
  ├─ 100ms poll ticker
  │    └─ flush() — for each path where deadline (500ms debounce) passed:
  │         ├─ Remove/Rename → store.DeleteNodesByFile(path)
  │         └─ Create/Write  → reindexFile(path):
  │              ├─ store.DeleteNodesByFile(path)
  │              ├─ scanner.ScanFile(ctx, path)  → []Node
  │              ├─ store.BulkUpsertNodes(nodes)
  │              ├─ lsp.Enrich(ctx, nodes, store) → []Edge
  │              └─ store.BulkUpsertEdges(edges)
  └─ 5-minute idle timer
       └─ no file activity for 5 min → cancel() → daemon exits
```

### Daemon Mode

The watcher runs as a background daemon managed through a PID file:

```
context0 codemap watch      — start daemon in foreground; writes PID to codemap.pid;
                              blocks until 5-minute idle timeout fires
```

`daemon.Spawn()` launches `context0 codemap --project <root> watch` as a detached child process (new session via `Setsid`) with stdin/stdout/stderr closed.

### CLI Commands

```
context0 codemap [--project <dir>] watch
context0 codemap [--project <dir>] index
context0 codemap [--project <dir>] status
context0 codemap [--project <dir>] symbols <file> [--json]
context0 codemap [--project <dir>] symbol  <name>  [--source] [--json]
context0 codemap [--project <dir>] impact  <name>  [--json]
```

Read-only commands (`status`, `symbols`, `impact`) use `graph.OpenReadOnly()` and return `"project has not been indexed yet — run: context0 codemap index"` if no index exists.

## Utility Layer

### `util.NodeID(filePath, name, kind)`

Produces stable, collision-resistant graph node IDs:

```
SHA256(filePath + ":" + name + ":" + kind) → first 16 bytes → 32-char hex
```

### `util.PathToURI` / `util.URIToPath`

Converts between OS file paths and LSP `file://` URIs using `url.URL`.

### `util.FindGitRoot(dir)`

Walks up the directory tree checking for a `.git` directory. Used by `codemapserver` to anchor the index at the repository root rather than the invocation directory.

---

## Key Design Decisions

**CLI-only.** All three engines (Memory, Agenda, Code Exploration) are exposed exclusively through CLI subcommands. There is no MCP server. AI agents interact with the tool by invoking CLI commands directly via skill files.

**Root-level `--project` flag.** All three top-level command groups (`memory`, `agenda`, `codemap`) inherit a single `--project / -p` persistent flag from the root `context0` command. No command relies on CWD implicitly at the engine level.

**Daemon with PID file.** The codemap watcher runs as a separate background process tracked by a PID file. If a live daemon is already running, `context0 codemap watch` detects it via the PID file and exits cleanly without spawning a duplicate. Short-lived CLI commands (`status`, `symbols`, etc.) benefit from the warm index maintained by the daemon.

**Read-only guard (`ErrNotIndexed`).** `graph.OpenReadOnly()` checks for the DB file's existence before opening with `mode=ro`, returning a clear `ErrNotIndexed` sentinel. This prevents accidental DB creation by read-only commands and gives users an actionable error message.

**Hybrid search (Memory).** FTS5 alone would fail on vocabulary mismatch between how memories are written and how they are queried. Vector search alone would miss exact keyword matches. RRF fusion combines both, giving better recall than either approach alone.

**Hard fail on embed error (Memory).** If the embedding server is unreachable, `SaveMemory` returns an error and writes nothing. This preserves the invariant that every doc in `docs` has a corresponding vector in `docs_vec`. Silently saving without a vector would degrade search quality and produce inconsistent hybrid ranking.

**FTS5-only for Agenda.** Agenda queries are keyword-oriented (task title, status). Embedding-based search provides no meaningful benefit and would introduce an unnecessary dependency on the embedding server for task management.

**sqlite-vec for vector storage.** Keeps the entire stack in a single SQLite file per feature. No separate vector database process, no network hop for ANN queries, no synchronization complexity between stores.

**Tree-sitter + LSP separation.** Tree-sitter provides fast, offline symbol extraction (nodes). LSP provides cross-reference and implementation edges that require a running language server. Separating them allows the index to partially succeed even if LSP servers are unavailable.

**Debounced incremental re-indexing.** A 500ms debounce prevents excessive LSP calls during rapid file edits (e.g. save-on-type). The watcher self-terminates after 5 minutes of inactivity to avoid holding LSP server processes open indefinitely.

**Silent background LSP binary upgrades.** When `pkgmgr.ResolveBinary` locates a binary (from cache or PATH), it immediately fires a goroutine that checks for a newer version via the upstream registry (GitHub releases, npm, pip, or Go module proxy) and silently reinstalls if one is found. The check is bounded to once per binary per process lifetime via `sync.Map`. This keeps language servers up to date without blocking indexing or requiring user intervention.
