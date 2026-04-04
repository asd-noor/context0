"""Native Context7 MCP client.

Implements the MCP Streamable HTTP transport against the Context7 documentation
service at https://mcp.context7.com/mcp using only the Python standard library
(urllib.request + json).  No extra dependencies required.

MCP session lifecycle (one session per call — stateless reuse):
  1. POST /mcp  initialize  → captures Mcp-Session-Id response header
  2. POST /mcp  tools/call  → captures tool result from JSON response

Public API
----------
resolve_library(name, query="") -> str
    Resolve a human-readable library name to a Context7 library ID.
    Returns the library ID string (e.g. "/facebook/react") or raises
    Context7Error on failure.

get_docs(library_id, query, tokens=5000) -> str
    Fetch documentation for library_id filtered to query.
    Returns the raw markdown string or raises Context7Error.
"""

from __future__ import annotations

import json
import logging
import urllib.request
import urllib.error
from typing import Any

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

_MCP_URL = "https://mcp.context7.com/mcp"
_PROTOCOL_VERSION = "2024-11-05"
_CLIENT_INFO = {"name": "context0", "version": "1.0"}

# Accept both JSON (preferred) and SSE so the server has a choice.
_ACCEPT = "application/json, text/event-stream"


# ---------------------------------------------------------------------------
# Errors
# ---------------------------------------------------------------------------


class Context7Error(RuntimeError):
    """Raised when a Context7 MCP call fails."""


# ---------------------------------------------------------------------------
# MCP session
# ---------------------------------------------------------------------------


class _Session:
    """A single-use MCP session over Streamable HTTP."""

    def __init__(self) -> None:
        self._session_id: str | None = None

    # ------------------------------------------------------------------
    # Session lifecycle
    # ------------------------------------------------------------------

    def initialize(self) -> None:
        """Send the MCP initialize handshake and capture the session ID."""
        body = _jsonrpc(
            "initialize",
            {
                "protocolVersion": _PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": _CLIENT_INFO,
            },
            id=1,
        )
        resp_headers, data = self._post(body)
        # Capture the server-assigned session ID (may be absent for stateless servers).
        self._session_id = resp_headers.get("mcp-session-id")
        # Validate the response — the server must reply with an initialize result.
        result = _unwrap(data, id=1)
        if "protocolVersion" not in result:
            raise Context7Error(
                f"unexpected initialize result: {json.dumps(result)[:200]}"
            )

    # ------------------------------------------------------------------
    # Tool calls
    # ------------------------------------------------------------------

    def call_tool(self, tool_name: str, arguments: dict[str, Any]) -> Any:
        """Call a tool and return its parsed result value."""
        body = _jsonrpc(
            "tools/call",
            {
                "name": tool_name,
                "arguments": arguments,
            },
            id=2,
        )
        _, data = self._post(body)
        result = _unwrap(data, id=2)
        # MCP tools/call result: {"content": [{"type": "text", "text": "..."}], ...}
        content = result.get("content", [])
        if not content:
            raise Context7Error(f"tool {tool_name!r} returned empty content")
        # Concatenate all text chunks.
        parts: list[str] = []
        for chunk in content:
            if isinstance(chunk, dict) and chunk.get("type") == "text":
                parts.append(chunk.get("text", ""))
        if not parts:
            raise Context7Error(
                f"tool {tool_name!r} returned no text content: {json.dumps(content)[:200]}"
            )
        return "\n".join(parts)

    # ------------------------------------------------------------------
    # HTTP
    # ------------------------------------------------------------------

    def _post(self, body: bytes) -> tuple[dict[str, str], dict[str, Any]]:
        """POST body to the MCP endpoint and return (response_headers, parsed_json)."""
        headers: dict[str, str] = {
            "Content-Type": "application/json",
            "Accept": _ACCEPT,
        }
        if self._session_id:
            headers["Mcp-Session-Id"] = self._session_id

        req = urllib.request.Request(
            _MCP_URL,
            data=body,
            headers=headers,
            method="POST",
        )
        try:
            with urllib.request.urlopen(req, timeout=30) as resp:
                resp_headers = {k.lower(): v for k, v in resp.headers.items()}
                raw = resp.read()
        except urllib.error.HTTPError as exc:
            raise Context7Error(f"Context7 HTTP {exc.code}: {exc.reason}") from exc
        except OSError as exc:
            raise Context7Error(f"Context7 network error: {exc}") from exc

        content_type = resp_headers.get("content-type", "")

        # SSE fallback: the server may respond with text/event-stream even when
        # we prefer application/json.  Parse the first "data:" line.
        if "text/event-stream" in content_type:
            raw = _extract_sse_data(raw)

        try:
            parsed: dict[str, Any] = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise Context7Error(f"Context7 returned non-JSON: {raw[:200]!r}") from exc

        return resp_headers, parsed


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def resolve_library(name: str, query: str = "") -> str:
    """Resolve *name* to a Context7 library ID.

    Args:
        name:  Human-readable library name (e.g. ``"react"``, ``"numpy"``).
        query: Optional hint to improve disambiguation.

    Returns:
        The Context7 library ID string (e.g. ``"/facebook/react"``).

    Raises:
        Context7Error: on any network or protocol failure.
    """
    session = _Session()
    session.initialize()

    args: dict[str, Any] = {"libraryName": name}
    if query:
        args["query"] = query

    raw = session.call_tool("resolve-library-id", args)
    log.debug("resolve_library %r → %r", name, raw[:80])

    # The tool returns a plain-text library ID (possibly with surrounding text).
    # Extract the first token that looks like a path ("/org/project").
    library_id = _extract_library_id(raw)
    if not library_id:
        raise Context7Error(
            f"could not extract library ID from resolve-library-id response: {raw[:200]}"
        )
    return library_id


def get_docs(library_id: str, query: str, tokens: int = 5000) -> str:
    """Fetch documentation for *library_id* filtered to *query*.

    Args:
        library_id: Context7 library ID returned by :func:`resolve_library`.
        query:      Specific question or topic to focus the documentation on.
        tokens:     Maximum documentation tokens to return (default 5000).

    Returns:
        Markdown documentation string.

    Raises:
        Context7Error: on any network or protocol failure.
    """
    session = _Session()
    session.initialize()

    result = session.call_tool(
        "get-library-docs",
        {
            "libraryId": library_id,
            "query": query,
            "tokens": tokens,
        },
    )
    log.debug("get_docs %r / %r → %d chars", library_id, query, len(result))
    return result


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _jsonrpc(method: str, params: dict[str, Any], *, id: int) -> bytes:
    """Serialise a JSON-RPC 2.0 request."""
    return json.dumps(
        {"jsonrpc": "2.0", "id": id, "method": method, "params": params},
        separators=(",", ":"),
    ).encode()


def _unwrap(data: dict[str, Any], *, id: int) -> dict[str, Any]:
    """Extract the result from a JSON-RPC response, raising on error."""
    if "error" in data:
        err = data["error"]
        msg = err.get("message", str(err)) if isinstance(err, dict) else str(err)
        raise Context7Error(f"MCP error: {msg}")
    if data.get("id") != id:
        raise Context7Error(
            f"unexpected JSON-RPC id {data.get('id')!r} (expected {id})"
        )
    result = data.get("result")
    if result is None:
        raise Context7Error(
            f"JSON-RPC response missing 'result': {json.dumps(data)[:200]}"
        )
    return result


def _extract_sse_data(raw: bytes) -> bytes:
    """Extract the first ``data:`` payload from an SSE byte stream."""
    for line in raw.splitlines():
        text = line.decode(errors="replace").strip()
        if text.startswith("data:"):
            payload = text[len("data:") :].strip()
            return payload.encode()
    raise Context7Error("SSE response contained no 'data:' line")


def _extract_library_id(text: str) -> str:
    """Extract the first ``/org/repo`` token from *text*."""
    import re

    # Library IDs look like /facebook/react or /numpy/numpy
    match = re.search(r"/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+", text)
    return match.group(0) if match else ""
