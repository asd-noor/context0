"""Tests for sidecar/ralph.py — pure logic and mocked subprocess/inference."""

from unittest.mock import MagicMock, patch, call
import pytest

from sidecar.ralph import (
    _strip_fences,
    strip_fences,
    ralph_exec,
    _fetch_repair_docs,
    MAX_RETRIES,
)


# ---------------------------------------------------------------------------
# _strip_fences / strip_fences
# ---------------------------------------------------------------------------


class TestStripFences:
    def test_plain_text_unchanged(self):
        assert _strip_fences("hello world") == "hello world"

    def test_python_fence(self):
        text = "```python\nprint('hi')\n```"
        assert _strip_fences(text) == "print('hi')"

    def test_bare_fence(self):
        text = "```\nx = 1\n```"
        assert _strip_fences(text) == "x = 1"

    def test_only_open_fence(self):
        # Only opening fence — no closing fence
        text = "```\nx = 1"
        assert _strip_fences(text) == "x = 1"

    def test_empty_string(self):
        assert _strip_fences("") == ""

    def test_multiline_body_preserved(self):
        text = "```\nline1\nline2\nline3\n```"
        assert _strip_fences(text) == "line1\nline2\nline3"

    def test_public_alias(self):
        # strip_fences must delegate to _strip_fences
        assert strip_fences("```\nfoo\n```") == "foo"


# ---------------------------------------------------------------------------
# ralph_exec — success / retry / abort paths
# ---------------------------------------------------------------------------


class TestRalphExec:
    def _make_inference(self):
        return MagicMock()

    def test_success_first_attempt(self):
        inf = self._make_inference()
        with patch(
            "sidecar.ralph._run_script", return_value=("output", None)
        ) as mock_run:
            out, err = ralph_exec("print(1)", None, inf)
        assert err is None
        assert out == "output"
        mock_run.assert_called_once_with("print(1)", None)
        inf.generate.assert_not_called()

    def test_retries_then_succeeds(self):
        inf = self._make_inference()
        responses = [("", "SyntaxError"), ("output", None)]

        with (
            patch("sidecar.ralph._run_script", side_effect=responses) as mock_run,
            patch("sidecar.ralph._fetch_repair_docs", return_value=None),
            patch("sidecar.ralph._ask_repair", return_value="fixed_script"),
        ):
            out, err = ralph_exec("bad_script", None, inf)

        assert err is None
        assert out == "output"
        assert mock_run.call_count == 2
        assert mock_run.call_args_list[1] == call("fixed_script", None)

    def test_gives_up_after_max_retries(self):
        inf = self._make_inference()
        # Always fail — need MAX_RETRIES + 1 run responses + MAX_RETRIES repair responses
        run_responses = [("", f"error{i}") for i in range(MAX_RETRIES + 1)]
        repair_scripts = [f"fixed{i}" for i in range(MAX_RETRIES)]

        with (
            patch("sidecar.ralph._run_script", side_effect=run_responses),
            patch("sidecar.ralph._fetch_repair_docs", return_value=None),
            patch("sidecar.ralph._ask_repair", side_effect=repair_scripts),
        ):
            out, err = ralph_exec("initial", None, inf)

        assert err is not None
        assert "error" in err

    def test_aborts_on_identical_repair(self):
        """Model returns same script → abort immediately (only 2 _run_script calls)."""
        inf = self._make_inference()
        run_responses = [("", "err1"), ("", "err2")]

        with (
            patch("sidecar.ralph._run_script", side_effect=run_responses) as mock_run,
            patch("sidecar.ralph._fetch_repair_docs", return_value=None),
            patch("sidecar.ralph._ask_repair", return_value="initial"),
        ):
            out, err = ralph_exec("initial", None, inf)

        # Aborts after 2 runs (first fail + repair returns same script → stop)
        assert mock_run.call_count == 1
        assert err is not None

    def test_passes_project_to_run_script(self):
        inf = self._make_inference()
        with patch("sidecar.ralph._run_script", return_value=("ok", None)) as mock_run:
            ralph_exec("script", "/my/project", inf)
        mock_run.assert_called_once_with("script", "/my/project")


# ---------------------------------------------------------------------------
# _fetch_repair_docs — graceful degradation
# ---------------------------------------------------------------------------


class TestFetchRepairDocs:
    def _make_inference(self, return_value="null"):
        inf = MagicMock()
        inf.generate.return_value = return_value
        return inf

    def test_inference_raises_returns_none(self):
        inf = MagicMock()
        inf.generate.side_effect = RuntimeError("model dead")
        result = _fetch_repair_docs("script", "err", inf)
        assert result is None

    def test_null_response_returns_none(self):
        result = _fetch_repair_docs("script", "err", self._make_inference("null"))
        assert result is None

    def test_empty_response_returns_none(self):
        result = _fetch_repair_docs("script", "err", self._make_inference(""))
        assert result is None

    def test_non_json_returns_none(self):
        result = _fetch_repair_docs("script", "err", self._make_inference("not json"))
        assert result is None

    def test_json_without_library_returns_none(self):
        payload = '{"query": "how to use numpy"}'
        result = _fetch_repair_docs("script", "err", self._make_inference(payload))
        assert result is None

    def test_json_without_query_returns_none(self):
        payload = '{"library": "numpy"}'
        result = _fetch_repair_docs("script", "err", self._make_inference(payload))
        assert result is None

    def test_context7_error_returns_none(self):
        from sidecar.context7 import Context7Error

        payload = '{"library": "numpy", "query": "array creation"}'
        inf = self._make_inference(payload)
        with patch(
            "sidecar.ralph.context7.resolve_library", side_effect=Context7Error("fail")
        ):
            result = _fetch_repair_docs("script", "err", inf)
        assert result is None

    def test_unexpected_exception_returns_none(self):
        payload = '{"library": "numpy", "query": "array creation"}'
        inf = self._make_inference(payload)
        with patch(
            "sidecar.ralph.context7.resolve_library", side_effect=ValueError("boom")
        ):
            result = _fetch_repair_docs("script", "err", inf)
        assert result is None

    def test_happy_path_returns_docs(self):
        payload = '{"library": "numpy", "query": "array creation"}'
        inf = self._make_inference(payload)
        with (
            patch(
                "sidecar.ralph.context7.resolve_library", return_value="/numpy/numpy"
            ),
            patch("sidecar.ralph.context7.get_docs", return_value="# numpy docs"),
        ):
            result = _fetch_repair_docs("script", "err", inf)
        assert result == "# numpy docs"

    def test_fenced_json_parsed(self):
        """Model sometimes wraps its JSON in a code fence."""
        payload = '```json\n{"library": "requests", "query": "get method"}\n```'
        inf = self._make_inference(payload)
        with (
            patch(
                "sidecar.ralph.context7.resolve_library", return_value="/psf/requests"
            ),
            patch("sidecar.ralph.context7.get_docs", return_value="# requests docs"),
        ):
            result = _fetch_repair_docs("script", "err", inf)
        assert result == "# requests docs"
