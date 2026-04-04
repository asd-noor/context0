"""Model downloader — ensures HuggingFace models are cached under ~/.context0/models/.

All context0 model weights live in one place:
    ~/.context0/models/<org>--<name>/

Override the base directory with $CTX0_MODELS_DIR.

Two public helpers:
    ensure_model(repo_id)      — download all files (used for embedding models)
    ensure_mlx_model(repo_id)  — download only MLX-compatible files (used for inference models)
"""

from __future__ import annotations

import logging
import os
from pathlib import Path

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

_MODELS_DIR = Path(os.getenv("CTX0_MODELS_DIR") or Path.home() / ".context0" / "models")

# File patterns that mlx-lm needs; excludes PyTorch / TF / Flax weights so we
# don't download multi-GB blobs that are never used on Apple Silicon.
_MLX_PATTERNS: list[str] = [
    "*.json",
    "model*.safetensors",
    "*.py",
    "tokenizer.model",
    "*.tiktoken",
    "tiktoken.model",
    "*.txt",
    "*.jsonl",
    "*.jinja",
]


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _local_dir(repo_id: str) -> Path:
    """Map ``org/name`` → ``~/.context0/models/org--name``."""
    return _MODELS_DIR / repo_id.replace("/", "--")


def _is_complete(path: Path) -> bool:
    """Return True if the directory looks like a fully downloaded model.

    We require config.json (present in every HF model repo) and at least one
    .safetensors shard so we know the weights are there too.
    """
    if not (path / "config.json").exists():
        return False
    return any(path.glob("*.safetensors"))


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def ensure_model(repo_id: str, allow_patterns: list[str] | None = None) -> str:
    """Return the local directory for *repo_id*, downloading if necessary.

    Files are stored at ``~/.context0/models/<org>--<name>/``.  Subsequent
    calls with the same *repo_id* are instant (existence check only).

    Args:
        repo_id:        HuggingFace repository ID, e.g. ``"BAAI/bge-small-en-v1.5"``.
        allow_patterns: Optional list of glob patterns passed to
                        ``snapshot_download``.  ``None`` downloads everything.

    Returns:
        Absolute path to the local model directory as a string.
    """
    local = _local_dir(repo_id)

    if _is_complete(local):
        log.debug("model cache hit: %s", local)
        return str(local)

    from huggingface_hub import snapshot_download

    log.info("downloading %s → %s", repo_id, local)
    local.mkdir(parents=True, exist_ok=True)

    snapshot_download(
        repo_id,
        local_dir=str(local),
        allow_patterns=allow_patterns,
    )

    log.info("download complete: %s", local)
    return str(local)


def ensure_mlx_model(repo_id: str) -> str:
    """Like :func:`ensure_model` but restricted to MLX-compatible file patterns.

    Skips PyTorch / TF / Flax weights — only downloads what ``mlx-lm`` needs.
    Use this for inference models (e.g. Qwen2.5-Coder).
    """
    return ensure_model(repo_id, allow_patterns=_MLX_PATTERNS)
