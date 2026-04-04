"""Ralph-loop — execute a Python script via *uv* with local self-correction.

On failure, Qwen-coder-3B receives the traceback and attempts a fix.
It handles common cases: missing imports, syntax errors, off-by-one logic.
After MAX_RETRIES failed repair attempts the original error is returned
to the caller unmodified.
"""

from __future__ import annotations

import subprocess
import logging
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from .inference import InferenceEngine

from .prompts import EXEC_REPAIR_SYSTEM, EXEC_REPAIR_USER

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
        fixed = _ask_repair(current, err, inference)
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
) -> str:
    """Ask the inference model to fix *script* given *error*."""
    raw = inference.generate(
        [
            {"role": "system", "content": EXEC_REPAIR_SYSTEM},
            {
                "role": "user",
                "content": EXEC_REPAIR_USER.format(script=script, error=error),
            },
        ],
        max_tokens=1024,
        temperature=0.1,
    )
    return _strip_fences(raw.strip())


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
