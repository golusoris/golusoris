# syntax=docker/dockerfile:1.20@sha256:26147acbda4f14c5add9946e2fd2ed543fc402884fd75146bd342a7f6271dc1d
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

ARG PYTHON_IMAGE="python:3.14.8-slim-trixie@sha256:a2b82f3c48559aa0a8446d9af49826b6e2b2016f4cd2afabfe6013ec53729170"
FROM ${PYTHON_IMAGE}

ARG VCS_REF="unknown"
LABEL org.opencontainers.image.source="https://github.com/golusoris/golusoris" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.title="Golusoris LiteRT inference sidecar"

ENV HOME=/tmp/sidecar-home \
    PATH=/opt/venv/bin:${PATH} \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/opt/golusoris \
    PYTHONUNBUFFERED=1

COPY requirements.lock /tmp/requirements.lock
RUN python -m venv /opt/venv \
    && /opt/venv/bin/python -m pip install --disable-pip-version-check \
       --no-cache-dir --require-hashes -r /tmp/requirements.lock \
    && /usr/local/bin/python -m pip uninstall --yes pip setuptools wheel \
    && rm /tmp/requirements.lock \
    && groupadd --gid 65532 sidecar \
    && useradd --no-log-init --uid 65532 --gid 65532 --home-dir /tmp/sidecar-home sidecar \
    && mkdir -p /opt/golusoris/ai/tiny/serve/tflite/sidecar /tmp/sidecar-home /models \
    && chown -R 65532:65532 /tmp/sidecar-home

COPY __init__.py runtime.py server.py /opt/golusoris/ai/tiny/serve/tflite/sidecar/

USER 65532:65532
WORKDIR /opt/golusoris
EXPOSE 8501
ENTRYPOINT ["python", "-m", "ai.tiny.serve.tflite.sidecar.server"]
