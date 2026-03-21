# Usage Guide

All commands accept `--project <dir>` (or `-p`) to target a specific project directory. Defaults to the current working directory.

---

## Memory Engine

Persistent, per-project knowledge store with hybrid search (keyword + vector).

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

Requires a running embedding server (Ollama). If unreachable, the save fails cleanly with no partial writes.

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

### Create an agenda

```
context0 agenda create --title <t> --description <d> [--task <T>]... [--task-guard <g>]... [--task-optional <bool>]...
```

- `--task` (`-T`): task description (repeat for multiple tasks)
- `--task-guard`: acceptance criteria for the corresponding task (positional pairing with `--task`)
- `--task-optional`: mark the corresponding task as optional (does not block auto-deactivation)

Example:
```
context0 agenda create \
  --title "Add authentication" \
  --description "JWT middleware for all protected routes" \
  --task "Create JWT validation in internal/auth" \
  --task-guard "go vet and go test pass" \
  --task "Add middleware to router" \
  --task-guard "Protected routes return 401 without token" \
  --task "Update API docs" \
  --task-guard "README documents auth header format"
```

### List agendas

```
context0 agenda list [--all]
```

By default, only active agendas are shown. Use `--all` to include completed (inactive) ones.

### View an agenda

```
context0 agenda get <id>
```

Shows the full agenda with all tasks, their completion status, and acceptance criteria.

Output:
```
Agenda #5 [active]
  Title      : Add authentication
  Description: JWT middleware for all protected routes
  Created    : 2026-03-21 14:30:00
  Tasks (3):
    [ ] #1: Create JWT validation in internal/auth
         Done when: go vet and go test pass
    [ ] #2: Add middleware to router
         Done when: Protected routes return 401 without token
    [ ] #3: Update API docs
         Done when: README documents auth header format
```

### Mark a task done

```
context0 agenda task done <agenda-id> <task-number>
```

Tasks are identified by **agenda ID** and **1-based task number** as displayed by `agenda get`.

Before marking a task done, verify its acceptance criteria ("Done when:" condition) is satisfied.

Example:
```
context0 agenda task done 5 1
```

Output: `agenda 5: task 1 marked as completed`

When all required (non-optional) tasks are complete, the agenda is automatically deactivated.

### Reopen a task

```
context0 agenda task reopen <agenda-id> <task-number>
```

### Search agendas

```
context0 agenda search <query> [--limit <n>]
```

FTS5 keyword search on agenda titles and descriptions.

### Update an agenda

```
context0 agenda update <id> [--title <t>] [--description <d>] [--deactivate] [--tasks <json>]
```

- `--tasks`: JSON array of tasks to append, e.g. `'[{"Details":"New task","AcceptanceGuard":"condition","IsOptional":false}]'`
- `--deactivate`: manually mark the agenda as inactive

### Delete an agenda

```
context0 agenda delete <id>
```

Only inactive (completed or deactivated) agendas can be deleted. Active agendas are protected.

---

## Code Exploration Engine

Semantic code graph built from Tree-sitter AST parsing and LSP cross-reference enrichment.

### Start the watcher daemon

```
context0 codemap watch
```

Spawns a background daemon that:
1. Performs a full index (Tree-sitter scan + LSP enrichment)
2. Watches for file changes and incrementally re-indexes
3. Auto-stops after 5 minutes of file inactivity

Output on success: `Watcher started, PIDFILE: <path>`
Output if already running: `codemap daemon is already running, PIDFILE: <path>`

Safe to call repeatedly -- it detects an existing daemon via the PID file.

### Check index status

```
context0 codemap status
```

Reports node/edge counts. Example: `nodes=215  edges=417`

### List symbols in a file

```
context0 codemap symbols <file> [--json]
```

Lists all symbols (functions, methods, types, classes, interfaces) in a file.

### Look up a symbol

```
context0 codemap symbol <name> [--source] [--json]
```

- Default: compact definition with file path and line numbers
- `--source`: includes the full source code in a fenced code block with language tag

Example:
```
context0 codemap symbol SaveMemory --source
```

### Analyze change impact

```
context0 codemap impact <name> [--json]
```

Recursive reverse traversal of the edge graph. Returns all symbols that directly or transitively depend on the target. Use this before modifying a public symbol to understand the blast radius.

### Force a full re-index

```
context0 codemap index
```

Only use this if the daemon cannot be started or the index is corrupt. The daemon handles indexing automatically under normal operation.

### Supported languages

| Language | Extensions | Symbol kinds | LSP server |
|---|---|---|---|
| Go | `.go` | function, method, type | gopls |
| Python | `.py` | function, class | pylsp |
| JavaScript | `.js`, `.jsx` | function, method, class | typescript-language-server |
| TypeScript | `.ts`, `.tsx` | function, method, class, interface, type | typescript-language-server |
| Lua | `.lua` | function, method | lua-language-server |
| Zig | `.zig` | function | zls |
| Templ | `.templ` | function (components, css, scripts) | templ |

### Skipped paths

`vendor/`, `node_modules/`, `__pycache__/`, `.git/`, `.venv/`, `zig-cache/`, `zig-out/`, and generated files (`_templ.go`, `.sql.go`, `_string.go`). Respects `.gitignore`.
