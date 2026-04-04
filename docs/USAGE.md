# Usage Guide

All commands accept `--project <dir>` (or `-p`) to target a specific project directory. Defaults to the current working directory.

```
context0 --version   # print version and exit
context0 -v          # shorthand
```

---

## Python Sidecar

The sidecar is a local Python process that provides embedding and LLM inference. It must be running before using the Memory engine or the `ask`/`exec` commands.

### Start the sidecar

```
context0 --daemon
```

Spawns the Python sidecar as a detached background process via `uv run sidecar/main.py`. On first run it downloads and caches the embedding model (`mlx-community/bge-small-en-v1.5-4bit`) and inference model (`mlx-community/Qwen2.5-Coder-3B-Instruct-4bit`) under `~/.context0/models/`. Subsequent starts use the local cache and are fast.

Idempotent -- safe to call when already running.

Output: `sidecar started` (or `sidecar already running` if it was already live).

### Stop the sidecar

```
context0 --kill-daemon
```

Sends SIGTERM to the sidecar process.

### Liveness check

The Go binary checks the Unix socket (`~/.context0/channel.sock`) directly, not the PID file. This correctly handles stale PID files left by crashed processes.

---

## Memory Engine

Persistent, per-project knowledge store with hybrid search (keyword + vector). **Requires the sidecar to be running.**

### Save a memory

```
context0 memory save --category <c> --topic <t> --content <C>
```

- `--category` (`-c`) -- Classification (e.g. `architecture`, `decision`, `bug`, `feature`, `migration`)
- `--topic` (`-t`) -- Short descriptive title, indexed for search
- `--content` (`-C`) -- Full memory text

Example:
```
context0 memory save \
  --category decision \
  --topic "Database driver choice" \
  --content "Chose mattn/go-sqlite3 over modernc because CGo is acceptable and go-sqlite3 supports sqlite-vec extensions."
```

Requires a running sidecar for embedding generation. If the sidecar is unreachable, the save fails cleanly with no partial writes.

### Query memories

```
context0 memory query <text> [--top <k>] [--minimal]
```

- Default output: full untruncated content in structured blocks (designed for AI agent consumption)
- `--minimal`: compact table with content truncated to 80 characters (for quick human scanning)
- `--top` (`-k`): number of results (default: 3)

Combines BM25 keyword matching (FTS5) and vector similarity (sqlite-vec) via Reciprocal Rank Fusion (RRF).

Example:
```
context0 memory query "why did we choose sqlite"
```

Output:
```
ID: 1 | Category: decision | Topic: Database driver choice | Score: 0.0331

Chose mattn/go-sqlite3 over modernc because CGo is acceptable...
```

### Update a memory

```
context0 memory update <id> [--category <c>] [--topic <t>] [--content <C>]
```

Only provided fields are updated; omitted fields keep their existing values. Re-embeds the full document.

### Delete a memory

```
context0 memory delete <id>
```

Removes the memory and its vector embedding.

---

## Agenda Engine

Structured task and plan management with acceptance criteria and automatic completion tracking.

### Plans

#### Create a plan

```
context0 agenda plan create --title <t> --description <d> [--guard <g>] [--task <T>]... [--task-optional <bool>]...
```

- `--task` (`-T`): task description (repeat for multiple tasks)
- `--guard`: acceptance criteria for the plan as a whole ("Completion Condition:" condition)
- `--task-optional`: mark the corresponding task as optional (does not block auto-deactivation)

Example:
```
context0 agenda plan create \
  --title "Add authentication" \
  --description "JWT middleware for all protected routes" \
  --guard "All protected routes return 401 without token and docs are updated" \
  --task "Create JWT validation in internal/auth" \
  --task "Add middleware to router" \
  --task "Update API docs"
```

#### List plans

```
context0 agenda plan list [--all]
```

By default, only active plans are shown. Use `--all` to include completed (inactive) ones.

#### View a plan

```
context0 agenda plan get <id>
```

Shows the full plan with all tasks, their status, and acceptance criteria.

Task status symbols:
- `[ ]` — pending (not yet started)
- `[→]` — in progress (actively being worked on)
- `[x]` — completed

Output:
```
Agenda #5 [active]
  Title: Add authentication
  Description: JWT middleware for all protected routes
  Completion Condition: All protected routes return 401 without token and docs are updated
  Created: 2026-03-21 14:30:00
  Tasks (3):
    [ ] #1: Create JWT validation in internal/auth
    [→] #2: Add middleware to router
    [x] #3: Update API docs
```

#### Search plans

```
context0 agenda plan search <query> [--limit <n>]
```

FTS5 keyword search on plan titles and descriptions.

#### Update a plan

```
context0 agenda plan update <id> [--title <t>] [--description <d>] [--guard <g>] [--deactivate] [--tasks <json>]
```

- `--tasks`: JSON array of tasks to append, e.g. `'[{"Details":"New task","IsOptional":false}]'`
- `--guard`: update the plan-level acceptance criteria
- `--deactivate`: manually mark the plan as inactive

#### Delete a plan

```
context0 agenda plan delete <id>
```

Only inactive (completed or deactivated) plans can be deleted. Active plans are protected.

### Tasks

#### Add a task to an existing plan

```
context0 agenda task add <plan-id> --details <T> [--optional]
```

Appends a new task to an existing plan regardless of the plan's current active state.

- `--details` (`-T`): task description (required)
- `--optional`: mark task as optional (does not block auto-deactivation)

Example:
```
context0 agenda task add 5 --details "Write migration script"
```

Output: `added task id=4 to plan 5`

#### Mark a task as in-progress

```
context0 agenda task start <plan-id> <task-number>
```

Marks the task as actively in progress. The plan remains active.

Example:
```
context0 agenda task start 5 2
```

Output: `agenda 5: task 2 marked as in_progress`

#### Mark a task done

```
context0 agenda task done <plan-id> <task-number>
```

Tasks are identified by **plan ID** and **1-based task number** as displayed by `agenda plan get`.

Before marking the agenda as inactive, verify the plan's acceptance criteria ("Completion Condition:" condition) is satisfied.

Example:
```
context0 agenda task done 5 1
```

Output: `agenda 5: task 1 marked as completed`

When all required (non-optional) tasks are completed, the plan is automatically deactivated.

#### Reopen a task

```
context0 agenda task reopen <plan-id> <task-number>
```

Resets a task to `pending`. Tasks can be reopened from any status (in_progress or completed).

---

## Ask

Natural-language query orchestrated across all context0 engines. **Requires the sidecar to be running.**

```
context0 ask <query>
```

The sidecar plans which `memory`, `codemap`, and `agenda` commands to run, executes them, and compresses the results into a single answer. Arguments are joined, so quotes are not required.

Example:
```
context0 ask What caching strategy does this project use?
```

---

## Exec

Execute a Python script via the sidecar's Ralph-loop with automatic self-correction. **Requires the sidecar to be running.**

```
context0 exec <script-file>      # run a file
context0 exec -                  # read script from stdin
context0 exec 'print("hello")'   # inline one-liner
```

On failure the inference model automatically attempts to repair the script up to 2 times. Before each repair attempt a triage step checks whether the error is library-API-related; if so it fetches up-to-date Context7 docs and passes them into the repair prompt. Failures in triage or doc fetch degrade silently — the repair always runs regardless.

Example:
```
context0 exec analysis.py
echo 'import os; print(os.getcwd())' | context0 exec -
```

---

## Library Docs

Fetch up-to-date official documentation for any library, framework, or tool. **Requires the sidecar to be running.**

```
context0 docs-lib <library> <question>
```

Arguments are joined, so quotes are optional. Resolves the library name to a Context7 library ID, fetches docs filtered to the question, and prints them as markdown.

- `--tokens` / `-n`: maximum documentation tokens to return (default: 5000)

Examples:
```
context0 docs-lib react "how does useEffect work"
context0 docs-lib "go cobra" persistent flags
context0 docs-lib numpy broadcasting rules
context0 docs-lib -n 2000 pandas groupby aggregation
```

Output format:
```
# react (/facebook/react)

<markdown documentation>
```

The `ask` planner calls `docs-lib` automatically when a query involves a specific external dependency.

**API key**: set `CONTEXT7_API_KEY` in the environment for higher rate limits (get a free key at context7.com/dashboard). Without a key the service is still usable but rate-limited.

---

## Code Exploration Engine

Semantic code graph built from Tree-sitter AST parsing and LSP cross-reference enrichment.

### Start the watcher daemon

```
context0 codemap watch [--foreground]
```

Without `--foreground`, spawns a detached background daemon that:
1. Performs a full index (Tree-sitter scan + LSP enrichment)
2. Watches for file changes and incrementally re-indexes
3. Auto-stops after 5 minutes of file inactivity

With `--foreground`, runs the watcher in the current process instead of spawning a background daemon. The process blocks until it receives SIGINT or SIGTERM. The idle-timeout auto-stop is disabled — the caller is fully responsible for the process lifecycle. Useful for process supervisors (systemd, Docker, etc.) that manage the lifetime externally.

Output on success: `Watcher started, PIDFILE: <path>` (background) or `Watcher running in foreground, PIDFILE: <path>` (foreground)
Output if already running: `codemap daemon is already running, PIDFILE: <path>`

Safe to call repeatedly -- it detects an existing daemon via the PID file.

### Check index status

```
context0 codemap status
```

Reports node/edge counts. Example: `nodes=215  edges=417`

### List symbols in a file

```
context0 codemap outline <file> [--json]
```

Lists all symbols (functions, methods, types, classes, interfaces) in a file.

### Look up a symbol

```
context0 codemap find <name> [--source] [--json] [--lang <lang>]
```

- Default: compact definition with file path and line numbers
- `--source`: includes the full source code in a fenced code block with language tag
- `--lang`: filter results by language (e.g. `go`, `python`, `typescript`)

Example:
```
context0 codemap find SaveMemory --source
```

### Analyze change impact

```
context0 codemap impact <name> [--json]
```

Recursive reverse traversal of the edge graph. Returns all symbols that directly or transitively depend on the target. Use this before modifying a public symbol to understand the blast radius.

### List LSP Diagnostics

```
context0 codemap diagnostics [--file <path>] [--severity <level>] [--json]
```

Returns categorized LSP diagnostics across the codebase, collected during the last index run. Output is ordered by severity (error -> warning -> info -> hint) and file path. Use `--severity` to restrict output to a specific level (1=error, 2=warning, 3=info, 4=hint).

### Force a full re-index

```
context0 codemap index
```

Only use this if the daemon cannot be started or the index is corrupt. The daemon handles indexing automatically under normal operation.

### Discover (natural-language search)

```
context0 codemap discover <query>
```

Natural-language codebase search for languages not indexed by the codemap engine, or for ad-hoc structural queries. Generates a targeted `fd`/`rg` script via the local inference model and executes it with the Ralph-loop for automatic self-correction. **Requires the sidecar to be running.**

Example:
```
context0 codemap discover "Find all files that import sqlite3"
```

### Supported languages

| Language | Extensions | Symbol kinds | LSP server |
|---|---|---|---|
| Go | `.go` | function, method, type | gopls |
| Python | `.py` | function, class | pyright-langserver |
| JavaScript | `.js`, `.jsx` | function, method, class | typescript-language-server |
| TypeScript | `.ts`, `.tsx` | function, method, class, interface, type | typescript-language-server |
| Lua | `.lua` | function, method | lua-language-server |
| Zig | `.zig` | function | zls |

### Skipped paths

`vendor/`, `node_modules/`, `__pycache__/`, `.git/`, `.venv/`, `zig-cache/`, `zig-out/`, and generated files (`.sql.go`, `_string.go`). Respects `.gitignore`.

---

## Data Management

Four commands cover database backup and portability. All four use the same underlying tar.gz logic and exclude PID files from archives.

### Backup (automatic snapshots)

```
context0 backup
```

Snapshots all project databases to:

```
~/.context0/backup/<encoded-project>/<timestamp>.tar.gz
```

No arguments. The destination is always the managed backup directory. Use this for routine or pre-change snapshots.

Example output:
```
backed up → /Users/alice/.context0/backup/Users=alice=myrepo/20260404-143000.tar.gz
```

### Recover (restore latest snapshot)

```
context0 recover
```

Finds the most recent `.tar.gz` in `~/.context0/backup/<encoded-project>/`, snapshots the current state first for safety, then extracts.

No arguments — it always picks the latest backup. To restore from a specific archive use `import`.

Example output:
```
snapshot → /Users/alice/.context0/backup/Users=alice=myrepo/20260404-150000.tar.gz
3 file(s) restored → /Users/alice/.context0/Users=alice=myrepo/
```

### Export (portable archive)

```
context0 export [--output <path>]
```

Packs all project databases into a `.tar.gz` at the given path. Files are stored by base name only, making the archive portable across machines.

- `--output` / `-o`: destination directory or explicit file path (default: current directory)
  - If a directory is given, the file is named `<project>-ctx0-<timestamp>.tar.gz` automatically.

Example:
```
context0 export                          # → ./myrepo-ctx0-20260404-143000.tar.gz
context0 export -o /tmp/myrepo.tar.gz   # → /tmp/myrepo.tar.gz
```

Example output:
```
exported 3 file(s) → /Users/alice/myrepo-ctx0-20260404-143000.tar.gz
```

### Import (restore from archive)

```
context0 import <archive.tar.gz>
```

Snapshots the current state first for safety, then extracts the given archive into the project's data directory, overwriting existing files atomically.

Accepts any `.tar.gz` produced by `export` (or `backup`). Useful for transferring databases between machines or restoring from a manually managed archive.

Example:
```
context0 import /tmp/myrepo.tar.gz
```

Example output:
```
snapshot → /Users/alice/.context0/backup/Users=alice=myrepo/20260404-150000.tar.gz
3 file(s) restored → /Users/alice/.context0/Users=alice=myrepo/
```
