#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

# Verify that declared Debian packages are installed. With --install, first
# install them through bounded apt-get calls (GitHub-hosted runners, Debian
# container jobs); without it, a self-hosted image must already provide them.

set -euo pipefail

readonly max_packages=64
readonly apt_retries=3
readonly apt_update_timeout_seconds=180
readonly apt_install_timeout_seconds=420

usage() {
  echo "usage: ${0##*/} [--install] <package[=version]>..." >&2
}

as_root() {
  if ((EUID == 0)); then
    "$@"
    return
  fi
  if ! command -v sudo >/dev/null 2>&1; then
    echo "::error::installing system packages needs root or sudo"
    return 1
  fi
  sudo -n "$@"
}

install_packages() {
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "::error::installing system packages requires a Debian-family runner with apt-get"
    return 1
  fi
  as_root timeout "$apt_update_timeout_seconds" \
    apt-get update -o Acquire::Retries="$apt_retries"
  as_root env DEBIAN_FRONTEND=noninteractive timeout "$apt_install_timeout_seconds" \
    apt-get install -y --no-install-recommends -o Acquire::Retries="$apt_retries" "$@"
}

install=false
if [[ "${1:-}" == --install ]]; then
  install=true
  shift
fi
if [[ "${1:-}" == -* ]]; then
  usage
  exit 2
fi

if (( $# == 0 )); then
  exit 0
fi
if (( $# > max_packages )); then
  echo "::error::declared $# system packages; expected at most $max_packages"
  exit 1
fi

# Validate every declaration before apt-get or dpkg-query sees any of them.
for spec in "$@"; do
  if [[ ! "$spec" =~ ^[a-zA-Z0-9][a-zA-Z0-9.+:-]*(=[a-zA-Z0-9~.+:-]+)?$ ]]; then
    echo "::error::invalid system package declaration: $spec"
    exit 1
  fi
done

if [[ "$install" == true ]]; then
  install_packages "$@"
fi

if ! command -v dpkg-query >/dev/null 2>&1; then
  echo "::error::declared system packages require a Debian-family runner with dpkg-query"
  exit 1
fi

for spec in "$@"; do
  package="${spec%%=*}"
  # dpkg-query fails for a package dpkg does not know; that is the not-installed answer below.
  # ${db:Status-Status} prints the bare state word ("installed"), not the three-word ${Status}.
  status=""
  version=""
  if queried="$(dpkg-query -W -f='${db:Status-Status} ${Version}' "$package" 2>/dev/null)"; then
    status="${queried%% *}"
    version="${queried#* }"
  fi
  if [[ "$status" != "installed" ]]; then
    echo "::error::required system package is not installed: $spec (install it with --install or bake it into the runner image)"
    exit 1
  fi
  if [[ "$spec" == *=* && "$version" != "${spec#*=}" ]]; then
    echo "::error::system package $package is version $version, declared ${spec#*=}"
    exit 1
  fi
done
