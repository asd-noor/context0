---
name: code-scout
description: Explore a codebase's symbol graph using context0's Code Exploration Engine. Use when finding symbol definitions, listing file symbols, analyzing change impact, or starting the indexing daemon. Triggers on codemap, symbols, definitions, references, impact analysis, codebase exploration.
license: GPL-3.0
compatibility: Requires context0 binary in PATH. LSP servers (gopls, pylsp, typescript-language-server, lua-language-server, zls) are auto-downloaded if not in PATH.
allowed-tools: Bash
---

# Code Scout

Use this skill to explore a codebase's symbol graph using context0's Code Exploration Engine.

## When to use

- You need to find where a function, method, type, or class is defined
- You want to see all symbols in a specific file
- You need to understand what would break if a symbol changed (impact analysis)
- You want to trigger or check the status of a codebase index

## CLI commands

```
context0 codemap watch                        # Start background daemon for live re-indexing
context0 codemap status                       # Current index status and elapsed duration
context0 codemap symbols <file>               # List all symbols in a file
context0 codemap symbol  <name> [--source]    # Find symbol definition; --source includes code
context0 codemap impact  <name>               # All dependents of a symbol (recursive)
context0 codemap index                        # Force full re-index (last resort only)
```

Add `--project <dir>` to any command to target a specific project directory instead of CWD.

## Prerequisites

The daemon builds the index on first run and keeps it up to date as files change. Always start it at the beginning of a session. The command returns immediately — the daemon runs in the background:

```
context0 codemap watch --project <dir>
```

Output on success: `Watcher started, PIDFILE: <path>`
Output if already running: `codemap daemon is already running, PIDFILE: <path>`

The daemon auto-stops after 5 minutes of file inactivity. Simply run `watch` again if you resume after a long break.

## Workflow

### Recommended sequence for codebase exploration

1. Start the daemon:
   ```
   context0 codemap watch --project <dir>
   ```

2. Check the index is ready:
   ```
   context0 codemap status
   ```
   `watch` returns immediately — the daemon indexes in the background. Wait and retry `status` if it reports `InProgress` or no index yet.

3. List symbols in a specific file to understand its structure:
   ```
   context0 codemap symbols internal/memory/engine.go
   ```

4. Look up a specific symbol definition:
   ```
   context0 codemap symbol SaveMemory --source
   ```

5. Analyse change impact before modifying a symbol:
   ```
   context0 codemap impact SaveMemory
   ```

### Impact analysis workflow

Before changing any public symbol, run `impact` to understand the blast radius:

```
context0 codemap impact QueryMemory
```

This performs a recursive reverse traversal of the edge graph, returning all symbols that directly or transitively depend on the target. Edge relations traversed: `calls`, `implements`, `references`, `imports`.

### Manual indexing (last resort)

Only use `context0 codemap index` if:
- The daemon cannot be started (e.g. no file-watch support in the environment)
- The index is corrupt or missing and `watch` fails to recover it

```
context0 codemap index
```

Do not use `index` as a routine step — the daemon handles indexing automatically.

## Storage and graph details

- Database: `~/.context0/<project>/codemap.sqlite`
- Node ID: `SHA256(filePath:name:kind)[:16]` — stable 32-char hex
- Supported languages: Go (`.go`), Python (`.py`), JavaScript (`.js`, `.jsx`), TypeScript (`.ts`, `.tsx`), Lua (`.lua`), Zig (`.zig`)
- Extracted kinds: `function`, `method`, `type`, `class`, `interface`
- Edge relations: `calls`, `implements`, `references`, `imports`
- Skipped paths: `vendor/`, `node_modules/`, `__pycache__/`, `.git/`, `zig-cache/`, `zig-out/`, generated files

The index is built in two phases:
1. **Tree-sitter scan** — fast, offline; extracts symbol nodes from AST
2. **LSP enrichment** — extracts cross-reference edges using `gopls`, `pylsp`, `typescript-language-server`, `lua-language-server`, or `zls`

LSP binaries are resolved automatically: `PATH` → cache (`~/.context0/bin/`) → auto-download.

## Tips

- Run `codemap watch` at the start of every session — it is safe to call even if already running
- Use `codemap symbols` on entry-point files first to get an overview before drilling into specific symbols
- Use `--source` on `codemap symbol` only when you need the implementation; omit it for fast definition lookups
- `codemap impact` traverses all edges recursively — for highly connected symbols the result set can be large; review it before making changes
