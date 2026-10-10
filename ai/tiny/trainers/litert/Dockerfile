# syntax=docker/dockerfile:1.28@sha256:bb22d9815c728170f72750f4e5b0d672e06176142e1d602c7e66c050100b7e5b
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

ARG PYTHON_IMAGE="python:3.14.8-slim-bookworm@sha256:a66d3a463d0be3cc537c269b0d3418746dda512417a975559a0eaa8a6406baaf"
FROM ${PYTHON_IMAGE}

ARG VCS_REF="unknown"
LABEL org.opencontainers.image.source="https://github.com/golusoris/golusoris" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.title="Golusoris LiteRT trainer"

ENV HOME=/tmp/trainer-home \
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
    && groupadd --gid 65532 trainer \
    && useradd --no-log-init --uid 65532 --gid 65532 --home-dir /tmp/trainer-home trainer \
    && mkdir -p /opt/golusoris/trainers/common /opt/golusoris/trainers/litert /tmp/trainer-home \
    && chown -R 65532:65532 /tmp/trainer-home

COPY --from=common __init__.py contract.py /opt/golusoris/trainers/common/
COPY __init__.py trainer.py /opt/golusoris/trainers/litert/

USER 65532:65532
WORKDIR /work
ENTRYPOINT ["python", "-m", "trainers.litert.trainer"]
