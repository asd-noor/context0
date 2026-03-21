# Context0 — Project Report

## Overview

Context0 (`context0`) is a CLI tool and MCP server that acts as a persistent knowledge layer for AI coding agents. It provides three independent engines — Memory, Agenda, and Code Exploration — each backed by a per-project SQLite database stored under `~/.context0/<project-dir>/`.

The tool is exposed in two modes:
- **CLI** (`context0 memory ...`, `context0 agenda ...`, `context0 codemap ...`) for direct human or script use
- **MCP stdio server** (`context0 mcp`, `context0 codemap mcp`) for AI agent integration via the Model Context Protocol

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
| AST parsing | Tree-sitter via `smacker/go-tree-sitter` (CGo) |
| LSP integration | JSON-RPC 2.0 over stdio (custom client) |
| File watching | `fsnotify` |
| Embedding inference | LM Studio HTTP API (`/v1/embeddings`, OpenAI-compatible) |
| Embedding model | `BAAI/bge-small-en-v1.5` (384 dimensions, GGUF) |
| MCP server | `mark3labs/mcp-go` |
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
│   ├── mcp/mcp.go              # context0 mcp (Memory + Agenda MCP server)
│   └── codemap/codemap.go      # context0 codemap subcommands (CLI-first + MCP)
├── internal/
│   ├── db/path.go              # Per-project DB path resolution + PIDPath helper
│   ├── daemon/daemon.go        # PID file management + detached process spawn
│   ├── memory/                 # Memory engine
│   │   ├── db.go               # Schema: docs, docs_fts, docs_vec, triggers
│   │   ├── embed.go            # HTTP embedding client (LM Studio / Ollama)
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
│   │   ├── query.go            # Shared CLI+MCP helpers (NodeWithSource, GetSymbolWithSource, …)
│   │   ├── tools.go            # MCP tool registrations
│   │   ├── resources.go        # MCP resource (usage-guidelines)
│   │   └── prompts.go          # MCP prompts
│   └── pkgmgr/                 # LSP binary resolver
│       ├── manager.go
│       └── metadata.go
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

The project path is transformed by replacing path separators with `;`:

```
/Users/noor/myproject  →  ~/.context0/Users;noor;myproject/
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

Text embedding is performed by calling LM Studio's OpenAI-compatible API:

```
POST http://localhost:1234/v1/embeddings
model: BAAI/bge-small-en-v1.5 (GGUF, 384 dimensions)
```

Configuration is overridable via environment variables:
- `CTX0_EMBED_ENDPOINT` — defaults to `http://localhost:1234`
- `CTX0_EMBED_MODEL` — defaults to `BAAI/bge-small-en-v1.5`

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

The embed call happens before any database write. If LM Studio is unreachable, the operation fails entirely — no partial writes occur.

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
context0 memory query   <text> [--top <k>]
context0 memory update  <id> [--category] [--topic] [--content]
context0 memory delete  <id>
```

### MCP Tools

```
save_memory    (category, topic, content)
query_memory   (query, top_k?)
update_memory  (id, category?, topic?, content?)
delete_memory  (id)
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
context0 agenda create  --title <t> --description <d> [--task <t>]...
context0 agenda list    [--all]
context0 agenda get     <id>
context0 agenda search  <query> [--limit <n>]
context0 agenda task    done   <task-id>
context0 agenda task    reopen <task-id>
context0 agenda update  <id> [--title] [--description] [--deactivate] [--tasks <json>]
context0 agenda delete  <id>
```

### MCP Tools

```
create_agenda   (title, description, tasks[])
list_agendas    (active_only?)
get_agenda      (id)
search_agendas  (query, limit?)
update_task     (task_id, is_completed)
update_agenda   (id, title?, description?, is_active?, new_tasks[]?)
delete_agenda   (id)
```

---

## Feature 3: Code Exploration Engine

### Purpose

Builds and maintains a real-time semantic graph of the codebase. Combines Tree-sitter AST parsing for symbol extraction with LSP-based cross-reference enrichment. Exposed directly via CLI commands and to AI agents via MCP tools for symbol lookup, definition navigation, and change impact analysis.

### Architecture

```
codemapserver.Server
  ├─ scanner.Scanner      (Tree-sitter: extracts Nodes from source files)
  ├─ lsp.Service          (LSP clients: extracts Edges from cross-references)
  ├─ graph.Store          (SQLite: stores Nodes + Edges)
  └─ watcher.Watcher      (fsnotify: drives incremental re-index on file changes)
```

Two constructors serve different use cases:
- `codemapserver.New(ctx, rootDir)` — used by `index`, `symbol`, and `mcp` subcommands; idle-timeout from the watcher is suppressed (process lifetime governs shutdown).
- `codemapserver.NewWatch(ctx, cancel, rootDir)` — used by `watch`; passes the real cancel so the watcher's idle-timeout fires `cancel()` and the daemon exits cleanly.

### Storage Schema (`codemap.sqlite`)

```sql
nodes (id TEXT PK, name, kind, file_path, line_start, line_end,
       col_start, col_end, symbol_uri)
edges (source_id, target_id, relation)
```

Node IDs are stable 32-character hex strings: `SHA256(filePath:name:kind)[:16]`.

Edge relations: `calls`, `implements`, `references`, `imports`.

Indexes on `file_path`, `name`, `source_id`, `target_id` for fast lookup.

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

Skipped: `vendor/`, `node_modules/`, `__pycache__/`, `.git/`, generated files (`_templ.go`, `.sql.go`, `_string.go`). Respects `.gitignore`.

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

Language server binaries are resolved by `pkgmgr` in order: cache (`~/.context0/bin/`) → `PATH` → auto-download.

Supported servers: `gopls`, `pylsp`, `typescript-language-server`, `lua-language-server`.

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
context0 codemap mcp        — checks PID file; if daemon is not alive, spawns it as a
                              detached background process and returns a retry message;
                              otherwise starts the MCP stdio server normally
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
context0 codemap [--project <dir>] mcp
```

Read-only commands (`status`, `symbols`, `impact`) use `graph.OpenReadOnly()` and return `"project has not been indexed yet — run: context0 codemap index"` if no index exists.

### MCP Tools

```
index                           — Force full re-index, return node/edge counts
index_status                    — Return current status + elapsed duration
get_symbols_in_file(file_path)  — List all symbols in a file
get_symbol(name, with_source?)  — Find symbol definition + optionally source
find_impact(symbol_name)        — Recursive reverse traversal of all dependents
```

### MCP Resources

```
codemap://usage-guidelines      — Markdown guide: tools + recommended workflow
```

### MCP Prompts

```
explore_codebase                — Step-by-step codebase exploration workflow
analyze_change_impact(symbol)   — Impact analysis workflow for a named symbol
```

---

## MCP Server Architecture

Context0 exposes two independent MCP stdio servers:

### `context0 mcp` — Memory + Agenda

```
stdin/stdout (JSON-RPC / MCP)
  └─ mcp-go server
       ├─ save_memory, query_memory, update_memory, delete_memory
       └─ create_agenda, list_agendas, get_agenda, search_agendas,
          update_task, update_agenda, delete_agenda
```

Project path is taken from the `--project` flag (defaults to CWD). Each tool call is stateless — a fresh engine is opened and closed per call.

### `context0 codemap mcp` — Code Exploration

```
stdin/stdout (JSON-RPC / MCP)
  └─ mcp-go server
       ├─ Tools:    index, index_status, get_symbols_in_file,
       │            get_symbol, find_impact
       ├─ Resource: codemap://usage-guidelines
       └─ Prompts:  explore_codebase, analyze_change_impact
```

The server is stateful — it holds a `codemapserver.Server` instance with the index, LSP clients, and file watcher for the lifetime of the MCP session. If the codemap daemon is not running when `context0 codemap mcp` is invoked, it is auto-spawned and the caller is asked to retry in 5 seconds.

---

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

**CLI-first for Code Exploration.** Every codemap capability (`index`, `status`, `symbols`, `symbol`, `impact`) is a direct CLI command. MCP is an additional surface, not the primary one. This makes the tool usable without an AI agent and simplifies debugging.

**Root-level `--project` flag.** All four top-level command groups (`memory`, `agenda`, `mcp`, `codemap`) inherit a single `--project / -p` persistent flag from the root `context0` command. No command relies on CWD implicitly at the engine level.

**Daemon with PID file.** The codemap watcher runs as a separate background process tracked by a PID file. This allows the short-lived CLI commands (`status`, `symbols`, etc.) and the MCP server to share the same watcher without each spawning their own. `context0 codemap mcp` auto-spawns the daemon if it is not running.

**Read-only guard (`ErrNotIndexed`).** `graph.OpenReadOnly()` checks for the DB file's existence before opening with `mode=ro`, returning a clear `ErrNotIndexed` sentinel. This prevents accidental DB creation by read-only commands and gives users an actionable error message.

**Hybrid search (Memory).** FTS5 alone would fail on vocabulary mismatch between how memories are written and how they are queried. Vector search alone would miss exact keyword matches. RRF fusion combines both, giving better recall than either approach alone.

**Hard fail on embed error (Memory).** If the embedding server is unreachable, `SaveMemory` returns an error and writes nothing. This preserves the invariant that every doc in `docs` has a corresponding vector in `docs_vec`. Silently saving without a vector would degrade search quality and produce inconsistent hybrid ranking.

**FTS5-only for Agenda.** Agenda queries are keyword-oriented (task title, status). Embedding-based search provides no meaningful benefit and would introduce an unnecessary dependency on the embedding server for task management.

**Two separate MCP servers.** Memory/Agenda and Code Exploration have different lifecycles — the codemap server is stateful (holds the LSP subprocess pool and file watcher), while the memory/agenda server is stateless per-call. Separating them avoids forcing the heavy codemap initialization on every memory/agenda operation.

**sqlite-vec for vector storage.** Keeps the entire stack in a single SQLite file per feature. No separate vector database process, no network hop for ANN queries, no synchronization complexity between stores.

**Tree-sitter + LSP separation.** Tree-sitter provides fast, offline symbol extraction (nodes). LSP provides cross-reference and implementation edges that require a running language server. Separating them allows the index to partially succeed even if LSP servers are unavailable.

**Debounced incremental re-indexing.** A 500ms debounce prevents excessive LSP calls during rapid file edits (e.g. save-on-type). The watcher self-terminates after 5 minutes of inactivity to avoid holding LSP server processes open indefinitely.
