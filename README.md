# Context0

A persistent knowledge layer for AI coding agents. Context0 gives agents long-term memory, structured task management, codebase understanding, and local LLM inference -- all through a single CLI binary backed by per-project SQLite databases.

## What it does

- **Memory** -- Save and retrieve project knowledge using hybrid search (keyword + vector). AI agents store decisions, architecture notes, bug fixes, and context that persists across sessions.
- **Agenda** -- Structured task plans with acceptance criteria, optional tasks, and automatic completion tracking. Agents create plans, work through tasks, and verify done-conditions before marking them complete.
- **Code Exploration** -- A semantic code graph built from Tree-sitter AST parsing and LSP cross-references. Agents look up symbol definitions, list file symbols, and analyze change impact before modifying code. Supports Go, Python, JavaScript, TypeScript, Lua, and Zig. Also captures and graphs LSP diagnostics across files.
- **Library Docs** -- `context0 docs-lib <library> <question>` fetches up-to-date official documentation from Context7 for any library or framework. The `ask` planner calls this automatically when a query involves a specific external dependency.
- **Python Sidecar** -- A local MLX-backed inference and embedding server. Powers embedding (replacing Ollama), LLM generation (`ask`, `exec`, `discover`), and self-correcting Python script execution via the Ralph-loop. Before each repair attempt the sidecar runs a triage inference call: if the error is library-API-related it fetches Context7 docs and passes them into the repair prompt; failures degrade silently so the loop always continues. Managed with `context0 --daemon`.
- **Data Management** -- Four commands cover backup and portability: `backup` snapshots databases to `~/.context0/backup/`; `recover` restores the latest snapshot automatically; `export` packs databases into a portable `.tar.gz`; `import` restores from any `.tar.gz` after creating a safety snapshot.

## Quick start

```
# Build
CGO_ENABLED=1 go build -tags fts5 -o context0 .

# Or with mise (injects git tag as version)
mise run build

# Check version
context0 --version

# Start the Python sidecar (required for memory and ask/exec)
context0 --daemon

# Save a memory
context0 memory save --category architecture --topic "Auth design" --content "JWT with refresh tokens..."

# Query memories (full content by default)
context0 memory query "authentication"

# Create a task plan with acceptance criteria
context0 agenda plan create --title "Add auth" \
  --task "Implement JWT validation" --task-guard "go test passes" \
  --task "Add middleware to router" --task-guard "401 on missing token"

# Ask a natural-language question across all engines
context0 ask "What caching strategy does this project use?"

# Start the code exploration daemon
context0 codemap watch

# Look up a symbol with source code
context0 codemap symbol SaveMemory --source
```

All commands accept `--project <dir>` (or `-p`) to target a specific project. Defaults to CWD.

Set `CONTEXT7_API_KEY` in your environment for higher rate limits on `docs-lib` and the Ralph-loop doc triage (free key at context7.com/dashboard; unauthenticated access still works but is rate-limited).

## Documentation

- **[Installation](docs/INSTALL.md)** -- Prerequisites, build instructions, and sidecar setup
- **[Usage Guide](docs/USAGE.md)** -- Complete CLI reference for all engines
- **[Architecture](docs/ARCHITECTURE.md)** -- System design, data flows, schemas, and key decisions

## Tech stack

Go, SQLite (FTS5 + sqlite-vec), Tree-sitter, LSP, Python (MLX + uv), cobra.

## Data storage

All data lives in `~/.context0/<project-path>/` as SQLite files. Database names carry a `-ctx0` suffix for easy identification (e.g. `memory-ctx0.sqlite`, `agenda-ctx0.sqlite`, `<project>-ctx0.sqlite`). The sidecar socket and PID file live at `~/.context0/channel.sock` and `~/.context0/sidecar.pid`. Automatic backups are written to `~/.context0/backup/<project>/`.

## License

[GPL-3.0](LICENSE)
