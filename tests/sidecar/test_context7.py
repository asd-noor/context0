"""Tests for sidecar/context7.py — pure-logic helpers only (no network)."""

import json
import pytest

from sidecar.context7 import (
    Context7Error,
    _jsonrpc,
    _unwrap,
    _extract_sse_data,
    _extract_library_id,
)


# ---------------------------------------------------------------------------
# _jsonrpc
# ---------------------------------------------------------------------------


class TestJsonrpc:
    def test_structure(self):
        result = json.loads(_jsonrpc("tools/call", {"name": "foo"}, id=2))
        assert result["jsonrpc"] == "2.0"
        assert result["id"] == 2
        assert result["method"] == "tools/call"
        assert result["params"] == {"name": "foo"}

    def test_returns_bytes(self):
        assert isinstance(_jsonrpc("ping", {}, id=1), bytes)

    def test_compact_separators(self):
        # Should not contain ": " or ", " — must use compact separators
        raw = _jsonrpc("m", {"k": "v"}, id=99).decode()
        assert ": " not in raw
        assert ", " not in raw


# ---------------------------------------------------------------------------
# _unwrap
# ---------------------------------------------------------------------------


class TestUnwrap:
    def test_returns_result(self):
        data = {"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": "2024-11-05"}}
        assert _unwrap(data, id=1) == {"protocolVersion": "2024-11-05"}

    def test_raises_on_error_field(self):
        data = {"jsonrpc": "2.0", "id": 1, "error": {"code": -32600, "message": "bad"}}
        with pytest.raises(Context7Error, match="bad"):
            _unwrap(data, id=1)

    def test_raises_on_string_error(self):
        data = {"jsonrpc": "2.0", "id": 1, "error": "something went wrong"}
        with pytest.raises(Context7Error, match="something went wrong"):
            _unwrap(data, id=1)

    def test_raises_on_wrong_id(self):
        data = {"jsonrpc": "2.0", "id": 99, "result": {}}
        with pytest.raises(Context7Error, match="unexpected JSON-RPC id"):
            _unwrap(data, id=1)

    def test_raises_on_missing_result(self):
        data = {"jsonrpc": "2.0", "id": 1}
        with pytest.raises(Context7Error, match="missing 'result'"):
            _unwrap(data, id=1)


# ---------------------------------------------------------------------------
# _extract_sse_data
# ---------------------------------------------------------------------------


class TestExtractSseData:
    def test_single_data_line(self):
        raw = b'data: {"jsonrpc":"2.0","id":1,"result":{}}\n'
        result = _extract_sse_data(raw)
        assert result == b'{"jsonrpc":"2.0","id":1,"result":{}}'

    def test_skips_metadata_lines(self):
        raw = b"event: message\nid: 1\ndata: hello\n\n"
        assert _extract_sse_data(raw) == b"hello"

    def test_strips_leading_space_after_colon(self):
        raw = b"data:   trimmed\n"
        assert _extract_sse_data(raw) == b"trimmed"

    def test_no_data_line_raises(self):
        raw = b"event: open\nid: 42\n"
        with pytest.raises(Context7Error, match="no 'data:' line"):
            _extract_sse_data(raw)

    def test_empty_raises(self):
        with pytest.raises(Context7Error):
            _extract_sse_data(b"")


# ---------------------------------------------------------------------------
# _extract_library_id
# ---------------------------------------------------------------------------


class TestExtractLibraryId:
    def test_extracts_slash_org_repo(self):
        text = "The library ID is /facebook/react (version 18)."
        assert _extract_library_id(text) == "/facebook/react"

    def test_extracts_from_plain_id(self):
        assert _extract_library_id("/numpy/numpy") == "/numpy/numpy"

    def test_not_found_returns_empty(self):
        assert _extract_library_id("no id here") == ""

    def test_empty_string(self):
        assert _extract_library_id("") == ""

    def test_accepts_dots_and_dashes(self):
        text = "use /some-org/my.lib for this"
        assert _extract_library_id(text) == "/some-org/my.lib"
