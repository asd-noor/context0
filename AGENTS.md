# AGENTS.md — Working on context0

This file is the operating guide for AI agents working on the context0 codebase.

context0 is a CLI tool that gives AI agents persistent memory, task management, and code exploration — backed by per-project SQLite databases and a local Python inference sidecar. You are working on the tool itself, so you can (and should) use it on itself.

---

## Before you start

### Query existing memory first

Before exploring the codebase, query the project memory. It contains architecture notes, bug fixes, design decisions, and implementation details that are faster to retrieve than re-reading source.

```sh
/tmp/context0-dev memory query "<your topic>" --top 5
```

If `/tmp/context0-dev` does not exist, build it:

```sh
mise run dev
```

If the sidecar is not running, start it first:

```sh
mise run daemon
```

### Check active agenda plans

```sh
/tmp/context0-dev agenda plan list
```

---

## Navigating the codebase

Use codemap to find and understand symbols before modifying them.

```sh
# Find a symbol definition (with source)
/tmp/context0-dev codemap find <SymbolName> --source

# List all symbols in a file
/tmp/context0-dev codemap outline internal/memory/engine.go

# Understand the blast radius before changing a public symbol
/tmp/context0-dev codemap impact <SymbolName>

# Check the index is built
/tmp/context0-dev codemap status
```

If the codemap index is missing, build it:

```sh
mise run codemap:index
```

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for full system design.

---

## Build and test

All tasks are in `mise.toml`. The dev binary always goes to `/tmp/context0-dev`, never `~/.local/bin/context0` (that is the released version).

```sh
mise run dev             # build dev binary → /tmp/context0-dev
mise run check           # gofmt + go vet
mise run test:fast       # packages with no external dependencies
mise run test:sidecar    # Python sidecar tests (no prerequisites)
mise run test            # full suite (requires daemon + codemap index)
```

### Test prerequisites

| Requirement | Command | Needed for |
|---|---|---|
| Sidecar running | `mise run daemon` | `internal/memory`, `cmd/memory`, `cmd/exec` |
| Codemap index built | `mise run codemap:index` | `cmd/codemap` AfterIndex tests |
| *(none)* | — | `mise run test:sidecar` (Python) |

Packages that need the sidecar (`internal/memory`, `cmd/memory`, `cmd/exec`) skip automatically with a clear message if it is not running. `cmd/codemap` AfterIndex tests panic with a clear error if the index is missing.

See [docs/TESTING.md](docs/TESTING.md) for full test details.

---

## Repository layout

```
main.go                       # root command; --start-sidecar/--stop-sidecar/--project
cmd/                          # one file per top-level subcommand
  memory/, agenda/, codemap/  # the three engines
  ask/, exec/, docs-lib/      # sidecar-delegating commands
  backup/, recover/, export/, import/   # data management
internal/
  db/path.go                  # per-project DB path encoding
  archive/archive.go          # shared tar.gz helpers
  daemon/daemon.go            # PID file management + detached spawn
  sidecar/sidecar.go          # sidecar lifecycle + UDS client
  memory/                     # memory engine (db, embed, engine, rrf)
  agenda/                     # agenda engine (db, engine)
  graph/                      # code graph types, hash, SQLite store
  scanner/                    # Tree-sitter walker + per-language queries
  lsp/                        # LSP JSON-RPC client + uri + langroot
  watcher/                    # fsnotify incremental re-indexer
  codemapserver/              # codemap wiring: server, git, query helpers
  pkgmgr/                     # LSP binary resolver (PATH → cache → download)
sidecar/                      # Python sidecar (MLX embed + inference)
tests/sidecar/                # Python sidecar tests (excluded from //go:embed)
docs/                         # ARCHITECTURE, USAGE, INSTALL, TESTING
```

---

## Conventions

### Build tags and CGo

All packages that import SQLite (`mattn/go-sqlite3`, `sqlite-vec`, `tree-sitter`) require CGo and the `sqlite_fts5` build tag. This is set globally via `GOFLAGS="-tags=sqlite_fts5"` in `mise.toml`. Always build with `CGO_ENABLED=1`.

### Database naming

- Files: `memory-ctx0.sqlite`, `agenda-ctx0.sqlite`, `codemap-ctx0.sqlite`
- All stored under `~/.context0/<encoded-project-path>/` — path separators replaced with `=` (e.g. `/home/user/myrepo` → `home=user=myrepo`)

### Node IDs

Codemap node IDs are `SHA256(filePath:name:kind)[:16]` — stable 32-char hex strings.

### Task status values

Agenda tasks use exactly three status strings: `pending`, `in_progress`, `completed`.

### Error handling in engine functions

- Return the error directly; do not wrap unless adding context.
- Sidecar errors are hard failures (no partial writes).
- `DeleteMemory` on a non-existent ID is a silent no-op.
- `UpdateMemory` on a non-existent ID returns an error.

### Sidecar paths (all overridable via env vars)

| Resource | Default |
|---|---|
| Socket | `~/.context0/channel.sock` |
| PID file | `~/.context0/sidecar.pid` |
| Embed model | `mlx-community/bge-small-en-v1.5-4bit` |
| Infer model | `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit` |

### codemap constructors

- `codemapserver.New(ctx, rootDir)` — for read commands (`find`, `outline`, `impact`, etc.) and `watch --foreground`; idle timeout suppressed.
- `codemapserver.NewWatch(ctx, cancel, rootDir)` — for the background daemon; 5-minute idle auto-stop fires `cancel()`.

---

## Gotchas

**`sqlite-vec` virtual tables do not support `INSERT OR REPLACE`.**  
Use explicit `DELETE` then `INSERT` for upserts on `docs_vec`. The `UpdateMemory` function was bitten by this — see `internal/memory/engine.go:~180` and memory #22.

**macOS `t.TempDir()` + `t.Setenv("HOME", ...)` = false test failure.**  
macOS creates `~/Library` inside a temp HOME, which causes `t.TempDir()` cleanup to fail. Use `os.MkdirTemp("", "ctx0-test-*")` with a manual `os.RemoveAll` that ignores errors. See `cmd/codemap/codemap_test.go` for the pattern.

**`codemap watch --foreground` uses `New()`, not `NewWatch()`.**  
The foreground mode suppresses the idle timer by design — the process supervisor owns the lifecycle.

**`go test ./...` at the root requires `CGO_ENABLED=1`.**  
The `GOFLAGS` env var in `mise.toml` sets the build tag but not the CGo flag. Always export `CGO_ENABLED=1` or use `mise run test`.

**Do not add `__init__.py` to `tests/sidecar/`.**  
It shadows the real `sidecar` package at import time, breaking all sidecar imports in the test files. pytest's rootdir-relative import mode does not require it.

---

## After your changes

1. Update relevant memories:
   ```sh
   /tmp/context0-dev memory update <id> --content "<updated content>"
   /tmp/context0-dev memory save --category <c> --topic "<t>" --content "<...>"
   ```
2. Run `mise run check` (fmt + vet).
3. Run `mise run test:fast` at minimum; `mise run test` for a full pass.
4. Update `docs/ARCHITECTURE.md` or `docs/USAGE.md` if the change affects the public interface or system design.
