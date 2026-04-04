"""Inference engine — Qwen2.5-Coder-3B via mlx-lm.

Exposes a single synchronous :meth:`generate` call that is safe to invoke
from executor threads inside the asyncio server.
"""

from __future__ import annotations

import os
import logging
from typing import TYPE_CHECKING

from .protocol import DEFAULT_INFER_MODEL
from .downloader import ensure_mlx_model

if TYPE_CHECKING:
    pass

log = logging.getLogger(__name__)


class InferenceEngine:
    """Wraps an mlx-lm model and exposes a blocking :meth:`generate` call."""

    def __init__(self, model_id: str | None = None) -> None:
        self._model_id = model_id or os.getenv("CTX0_INFER_MODEL", DEFAULT_INFER_MODEL)
        self._model = None
        self._tokenizer = None

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    def load(self) -> None:
        """Preload model weights into memory (called once at sidecar startup)."""
        from mlx_lm import load

        local_path = ensure_mlx_model(self._model_id)
        log.info("loading inference model: %s", local_path)
        self._model, self._tokenizer = load(local_path)
        log.info("inference model ready")

    # ------------------------------------------------------------------
    # Inference
    # ------------------------------------------------------------------

    def generate(
        self,
        messages: list[dict],
        max_tokens: int = 512,
        temperature: float = 0.2,
    ) -> str:
        """Run a chat-completion and return the full generated text.

        Args:
            messages:    OpenAI-style list of ``{"role": ..., "content": ...}``
                         dicts.
            max_tokens:  Maximum number of tokens to generate.
            temperature: Sampling temperature (0.0 → greedy).

        Raises:
            RuntimeError: If :meth:`load` has not been called.
        """
        if self._model is None or self._tokenizer is None:
            raise RuntimeError("inference model is not loaded — call load() first")

        from mlx_lm import stream_generate
        from mlx_lm.sample_utils import make_sampler

        # Build the prompt string from the message list.
        prompt: str = self._tokenizer.apply_chat_template(
            messages,
            add_generation_prompt=True,
            tokenize=False,
        )

        sampler = make_sampler(temp=temperature)

        parts: list[str] = []
        for response in stream_generate(
            self._model,
            self._tokenizer,
            prompt,
            max_tokens=max_tokens,
            sampler=sampler,
        ):
            parts.append(response.text)

        return "".join(parts)
