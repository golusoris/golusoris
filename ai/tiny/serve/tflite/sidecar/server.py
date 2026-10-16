# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""HTTP inference sidecar server for LiteRT models."""

from __future__ import annotations

import argparse
import json
import logging
import os
import sys
import threading
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

from ai.tiny.serve.tflite.sidecar.runtime import (
    ModelNotLoadedError,
    OversizedError,
    Runtime,
)

logger = logging.getLogger("litert-sidecar")

DEFAULT_PORT = 8501
DEFAULT_HOST = "0.0.0.0"
MAX_LOAD_BODY_BYTES = 1 << 20  # 1 MiB
MAX_CLASSIFY_BODY_BYTES = 20 << 20  # 20 MiB (allows 16 MiB image base64 + JSON)
DEFAULT_CONCURRENCY = 8
DEFAULT_SOCKET_TIMEOUT = 10.0
MIN_SOCKET_TIMEOUT = 0.5
MAX_SOCKET_TIMEOUT = 300.0


class SidecarHandler(BaseHTTPRequestHandler):
    """HTTP request handler for LiteRT sidecar endpoints."""

    server: SidecarServer
    timeout: float = DEFAULT_SOCKET_TIMEOUT

    def setup(self) -> None:
        if (
            hasattr(self.server, "socket_timeout")
            and self.server.socket_timeout is not None
        ):
            self.timeout = self.server.socket_timeout
        super().setup()

    def do_GET(self) -> None:
        if self.path == "/healthz":
            self._handle_healthz()
            return
        self._send_json(HTTPStatus.NOT_FOUND, {"error": "not found"})

    def do_POST(self) -> None:
        if self.path == "/load":
            self._handle_load()
            return
        if self.path == "/classify":
            self._handle_classify()
            return
        self._send_json(HTTPStatus.NOT_FOUND, {"error": "not found"})

    def _handle_healthz(self) -> None:
        """Report readiness only. Never leak model details, inputs, or credentials."""
        status = "ready" if self.server.runtime.is_ready else "not_ready"
        http_code = (
            HTTPStatus.OK
            if self.server.runtime.is_ready
            else HTTPStatus.SERVICE_UNAVAILABLE
        )
        self._send_json(
            http_code,
            {
                "status": status,
                "backend": self.server.runtime.backend,
            },
        )

    def _handle_load(self) -> None:
        if not self.server.acquire_concurrency():
            self._send_json(
                HTTPStatus.TOO_MANY_REQUESTS, {"error": "concurrency limit exceeded"}
            )
            return
        try:
            body = self._read_json_body(MAX_LOAD_BODY_BYTES)
            if body is None:
                return
            if not isinstance(body, dict):
                self._send_json(
                    HTTPStatus.BAD_REQUEST,
                    {"error": "request body must be a JSON object"},
                )
                return

            uri = body.get("uri")
            sha256 = body.get("sha256")
            labels = body.get("labels")

            if not uri or not isinstance(uri, str):
                self._send_json(HTTPStatus.BAD_REQUEST, {"error": "uri is required"})
                return
            if not sha256 or not isinstance(sha256, str):
                self._send_json(HTTPStatus.BAD_REQUEST, {"error": "sha256 is required"})
                return
            if not labels or not isinstance(labels, list):
                self._send_json(
                    HTTPStatus.BAD_REQUEST, {"error": "labels list is required"}
                )
                return

            self.server.runtime.load(uri, sha256, labels)
            self._send_json(HTTPStatus.OK, {"status": "loaded"})
        except FileNotFoundError as exc:
            self._send_json(HTTPStatus.NOT_FOUND, {"error": str(exc)})
        except TimeoutError as exc:
            self._send_json(HTTPStatus.GATEWAY_TIMEOUT, {"error": str(exc)})
        except OversizedError as exc:
            self._send_json(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": str(exc)})
        except ValueError as exc:
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})
        except Exception as exc:
            logger.exception("unexpected error in /load")
            self._send_json(
                HTTPStatus.INTERNAL_SERVER_ERROR, {"error": f"load failed: {exc}"}
            )
        finally:
            self.server.release_concurrency()

    def _handle_classify(self) -> None:
        if not self.server.acquire_concurrency():
            self._send_json(
                HTTPStatus.TOO_MANY_REQUESTS, {"error": "concurrency limit exceeded"}
            )
            return
        try:
            body = self._read_json_body(MAX_CLASSIFY_BODY_BYTES)
            if body is None:
                return
            if not isinstance(body, dict):
                self._send_json(
                    HTTPStatus.BAD_REQUEST,
                    {"error": "request body must be a JSON object"},
                )
                return

            image = body.get("image")
            if not image or not isinstance(image, str):
                self._send_json(
                    HTTPStatus.BAD_REQUEST,
                    {"error": "image field is required and must be non-empty string"},
                )
                return

            scores = self.server.runtime.classify(image)
            self._send_json(HTTPStatus.OK, {"scores": scores})
        except ModelNotLoadedError as exc:
            # classify-before-load
            self._send_json(HTTPStatus.CONFLICT, {"error": str(exc)})
        except OversizedError as exc:
            self._send_json(HTTPStatus.REQUEST_ENTITY_TOO_LARGE, {"error": str(exc)})
        except ValueError as exc:
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": str(exc)})
        except Exception as exc:
            logger.exception("unexpected error in /classify")
            self._send_json(
                HTTPStatus.INTERNAL_SERVER_ERROR, {"error": f"classify failed: {exc}"}
            )
        finally:
            self.server.release_concurrency()

    def _read_json_body(self, max_bytes: int) -> Any | None:
        content_length_hdr = self.headers.get("Content-Length")
        if content_length_hdr is None:
            self._send_json(
                HTTPStatus.LENGTH_REQUIRED, {"error": "Content-Length header required"}
            )
            return None
        try:
            content_length = int(content_length_hdr)
        except ValueError:
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": "invalid Content-Length"})
            return None

        if content_length < 0:
            self._send_json(HTTPStatus.BAD_REQUEST, {"error": "invalid Content-Length"})
            return None

        if content_length > max_bytes:
            self.close_connection = True
            self._send_json(
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                {"error": f"request body {content_length} exceeds limit {max_bytes}"},
            )
            return None

        try:
            raw = self.rfile.read(content_length)
        except TimeoutError:
            try:
                self._send_json(
                    HTTPStatus.REQUEST_TIMEOUT, {"error": "request read timed out"}
                )
            except OSError:
                pass
            self.close_connection = True
            return None

        if len(raw) != content_length:
            self._send_json(
                HTTPStatus.BAD_REQUEST, {"error": "incomplete request body"}
            )
            return None

        try:
            return json.loads(raw.decode("utf-8"))
        except (json.JSONDecodeError, UnicodeDecodeError) as exc:
            self._send_json(
                HTTPStatus.BAD_REQUEST, {"error": f"invalid JSON payload: {exc}"}
            )
            return None

    def _send_json(self, status: int, payload: dict[str, Any]) -> None:
        blob = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(blob)))
        self.send_header("Cache-Control", "no-store")
        if self.close_connection:
            self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(blob)

    def log_message(self, format: str, *args: Any) -> None:
        logger.debug(
            "%s - - [%s] %s",
            self.address_string(),
            self.log_date_time_string(),
            format % args,
        )


class SidecarServer(ThreadingHTTPServer):
    """Threading HTTP server with bounded concurrency and runtime binding."""

    def __init__(
        self,
        server_address: tuple[str, int],
        runtime: Runtime,
        max_concurrency: int = DEFAULT_CONCURRENCY,
        socket_timeout: float = DEFAULT_SOCKET_TIMEOUT,
    ) -> None:
        super().__init__(server_address, SidecarHandler)
        self.runtime = runtime
        self._concurrency_sem = threading.BoundedSemaphore(max_concurrency)
        self.socket_timeout = max(
            MIN_SOCKET_TIMEOUT, min(MAX_SOCKET_TIMEOUT, float(socket_timeout))
        )

    def acquire_concurrency(self, timeout: float = 0.0) -> bool:
        if timeout <= 0:
            return self._concurrency_sem.acquire(blocking=False)
        return self._concurrency_sem.acquire(timeout=timeout)

    def release_concurrency(self) -> None:
        try:
            self._concurrency_sem.release()
        except ValueError:
            pass


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="LiteRT HTTP inference sidecar")
    parser.add_argument(
        "--host",
        default=os.environ.get("LITERT_HOST", DEFAULT_HOST),
        help=f"bind host (default: {DEFAULT_HOST})",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=int(os.environ.get("LITERT_PORT", str(DEFAULT_PORT))),
        help=f"bind port (default: {DEFAULT_PORT})",
    )
    parser.add_argument(
        "--concurrency",
        type=int,
        default=int(os.environ.get("LITERT_MAX_CONCURRENCY", str(DEFAULT_CONCURRENCY))),
        help=f"maximum concurrent operations (default: {DEFAULT_CONCURRENCY})",
    )
    parser.add_argument(
        "--socket-timeout",
        "--timeout",
        dest="timeout",
        type=float,
        default=float(
            os.environ.get("LITERT_SOCKET_TIMEOUT", str(DEFAULT_SOCKET_TIMEOUT))
        ),
        help=f"per-connection socket timeout in seconds (default: {DEFAULT_SOCKET_TIMEOUT})",
    )
    args = parser.parse_args(argv)

    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s"
    )
    runtime = Runtime()
    server = SidecarServer(
        (args.host, args.port),
        runtime,
        max_concurrency=args.concurrency,
        socket_timeout=args.timeout,
    )
    logger.info("LiteRT inference sidecar listening on %s:%d", args.host, args.port)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logger.info("shutting down")
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
