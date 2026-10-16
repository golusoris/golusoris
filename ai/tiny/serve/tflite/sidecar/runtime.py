# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""LiteRT runtime for loading and executing .tflite image classifiers."""

from __future__ import annotations

import base64
import dataclasses
import hashlib
import io
import os
import socket
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import numpy as np
from PIL import Image

try:
    import ai_edge_litert
    from ai_edge_litert.interpreter import Interpreter
except ImportError as exc:
    raise ImportError(
        "ai-edge-litert is required but not installed; "
        "install dependencies from requirements.lock"
    ) from exc

LITERT_VERSION = getattr(ai_edge_litert, "__version__", "unknown")
BACKEND_NAME = f"ai-edge-litert {LITERT_VERSION}"


class SidecarError(Exception):
    """Base error for LiteRT sidecar."""


class OversizedError(SidecarError, ValueError):
    """Payload, artifact, or image exceeds allowed bounds."""


class OversizedArtifactError(OversizedError):
    """Model artifact file exceeds size limit."""


class OversizedImageError(OversizedError):
    """Image dimensions, pixel count, or bytes exceed size limits."""


class OversizedPayloadError(OversizedError):
    """Base64 image payload exceeds character limit."""


class InvalidModelError(SidecarError, ValueError):
    """Model header, shape, or labels are invalid."""


class InvalidImageError(SidecarError, ValueError):
    """Image format or decoded data is invalid."""


class ModelNotLoadedError(SidecarError, RuntimeError):
    """Inference called before model was loaded."""


DEFAULT_MODEL_DIR = Path("/models")
MAX_ARTIFACT_BYTES = 64 * 1024 * 1024
MAX_IMAGE_BASE64_LEN = 16 * 1024 * 1024
MAX_IMAGE_BYTES = 64 * 1024 * 1024
MAX_IMAGE_DIMENSION = 8192
MAX_IMAGE_PIXELS = 4096 * 4096
DEFAULT_TIMEOUT_SECONDS = 30.0


class StrictRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Enforce strict redirect policy: refuse cross-host and scheme changes."""

    def redirect_request(
        self,
        req: urllib.request.Request,
        fp: Any,
        code: int,
        msg: str,
        headers: Any,
        newurl: str,
    ) -> urllib.request.Request | None:
        orig_parsed = urllib.parse.urlsplit(req.full_url)
        new_parsed = urllib.parse.urlsplit(newurl)
        if new_parsed.scheme != "https":
            raise ValueError(
                f"redirect to non-https scheme forbidden: {new_parsed.scheme}"
            )
        if orig_parsed.netloc.lower() != new_parsed.netloc.lower():
            raise ValueError(
                f"cross-host redirect forbidden: {orig_parsed.netloc} -> {new_parsed.netloc}"
            )
        return super().redirect_request(req, fp, code, msg, headers, newurl)


@dataclasses.dataclass(frozen=True)
class ActiveModel:
    """Loaded model state held in runtime memory."""

    interpreter: Any
    labels: list[str]
    uri: str
    sha256: str
    input_detail: dict[str, Any]
    output_detail: dict[str, Any]
    lock: threading.Lock = dataclasses.field(default_factory=threading.Lock)


class Runtime:
    """Manages model loading, locator validation, and safe inference."""

    def __init__(
        self,
        model_dir: Path | None = None,
        max_artifact_bytes: int = MAX_ARTIFACT_BYTES,
        default_timeout: float = DEFAULT_TIMEOUT_SECONDS,
    ) -> None:
        if model_dir is not None:
            self._model_dir = model_dir.resolve()
        else:
            env_dir = os.environ.get("LITERT_MODEL_DIR")
            self._model_dir = Path(env_dir).resolve() if env_dir else DEFAULT_MODEL_DIR
        self._max_artifact_bytes = max_artifact_bytes
        self._default_timeout = default_timeout
        self._swap_lock = threading.Lock()
        self._active_model: ActiveModel | None = None

    @property
    def is_ready(self) -> bool:
        """Report true only if an active model is loaded."""
        with self._swap_lock:
            return self._active_model is not None

    @property
    def backend(self) -> str:
        """Report runtime backend and version."""
        return BACKEND_NAME

    def load(
        self,
        uri: str,
        expected_sha256: str,
        labels: Sequence[str],
        timeout: float | None = None,
    ) -> None:
        """Resolve locator, verify digest, validate TFLite model, and atomically swap."""
        validated_labels = self._validate_labels(labels)
        expected_digest = self._validate_digest_string(expected_sha256)
        effective_timeout = timeout if timeout is not None else self._default_timeout

        artifact_bytes = self._fetch_artifact(uri, effective_timeout)
        self._verify_sha256(artifact_bytes, expected_digest)
        self._validate_tflite_header(artifact_bytes)

        interpreter = self._allocate_interpreter(artifact_bytes)
        input_detail, output_detail = self._validate_shapes(
            interpreter, len(validated_labels)
        )

        new_model = ActiveModel(
            interpreter=interpreter,
            labels=validated_labels,
            uri=uri,
            sha256=expected_digest,
            input_detail=input_detail,
            output_detail=output_detail,
        )

        with self._swap_lock:
            self._active_model = new_model

    def classify(self, image_base64: str) -> dict[str, float]:
        """Classify a base64 encoded PNG or JPEG image against the active model."""
        with self._swap_lock:
            active = self._active_model

        if active is None:
            raise ModelNotLoadedError("classify-before-load: no active model loaded")

        raw_bytes = self._decode_image_payload(image_base64)
        resized_image = self._preprocess_image(raw_bytes)
        tensor_input = self._image_to_tensor(
            resized_image, active.input_detail["dtype"]
        )

        with active.lock:
            active.interpreter.set_tensor(active.input_detail["index"], tensor_input)
            active.interpreter.invoke()
            raw_output = active.interpreter.get_tensor(active.output_detail["index"])

        scores = self._extract_scores(raw_output, len(active.labels))
        return {label: float(scores[idx]) for idx, label in enumerate(active.labels)}

    def _validate_labels(self, labels: Sequence[str]) -> list[str]:
        if not isinstance(labels, (list, tuple)) or len(labels) < 2:
            raise ValueError("invalid labels: at least two unique labels required")
        seen: set[str] = set()
        clean: list[str] = []
        for idx, item in enumerate(labels):
            if not isinstance(item, str) or not item.strip():
                raise ValueError(f"invalid labels: label {idx} must be non-empty")
            if item in seen:
                raise ValueError(f"invalid labels: duplicate label {item!r}")
            seen.add(item)
            clean.append(item)
        return clean

    def _validate_digest_string(self, sha256: str) -> str:
        if not isinstance(sha256, str) or len(sha256.strip()) != 64:
            raise ValueError("invalid sha256: expected 64 hex characters")
        try:
            bytes.fromhex(sha256.strip())
        except ValueError as exc:
            raise ValueError(f"invalid sha256 hex string: {exc}") from exc
        return sha256.strip().lower()

    def _fetch_artifact(self, uri: str, timeout: float) -> bytes:
        if not isinstance(uri, str) or not uri.strip():
            raise ValueError("uri must be a non-empty string")
        parsed = urllib.parse.urlsplit(uri)
        if parsed.scheme == "file":
            return self._fetch_file_artifact(parsed)
        if parsed.scheme == "https":
            return self._fetch_https_artifact(uri, timeout)
        raise ValueError(
            f"unsupported locator scheme {parsed.scheme!r}: only https and file allowed"
        )

    def _fetch_file_artifact(self, parsed: urllib.parse.SplitResult) -> bytes:
        unquoted = urllib.parse.unquote(parsed.path)
        path = Path(unquoted).resolve()
        try:
            path.relative_to(self._model_dir)
        except ValueError as exc:
            raise ValueError(
                f"file path outside mounted model directory {self._model_dir}: {path}"
            ) from exc
        if not path.is_file():
            raise FileNotFoundError(f"model file not found: {path}")
        size = path.stat().st_size
        if size > self._max_artifact_bytes:
            raise OversizedArtifactError(
                f"oversized artifact: {size} bytes exceeds limit {self._max_artifact_bytes}"
            )
        return path.read_bytes()

    def _fetch_https_artifact(self, uri: str, timeout: float) -> bytes:
        opener = urllib.request.build_opener(StrictRedirectHandler())
        req = urllib.request.Request(
            uri, headers={"User-Agent": "golusoris-litert-sidecar/0.1.0"}
        )
        deadline = time.monotonic() + timeout
        chunk_size = 64 * 1024
        max_chunks = (self._max_artifact_bytes // chunk_size) + 2
        try:
            with opener.open(req, timeout=timeout) as resp:
                length_hdr = resp.headers.get("Content-Length")
                if length_hdr is not None:
                    try:
                        content_len = int(length_hdr)
                        if content_len > self._max_artifact_bytes:
                            raise OversizedArtifactError(
                                f"oversized artifact: {content_len} bytes exceeds limit {self._max_artifact_bytes}"
                            )
                    except ValueError as exc:
                        if isinstance(exc, OversizedArtifactError):
                            raise
                chunks: list[bytes] = []
                total_bytes = 0
                for _ in range(max_chunks):
                    if time.monotonic() > deadline:
                        raise TimeoutError(
                            f"artifact download timed out after {timeout}s: {uri}"
                        )
                    chunk = resp.read(chunk_size)
                    if not chunk:
                        break
                    total_bytes += len(chunk)
                    if total_bytes > self._max_artifact_bytes:
                        raise OversizedArtifactError(
                            f"oversized artifact: exceeds limit {self._max_artifact_bytes}"
                        )
                    chunks.append(chunk)
                else:
                    if resp.read(1):
                        raise OversizedArtifactError(
                            f"oversized artifact: exceeds limit {self._max_artifact_bytes}"
                        )
                return b"".join(chunks)
        except TimeoutError as exc:
            raise TimeoutError(
                f"artifact download timed out after {timeout}s: {uri}"
            ) from exc
        except urllib.error.URLError as exc:
            if isinstance(exc.reason, (TimeoutError, socket.timeout)):
                raise TimeoutError(
                    f"artifact download timed out after {timeout}s: {uri}"
                ) from exc
            raise

    def _verify_sha256(self, payload: bytes, expected_digest: str) -> None:
        computed = hashlib.sha256(payload).hexdigest().lower()
        if computed != expected_digest:
            raise ValueError(
                f"digest mismatch: expected {expected_digest}, got {computed}"
            )

    def _validate_tflite_header(self, payload: bytes) -> None:
        if len(payload) < 8 or payload[4:8] != b"TFL3":
            raise ValueError(
                "malformed model: missing or invalid TFL3 LiteRT magic header"
            )

    def _allocate_interpreter(self, payload: bytes) -> Any:
        try:
            interpreter = Interpreter(model_content=payload)
            interpreter.allocate_tensors()
            return interpreter
        except Exception as exc:
            raise ValueError(
                f"malformed model: failed to allocate interpreter: {exc}"
            ) from exc

    def _validate_shapes(
        self, interpreter: Any, label_count: int
    ) -> tuple[dict[str, Any], dict[str, Any]]:
        inputs = interpreter.get_input_details()
        if len(inputs) != 1:
            raise ValueError(
                f"input/output shape mismatch: expected 1 input, got {len(inputs)}"
            )
        in_shape = list(inputs[0]["shape"])
        if in_shape != [1, 160, 160, 3]:
            raise ValueError(
                f"input/output shape mismatch: expected input shape [1, 160, 160, 3], got {in_shape}"
            )

        outputs = interpreter.get_output_details()
        if len(outputs) != 1:
            raise ValueError(
                f"input/output shape mismatch: expected 1 output, got {len(outputs)}"
            )
        out_shape = list(outputs[0]["shape"])
        if out_shape not in ([1, label_count], [label_count]):
            raise ValueError(
                f"input/output shape mismatch: expected output dimension {label_count}, got {out_shape}"
            )

        return inputs[0], outputs[0]

    def _decode_image_payload(self, image_b64: str) -> bytes:
        if not isinstance(image_b64, str) or not image_b64.strip():
            raise ValueError("image payload must be a non-empty base64 string")
        if len(image_b64) > MAX_IMAGE_BASE64_LEN:
            raise OversizedPayloadError(
                f"oversized request: image base64 exceeds {MAX_IMAGE_BASE64_LEN} chars"
            )
        try:
            raw = base64.b64decode(image_b64, validate=True)
        except Exception as exc:
            raise InvalidImageError(f"invalid base64 image data: {exc}") from exc
        if len(raw) > MAX_IMAGE_BYTES:
            raise OversizedImageError(
                f"oversized image: {len(raw)} bytes exceeds {MAX_IMAGE_BYTES}"
            )
        return raw

    def _preprocess_image(self, raw_bytes: bytes) -> Image.Image:
        try:
            img = Image.open(io.BytesIO(raw_bytes))
        except Exception as exc:
            raise InvalidImageError(f"invalid image file format: {exc}") from exc
        if img.format not in {"PNG", "JPEG"}:
            raise InvalidImageError(
                f"unsupported image format {img.format!r}: want PNG or JPEG"
            )
        width, height = img.size
        if width > MAX_IMAGE_DIMENSION or height > MAX_IMAGE_DIMENSION:
            raise OversizedImageError(
                f"oversized image dimensions: {width}x{height} exceeds limit {MAX_IMAGE_DIMENSION}"
            )
        if width * height > MAX_IMAGE_PIXELS:
            raise OversizedImageError(
                f"oversized image pixels: {width * height} exceeds limit {MAX_IMAGE_PIXELS}"
            )
        try:
            img.load()
        except Exception as exc:
            raise InvalidImageError(f"invalid image file format: {exc}") from exc
        return img.convert("RGB").resize((160, 160), Image.Resampling.BILINEAR)

    def _image_to_tensor(self, img: Image.Image, dtype: Any) -> np.ndarray:
        arr = np.asarray(img, dtype=dtype)
        return np.expand_dims(arr, axis=0)

    def _extract_scores(self, raw_output: np.ndarray, label_count: int) -> np.ndarray:
        flat = raw_output.reshape(-1)
        if len(flat) != label_count:
            raise ValueError(
                f"output length {len(flat)} does not match label count {label_count}"
            )
        if not np.all(np.isfinite(flat)):
            raise ValueError("model produced non-finite output")
        for val in flat:
            if val < 0.0 or val > 1.0:
                raise ValueError(f"model probability {val} outside [0, 1]")
        return flat
