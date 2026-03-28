# Code Exploration Engine — Implementation Plan

Reference implementation: `codemap` (Go/MCP)
Target: Go, CLI + MCP daemon, SQLite (nodes + edges), Tree-sitter + LSP

---

## Overview

The Code Exploration Engine builds and maintains a real-time semantic graph of
the codebase. It combines fast AST parsing (Tree-sitter) with Language Server
Protocol integration to provide AI agents with deep code understanding:
symbol definitions, reference tracking, dependency analysis, and change-impact
queries.

The engine runs as a background process that is started on-demand (first CLI
invocation or first MCP tool call). It stays alive for 5 minutes of inactivity,
resetting the timer on every file change (with debouncing). It exposes its
results via MCP tools.

---

## Database Schema

File: `$HOME/.context0/<transformed-project-dir>/codemap.sqlite`

```sql
CREATE TABLE IF NOT EXISTS nodes (
    id         TEXT PRIMARY KEY,        -- stable hash: file_path + ":" + name + ":" + kind
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,
    file_path  TEXT NOT NULL,
    line_start INTEGER NOT NULL,
    line_end   INTEGER NOT NULL,
    col_start  INTEGER NOT NULL,
    col_end    INTEGER NOT NULL,
    symbol_uri TEXT,                    -- LSP URI (file:///...)
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_nodes_file_path ON nodes(file_path);
CREATE INDEX IF NOT EXISTS idx_nodes_name      ON nodes(name);

CREATE TABLE IF NOT EXISTS edges (
    source_id  TEXT NOT NULL,
    target_id  TEXT NOT NULL,
    relation   TEXT NOT NULL,           -- calls | implements | references | imports
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (source_id, target_id, relation),
    FOREIGN KEY (source_id) REFERENCES nodes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id);
CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id);
```

WAL mode enabled: `_journal_mode=WAL` for concurrent read safety.

---

## Core Data Types

```go
// Node represents a symbol in the codebase.
type Node struct {
    ID        string // stable hash
    Name      string
    Kind      string
    FilePath  string
    LineStart int
    LineEnd   int
    ColStart  int
    ColEnd    int
    SymbolURI string
}

// Edge represents a directed relationship between two symbols.
type Edge struct {
    SourceID string
    TargetID string
    Relation string // "calls" | "implements" | "references" | "imports"
}
```

---

## Phase 1 — Tree-sitter AST Scanning

### Supported Languages

| Extension      | Grammar                              |
|----------------|--------------------------------------|
| `.go`          | tree-sitter-go                       |
| `.py`          | tree-sitter-python                   |
| `.js` / `.jsx` | tree-sitter-javascript               |
| `.ts` / `.tsx` | tree-sitter-typescript               |
| `.lua`         | tree-sitter-lua                      |
| `.zig`         | tree-sitter-zig                      |

Generated/derived files are excluded (e.g. `*_templ.go`, `*.sql.go`,
`*_string.go`).

### Scanner Behaviour

- `Scan(ctx, rootDir)` — walks the entire workspace, respects `.gitignore`,
  skips hidden dirs and `node_modules` / `vendor` / `__pycache__`.
- `ScanFile(ctx, path)` — re-scans a single file (used by the watcher).
- For each supported file, parse with the matching Tree-sitter grammar and run
  a pre-compiled capture query to extract named symbols.
- Each symbol becomes a `Node`; its `ID` is a stable hash of
  `file_path + ":" + name + ":" + kind`.

### Tree-sitter Queries (per language)

Queries capture named declarations: functions, methods, classes, interfaces,
type definitions. Example captures (Go):

```scheme
(function_declaration name: (identifier) @name) @definition.function
(method_declaration   name: (field_identifier) @name) @definition.method
(type_declaration     (type_spec name: (type_identifier) @name)) @definition.type
```

Python, JavaScript/TypeScript, Lua, Zig follow the same pattern with
language-appropriate node types.

---

## Phase 2 — LSP Enrichment

After AST scanning produces nodes, the LSP layer enriches them with cross-file
edges. This runs in a worker pool (10 goroutines).

### Language Server Map

| Language   | Binary        | Args           | Auto-download |
|------------|---------------|----------------|---------------|
| Go         | `gopls`       | `serve`        | yes           |
| Python     | `pylsp`       | `--stdio`      | yes           |
| JS/TS      | `typescript-language-server` | `--stdio` | yes |
| Lua        | `lua-language-server` | `--stdio` | yes      |
| Zig        | `zls`         | _(none)_       | yes           |
| Templ      | `templ`       | `lsp`          | yes           |

**Resolution priority** (per language):
1. Already installed via CodeMap package manager → use cached binary
2. Found in system `$PATH` → use system binary
3. Auto-download via package manager

### LSP Client Protocol

Each language gets one persistent `Client` subprocess connected over
JSON-RPC 2.0 / stdio (standard LSP transport). The client:

1. Sends `initialize` + `initialized` handshake
2. For each file to enrich: sends `textDocument/didOpen`
3. For each definition-kind node: calls `textDocument/references`
4. For each interface-kind node: calls `textDocument/implementation`
5. Sends `textDocument/didClose` on completion
6. All calls are guarded by a 10-second per-call timeout

An adaptive wait (minimum 5 s from server start) allows gopls/pylsp to finish
their initial workspace indexing before queries are issued.

### Edge Generation

| LSP call              | Edge created                                      |
|-----------------------|---------------------------------------------------|
| `references` on A     | `(caller_node) --references--> (A)`               |
| `implementation` on I | `(impl_node) --implements--> (I)`                 |

`FindNode(path, line, col)` is used to map an LSP `Location` back to the
nearest enclosing `Node` in the graph store.

---

## Graph Store Operations

| Method                  | Description                                                 |
|-------------------------|-------------------------------------------------------------|
| `UpsertNode`            | INSERT OR REPLACE a single node                             |
| `BulkUpsertNodes`       | Transactional batch upsert                                  |
| `UpsertEdge`            | INSERT OR IGNORE a single edge                              |
| `BulkUpsertEdges`       | Transactional batch upsert                                  |
| `GetSymbolsInFile`      | All nodes for a path, ordered by `line_start`               |
| `GetSymbolLocation`     | All nodes matching a name, ordered by `file_path`           |
| `FindNode`              | Smallest node enclosing `(path, line, col)`                 |
| `FindImpact`            | Recursive CTE: all transitive dependents of a symbol name   |
| `DeleteNodesByFile`     | Remove all nodes (+ cascaded edges) for a path              |
| `PruneStaleFiles`       | Remove DB entries for files no longer in the workspace      |
| `Clear`                 | Truncate all nodes and edges                                |

### Impact Analysis Query

```sql
WITH RECURSIVE impacted AS (
    SELECT source_id
    FROM edges
    WHERE target_id IN (SELECT id FROM nodes WHERE name = ?)
    UNION
    SELECT e.source_id
    FROM edges e
    INNER JOIN impacted i ON e.target_id = i.source_id
)
SELECT DISTINCT n.*
FROM nodes n
JOIN impacted i ON n.id = i.source_id;
```

---

## File Watcher

Uses `fsnotify` for cross-platform file system events. Watches the entire
directory tree recursively (re-adds new subdirectories on `Create` events).
Respects `.gitignore` and skips hidden/vendor directories.

### Debouncing

- Each modified/created file is added to a `pendingFiles` map with a deadline
  of `now + 500ms`.
- A ticker goroutine polls every 100ms and processes files whose deadline has
  passed.
- This prevents thrashing during rapid saves (e.g. editor autosave).

### Per-file Re-index Sequence

1. `DeleteNodesByFile(path)` — remove stale nodes + cascade edges
2. `ScanFile(path)` — re-parse with Tree-sitter
3. `BulkUpsertNodes(nodes)` — store new nodes
4. `Enrich(nodes)` — LSP reference/impl edges
5. `BulkUpsertEdges(edges)` — store new edges

On `Remove` / `Rename`: only step 1 runs.

---

## Indexing Lifecycle & Status

```
IndexStatusIdle       → initial state
IndexStatusInProgress → scan + enrich running
IndexStatusReady      → index complete, queries served
IndexStatusFailed     → scan or enrich error
```

- Initial index runs in a background goroutine at startup.
- `WaitForIndex(ctx)` blocks callers (with 30 s timeout) until status is
  `Ready` or `Failed`, using a `chan struct{}` closed on completion.
- `index` MCP tool can force a full re-index; rejects concurrent requests.

---

## MCP Tools Exposed

| Tool                  | Args                                 | Description                                              |
|-----------------------|--------------------------------------|----------------------------------------------------------|
| `index`               | `force bool`                         | Scan workspace, update graph; returns node/edge counts   |
| `index_status`        | _(none)_                             | Returns current status + duration                        |
| `get_symbols_in_file` | `file_path string`                   | List all symbols in a file with name, kind, range        |
| `get_symbol`          | `symbol_name string`, `with_source bool` | Find symbol location(s); optionally include source   |
| `find_impact`         | `symbol_name string`                 | Recursive dependents of a symbol (change impact)         |

All query tools (`get_symbols_in_file`, `get_symbol`, `find_impact`) call
`WaitForIndex` before executing, transparently blocking until the graph is ready.

---

## MCP Resources

| URI                         | Description                                   |
|-----------------------------|-----------------------------------------------|
| `codemap://usage-guidelines`| System prompt / usage documentation for agents|

---

## MCP Prompts

| Name                     | Purpose                                               |
|--------------------------|-------------------------------------------------------|
| `explore_codebase`       | Guided workflow: index → list files → find symbols    |
| `analyze_change_impact`  | Workflow: find symbol → find_impact → summarize risk  |

---

## Go Package Layout

```
internal/
  scanner/
    scanner.go    # Scan(), ScanFile(), language registration
    queries.go    # Tree-sitter query strings per language
  graph/
    types.go      # Node, Edge, relation constants
    store.go      # SQLite store: upsert, query, prune, impact CTE
  db/
    db.go         # Open DB, WAL mode, schema migration, Execer interface
  lsp/
    lsp.go        # Service, Client, Enrich(), worker pool, edge generation
    transport.go  # ReadMessage / WriteMessage (LSP header framing)
    types.go      # JSON-RPC + LSP protocol structs
  server/
    server.go     # MCP server init, index status tracking, WaitForIndex
    tools.go      # Tool registrations (index, get_symbols_in_file, etc.)
    resources.go  # Resource registrations (usage-guidelines)
    prompts.go    # Prompt registrations
  watcher/
    watcher.go    # fsnotify watcher, debouncer, per-file re-index
  pkgmgr/
    manager.go    # Package manager: install dir, PATH management
    installer.go  # Download + install LSP binaries
    metadata.go   # Per-language LSP binary metadata (name, version, URL)
    resolver.go   # Version resolution
    updater.go    # Background update checker
    paths.go      # Platform-specific install paths
util/
  git.go          # FindGitRoot()
  uri.go          # PathToURI(), URIToPath()
  hash.go         # Stable node ID generation
main.go           # Startup: DB → Scanner → LSP → Watcher → MCP server
```

---

## Key Implementation Notes

1. **Node ID stability** — must be a deterministic hash of
   `(file_path, name, kind)` so upserts are idempotent across re-indexes.
2. **Cascade deletes** — `FOREIGN KEY ... ON DELETE CASCADE` on both edge
   columns means `DeleteNodesByFile` automatically cleans up all edges.
3. **Worker pool** — LSP enrichment uses 10 concurrent goroutines; each worker
   owns a `DidOpen` → `references` → `didClose` cycle for its assigned nodes.
4. **Adaptive LSP wait** — only blocks for `max(0, 5s − elapsed_since_start)`;
   avoids unnecessary delays on warm re-indexes.
5. **Gitignore compliance** — both the scanner and watcher load `.gitignore`
   via `go-gitignore` and skip matching paths.
6. **Generated file exclusion** — scanner skips `*_templ.go`, `*.sql.go`,
   `*_string.go` to avoid indexing machine-generated code.
7. **WAL + shared cache** — DSN: `file:path?cache=shared&mode=rwc&_journal_mode=WAL`
8. **Parameterized queries only** — never string-interpolate SQL.
9. **Context propagation** — all DB and LSP calls accept `context.Context` for
   cancellation and timeout propagation.
10. **LSP auto-download** — if a language server is absent from both the
    package manager cache and system `$PATH`, the package manager downloads and
    installs it automatically before the first enrichment run.
