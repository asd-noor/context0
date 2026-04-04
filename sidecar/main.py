"""Context0 Python sidecar — entry point.

Managed by ``context0 --daemon`` / ``context0 --kill-daemon``.

Startup sequence:
  1. Write PID to ~/.context0/sidecar.pid
  2. Preload embedding model (bge-small-en-v1.5)
  3. Preload inference model (Qwen2.5-Coder-3B)
  4. Bind Unix socket at ~/.context0/channel.sock
  5. Serve until SIGTERM / SIGINT

All paths can be overridden via environment variables for testing:
  CTX0_SOCKET      — UDS socket path  (default: ~/.context0/channel.sock)
  CTX0_SIDECAR_PID — PID file path    (default: ~/.context0/sidecar.pid)
  CTX0_EMBED_MODEL — embedding model  (default: mlx-community/bge-small-en-v1.5-4bit)
  CTX0_INFER_MODEL — inference model  (default: mlx-community/Qwen2.5-Coder-3B-Instruct-4bit)

Usage (invoked by the Go binary):
    uv run sidecar/main.py
"""

from __future__ import annotations

# ---------------------------------------------------------------------------
# Bootstrap: ensure the project root is on sys.path so that
# ``from sidecar.xxx import`` works when run as a plain script via
# ``uv run sidecar/main.py``.
# ---------------------------------------------------------------------------
import sys
import os
from pathlib import Path

_ROOT = Path(__file__).resolve().parent.parent
if str(_ROOT) not in sys.path:
    sys.path.insert(0, str(_ROOT))

# ---------------------------------------------------------------------------

import asyncio
import logging
import signal

from sidecar.embed import EmbedEngine
from sidecar.inference import InferenceEngine
from sidecar.server import SidecarServer

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

_CTX0_DIR = Path.home() / ".context0"
_SOCKET_PATH = os.getenv("CTX0_SOCKET") or str(_CTX0_DIR / "channel.sock")
_PID_PATH = os.getenv("CTX0_SIDECAR_PID") or str(_CTX0_DIR / "sidecar.pid")

# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [sidecar] %(levelname)s %(message)s",
    datefmt="%H:%M:%S",
    stream=sys.stderr,
)
log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# PID management
# ---------------------------------------------------------------------------


def _write_pid() -> None:
    _CTX0_DIR.mkdir(parents=True, exist_ok=True)
    Path(_PID_PATH).write_text(str(os.getpid()))


def _remove_pid() -> None:
    try:
        Path(_PID_PATH).unlink(missing_ok=True)
    except Exception:
        pass


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


async def _main() -> None:
    _write_pid()
    log.info("starting (pid=%d)", os.getpid())

    embed = EmbedEngine()
    inference = InferenceEngine()

    loop = asyncio.get_event_loop()

    log.info("preloading embedding model …")
    await loop.run_in_executor(None, embed.load)

    log.info("preloading inference model …")
    await loop.run_in_executor(None, inference.load)

    server = SidecarServer(
        socket_path=_SOCKET_PATH,
        embed=embed,
        inference=inference,
    )
    await server.start()

    # ------------------------------------------------------------------
    # Graceful shutdown on SIGTERM / SIGINT
    # ------------------------------------------------------------------
    stop_event = asyncio.Event()

    def _on_signal(sig: signal.Signals) -> None:
        log.info("received %s — initiating shutdown", sig.name)
        stop_event.set()

    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, _on_signal, sig)

    log.info("ready on %s", _SOCKET_PATH)

    # Run the server as a background task; wait for the stop event.
    serve_task = asyncio.create_task(server.serve_forever())
    await stop_event.wait()

    serve_task.cancel()
    try:
        await serve_task
    except asyncio.CancelledError:
        pass

    await server.stop()
    _remove_pid()
    log.info("stopped")


if __name__ == "__main__":
    try:
        asyncio.run(_main())
    except KeyboardInterrupt:
        pass
    finally:
        _remove_pid()
