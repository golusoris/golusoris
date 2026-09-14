#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

if (( $# == 0 )); then
  exit 0
fi

if ! command -v dpkg-query >/dev/null 2>&1; then
  echo "::error::declared system packages require a Debian-family ARC image with dpkg-query pre-baked"
  exit 1
fi

for spec in "$@"; do
  if [[ ! "$spec" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+:-]*(=[a-zA-Z0-9~.+:-]+)?$ ]]; then
    echo "::error::invalid system package declaration: $spec"
    exit 1
  fi

  package="${spec%%=*}"
  status="$(dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null || true)"
  if [[ "$status" != "installed ok installed" ]]; then
    echo "::error::required system package is not pre-baked in the ARC image: $spec"
    exit 1
  fi
done
