# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Comprehensive tests for the LiteRT HTTP inference sidecar runtime and server."""

from __future__ import annotations

import base64
import hashlib
import http.client
import io
import json
import subprocess
import tempfile
import threading
from http import HTTPStatus
from pathlib import Path
from typing import Any, Self

import pytest
from PIL import Image

from ai.tiny.serve.tflite.sidecar.runtime import (
    MAX_IMAGE_DIMENSION,
    Runtime,
)
from ai.tiny.serve.tflite.sidecar.server import SidecarServer

_CACHED_MODEL_BYTES: dict[tuple[int, tuple[int, ...]], bytes] = {}


def generate_real_tflite_model(
    num_classes: int = 2,
    input_shape: tuple[int, ...] = (160, 160, 3),
) -> bytes:
    """Build a real valid .tflite model with MobileNet-compatible input and softmax output."""
    key = (num_classes, input_shape)
    if key in _CACHED_MODEL_BYTES:
        return _CACHED_MODEL_BYTES[key]

    try:
        import tensorflow as tf

        inputs = tf.keras.Input(shape=input_shape, name="image")
        x = tf.keras.layers.GlobalAveragePooling2D()(inputs)
        outputs = tf.keras.layers.Dense(
            num_classes, activation="softmax", name="scores"
        )(x)
        model = tf.keras.Model(inputs=inputs, outputs=outputs)
        converter = tf.lite.TFLiteConverter.from_keras_model(model)
        payload: bytes = converter.convert()
    except ImportError:
        # Fallback to uv run with pinned tensorflow 2.21.0
        with tempfile.NamedTemporaryFile(suffix=".tflite", delete=False) as tmp:
            tmp_path = Path(tmp.name)
        try:
            script = f"""
import tensorflow as tf
inputs = tf.keras.Input(shape={input_shape!r}, name="image")
x = tf.keras.layers.GlobalAveragePooling2D()(inputs)
outputs = tf.keras.layers.Dense({num_classes}, activation="softmax", name="scores")(x)
model = tf.keras.Model(inputs=inputs, outputs=outputs)
converter = tf.lite.TFLiteConverter.from_keras_model(model)
open("{tmp_path.as_posix()}", "wb").write(converter.convert())
"""
            cmd = [
                "uv",
                "run",
                "--python",
                "3.11.16",
                "--with",
                "tensorflow==2.21.0",
                "python",
                "-c",
                script,
            ]
            subprocess.run(cmd, capture_output=True, check=True)
            payload = tmp_path.read_bytes()
        finally:
            if tmp_path.exists():
                tmp_path.unlink()

    if len(payload) < 8 or payload[4:8] != b"TFL3":
        raise RuntimeError(
            f"failed to generate valid TFLite FlatBuffer: len={len(payload)}"
        )

    _CACHED_MODEL_BYTES[key] = payload
    return payload


def make_test_image(
    format_name: str = "PNG",
    dimensions: tuple[int, int] = (200, 200),
    color: tuple[int, int, int] = (255, 128, 0),
) -> str:
    """Generate a base64 encoded test image."""
    img = Image.new("RGB", dimensions, color=color)
    bio = io.BytesIO()
    img.save(bio, format=format_name)
    return base64.b64encode(bio.getvalue()).decode("utf-8")


@pytest.fixture(scope="session")
def valid_tflite_bytes() -> bytes:
    return generate_real_tflite_model(num_classes=2, input_shape=(160, 160, 3))


@pytest.fixture
def model_dir(tmp_path: Path) -> Path:
    d = tmp_path / "models"
    d.mkdir()
    return d


class TestRuntimeLoadAndClassify:
    """Unit tests for Runtime."""

    def test_load_file_and_classify_happy_path(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        assert not runtime.is_ready

        runtime.load(f"file://{model_file}", sha, ["cat", "dog"])
        assert runtime.is_ready

        img_b64 = make_test_image("PNG")
        scores = runtime.classify(img_b64)
        assert set(scores.keys()) == {"cat", "dog"}
        for prob in scores.values():
            assert 0.0 <= prob <= 1.0

    def test_classify_before_load_raises(self, model_dir: Path) -> None:
        runtime = Runtime(model_dir=model_dir)
        img_b64 = make_test_image("PNG")
        with pytest.raises(RuntimeError, match="classify-before-load"):
            runtime.classify(img_b64)

    def test_unsupported_locator_scheme(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        runtime = Runtime(model_dir=model_dir)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()
        with pytest.raises(ValueError, match="unsupported locator scheme"):
            runtime.load("http://example.com/model.tflite", sha, ["a", "b"])
        with pytest.raises(ValueError, match="unsupported locator scheme"):
            runtime.load("s3://bucket/model.tflite", sha, ["a", "b"])

    def test_file_outside_model_dir_rejected(
        self, tmp_path: Path, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        outside_file = tmp_path / "outside.tflite"
        outside_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        with pytest.raises(ValueError, match="outside mounted model directory"):
            runtime.load(f"file://{outside_file}", sha, ["a", "b"])

    def test_file_not_found(self, model_dir: Path) -> None:
        runtime = Runtime(model_dir=model_dir)
        sha = "a" * 64
        with pytest.raises(FileNotFoundError):
            runtime.load(f"file://{model_dir}/missing.tflite", sha, ["a", "b"])

    def test_digest_mismatch_rejected(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        bad_sha = "f" * 64

        runtime = Runtime(model_dir=model_dir)
        with pytest.raises(ValueError, match="digest mismatch"):
            runtime.load(f"file://{model_file}", bad_sha, ["a", "b"])

    def test_malformed_model_rejected(self, model_dir: Path) -> None:
        model_file = model_dir / "bad.tflite"
        bad_bytes = b"NOT_TFLITE_BYTES"
        model_file.write_bytes(bad_bytes)
        sha = hashlib.sha256(bad_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        with pytest.raises(ValueError, match="malformed model"):
            runtime.load(f"file://{model_file}", sha, ["a", "b"])

    def test_input_shape_mismatch_rejected(self, model_dir: Path) -> None:
        wrong_input_bytes = generate_real_tflite_model(
            num_classes=2, input_shape=(224, 224, 3)
        )
        model_file = model_dir / "wrong_input.tflite"
        model_file.write_bytes(wrong_input_bytes)
        sha = hashlib.sha256(wrong_input_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        with pytest.raises(ValueError, match="input/output shape mismatch"):
            runtime.load(f"file://{model_file}", sha, ["a", "b"])

    def test_output_shape_mismatch_rejected(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        # valid_tflite_bytes has 2 outputs, provide 3 labels
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        with pytest.raises(ValueError, match="input/output shape mismatch"):
            runtime.load(f"file://{model_file}", sha, ["a", "b", "c"])

    def test_invalid_labels_rejected(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        # Empty list
        with pytest.raises(ValueError, match="invalid labels"):
            runtime.load(f"file://{model_file}", sha, [])
        # Single label
        with pytest.raises(ValueError, match="invalid labels"):
            runtime.load(f"file://{model_file}", sha, ["cat"])
        # Duplicate labels
        with pytest.raises(ValueError, match="duplicate label"):
            runtime.load(f"file://{model_file}", sha, ["cat", "cat"])
        # Empty string label
        with pytest.raises(ValueError, match="must be non-empty"):
            runtime.load(f"file://{model_file}", sha, ["cat", "  "])

    def test_failed_reload_preserves_last_valid_model(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        good_file = model_dir / "good.tflite"
        good_file.write_bytes(valid_tflite_bytes)
        good_sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        bad_file = model_dir / "bad.tflite"
        bad_bytes = b"TFL3_corrupted_content"
        bad_file.write_bytes(bad_bytes)
        bad_sha = hashlib.sha256(bad_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{good_file}", good_sha, ["good_a", "good_b"])
        assert runtime.is_ready

        # Attempt reload with bad model
        with pytest.raises(ValueError, match="malformed model"):
            runtime.load(f"file://{bad_file}", bad_sha, ["bad_a", "bad_b"])

        # Preserved original model
        assert runtime.is_ready
        scores = runtime.classify(make_test_image("PNG"))
        assert set(scores.keys()) == {"good_a", "good_b"}

    def test_oversized_artifact_rejected(self, model_dir: Path) -> None:
        big_file = model_dir / "big.tflite"
        big_file.write_bytes(b"x" * 1024)
        sha = hashlib.sha256(big_file.read_bytes()).hexdigest()

        runtime = Runtime(model_dir=model_dir, max_artifact_bytes=512)
        with pytest.raises(ValueError, match="oversized artifact"):
            runtime.load(f"file://{big_file}", sha, ["a", "b"])

    def test_image_dimension_limits(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{model_file}", sha, ["cat", "dog"])

        oversized_dim_img = make_test_image(dimensions=(MAX_IMAGE_DIMENSION + 1, 10))
        with pytest.raises(ValueError, match="oversized image dimensions"):
            runtime.classify(oversized_dim_img)

    def test_preprocess_image_size_check_before_load(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        runtime = Runtime()
        oversized_dim_img = make_test_image(dimensions=(MAX_IMAGE_DIMENSION + 1, 10))
        raw_bytes = base64.b64decode(oversized_dim_img)

        load_called = False
        orig_load = Image.Image.load

        def mock_load(self_img: Image.Image) -> Any:
            nonlocal load_called
            load_called = True
            return orig_load(self_img)

        monkeypatch.setattr(Image.Image, "load", mock_load)

        with pytest.raises(ValueError):
            runtime._preprocess_image(raw_bytes)

        assert not load_called, "img.load() was called before size/pixel check!"

    def test_image_unsupported_format(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{model_file}", sha, ["cat", "dog"])

        gif_img = make_test_image("GIF")
        with pytest.raises(ValueError, match="unsupported image format"):
            runtime.classify(gif_img)

    def test_concurrent_calls(self, model_dir: Path, valid_tflite_bytes: bytes) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{model_file}", sha, ["cat", "dog"])

        errors: list[Exception] = []
        img_b64 = make_test_image("JPEG")

        def worker() -> None:
            for _ in range(5):
                try:
                    res = runtime.classify(img_b64)
                    assert len(res) == 2
                except (RuntimeError, ValueError, OSError) as exc:
                    errors.append(exc)

        def reloader() -> None:
            for _ in range(3):
                try:
                    runtime.load(f"file://{model_file}", sha, ["cat", "dog"])
                except (RuntimeError, ValueError, OSError) as exc:
                    errors.append(exc)

        threads = [threading.Thread(target=worker) for _ in range(6)] + [
            threading.Thread(target=reloader) for _ in range(2)
        ]
        for t in threads:
            t.start()
        for t in threads:
            t.join()

        assert not errors


class TestPolicyAndServer:
    """Tests for redirect policy, timeout, and HTTP server endpoints."""

    def test_strict_redirect_policy_cross_host(self) -> None:
        import urllib.request

        from ai.tiny.serve.tflite.sidecar.runtime import StrictRedirectHandler

        handler = StrictRedirectHandler()
        req = urllib.request.Request("https://first-host.org/model.tflite")
        with pytest.raises(ValueError, match="cross-host redirect forbidden"):
            handler.redirect_request(
                req, None, 302, "Found", {}, "https://second-host.org/model.tflite"
            )

    def test_strict_redirect_policy_non_https(self) -> None:
        import urllib.request

        from ai.tiny.serve.tflite.sidecar.runtime import StrictRedirectHandler

        handler = StrictRedirectHandler()
        req = urllib.request.Request("https://first-host.org/model.tflite")
        with pytest.raises(ValueError, match="redirect to non-https scheme forbidden"):
            handler.redirect_request(
                req, None, 302, "Found", {}, "http://first-host.org/model.tflite"
            )

    def test_https_timeout_handling(self, monkeypatch: pytest.MonkeyPatch) -> None:
        import urllib.request

        runtime = Runtime()

        def timeout_open(*_args: Any, **_kwargs: Any) -> Any:
            raise TimeoutError("timed out")

        monkeypatch.setattr(urllib.request.OpenerDirector, "open", timeout_open)
        with pytest.raises(TimeoutError, match="timed out"):
            runtime.load(
                "https://example.com/model.tflite", "0" * 64, ["a", "b"], timeout=0.1
            )

    def test_fetch_https_artifact_download_deadline(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        import time
        import urllib.request

        runtime = Runtime()

        class SlowReader:
            def __init__(self) -> None:
                self.headers = {"Content-Length": "1000"}
                self.calls = 0

            def read(self, _size: int = -1) -> bytes:
                self.calls += 1
                time.sleep(0.04)
                if self.calls <= 5:
                    return b"A" * 10
                return b""

            def __enter__(self) -> Self:
                return self

            def __exit__(self, *args: object) -> None:
                pass

        class MockOpener:
            def open(self, req: Any, timeout: float = 30.0) -> Any:
                return SlowReader()

        monkeypatch.setattr(urllib.request, "build_opener", lambda *args: MockOpener())

        with pytest.raises(TimeoutError, match="timed out"):
            runtime._fetch_https_artifact(
                "https://example.com/model.tflite", timeout=0.05
            )

    @staticmethod
    def _assert_pre_load_endpoints(base_url: str) -> None:
        import urllib.request

        # 1. GET /healthz when not loaded -> 503
        req = urllib.request.Request(f"{base_url}/healthz")
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == HTTPStatus.SERVICE_UNAVAILABLE
        body = json.loads(exc_info.value.read().decode())
        assert body["status"] == "not_ready"
        assert body["backend"].startswith("ai-edge-litert ")

        # 2. POST /classify before load -> 409
        req = urllib.request.Request(
            f"{base_url}/classify",
            data=json.dumps({"image": make_test_image("PNG")}).encode(),
            headers={"Content-Type": "application/json"},
        )
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == HTTPStatus.CONFLICT

    @staticmethod
    def _assert_load_and_classify(base_url: str, model_file: Path, sha: str) -> None:
        import urllib.request

        # 3. POST /load with valid model -> 200
        load_body = {
            "uri": f"file://{model_file}",
            "sha256": sha,
            "labels": ["cats", "dogs"],
        }
        req = urllib.request.Request(
            f"{base_url}/load",
            data=json.dumps(load_body).encode(),
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req) as resp:
            assert resp.status == HTTPStatus.OK
            assert json.loads(resp.read().decode()) == {"status": "loaded"}

        # 4. GET /healthz after load -> 200
        req = urllib.request.Request(f"{base_url}/healthz")
        with urllib.request.urlopen(req) as resp:
            assert resp.status == HTTPStatus.OK
            health_data = json.loads(resp.read().decode())
            assert health_data["status"] == "ready"
            assert health_data["backend"].startswith("ai-edge-litert ")

        # 5. POST /classify after load -> 200
        req = urllib.request.Request(
            f"{base_url}/classify",
            data=json.dumps({"image": make_test_image("PNG")}).encode(),
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req) as resp:
            assert resp.status == HTTPStatus.OK
            scores = json.loads(resp.read().decode())["scores"]
            assert set(scores.keys()) == {"cats", "dogs"}
            for val in scores.values():
                assert 0.0 <= val <= 1.0

    @staticmethod
    def _assert_edge_endpoints(base_url: str, host: str, port: int) -> None:
        import urllib.request

        # 6. POST /load with oversized request body -> 413
        oversized_conn = http.client.HTTPConnection(host, port, timeout=5.0)
        oversized_conn.putrequest("POST", "/load")
        oversized_conn.putheader("Content-Type", "application/json")
        oversized_conn.putheader("Content-Length", str((1 << 20) + 1024))
        oversized_conn.endheaders()
        oversized_resp = oversized_conn.getresponse()
        assert oversized_resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE
        oversized_conn.close()

        # 7. GET /unknown -> 404
        req = urllib.request.Request(f"{base_url}/unknown")
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == HTTPStatus.NOT_FOUND

    def test_server_healthz_load_classify_cycle(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever)
        server_thread.daemon = True
        server_thread.start()

        host, port = server.server_address
        base_url = f"http://{host}:{port}"

        try:
            self._assert_pre_load_endpoints(base_url)
            self._assert_load_and_classify(base_url, model_file, sha)
            self._assert_edge_endpoints(base_url, host, port)
        finally:
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_content_length_negative_rejected(self, model_dir: Path) -> None:
        runtime = Runtime(model_dir=model_dir)
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.putrequest("POST", "/load")
            conn.putheader("Content-Type", "application/json")
            conn.putheader("Content-Length", "-1")
            conn.endheaders()
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.BAD_REQUEST
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_socket_timeout_stalling_client_releases_slot(
        self, model_dir: Path
    ) -> None:
        import socket

        runtime = Runtime(model_dir=model_dir)
        server = SidecarServer(
            ("127.0.0.1", 0), runtime, max_concurrency=1, socket_timeout=1.0
        )
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            s.settimeout(3.0)
            s.connect((host, port))
            req_data = (
                b"POST /load HTTP/1.1\r\n"
                b"Host: 127.0.0.1\r\n"
                b"Content-Type: application/json\r\n"
                b"Content-Length: 100\r\n"
                b"\r\n"
                b'{"partial": 1'
            )
            s.sendall(req_data)
            resp_bytes = b""
            try:
                for _ in range(64):
                    chunk = s.recv(1024)
                    if not chunk:
                        break
                    resp_bytes += chunk
                else:
                    pytest.fail("stalled client read loop exceeded maximum iterations")
            except TimeoutError:
                pytest.fail(
                    "stalled client timed out: server did not close or answer within ~3s"
                )
            finally:
                s.close()

            assert resp_bytes == b"" or b"408" in resp_bytes

            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/load",
                body=b"{}",
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.BAD_REQUEST
            conn.close()
        finally:
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_server_maps_oversized_error_to_413_without_string_match(self) -> None:
        from ai.tiny.serve.tflite.sidecar.runtime import OversizedError

        runtime = Runtime()
        runtime._active_model = object()  # type: ignore

        def mock_classify(_image: str) -> None:
            raise OversizedError("payload limit exceeded")

        runtime.classify = mock_classify  # type: ignore

        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/classify",
                body=b'{"image": "abc"}',
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_healthz_exposes_backend(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request("GET", "/healthz")
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.SERVICE_UNAVAILABLE
            data = json.loads(resp.read().decode())
            assert "backend" in data
            assert data["backend"].startswith("ai-edge-litert ")
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_server_oversized_classify_body_returns_413(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=5.0)
            conn.putrequest("POST", "/classify")
            conn.putheader("Content-Type", "application/json")
            conn.putheader("Content-Length", str((20 << 20) + 1024))
            conn.endheaders()
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_server_oversized_base64_payload_returns_413(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        import ai.tiny.serve.tflite.sidecar.runtime as rt_mod

        monkeypatch.setattr(rt_mod, "MAX_IMAGE_BASE64_LEN", 100)

        runtime = Runtime()
        runtime._active_model = object()  # type: ignore
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=5.0)
            payload = json.dumps({"image": "A" * 150}).encode("utf-8")
            conn.request(
                "POST",
                "/classify",
                body=payload,
                headers={
                    "Content-Type": "application/json",
                    "Content-Length": str(len(payload)),
                },
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_server_concurrency_cap_exceeded_returns_429(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime, max_concurrency=1)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        assert server.acquire_concurrency(timeout=1.0)
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/classify",
                body=b'{"image": "abc"}',
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.TOO_MANY_REQUESTS
            conn.close()
        finally:
            server.release_concurrency()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_healthz_does_not_leak_model_uri(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "secret_confidential_weights.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{model_file}", sha, ["c1", "c2"])

        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request("GET", "/healthz")
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.OK
            body_text = resp.read().decode("utf-8")
            data = json.loads(body_text)
            assert set(data.keys()) == {"status", "backend"}
            assert "secret_confidential_weights" not in body_text
            assert "file://" not in body_text
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_strict_redirect_policy_same_host_allowed(self) -> None:
        import urllib.request

        from ai.tiny.serve.tflite.sidecar.runtime import StrictRedirectHandler

        handler = StrictRedirectHandler()
        req = urllib.request.Request("https://weights.example.com/v1/model.tflite")
        redirect_req = handler.redirect_request(
            req, None, 302, "Found", {}, "https://weights.example.com/v2/model.tflite"
        )
        assert redirect_req is not None
        assert redirect_req.full_url == "https://weights.example.com/v2/model.tflite"

    def test_missing_ai_edge_litert_raises_import_error(self) -> None:
        import sys

        cmd = [
            sys.executable,
            "-c",
            "import sys; sys.modules['ai_edge_litert'] = None; import ai.tiny.serve.tflite.sidecar.runtime",
        ]
        res = subprocess.run(cmd, capture_output=True, text=True, check=False)
        assert res.returncode != 0
        assert "ai-edge-litert is required" in res.stderr

    def test_content_length_non_int_returns_400(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.putrequest("POST", "/load")
            conn.putheader("Content-Type", "application/json")
            conn.putheader("Content-Length", "invalid_not_an_int")
            conn.endheaders()
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.BAD_REQUEST
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_content_length_missing_returns_411(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.putrequest("POST", "/load")
            conn.putheader("Content-Type", "application/json")
            conn.endheaders()
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.LENGTH_REQUIRED
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_server_oversized_load_body_returns_413(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=5.0)
            conn.putrequest("POST", "/load")
            conn.putheader("Content-Type", "application/json")
            conn.putheader("Content-Length", str((1 << 20) + 1024))
            conn.endheaders()
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_classify_empty_image_field_returns_400(self) -> None:
        runtime = Runtime()
        runtime._active_model = object()  # type: ignore
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/classify",
                body=b'{"image": ""}',
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.BAD_REQUEST
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_classify_invalid_base64_returns_400(self) -> None:
        runtime = Runtime()
        runtime._active_model = object()  # type: ignore
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/classify",
                body=b'{"image": "???not_valid_b64???"}',
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.BAD_REQUEST
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_classify_oversized_image_dimensions_returns_413(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{model_file}", sha, ["c1", "c2"])

        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            oversized_img = make_test_image(dimensions=(MAX_IMAGE_DIMENSION + 1, 10))
            payload = json.dumps({"image": oversized_img}).encode("utf-8")
            conn = http.client.HTTPConnection(host, port, timeout=5.0)
            conn.request(
                "POST",
                "/classify",
                body=payload,
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_classify_unsupported_image_format_returns_400(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        runtime.load(f"file://{model_file}", sha, ["c1", "c2"])

        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            gif_img = make_test_image(format_name="GIF")
            payload = json.dumps({"image": gif_img}).encode("utf-8")
            conn = http.client.HTTPConnection(host, port, timeout=5.0)
            conn.request(
                "POST",
                "/classify",
                body=payload,
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            assert resp.status == HTTPStatus.BAD_REQUEST
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_load_missing_required_fields_returns_400(self) -> None:
        runtime = Runtime()
        server = SidecarServer(("127.0.0.1", 0), runtime)
        server_thread = threading.Thread(target=server.serve_forever, daemon=True)
        server_thread.start()
        host, port = server.server_address
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            # missing uri
            conn.request(
                "POST",
                "/load",
                body=b'{"sha256": "abc", "labels": ["a", "b"]}',
                headers={"Content-Type": "application/json"},
            )
            assert conn.getresponse().status == HTTPStatus.BAD_REQUEST

            # non-dict body
            conn.request(
                "POST",
                "/load",
                body=b'["not_a_dict"]',
                headers={"Content-Type": "application/json"},
            )
            assert conn.getresponse().status == HTTPStatus.BAD_REQUEST
        finally:
            conn.close()
            server.shutdown()
            server.server_close()
            server_thread.join()

    def test_fetch_https_artifact_chunked_exceeds_max_bytes(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        import urllib.request

        from ai.tiny.serve.tflite.sidecar.runtime import OversizedArtifactError

        runtime = Runtime(max_artifact_bytes=100)

        class LargeReader:
            def __init__(self) -> None:
                self.headers = {"Content-Length": "1000"}

            def read(self, size: int = -1) -> bytes:
                return b"A" * 60

            def __enter__(self) -> Self:
                return self

            def __exit__(self, *args: object) -> None:
                pass

        class MockOpener:
            def open(self, req: Any, timeout: float = 30.0) -> Any:
                return LargeReader()

        monkeypatch.setattr(urllib.request, "build_opener", lambda *args: MockOpener())
        with pytest.raises(OversizedArtifactError):
            runtime._fetch_https_artifact(
                "https://example.com/model.tflite", timeout=10.0
            )
