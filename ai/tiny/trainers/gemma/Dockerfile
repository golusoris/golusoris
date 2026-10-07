# syntax=docker/dockerfile:1.20@sha256:26147acbda4f14c5add9946e2fd2ed543fc402884fd75146bd342a7f6271dc1d
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

ARG PYTHON_IMAGE="python:3.11.16-slim-bookworm@sha256:4b4c524dc3dce996864e030c7bd9c6b0e517597189fee48f48e05b499442444b"
FROM ${PYTHON_IMAGE}

ARG VCS_REF="unknown"
LABEL org.opencontainers.image.source="https://github.com/golusoris/golusoris" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.title="Golusoris Gemma trainer"

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
    && mkdir -p /opt/golusoris/trainers/common /opt/golusoris/trainers/gemma /tmp/trainer-home \
    && chown -R 65532:65532 /tmp/trainer-home

COPY --from=common __init__.py contract.py /opt/golusoris/trainers/common/
COPY __init__.py trainer.py /opt/golusoris/trainers/gemma/

USER 65532:65532
WORKDIR /work
ENTRYPOINT ["python", "-m", "trainers.gemma.trainer"]
