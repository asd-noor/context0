"""ask orchestration — plan → execute → compress.

The orchestrator interprets a natural-language query, decides which context0
CLI commands to run, collects their output, and compresses everything into a
single answer using the inference model.

This module is intentionally synchronous: it is executed inside a thread-pool
executor so the asyncio event loop stays unblocked.
"""

from __future__ import annotations

import json
import logging
import subprocess
from typing import Callable, TYPE_CHECKING

if TYPE_CHECKING:
    from .inference import InferenceEngine

from .prompts import (
    ASK_PLAN_SYSTEM,
    ASK_PLAN_USER,
    ASK_COMPRESS_SYSTEM,
    ASK_COMPRESS_USER,
    ASK_DIRECT_USER,
)

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

RunCommandFn = Callable[[list[str]], str]


def ask(
    query: str,
    project: str,
    inference: "InferenceEngine",
    run_command: RunCommandFn,
) -> str:
    """Orchestrate a query and return a compressed answer.

    Args:
        query:       Natural-language question from the caller.
        project:     Absolute path to the project root (passed to context0 CLI).
        inference:   Loaded :class:`~sidecar.inference.InferenceEngine`.
        run_command: Callable that takes a list of CLI args (e.g.
                     ``["memory", "query", "caching"]``) and returns stdout
                     as a string.  The implementation in server.py prepends
                     ``context0 --project <project>`` automatically.

    Returns:
        A compressed, accurate answer string.
    """
    # --- Step 1: plan ---------------------------------------------------
    plan = _plan(query, inference)
    log.info("ask: planned %d command(s) for query %r", len(plan), query[:60])

    # --- Step 2: execute ------------------------------------------------
    context_parts: list[str] = []
    for args in plan:
        label = " ".join(args)
        try:
            result = run_command(args)
            context_parts.append(f"[{label}]\n{result.strip()}")
        except Exception as exc:
            log.warning("ask: command %r failed: %s", label, exc)
            context_parts.append(f"[{label}]\nerror: {exc}")

    # --- Step 3: compress -----------------------------------------------
    if not context_parts:
        # No context gathered — answer directly from the model.
        return inference.generate(
            [
                {"role": "system", "content": ASK_COMPRESS_SYSTEM},
                {"role": "user", "content": ASK_DIRECT_USER.format(query=query)},
            ],
            max_tokens=512,
            temperature=0.3,
        )

    context = "\n\n".join(context_parts)
    return inference.generate(
        [
            {"role": "system", "content": ASK_COMPRESS_SYSTEM},
            {
                "role": "user",
                "content": ASK_COMPRESS_USER.format(query=query, context=context),
            },
        ],
        max_tokens=512,
        temperature=0.2,
    )


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _plan(query: str, inference: "InferenceEngine") -> list[list[str]]:
    """Ask the model which context0 commands to run for *query*."""
    raw = inference.generate(
        [
            {"role": "system", "content": ASK_PLAN_SYSTEM},
            {"role": "user", "content": ASK_PLAN_USER.format(query=query)},
        ],
        max_tokens=256,
        temperature=0.1,
    ).strip()

    # Strip accidental markdown fences.
    if raw.startswith("```"):
        lines = raw.splitlines()
        raw = "\n".join(
            lines[1:-1] if lines and lines[-1].startswith("```") else lines[1:]
        )

    try:
        parsed = json.loads(raw)
    except json.JSONDecodeError:
        log.warning("ask: model returned non-JSON plan: %r", raw[:200])
        return []

    if not isinstance(parsed, list):
        return []

    # Validate: each item must be a non-empty list of strings.
    valid: list[list[str]] = []
    for item in parsed:
        if isinstance(item, list) and item and all(isinstance(x, str) for x in item):
            valid.append(item)
    return valid
