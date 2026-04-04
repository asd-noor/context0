# Testing

## Python sidecar tests

No prerequisites. Tests cover pure logic only — no model or running sidecar required.

```sh
mise run test:sidecar
# or directly:
uv run --group dev pytest tests/sidecar/ -v
```

61 tests across four files, runs in ~0.06s.

| File | What is covered |
|---|---|
| `tests/sidecar/test_context7.py` | `_jsonrpc`, `_unwrap`, `_extract_sse_data`, `_extract_library_id` |
| `tests/sidecar/test_ralph.py` | `_strip_fences`/`strip_fences`, `ralph_exec` (5 paths), `_fetch_repair_docs` (10 degradation paths) |
| `tests/sidecar/test_ask.py` | `_plan` (JSON parsing, validation, fence-stripping), `ask` orchestration (4 paths) |
| `tests/sidecar/test_server.py` | `_dispatch`: ping, unknown/missing cmd, embed success/error, context7 field validation/success/error |

---

## Prerequisites

### Sidecar (memory tests)

The memory engine requires a running sidecar for embedding generation. Start it once before running tests:

```sh
go build -o /tmp/context0-dev . && /tmp/context0-dev --daemon
```

Tests in `internal/memory/` and `cmd/memory/` skip automatically with a clear message when the sidecar is not running.

### Codemap index (`cmd/codemap` tests)

`cmd/codemap` tests that exercise read commands (`status`, `outline`, `find`, `impact`, `diagnostics`) query the **real project index**. Build it once:

```sh
go build -o /tmp/context0-dev . && /tmp/context0-dev codemap index
```

The index is stored at `~/.context0/<encoded-project-path>/codemap-ctx0.sqlite`. `TestMain` panics with a clear error message if it is absent.

## Running tests

```sh
mise run test                        # full suite (sidecar pytest + go test ./...)
mise run test:sidecar                # Python sidecar tests only (no prerequisites)
mise run test:fast                   # Go packages with no external dependencies
go test ./...                        # go test directly (requires CGO_ENABLED=1)
go test ./internal/memory/ -v        # one package, verbose
go test ./cmd/codemap/ -v -timeout 120s
```

## Test categories

Tests fall into two categories depending on whether they need isolation.

### Real-project tests (no isolation)

These tests run against the actual project state — the real `$HOME`, the real databases, the real index. They are simpler, faster, and produce more meaningful results because they exercise the tool against the codebase it manages.

Used by: `cmd/codemap` AfterIndex tests (`TestStatusAfterIndex`, `TestOutlineAfterIndex`, `TestFindAfterIndex`, etc.)

### Isolation tests

These tests write data (create a fresh index, save memory docs, etc.) and must not touch the developer's real databases. They redirect `$HOME` to a temporary directory.

**macOS note**: `t.Setenv("HOME", t.TempDir())` causes macOS to create `~/Library` inside the temp HOME. Because `t.TempDir()` uses `os.RemoveAll` on cleanup and reports an error when it fails, this marks the test as failed even though nothing went wrong. The fix used throughout this codebase is `os.MkdirTemp` with a manual `os.RemoveAll` cleanup that ignores errors:

```go
func setupGoProject(t *testing.T) string {
    tmpHome, _ := os.MkdirTemp("", "ctx0-test-*")
    origHome := os.Getenv("HOME")
    os.Setenv("HOME", tmpHome)
    t.Cleanup(func() {
        os.Setenv("HOME", origHome)
        os.RemoveAll(tmpHome) // best-effort; macOS Library remnants are harmless
    })
    return projectRoot(t)
}
```

Used by: `cmd/codemap` NoIndex tests and write tests (`TestIndexCreatesDB`, `TestIndexOutputsNodeCount`); `internal/memory` (via `newEngine`); `internal/archive` (via `t.Setenv("HOME", ...)`).

## Package coverage

| Package | Test file | What is covered |
|---|---|---|
| `internal/memory` | `engine_test.go`, `rrf_test.go` | `SaveMemory`, `QueryMemory`, `UpdateMemory`, `DeleteMemory`; topK defaulting; FTS5/vector trigger cleanup; RRF score formula and ordering |
| `internal/archive` | `archive_test.go` | `Write`/`Extract` round-trip; base-name-only storage; PID file exclusion; atomic overwrite; `BackupDir` path structure; `Snapshot` noop and happy-path |
| `internal/daemon` | `daemon_test.go` | `WritePID`/`RemovePID`; `IsAlive` for current process, missing file, garbage content, zero PID, dead PID |
| `internal/agenda` | `agenda_test.go` | Full CRUD, FTS5 search (agenda fields + task details/notes), task lifecycle, auto-deactivation, blocked status, priority ordering, soft-delete/restore, notes, completed_at, memory hook |
| `internal/db` | `path_test.go` | `ProjectDir` encoding; DB name constants |
| `internal/codemapserver` | `git_test.go` | Git root detection |
| `internal/lsp` | `uri_test.go` | `file://` URI ↔ OS path conversion |
| `internal/graph` | *(in-package)* | Node/edge store, `ErrNotIndexed` |
| `internal/scanner` | *(in-package)* | Tree-sitter scan, language detection |
| `internal/sidecar` | `commands_test.go`, `embed_test.go` | Command serialization; embed client (skips without sidecar) |
| `cmd/codemap` | `codemap_test.go` | Index creation; no-index error paths; `status`, `outline`, `find`, `impact`, `diagnostics` output; JSON flags |
| `cmd/agenda` | `agenda_test.go` | CLI surface for plan and task subcommands; priority, branch, notes, soft-delete, restore, block, search by task content |
| `cmd/memory` | `memory_test.go` | CLI surface for memory subcommands (skips without sidecar) |
| `cmd/exec` | `exec_test.go` | CLI surface for exec (skips without sidecar) |
| `cmd/recover` | `recover_test.go` | `latestArchive`: missing dir, empty dir, non-`.tar.gz` ignored, single file, lexicographic selection, directory-with-.tar.gz-name ignored |
| `tests/sidecar/test_context7.py` | *(Python)* | `_jsonrpc`, `_unwrap`, `_extract_sse_data`, `_extract_library_id` |
| `tests/sidecar/test_ralph.py` | *(Python)* | `_strip_fences`/`strip_fences`, `ralph_exec` (5 paths), `_fetch_repair_docs` (10 degradation paths) |
| `tests/sidecar/test_ask.py` | *(Python)* | `_plan` JSON parsing/validation/fence-stripping, `ask` orchestration |
| `tests/sidecar/test_server.py` | *(Python)* | `_dispatch`: ping, unknown cmd, embed, context7 |

## Known issues fixed

**`UpdateMemory` UNIQUE constraint error** (`internal/memory/engine.go`): `sqlite-vec` virtual tables do not support `INSERT OR REPLACE`. The original code used `INSERT OR REPLACE INTO docs_vec` on update, which violated the primary key constraint. Fixed by replacing it with an explicit `DELETE` then `INSERT`.
