# project-notes

Use this skill to persist and retrieve project-specific knowledge using context0's Memory Engine.

## When to use

- You want to save a decision, finding, architecture note, bug fix, or checkpoint for the current project
- You need to look up what was previously recorded about this project
- You want to update or delete an existing memory

## CLI commands

```
context0 memory save    --category <c> --topic <t> --content <C>
context0 memory query   <text> [--top <k>]
context0 memory update  <id> [--category <c>] [--topic <t>] [--content <C>]
context0 memory delete  <id>
```

Add `--project <dir>` to any command to target a specific project directory instead of CWD.

## Workflow

### Saving a memory

Use `memory save` whenever you make a meaningful decision, discover important context, or complete a significant step.

Good `--category` values: `architecture`, `decision`, `bug`, `api`, `environment`, `checkpoint`, `dependency`

Example:
```
context0 memory save \
  --category decision \
  --topic "database driver choice" \
  --content "Chose mattn/go-sqlite3 over modernc because CGo linkage is acceptable and go-sqlite3 supports sqlite-vec via CGo extensions."
```

### Querying memories

Use `memory query` before starting a new task — it combines keyword (BM25) and vector search and returns the most relevant stored memories.

Example:
```
context0 memory query "why did we choose the sqlite driver" --top 5
```

### Updating a memory

When a decision changes or new information supersedes an old entry, update it rather than creating a duplicate:

```
context0 memory update 42 --content "Switched to modernc/sqlite3 to remove CGo dependency."
```

### Deleting a memory

Remove memories that are no longer accurate:

```
context0 memory delete 42
```

## Storage details

- Database: `~/.context0/<project>/memory.sqlite`
- Embedding model: `qllama/bge-small-en-v1.5` (384-dim) via Ollama at `http://localhost:11434` (LM Studio also supported)
- Overridable via `CTX0_EMBED_ENDPOINT` and `CTX0_EMBED_MODEL` environment variables
- Every `memory save` call requires the embedding server to be reachable — the call fails cleanly if it is not

## Tips

- Keep `--topic` short and specific — it is indexed and used in both keyword and vector search
- Include enough context in `--content` so the memory is self-contained when retrieved later
- Prefer updating over re-saving when a decision evolves — avoids stale duplicates in search results
- Use `--top` values between 3 and 10; higher values improve recall for broad queries
