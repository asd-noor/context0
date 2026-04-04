# Installation

## Prerequisites

- **Go 1.26+** — context0 is written in Go and requires CGo for SQLite and Tree-sitter bindings.
- **C compiler** — CGo requires a working C toolchain (`gcc` or `clang`). On macOS this is provided by Xcode Command Line Tools; on Linux, install `build-essential` or equivalent.
- **Python 3.11+** and **[uv](https://docs.astral.sh/uv/)** — required for the Python sidecar (memory, ask, exec). Install uv with `curl -LsSf https://astral.sh/uv/install.sh | sh`.

## Build from source

```sh
CGO_ENABLED=1 go build -tags fts5 -o context0 .
```

Both flags are required:
- `CGO_ENABLED=1` — enables C bindings for SQLite, sqlite-vec, and Tree-sitter.
- `-tags fts5` — enables SQLite FTS5 full-text search. Without it, Memory and Agenda will fail with `no such module: fts5`.

### Using mise

```sh
mise run build    # versioned binary in ./context0
mise run install  # build + install to ~/.local/bin (or $INSTALL_DIR)
```

### Verify

```sh
context0 --version
```

## Python sidecar

The sidecar provides embedding and LLM inference for the Memory engine and the `ask`/`exec` commands. It is managed entirely by context0.

### Start

```sh
context0 --start-sidecar
```

On first run, uv installs Python dependencies into `.venv/` and downloads the models into `~/.context0/models/`:

| Model | Default |
|---|---|
| Embedding | `mlx-community/bge-small-en-v1.5-4bit` (384 dims) |
| Inference | `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit` |

Subsequent starts use the local cache and are fast.

### Stop

```sh
context0 --stop-sidecar
```

### Override defaults

| Variable | Default | Description |
|---|---|---|
| `CTX0_SOCKET` | `~/.context0/channel.sock` | Unix socket path |
| `CTX0_SIDECAR_PID` | `~/.context0/sidecar.pid` | PID file path |
| `CTX0_EMBED_MODEL` | `mlx-community/bge-small-en-v1.5-4bit` | Embedding model |
| `CTX0_INFER_MODEL` | `mlx-community/Qwen2.5-Coder-3B-Instruct-4bit` | Inference model |
| `CONTEXT7_API_KEY` | *(none)* | Context7 API key for higher rate limits (free at context7.com/dashboard) |

## LSP servers (Code Exploration)

The codemap engine uses language servers for cross-reference enrichment. They are resolved automatically in order: **system PATH** → **cache** (`~/.context0/bin/`) → **auto-download**. You do not need to install them manually.

| Language | Server |
|---|---|
| Go | `gopls` |
| Python | `pyright-langserver` |
| TypeScript / JavaScript | `typescript-language-server` |
| Lua | `lua-language-server` |
| Zig | `zls` |

A background goroutine checks for newer versions and silently upgrades cached binaries.

## Data directory

All per-project data is stored under `~/.context0/<transformed-project-path>/`, where path separators are replaced with `=`:

```
/home/user/myproject  →  ~/.context0/home=user=myproject/
```

Files per project:

| File | Engine |
|---|---|
| `memory-ctx0.sqlite` | Memory |
| `agenda-ctx0.sqlite` | Agenda |
| `codemap-ctx0.sqlite` | Code Exploration |
