# Context0

A persistent knowledge layer for AI coding agents — long-term memory, task management, and codebase understanding, backed by per-project SQLite databases.

> **macOS (Apple Silicon) only.** The Python sidecar uses [MLX](https://github.com/ml-explore/mlx) for local inference and embedding, which requires an M-series Mac.

## Features

- **Memory** — hybrid keyword + vector search for project knowledge across sessions
- **Agenda** — structured task plans with acceptance criteria and completion tracking
- **Code Exploration** — semantic graph from Tree-sitter + LSP; symbol lookup and impact analysis
- **Library Docs** — fetch up-to-date official docs via `context0 docs-lib <library> <question>`
- **Python Sidecar** — local MLX inference and embedding server; powers `ask`, `exec`, and memory
- **Data Management** — `backup`, `recover`, `export`, `import` for database portability

## Quick start

```sh
# Build and install
mise run build
mise run install

# Start the sidecar (required for memory and ask/exec)
context0 --start-sidecar

# Save and query memory
context0 memory save --category arch --topic "Auth" --content "JWT with refresh tokens"
context0 memory query "authentication"

# Create a task plan
context0 agenda plan create --title "Add auth" \
  --guard "all tests pass" \
  --task "Implement JWT validation"

# Index and explore the codebase
context0 codemap watch
context0 codemap find SaveMemory --source
```

All commands accept `-p <dir>` to target a specific project (defaults to CWD).

## Development

```sh
mise run dev            # build dev binary to /tmp/context0-dev
mise run check          # fmt + vet
mise run test:fast      # tests with no external deps
mise run daemon         # build dev binary + start sidecar
mise run codemap:index  # build dev binary + index codebase
mise run test           # full suite (requires daemon + index)
```

See [Testing](docs/TESTING.md) for setup details.

## Documentation

- [Installation](docs/INSTALL.md) — prerequisites, build, sidecar setup
- [Usage Guide](docs/USAGE.md) — full CLI reference
- [Architecture](docs/ARCHITECTURE.md) — design, data flows, schemas
- [Testing](docs/TESTING.md) — test setup and coverage

## Tech stack

Go, SQLite (FTS5 + sqlite-vec), Tree-sitter, LSP, Python (MLX + uv), cobra.

## License

[GPL-3.0](LICENSE)

## Acknowledgements

Library documentation powered by [Context7](https://context7.com).
