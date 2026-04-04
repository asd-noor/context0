"""Tests for SidecarServer._dispatch — async, no real engines needed."""

import pytest
from unittest.mock import MagicMock, AsyncMock, patch

from sidecar.server import SidecarServer
from sidecar.protocol import CMD_PING, CMD_EMBED, CMD_CONTEXT7


def _make_server() -> SidecarServer:
    """Return a SidecarServer with mock engines (no models loaded)."""
    return SidecarServer(
        socket_path="/tmp/test-sidecar.sock",
        embed=MagicMock(),
        inference=MagicMock(),
    )


# ---------------------------------------------------------------------------
# ping
# ---------------------------------------------------------------------------


class TestDispatchPing:
    async def test_ping_returns_ok(self):
        server = _make_server()
        resp = await server._dispatch({"cmd": CMD_PING})
        assert resp == {"ok": True}


# ---------------------------------------------------------------------------
# unknown / missing command
# ---------------------------------------------------------------------------


class TestDispatchUnknown:
    async def test_unknown_command(self):
        server = _make_server()
        resp = await server._dispatch({"cmd": "nope"})
        assert resp["ok"] is False
        assert "unknown command" in resp["error"]

    async def test_missing_cmd_key(self):
        server = _make_server()
        resp = await server._dispatch({})
        assert resp["ok"] is False
        assert "unknown command" in resp["error"]


# ---------------------------------------------------------------------------
# embed
# ---------------------------------------------------------------------------


class TestDispatchEmbed:
    async def test_embed_calls_engine(self):
        server = _make_server()
        server._embed.embed.return_value = [0.1, 0.2, 0.3]

        resp = await server._dispatch({"cmd": CMD_EMBED, "text": "hello"})

        assert resp["ok"] is True
        assert resp["embedding"] == [0.1, 0.2, 0.3]
        server._embed.embed.assert_called_once_with("hello")

    async def test_embed_engine_error(self):
        server = _make_server()
        server._embed.embed.side_effect = RuntimeError("model exploded")

        resp = await server._dispatch({"cmd": CMD_EMBED, "text": "hello"})

        assert resp["ok"] is False
        assert "model exploded" in resp["error"]


# ---------------------------------------------------------------------------
# context7
# ---------------------------------------------------------------------------


class TestDispatchContext7:
    async def test_context7_missing_library(self):
        server = _make_server()
        resp = await server._dispatch(
            {"cmd": CMD_CONTEXT7, "library": "", "query": "something"}
        )
        assert resp["ok"] is False
        assert "library" in resp["error"]

    async def test_context7_missing_query(self):
        server = _make_server()
        resp = await server._dispatch(
            {"cmd": CMD_CONTEXT7, "library": "numpy", "query": ""}
        )
        assert resp["ok"] is False
        assert "query" in resp["error"]

    async def test_context7_success(self):
        server = _make_server()
        with (
            patch(
                "sidecar.server._context7.resolve_library", return_value="/numpy/numpy"
            ),
            patch("sidecar.server._context7.get_docs", return_value="# docs"),
        ):
            resp = await server._dispatch(
                {"cmd": CMD_CONTEXT7, "library": "numpy", "query": "array creation"}
            )

        assert resp["ok"] is True
        assert resp["library_id"] == "/numpy/numpy"
        assert resp["docs"] == "# docs"

    async def test_context7_error_propagated(self):
        from sidecar.context7 import Context7Error

        server = _make_server()
        with patch(
            "sidecar.server._context7.resolve_library",
            side_effect=Context7Error("network timeout"),
        ):
            resp = await server._dispatch(
                {"cmd": CMD_CONTEXT7, "library": "numpy", "query": "ndarray"}
            )

        assert resp["ok"] is False
        assert "network timeout" in resp["error"]
