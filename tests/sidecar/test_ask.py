"""Tests for sidecar/ask.py — pure logic with mocked inference."""

import json
from unittest.mock import MagicMock, call
import pytest

from sidecar.ask import _plan, ask


# ---------------------------------------------------------------------------
# _plan
# ---------------------------------------------------------------------------


class TestPlan:
    def _make_inference(self, return_value: str) -> MagicMock:
        inf = MagicMock()
        inf.generate.return_value = return_value
        return inf

    def test_valid_commands(self):
        payload = json.dumps(
            [["memory", "query", "caching"], ["agenda", "task", "list"]]
        )
        inf = self._make_inference(payload)
        result = _plan("what do I remember?", inf)
        assert result == [["memory", "query", "caching"], ["agenda", "task", "list"]]

    def test_invalid_json_returns_empty(self):
        inf = self._make_inference("not json at all")
        assert _plan("q", inf) == []

    def test_not_a_list_returns_empty(self):
        inf = self._make_inference('{"key": "value"}')
        assert _plan("q", inf) == []

    def test_filters_non_list_items(self):
        payload = json.dumps([["memory", "query"], "bad_item", 42])
        inf = self._make_inference(payload)
        assert _plan("q", inf) == [["memory", "query"]]

    def test_filters_empty_lists(self):
        payload = json.dumps([["memory", "query"], []])
        inf = self._make_inference(payload)
        assert _plan("q", inf) == [["memory", "query"]]

    def test_filters_non_string_elements(self):
        payload = json.dumps([["memory", 42, "query"]])
        inf = self._make_inference(payload)
        assert _plan("q", inf) == []

    def test_strips_markdown_fence(self):
        payload = "```json\n" + json.dumps([["memory", "query"]]) + "\n```"
        inf = self._make_inference(payload)
        assert _plan("q", inf) == [["memory", "query"]]

    def test_strips_bare_fence(self):
        payload = "```\n" + json.dumps([["memory", "query"]]) + "\n```"
        inf = self._make_inference(payload)
        assert _plan("q", inf) == [["memory", "query"]]


# ---------------------------------------------------------------------------
# ask
# ---------------------------------------------------------------------------


class TestAsk:
    def _make_inference(self, answer: str = "compressed answer") -> MagicMock:
        inf = MagicMock()
        inf.generate.return_value = answer
        return inf

    def test_runs_commands_and_compresses(self):
        plan_json = json.dumps([["memory", "query", "caching"]])
        inf = self._make_inference("the answer")
        # First generate call returns the plan; second returns the compressed answer.
        inf.generate.side_effect = [plan_json, "the answer"]

        run_cmd = MagicMock(return_value="some output")
        result = ask("what do I remember?", "/proj", inf, run_cmd)

        assert result == "the answer"
        run_cmd.assert_called_once_with(["memory", "query", "caching"])
        # Two generate calls: plan + compress
        assert inf.generate.call_count == 2

    def test_empty_plan_answers_directly(self):
        """When the model returns an empty plan, ask() falls through to a direct answer."""
        inf = self._make_inference()
        inf.generate.side_effect = ["[]", "direct answer"]

        run_cmd = MagicMock()
        result = ask("hi", "/proj", inf, run_cmd)

        assert result == "direct answer"
        run_cmd.assert_not_called()

    def test_failed_command_handled_gracefully(self):
        """A run_command exception does not abort — error text is included in context."""
        plan_json = json.dumps([["memory", "query", "x"]])
        inf = self._make_inference()
        inf.generate.side_effect = [plan_json, "answer despite error"]

        run_cmd = MagicMock(side_effect=RuntimeError("context0 not found"))
        result = ask("q", "/proj", inf, run_cmd)

        assert result == "answer despite error"
        # Compress call must still happen (context contains the error text)
        assert inf.generate.call_count == 2

    def test_multiple_commands_all_executed(self):
        plan_json = json.dumps([["memory", "query", "a"], ["agenda", "task", "list"]])
        inf = self._make_inference()
        inf.generate.side_effect = [plan_json, "final"]

        run_cmd = MagicMock(return_value="output")
        ask("q", "/proj", inf, run_cmd)

        assert run_cmd.call_count == 2
        run_cmd.assert_any_call(["memory", "query", "a"])
        run_cmd.assert_any_call(["agenda", "task", "list"])
