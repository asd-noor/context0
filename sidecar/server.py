"""UDS server — accepts JSON requests and dispatches to engine handlers.

Protocol (newline-delimited JSON, one request per connection):
  Client → Server  : one JSON object terminated by \\n
  Server → Client  : one JSON object terminated by \\n, then close

Concurrency model:
  - The asyncio event loop handles I/O multiplexing.
  - All model calls (embed / inference) are offloaded to a thread-pool
    executor so the event loop stays responsive.
  - Two asyncio.Lock objects serialise access to each model independently,
    allowing embed and inference to run concurrently when needed (e.g. an
    ``ask`` call spawns a subprocess that calls ``memory query``, which in
    turn calls the embed endpoint — those must not deadlock).
"""

from __future__ import annotations

import asyncio
import json
import logging
import os
import shutil
import subprocess
from pathlib import Path
from typing import Any

from .protocol import (
    CMD_PING,
    CMD_EMBED,
    CMD_GENERATE,
    CMD_ASK,
    CMD_EXEC,
    CMD_DISCOVER,
    CMD_CONTEXT7,
)
from .embed import EmbedEngine
from .inference import InferenceEngine
from .ralph import ralph_exec, strip_fences
from .ask import ask as _ask_handler
from .prompts import DISCOVER_SYSTEM, DISCOVER_USER
from . import context7 as _context7

log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Server
# ---------------------------------------------------------------------------


class SidecarServer:
    """Asyncio Unix-socket server that wraps the embedding and inference engines."""

    def __init__(
        self,
        socket_path: str,
        embed: EmbedEngine,
        inference: InferenceEngine,
    ) -> None:
        self._socket_path = socket_path
        self._embed = embed
        self._inference = inference
        # Independent locks: embed and inference can run concurrently.
        self._embed_lock = asyncio.Lock()
        self._infer_lock = asyncio.Lock()
        self._server: asyncio.AbstractServer | None = None

    # ------------------------------------------------------------------
    # Lifecycle
    # ------------------------------------------------------------------

    async def start(self) -> None:
        """Bind the UDS socket and start accepting connections."""
        sock = Path(self._socket_path)
        sock.unlink(missing_ok=True)
        sock.parent.mkdir(parents=True, exist_ok=True)

        self._server = await asyncio.start_unix_server(
            self._handle_connection,
            path=self._socket_path,
        )
        log.info("sidecar listening on %s", self._socket_path)

    async def serve_forever(self) -> None:
        if self._server is None:
            raise RuntimeError("call start() before serve_forever()")
        async with self._server:
            await self._server.serve_forever()

    async def stop(self) -> None:
        if self._server:
            self._server.close()
            await self._server.wait_closed()
        Path(self._socket_path).unlink(missing_ok=True)

    # ------------------------------------------------------------------
    # Connection handler
    # ------------------------------------------------------------------

    async def _handle_connection(
        self,
        reader: asyncio.StreamReader,
        writer: asyncio.StreamWriter,
    ) -> None:
        try:
            line = await reader.readline()
            if not line:
                return

            try:
                req: dict[str, Any] = json.loads(line.decode())
            except json.JSONDecodeError as exc:
                await _send(writer, {"ok": False, "error": f"invalid JSON: {exc}"})
                return

            resp = await self._dispatch(req)
            await _send(writer, resp)

        except Exception:
            log.exception("unhandled error while processing connection")
            try:
                await _send(writer, {"ok": False, "error": "internal sidecar error"})
            except Exception:
                pass
        finally:
            writer.close()
            try:
                await writer.wait_closed()
            except Exception:
                pass

    # ------------------------------------------------------------------
    # Dispatcher
    # ------------------------------------------------------------------

    async def _dispatch(self, req: dict[str, Any]) -> dict[str, Any]:
        cmd = req.get("cmd")
        loop = asyncio.get_event_loop()

        # ---- ping ------------------------------------------------------
        if cmd == CMD_PING:
            return {"ok": True}

        # ---- embed -----------------------------------------------------
        if cmd == CMD_EMBED:
            text: str = req.get("text", "")
            async with self._embed_lock:
                try:
                    vec = await loop.run_in_executor(None, self._embed.embed, text)
                except Exception as exc:
                    return {"ok": False, "error": str(exc)}
            return {"ok": True, "embedding": vec}

        # ---- generate --------------------------------------------------
        if cmd == CMD_GENERATE:
            messages: list[dict] = req.get("messages", [])
            max_tokens: int = int(req.get("max_tokens", 512))
            temperature: float = float(req.get("temperature", 0.2))
            async with self._infer_lock:
                try:
                    text = await loop.run_in_executor(
                        None,
                        lambda: self._inference.generate(
                            messages, max_tokens, temperature
                        ),
                    )
                except Exception as exc:
                    return {"ok": False, "error": str(exc)}
            return {"ok": True, "text": text}

        # ---- ask -------------------------------------------------------
        if cmd == CMD_ASK:
            query: str = req.get("query", "")
            project: str = req.get("project") or os.getcwd()

            def _run_cmd(args: list[str]) -> str:
                exe = _context0_exe()
                full = [exe, "--project", project, *args]
                result = subprocess.run(
                    full, capture_output=True, text=True, timeout=15
                )
                out = result.stdout
                if result.returncode != 0:
                    out += result.stderr
                return out

            # Planning and compression both need the inference lock;
            # CLI execution (run_cmd) may call back via the embed endpoint
            # which uses a separate lock — no deadlock.
            async with self._infer_lock:
                try:
                    answer = await loop.run_in_executor(
                        None,
                        lambda: _ask_handler(query, project, self._inference, _run_cmd),
                    )
                except Exception as exc:
                    return {"ok": False, "error": str(exc)}
            return {"ok": True, "answer": answer}

        # ---- exec ------------------------------------------------------
        if cmd == CMD_EXEC:
            script: str = req.get("script", "")
            project_exec: str | None = req.get("project")
            async with self._infer_lock:
                try:
                    output, err = await loop.run_in_executor(
                        None,
                        lambda: ralph_exec(script, project_exec, self._inference),
                    )
                except Exception as exc:
                    return {"ok": False, "error": str(exc), "output": ""}
            if err:
                return {"ok": False, "error": err, "output": output}
            return {"ok": True, "output": output}

        # ---- discover --------------------------------------------------
        if cmd == CMD_DISCOVER:
            query_disc: str = req.get("query", "")
            project_disc: str = req.get("project") or os.getcwd()

            async with self._infer_lock:
                try:

                    def _full_discover() -> tuple[str, str | None]:
                        # Step 1: generate fd/rg script.
                        script = self._inference.generate(
                            [
                                {"role": "system", "content": DISCOVER_SYSTEM},
                                {
                                    "role": "user",
                                    "content": DISCOVER_USER.format(
                                        query=query_disc, project=project_disc
                                    ),
                                },
                            ],
                            max_tokens=512,
                            temperature=0.1,
                        )
                        script = strip_fences(script.strip())
                        # Step 2: run with Ralph-loop.
                        return ralph_exec(script, project_disc, self._inference)

                    output, err = await loop.run_in_executor(None, _full_discover)
                except Exception as exc:
                    return {"ok": False, "error": str(exc), "output": ""}

            if err:
                return {"ok": False, "error": err, "output": output}
            return {"ok": True, "output": output}

        # ---- context7 --------------------------------------------------
        if cmd == CMD_CONTEXT7:
            library: str = req.get("library", "")
            query_c7: str = req.get("query", "")
            tokens_c7: int = int(req.get("tokens", 5000))

            if not library:
                return {"ok": False, "error": "context7: 'library' field is required"}
            if not query_c7:
                return {"ok": False, "error": "context7: 'query' field is required"}

            # Pure HTTP — no model lock needed.  Run in executor to avoid
            # blocking the event loop on network I/O.
            try:

                def _fetch() -> tuple[str, str]:
                    lib_id = _context7.resolve_library(library, query_c7)
                    docs = _context7.get_docs(lib_id, query_c7, tokens_c7)
                    return lib_id, docs

                lib_id, docs = await loop.run_in_executor(None, _fetch)
            except _context7.Context7Error as exc:
                return {"ok": False, "error": str(exc)}
            except Exception as exc:
                return {"ok": False, "error": f"context7: unexpected error: {exc}"}
            return {"ok": True, "docs": docs, "library_id": lib_id}

        return {"ok": False, "error": f"unknown command: {cmd!r}"}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


async def _send(writer: asyncio.StreamWriter, data: dict) -> None:
    """Serialise *data* as a single JSON line and flush."""
    line = json.dumps(data, separators=(",", ":")) + "\n"
    writer.write(line.encode())
    await writer.drain()


def _context0_exe() -> str:
    """Locate the context0 binary (PATH first, then project root sibling)."""
    exe = shutil.which("context0")
    if exe:
        return exe
    # During development the binary lives next to the sidecar package.
    candidate = Path(__file__).resolve().parent.parent / "context0"
    if candidate.exists():
        return str(candidate)
    return "context0"
