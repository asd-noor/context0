# Installation

## Prerequisites

- **Go 1.26+** -- Context0 is written in Go and requires CGo for SQLite and Tree-sitter bindings.
- **C compiler** -- CGo requires a working C toolchain (`gcc` or `clang`). On macOS this is provided by Xcode Command Line Tools; on Linux, install `build-essential` or equivalent.
- **Ollama** -- Required for the Memory engine's embedding generation. The Agenda and CodeMap engines do not need it.

## Build from source

```
CGO_ENABLED=1 go build -tags fts5 -o context0 .
```

Both `CGO_ENABLED=1` and `-tags fts5` are required:
- `CGO_ENABLED=1` -- Enables the C bindings for SQLite, sqlite-vec, and Tree-sitter.
- `-tags fts5` -- Enables SQLite FTS5 full-text search support at compile time. Without it, Memory and Agenda engines will fail with `no such module: fts5`.

### Using mise

If you use [mise](https://mise.jdx.dev/) for tool management, the project includes a build task:

```
mise run build
```

This runs the same command as above. The `mise.toml` also pins the Go version to 1.26.1.

### Verify the build

```
./context0 --help
```

You should see the three subcommands: `memory`, `agenda`, `codemap`.

## Setting up Ollama

The Memory engine requires an embedding model served by Ollama (or any OpenAI-compatible endpoint).

### Install Ollama

```
# macOS
brew install ollama

# Linux
curl -fsSL https://ollama.com/install.sh | sh
```

### Start the server

```
ollama serve
```

By default, Ollama listens on `http://localhost:11434`.

### Pull the embedding model

```
ollama pull qllama/bge-small-en-v1.5
```

This is the default 384-dimensional embedding model. It will be pulled automatically on first `memory save` if not already present.

### Custom embedding endpoint

Override the defaults via environment variables:

| Variable | Default | Description |
|---|---|---|
| `CTX0_EMBED_ENDPOINT` | `http://localhost:11434` | Ollama or LM Studio base URL |
| `CTX0_EMBED_MODEL` | `qllama/bge-small-en-v1.5` | Embedding model name |

Any OpenAI-compatible `/v1/embeddings` endpoint will work (e.g. LM Studio, vLLM, text-embeddings-inference).

## LSP servers (Code Exploration)

The CodeMap engine uses language servers for cross-reference enrichment. These are resolved automatically in order:

1. **System PATH** -- If the binary is already installed on your system, it is used directly.
2. **Cache** (`~/.context0/bin/`) -- Previously downloaded binaries are reused.
3. **Auto-download** -- If not found, the binary is downloaded from its upstream source (GitHub releases, npm, pip, or Go module proxy).

Supported language servers:

| Language | Server | Install manually (optional) |
|---|---|---|
| Go | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| Python | `pylsp` | `pip install python-lsp-server` |
| TypeScript/JavaScript | `typescript-language-server` | `npm i -g typescript-language-server typescript` |
| Lua | `lua-language-server` | GitHub releases |
| Zig | `zls` | GitHub releases |

You do not need to install these manually -- Context0 will download them if needed. A background goroutine checks for newer versions and silently upgrades cached binaries.

## Data directory

All per-project data is stored under:

```
~/.context0/<transformed-project-path>/
```

The project path is transformed by replacing path separators with `=`:

```
/home/user/myproject  -->  ~/.context0/home=user=myproject/
```

Files stored per project:
- `memory.sqlite` -- Memory engine database
- `agenda.sqlite` -- Agenda engine database
- `codemap.sqlite` -- Code graph database
- `codemap.pid` -- Watcher daemon PID file (temporary)
