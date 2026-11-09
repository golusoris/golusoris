# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Comprehensive tests for the LiteRT HTTP inference sidecar runtime and server."""

from __future__ import annotations

import base64
import errno
import hashlib
import http.client
import io
import json
import os
import signal
import subprocess
import tempfile
import threading
import time
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


def expect(condition: object, message: str = "") -> None:
    if not condition:
        raise AssertionError(message)


def kill_process_group(proc: subprocess.Popen[bytes]) -> None:
    """SIGKILL the session proc leads; fall back to proc alone where groups are unsafe."""
    if not hasattr(os, "killpg"):
        proc.kill()
        return
    # A pid of 1 or our own group would signal unrelated processes.
    if not isinstance(proc.pid, int) or isinstance(proc.pid, bool) or proc.pid <= 1 or proc.pid == os.getpgrp():
        proc.kill()
        return
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except ProcessLookupError:
        # Group exited between TimeoutExpired and killpg.
        return


def run_in_process_group(cmd: list[str], timeout: float) -> None:
    """Run cmd in its own session; on timeout kill the whole group, not only cmd."""
    with subprocess.Popen(  # noqa: S603 - fixed argv from the caller. # nosec B603
        cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True
    ) as proc:
        try:
            stdout, stderr = proc.communicate(timeout=timeout)
        except subprocess.TimeoutExpired:
            # uv run spawns the converter as a child; killing only uv would orphan it.
            kill_process_group(proc)
            proc.communicate(timeout=60)
            raise
    if proc.returncode != 0:
        raise subprocess.CalledProcessError(proc.returncode, cmd, output=stdout, stderr=stderr)


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
            run_in_process_group(cmd, timeout=900)
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


def pid_running(pid: int) -> bool:
    """Tell a killed grandchild from a live one: kill(pid, 0) still succeeds for an unreaped zombie."""
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        pass  # EPERM: the pid exists but is not ours, so it still counts as alive.
    # Non-Linux platforms lack /proc; os.kill(pid, 0) probe suffices.
    if not Path("/proc").is_dir():
        return True
    # An orphan its new parent has not reaped yet is a zombie, so it no longer runs.
    try:
        stat_text = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8")
    except (FileNotFoundError, ProcessLookupError):
        # Process reaped before open, or between open and read.
        return False
    return not stat_text.rsplit(") ", 1)[-1].startswith("Z")


@pytest.mark.skipif(not hasattr(os, "killpg"), reason="process groups are POSIX-only")
class TestRunInProcessGroup:
    def test_timeout_kills_grandchild(self, tmp_path: Path) -> None:
        pid_file = tmp_path / "grandchild.pid"
        # The grandchild drops the pipes, so only a group kill can stop it.
        script = f"sleep 30 >/dev/null 2>&1 & echo $! > {pid_file}; wait"
        with pytest.raises(subprocess.TimeoutExpired):
            run_in_process_group(["sh", "-c", script], timeout=5)
        if not pid_file.exists():
            raise AssertionError(f"pid_file race: {pid_file} was not written before timeout fired")
        try:
            grandchild = int(pid_file.read_text(encoding="utf-8").strip())
        except (FileNotFoundError, ValueError) as err:
            raise AssertionError(f"pid_file race: could not read grandchild pid from {pid_file}: {err}") from err
        for _ in range(50):
            if not pid_running(grandchild):
                return
            time.sleep(0.1)
        raise AssertionError(f"grandchild {grandchild} survived the timeout")

    def test_timeout_returns_while_grandchild_holds_pipes(self) -> None:
        start = time.monotonic()
        with pytest.raises(subprocess.TimeoutExpired):
            run_in_process_group(["sh", "-c", "sleep 30 & wait"], timeout=1)
        elapsed = time.monotonic() - start
        expect(elapsed < 10, f"timeout took {elapsed:.1f}s; a grandchild kept the pipes open")

    def test_nonzero_exit_raises_called_process_error(self) -> None:
        with pytest.raises(subprocess.CalledProcessError) as caught:
            run_in_process_group(["sh", "-c", "echo out; echo boom >&2; exit 3"], timeout=10)
        expect(caught.value.returncode == 3, "exit code must propagate")
        expect(caught.value.output is not None and b"out" in caught.value.output, "stdout must be kept")
        expect(caught.value.stderr is not None and b"boom" in caught.value.stderr, "stderr must be kept")

    def test_zero_exit_returns(self) -> None:
        run_in_process_group(["sh", "-c", "exit 0"], timeout=10)


class _FakeProc:
    def __init__(self, pid: int) -> None:
        self.pid = pid
        self.killed = False

    def kill(self) -> None:
        self.killed = True


@pytest.mark.skipif(not hasattr(os, "killpg"), reason="process groups are POSIX-only")
class TestKillProcessGroup:
    def test_kill_process_group_swallows_process_lookup_error(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        def raise_lookup(pid: int, sig: int) -> None:
            raise ProcessLookupError

        monkeypatch.setattr(os, "killpg", raise_lookup)
        fake = _FakeProc(pid=99999)
        kill_process_group(fake)

    def test_kill_process_group_boundary_pids(
        self, monkeypatch: pytest.MonkeyPatch
    ) -> None:
        killpg_called = False

        def fake_killpg(pid: int, sig: int) -> None:
            nonlocal killpg_called
            killpg_called = True

        monkeypatch.setattr(os, "killpg", fake_killpg)

        fake_init = _FakeProc(pid=1)
        kill_process_group(fake_init)
        expect(fake_init.killed, "proc.kill() must be called for pid 1")
        expect(not killpg_called, "os.killpg must not be called for pid 1")

        fake_pgrp = _FakeProc(pid=os.getpgrp())
        kill_process_group(fake_pgrp)
        expect(fake_pgrp.killed, "proc.kill() must be called when pid matches getpgrp()")
        expect(not killpg_called, "os.killpg must not be called when pid matches getpgrp()")


class TestPidRunning:
    @pytest.mark.parametrize(
        "kill_error, proc_present, read_text, expected",
        [
            pytest.param(
                ProcessLookupError(errno.ESRCH, "No such process"),
                True,
                "12345 (sleep) S 1",
                False,
                id="kill-esrch",
            ),
            pytest.param(
                PermissionError(errno.EPERM, "Operation not permitted"),
                True,
                "12345 (sleep) S 1 1",
                True,
                id="kill-eperm",
            ),
            pytest.param(
                PermissionError(errno.EPERM, "Operation not permitted"),
                True,
                "12345 (sleep) Z 1 1",
                False,
                id="kill-eperm-zombie",
            ),
            pytest.param(None, False, "12345 (sleep) S 1", True, id="no-proc"),
            pytest.param(None, True, FileNotFoundError("reaped"), False, id="reaped-before-open"),
            pytest.param(
                None,
                True,
                ProcessLookupError(errno.ESRCH, "No such process"),
                False,
                id="reaped-mid-read",
            ),
            pytest.param(None, True, "12345 (sleep) Z 1 1", False, id="zombie"),
            pytest.param(None, True, "12345 (sleep) S 1 1", True, id="running"),
            pytest.param(None, True, "12345 (a) Z) R 1 1", True, id="paren-in-comm"),
        ],
    )
    def test_pid_running_probe_outcomes(
        self,
        monkeypatch: pytest.MonkeyPatch,
        kill_error: BaseException | None,
        proc_present: bool,
        read_text: str | BaseException,
        expected: bool,
    ) -> None:
        """Every probe outcome the timeout tests rely on, including a reap between kill and the /proc read."""

        def fake_kill(pid: int, sig: int) -> None:
            """Stand in for kill(pid, 0) so the case decides whether the probe succeeds or raises an error."""
            if kill_error is not None:
                raise kill_error

        def fake_read_text(self_path: Path, encoding: str = "utf-8") -> str:
            """Stand in for the /proc/<pid>/stat read so the case can return a state or raise a reap error."""
            if isinstance(read_text, BaseException):
                raise read_text
            return read_text

        monkeypatch.setattr(os, "kill", fake_kill)
        monkeypatch.setattr(Path, "is_dir", lambda self: proc_present)
        monkeypatch.setattr(Path, "read_text", fake_read_text)
        expect(
            pid_running(12345) is expected,
            f"pid_running(12345) outcome mismatch: expected {expected}",
        )

    def test_pid_running_reaped_child(self) -> None:
        cmd = ["sh", "-c", "exit 0"]
        proc = subprocess.Popen(  # noqa: S603 - fixed argv test process. # nosec B603
            cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
        )
        proc.wait(timeout=5)
        expect(not pid_running(proc.pid), "reaped child process must not be running")


class TestRuntimeLoadAndClassify:
    """Unit tests for Runtime."""

    def test_load_file_and_classify_happy_path(
        self, model_dir: Path, valid_tflite_bytes: bytes
    ) -> None:
        model_file = model_dir / "model.tflite"
        model_file.write_bytes(valid_tflite_bytes)
        sha = hashlib.sha256(valid_tflite_bytes).hexdigest()

        runtime = Runtime(model_dir=model_dir)
        expect(not runtime.is_ready)

        runtime.load(f"file://{model_file}", sha, ["cat", "dog"])
        expect(runtime.is_ready)

        img_b64 = make_test_image("PNG")
        scores = runtime.classify(img_b64)
        expect(set(scores.keys()) == {"cat", "dog"})
        for prob in scores.values():
            expect(0.0 <= prob <= 1.0)

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
        expect(runtime.is_ready)

        # Attempt reload with bad model
        with pytest.raises(ValueError, match="malformed model"):
            runtime.load(f"file://{bad_file}", bad_sha, ["bad_a", "bad_b"])

        # Preserved original model
        expect(runtime.is_ready)
        scores = runtime.classify(make_test_image("PNG"))
        expect(set(scores.keys()) == {"good_a", "good_b"})

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

        expect(not load_called, "img.load() was called before size/pixel check!")

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
                    expect(len(res) == 2)
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

        expect(not errors)


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
    def _request(
        host: str, port: int, method: str, path: str, body: object = None
    ) -> tuple[int, dict]:
        conn = http.client.HTTPConnection(host, port, timeout=5.0)
        try:
            headers = {}
            payload = None
            if body is not None:
                payload = json.dumps(body).encode()
                headers["Content-Type"] = "application/json"
            conn.request(method, path, body=payload, headers=headers)
            resp = conn.getresponse()
            raw = resp.read().decode()
            return resp.status, json.loads(raw) if raw else {}
        finally:
            conn.close()

    @classmethod
    def _assert_pre_load_endpoints(cls, host: str, port: int) -> None:
        # 1. GET /healthz when not loaded -> 503
        status, body = cls._request(host, port, "GET", "/healthz")
        expect(status == HTTPStatus.SERVICE_UNAVAILABLE)
        expect(body["status"] == "not_ready")
        expect(body["backend"].startswith("ai-edge-litert "))

        # 2. POST /classify before load -> 409
        status, _ = cls._request(
            host, port, "POST", "/classify", {"image": make_test_image("PNG")}
        )
        expect(status == HTTPStatus.CONFLICT)

    @classmethod
    def _assert_load_and_classify(
        cls, host: str, port: int, model_file: Path, sha: str
    ) -> None:
        # 3. POST /load with valid model -> 200
        load_body = {
            "uri": f"file://{model_file}",
            "sha256": sha,
            "labels": ["cats", "dogs"],
        }
        status, body = cls._request(host, port, "POST", "/load", load_body)
        expect(status == HTTPStatus.OK)
        expect(body == {"status": "loaded"})

        # 4. GET /healthz after load -> 200
        status, health_data = cls._request(host, port, "GET", "/healthz")
        expect(status == HTTPStatus.OK)
        expect(health_data["status"] == "ready")
        expect(health_data["backend"].startswith("ai-edge-litert "))

        # 5. POST /classify after load -> 200
        status, body = cls._request(
            host, port, "POST", "/classify", {"image": make_test_image("PNG")}
        )
        expect(status == HTTPStatus.OK)
        scores = body["scores"]
        expect(set(scores.keys()) == {"cats", "dogs"})
        for val in scores.values():
            expect(0.0 <= val <= 1.0)

    @classmethod
    def _assert_edge_endpoints(cls, host: str, port: int) -> None:
        # 6. POST /load with oversized request body -> 413
        oversized_conn = http.client.HTTPConnection(host, port, timeout=5.0)
        oversized_conn.putrequest("POST", "/load")
        oversized_conn.putheader("Content-Type", "application/json")
        oversized_conn.putheader("Content-Length", str((1 << 20) + 1024))
        oversized_conn.endheaders()
        oversized_resp = oversized_conn.getresponse()
        expect(oversized_resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE)
        oversized_conn.close()

        # 7. GET /unknown -> 404
        status, _ = cls._request(host, port, "GET", "/unknown")
        expect(status == HTTPStatus.NOT_FOUND)

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

        try:
            self._assert_pre_load_endpoints(host, port)
            self._assert_load_and_classify(host, port, model_file, sha)
            self._assert_edge_endpoints(host, port)
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
            expect(resp.status == HTTPStatus.BAD_REQUEST)
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

            expect(resp_bytes == b"" or b"408" in resp_bytes)

            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/load",
                body=b"{}",
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            expect(resp.status == HTTPStatus.BAD_REQUEST)
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
            expect(resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE)
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
            expect(resp.status == HTTPStatus.SERVICE_UNAVAILABLE)
            data = json.loads(resp.read().decode())
            expect("backend" in data)
            expect(data["backend"].startswith("ai-edge-litert "))
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
            expect(resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE)
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
            expect(resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE)
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
        expect(server.acquire_concurrency(timeout=1.0))
        try:
            conn = http.client.HTTPConnection(host, port, timeout=2.0)
            conn.request(
                "POST",
                "/classify",
                body=b'{"image": "abc"}',
                headers={"Content-Type": "application/json"},
            )
            resp = conn.getresponse()
            expect(resp.status == HTTPStatus.TOO_MANY_REQUESTS)
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
            expect(resp.status == HTTPStatus.OK)
            body_text = resp.read().decode("utf-8")
            data = json.loads(body_text)
            expect(set(data.keys()) == {"status", "backend"})
            expect("secret_confidential_weights" not in body_text)
            expect("file://" not in body_text)
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
        expect(redirect_req is not None)
        expect(redirect_req.full_url == "https://weights.example.com/v2/model.tflite")

    def test_missing_ai_edge_litert_raises_import_error(self) -> None:
        import sys

        repo_root = Path(__file__).resolve().parents[5]
        cmd = [
            sys.executable,
            "-c",
            "import sys; sys.modules['ai_edge_litert'] = None; import ai.tiny.serve.tflite.sidecar.runtime",
        ]
        res = subprocess.run(  # noqa: S603 - fixed argv, sys.executable
            cmd,
            capture_output=True,
            text=True,
            check=False,
            env={**os.environ, "PYTHONPATH": str(repo_root)},
            timeout=60,
        )
        expect(res.returncode != 0)
        expect("ai-edge-litert is required" in res.stderr)

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
            expect(resp.status == HTTPStatus.BAD_REQUEST)
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
            expect(resp.status == HTTPStatus.LENGTH_REQUIRED)
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
            expect(resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE)
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
            expect(resp.status == HTTPStatus.BAD_REQUEST)
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
            expect(resp.status == HTTPStatus.BAD_REQUEST)
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
            expect(resp.status == HTTPStatus.REQUEST_ENTITY_TOO_LARGE)
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
            expect(resp.status == HTTPStatus.BAD_REQUEST)
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
            expect(conn.getresponse().status == HTTPStatus.BAD_REQUEST)

            # non-dict body
            conn.request(
                "POST",
                "/load",
                body=b'["not_a_dict"]',
                headers={"Content-Type": "application/json"},
            )
            expect(conn.getresponse().status == HTTPStatus.BAD_REQUEST)
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
