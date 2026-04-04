"""Embedding engine — bge-small-en-v1.5 via mlx-embeddings.

The engine is intentionally synchronous: it is called from executor threads
inside the asyncio server so as not to block the event loop.
"""

from __future__ import annotations

import os
import logging
from typing import TYPE_CHECKING

from .protocol import DEFAULT_EMBED_MODEL, EMBED_DIM

if TYPE_CHECKING:
    import mlx.core as mx

log = logging.getLogger(__name__)


class EmbedEngine:
    """Wraps an mlx-embeddings model and exposes a single :meth:`embed` call."""

    def __init__(self, model_id: str | None = None) -> None:
        self._model_id = model_id or os.getenv("CTX0_EMBED_MODEL", DEFAULT_EMBED_MODEL)
        self._model = None
        self._tokenizer = None

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    def load(self) -> None:
        """Preload model weights into memory (called once at sidecar startup)."""
        from mlx_embeddings import load

        log.info("loading embedding model: %s", self._model_id)
        self._model, self._tokenizer = load(self._model_id)
        log.info("embedding model ready")

    # ------------------------------------------------------------------
    # Inference
    # ------------------------------------------------------------------

    def embed(self, text: str) -> list[float]:
        """Return a normalised 384-dim embedding vector for *text*.

        Raises :class:`RuntimeError` if :meth:`load` has not been called.
        """
        if self._model is None or self._tokenizer is None:
            raise RuntimeError("embedding model is not loaded — call load() first")

        import mlx.core as mx
        from mlx_embeddings import generate as mlx_generate

        output = mlx_generate(
            self._model,
            self._tokenizer,
            texts=text,
            max_length=512,
            padding=True,
            truncation=True,
        )

        # text_embeds: normalised mean-pooled embeddings, shape (1, dim)
        vec: mx.array = output.text_embeds
        mx.eval(vec)
        floats: list[float] = vec[0].tolist()

        if len(floats) != EMBED_DIM:
            raise ValueError(
                f"embed: expected {EMBED_DIM} dimensions, got {len(floats)}"
            )
        return floats
