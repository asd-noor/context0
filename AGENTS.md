# AGENTS.md

This document is the instruction manual for the agents involved in Context0 project.

# Context0

Context0 (`context0`) is a CLI tool that acts as a persistent knowledge layer for AI coding agents. Its features are:
- Memory management (project-specific decisions, notes, and information storage with semantic and keyword search capabilities)
- Agenda management (project-specific task tracking / todo management)
- Code exploration (explore codebase, powered by Tree-sitter and LSP)

The binary is named `context0`. All commands accept a root-level `--project / -p` flag (defaults to CWD) that is inherited by every subcommand.

The tool is exposed as a CLI only: `context0 memory ...`, `context0 agenda ...`, `context0 codemap ...`

Data is stored in SQLite databases under `$HOME/.context0/<transformed-project-dir>/`, where `<transformed-project-dir>` is the project directory path with separators replaced by equals signs.

## Tech Stack

- Go
- SQLite (with FTS5, Vector via sqlite-vec)
- Tree-sitter
- cobra (CLI framework)

## Dependencies

- Ollama (for embedding generation, used by Memory engine only; default model: `qllama/bge-small-en-v1.5`; LM Studio also supported)

## Memory Management

Long-term memory storage with semantic and keyword search capabilities.

Hybrid search: combines keyword (FTS5) and vector search using Reciprocal Rank Fusion (RRF).

SQLite-based storage with sqlite-fts5 and sqlite-vec extensions.

## Agenda Management

Project-specific structured agenda engine for task management with full-text search for plans and todo lists.

SQLite-based storage with sqlite-fts5 extension. No embeddings — all search is FTS5-based.

## Code Exploration

Two components:

**CLI commands** — exposed for querying the codebase: `index`, `status`, `symbols`, `symbol`, `impact`. Read-only commands return a clear error if no index exists yet rather than silently creating an empty database.

**Background daemon** — builds and maintains a real-time semantic graph of the codebase. Combines Tree-sitter AST parsing with LSP-based cross-reference enrichment. The daemon is started via `context0 codemap watch`, writes a PID file, and auto-stops after 5 minutes of file inactivity (timer resets with debouncing on each file edit).

LSP server binaries (`gopls`, `pylsp`, `typescript-language-server`, `lua-language-server`, `zls`) are resolved from PATH first, then cache (`~/.context0/bin/`), then auto-downloaded. After a binary is located, a background goroutine silently checks for a newer version and reinstalls if one is found (once per binary per process lifetime).

SQLite-based database for the semantic graph, enabling efficient retrieval and updates as the codebase evolves.
