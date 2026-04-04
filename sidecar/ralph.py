"""Ralph-loop — execute a Python script via *uv* with local self-correction.

On failure, Qwen-coder-3B receives the traceback and attempts a fix.
It handles common cases: missing imports, syntax errors, off-by-one logic.
After MAX_RETRIES failed repair attempts the original error is returned
to the caller unmodified.
"""

from __future__ import annotations

import json
import subprocess
import logging
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .inference import InferenceEngine

from . import context7
from .context7 import Context7Error
from .prompts import (
    EXEC_REPAIR_SYSTEM,
    EXEC_REPAIR_USER,
    REPAIR_TRIAGE_SYSTEM,
    REPAIR_TRIAGE_USER,
)

log = logging.getLogger(__name__)

MAX_RETRIES = 2


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def ralph_exec(
    script: str,
    project: str | None,
    inference: "InferenceEngine",
) -> tuple[str, str | None]:
    """Run *script* via ``uv run -``, self-correcting on failure.

    Args:
        script:    Python source code to execute.
        project:   Working directory for the subprocess (typically the project
                   root so relative imports / file paths resolve correctly).
        inference: Loaded :class:`~sidecar.inference.InferenceEngine` used for
                   repair generation.

    Returns:
        A ``(output, error)`` tuple where *error* is ``None`` on success or a
        string describing the final failure after all retries are exhausted.
    """
    current = script
    last_output = ""
    last_error: str | None = None

    for attempt in range(MAX_RETRIES + 1):
        output, err = _run_script(current, project)
        if err is None:
            return output, None

        last_output = output
        last_error = err

        if attempt == MAX_RETRIES:
            log.warning(
                "ralph-loop: giving up after %d attempts; last error: %s",
                attempt + 1,
                err[:120],
            )
            break

        log.info("ralph-loop: attempt %d failed, asking model to repair", attempt + 1)
        docs = _fetch_repair_docs(current, err, inference)
        fixed = _ask_repair(current, err, inference, docs=docs)
        if fixed == current:
            # Model returned the same script — no point retrying.
            log.warning("ralph-loop: model produced identical script, aborting")
            break
        current = fixed

    return last_output, last_error


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _run_script(script: str, project: str | None) -> tuple[str, str | None]:
    """Execute *script* via ``uv run -`` and return ``(stdout, error_or_None)``."""
    try:
        result = subprocess.run(
            ["uv", "run", "-"],
            input=script,
            capture_output=True,
            text=True,
            cwd=project or None,
            timeout=30,
        )
    except subprocess.TimeoutExpired:
        return "", "script timed out after 30 seconds"
    except FileNotFoundError:
        return "", "uv not found in PATH — install uv to use exec"

    if result.returncode == 0:
        return result.stdout, None

    # Prefer stderr; fall back to a generic message.
    err = (
        result.stderr or ""
    ).strip() or f"process exited with code {result.returncode}"
    return result.stdout, err


def _ask_repair(
    script: str,
    error: str,
    inference: "InferenceEngine",
    *,
    docs: str | None = None,
) -> str:
    """Ask the inference model to fix *script* given *error*.

    Args:
        script:    Python source that failed.
        error:     Captured stderr / error message.
        inference: Loaded inference engine.
        docs:      Optional library documentation fetched by :func:`_fetch_repair_docs`.
                   When provided it is injected into the repair prompt so the model
                   can reference up-to-date API information.
    """
    if docs:
        docs_section = (
            "\nLIBRARY DOCUMENTATION\n"
            "The following up-to-date library docs may help with the fix:\n\n"
            f"{docs}\n"
        )
    else:
        docs_section = ""

    raw = inference.generate(
        [
            {"role": "system", "content": EXEC_REPAIR_SYSTEM},
            {
                "role": "user",
                "content": EXEC_REPAIR_USER.format(
                    script=script,
                    error=error,
                    docs_section=docs_section,
                ),
            },
        ],
        max_tokens=1024,
        temperature=0.1,
    )
    return _strip_fences(raw.strip())


def _fetch_repair_docs(
    script: str,
    error: str,
    inference: "InferenceEngine",
) -> str | None:
    """Triage the error and fetch library docs if they would help.

    Uses a tiny, low-temperature inference call to decide whether the error is
    library-API-related and, if so, which library and query to use.  Then calls
    Context7 to fetch relevant documentation.

    Designed for total graceful degradation: **any** failure (inference,
    network, protocol) is caught and logged; the function always returns either
    a documentation string or ``None``.

    Args:
        script:    The Python script that failed.
        error:     The error / traceback string.
        inference: Loaded inference engine used for the triage call.

    Returns:
        Markdown documentation string, or ``None`` if docs are not needed or
        could not be fetched.
    """
    # --- triage inference ------------------------------------------------
    script_head = "\n".join(script.splitlines()[:40])
    try:
        raw = inference.generate(
            [
                {"role": "system", "content": REPAIR_TRIAGE_SYSTEM},
                {
                    "role": "user",
                    "content": REPAIR_TRIAGE_USER.format(
                        script_head=script_head,
                        error=error,
                    ),
                },
            ],
            max_tokens=128,
            temperature=0.1,
        )
    except Exception as exc:  # noqa: BLE001
        log.debug("ralph-loop: triage inference failed (degrading): %s", exc)
        return None

    decision_raw = raw.strip()
    log.debug("ralph-loop: triage response: %r", decision_raw[:120])

    # Model is supposed to output JSON or the literal word "null".
    if not decision_raw or decision_raw.lower() == "null":
        return None

    # Parse JSON — the model may wrap it in a code fence despite instructions.
    json_str = _strip_fences(decision_raw)
    try:
        decision = json.loads(json_str)
    except json.JSONDecodeError:
        log.debug(
            "ralph-loop: triage returned non-JSON %r, skipping docs", json_str[:80]
        )
        return None

    if not isinstance(decision, dict):
        return None

    library = (decision.get("library") or "").strip()
    query = (decision.get("query") or "").strip()
    if not library or not query:
        return None

    log.info("ralph-loop: triage → fetching docs for %r / %r", library, query)

    # --- context7 fetch --------------------------------------------------
    try:
        library_id = context7.resolve_library(library, query)
        docs = context7.get_docs(library_id, query, tokens=2000)
        log.info(
            "ralph-loop: fetched %d chars of docs for %r (%s)",
            len(docs),
            library,
            library_id,
        )
        return docs
    except Context7Error as exc:
        log.debug("ralph-loop: context7 fetch failed (degrading): %s", exc)
        return None
    except Exception as exc:  # noqa: BLE001
        log.debug("ralph-loop: unexpected error in doc fetch (degrading): %s", exc)
        return None


def strip_fences(text: str) -> str:
    """Public alias used by server.py for discover script cleaning."""
    return _strip_fences(text)


def _strip_fences(text: str) -> str:
    """Remove ``` / ```python fences that the model may wrap output in."""
    lines = text.splitlines()
    if lines and lines[0].startswith("```"):
        lines = lines[1:]
    if lines and lines[-1].startswith("```"):
        lines = lines[:-1]
    return "\n".join(lines)
