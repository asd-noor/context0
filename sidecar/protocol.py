"""Wire-protocol constants shared across all sidecar modules.

Every JSON message exchanged over the Unix Domain Socket has the shape:

    Request  → {"cmd": "<CMD_*>", ...payload fields...}
    Response → {"ok": true/false, ...result fields...}
              or {"ok": false, "error": "<message>"}

The connection lifecycle is one-shot: client opens a connection, writes one
request line (newline-terminated JSON), reads one response line, then closes.
"""

from __future__ import annotations

# ---------------------------------------------------------------------------
# Command identifiers
# ---------------------------------------------------------------------------

CMD_PING = "ping"
"""Health-check — no payload required. Response: {"ok": true}"""

CMD_EMBED = "embed"
"""Generate a 384-dim embedding vector.
Request:  {"cmd": "embed", "text": "<string>"}
Response: {"ok": true, "embedding": [<float>, ...]}
"""

CMD_GENERATE = "generate"
"""Raw LLM completion (for direct callers that manage prompts themselves).
Request:  {"cmd": "generate", "messages": [...], "max_tokens": 512,
           "temperature": 0.2}
Response: {"ok": true, "text": "<generated string>"}
"""

CMD_ASK = "ask"
"""Natural-language orchestration over all context0 engines.
Request:  {"cmd": "ask", "query": "<string>", "project": "<path>"}
Response: {"ok": true, "answer": "<compressed answer>"}
"""

CMD_EXEC = "exec"
"""Execute a Python script via uv with Ralph-loop self-correction.
Request:  {"cmd": "exec", "script": "<python source>", "project": "<path>"}
Response: {"ok": true,  "output": "<stdout>"}
        | {"ok": false, "error": "<message>", "output": "<partial stdout>"}
"""

CMD_DISCOVER = "discover"
"""Generate and run a find/grep script to answer a code-discovery query.
Request:  {"cmd": "discover", "query": "<string>", "project": "<path>"}
Response: {"ok": true,  "output": "<result>"}
        | {"ok": false, "error": "<message>", "output": "<partial output>"}
"""

CMD_CONTEXT7 = "context7"
"""Fetch library documentation from Context7 via the MCP HTTP protocol.
Request:  {"cmd": "context7", "library": "<name>", "query": "<question>",
           "tokens": 5000}
Response: {"ok": true, "docs": "<markdown>", "library_id": "<id>"}
        | {"ok": false, "error": "<message>"}
"""

# ---------------------------------------------------------------------------
# Model defaults  (overridable via environment variables)
# ---------------------------------------------------------------------------

#: Default embedding model — bge-small-en-v1.5, 384 dims.
#: Override with $CTX0_EMBED_MODEL.
DEFAULT_EMBED_MODEL: str = "mlx-community/bge-small-en-v1.5-4bit"

#: Default inference model — Qwen2.5-Coder-3B 4-bit quantised for Apple Silicon.
#: Override with $CTX0_INFER_MODEL.
DEFAULT_INFER_MODEL: str = "mlx-community/Qwen2.5-Coder-3B-Instruct-4bit"

#: Output dimensionality of bge-small-en-v1.5.
EMBED_DIM: int = 384
