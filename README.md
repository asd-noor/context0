# Context0

A persistent knowledge layer for AI coding agents. Context0 gives agents long-term memory, structured task management, and codebase understanding -- all through a single CLI binary backed by per-project SQLite databases.

## What it does

- **Memory** -- Save and retrieve project knowledge using hybrid search (keyword + vector). AI agents store decisions, architecture notes, bug fixes, and context that persists across sessions.
- **Agenda** -- Structured task plans with acceptance criteria, optional tasks, and automatic completion tracking. Agents create plans, work through tasks, and verify done-conditions before marking them complete.
- **Code Exploration** -- A semantic code graph built from Tree-sitter AST parsing and LSP cross-references. Agents look up symbol definitions, list file symbols, and analyze change impact before modifying code. Supports Go, Python, JavaScript, TypeScript, Lua, Zig, and Templ. Also captures and graphs LSP diagnostics across files.

## Quick start

```
# Build
CGO_ENABLED=1 go build -tags fts5 -o context0 .

# Or with mise
mise run build

# Save a memory
context0 memory save --category architecture --topic "Auth design" --content "JWT with refresh tokens..."

# Query memories (full content by default)
context0 memory query "authentication"

# Create a task plan with acceptance criteria
context0 agenda create --title "Add auth" \
  --task "Implement JWT validation" --task-guard "go test passes" \
  --task "Add middleware to router" --task-guard "401 on missing token"

# Start the code exploration daemon
context0 codemap watch

# Look up a symbol with source code
context0 codemap symbol SaveMemory --source
```

All commands accept `--project <dir>` (or `-p`) to target a specific project. Defaults to CWD.

## Documentation

- **[Installation](docs/INSTALL.md)** -- Prerequisites, build instructions, and Ollama setup
- **[Usage Guide](docs/USAGE.md)** -- Complete CLI reference for all three engines
- **[Architecture](docs/ARCHITECTURE.md)** -- System design, data flows, schemas, and key decisions

## Tech stack

Go, SQLite (FTS5 + sqlite-vec), Tree-sitter, LSP, Ollama, cobra.

## Data storage

All data lives in `~/.context0/<project-path>/` as SQLite files -- no external services except Ollama for embeddings (memory engine only).

## License

[GPL-3.0](LICENSE)
