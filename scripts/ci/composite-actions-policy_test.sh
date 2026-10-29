#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

check_workflow() {
  local workflow="$1"
  local status=0
  [[ -f "$workflow" ]] || return 1
  if grep -Eq '^[[:space:]]*[^#[:space:]].*actions/setup-go@' "$workflow"; then
    printf 'ci.yml must use the setup-go composite\n' >&2
    status=1
  fi
  if grep -Eq '^[[:space:]]*[^#[:space:]].*docker-hub-mirror[.]sh' "$workflow"; then
    printf 'ci.yml must use the docker-hub-mirror composite\n' >&2
    status=1
  fi
  return "$status"
}

if [[ $# -eq 2 && "$1" == --check ]]; then
  check_workflow "$2"
  exit
elif [[ $# -ne 0 ]]; then
  printf 'usage: %s [--check WORKFLOW]\n' "${0##*/}" >&2
  exit 2
fi

check_workflow "$repo_root/.github/workflows/ci.yml"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-composite-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

expect_failure() {
  local pattern="$1"
  local workflow="$2"
  local output status=0
  output="$(check_workflow "$workflow" 2>&1)" || status=$?
  if ((status != 1)) || [[ "$output" != *"$pattern"* ]]; then
    printf 'expected failure containing %q: %s\n' "$pattern" "$output" >&2
    return 1
  fi
}

sed 's|uses: ./.github/actions/setup-go|uses: actions/setup-go@planted|' \
  "$repo_root/.github/workflows/ci.yml" >"$suite_root/direct-setup-go.yml"
expect_failure 'must use the setup-go composite' "$suite_root/direct-setup-go.yml"

sed 's|uses: ./.github/actions/docker-hub-mirror|run: bash scripts/ci/docker-hub-mirror.sh|' \
  "$repo_root/.github/workflows/ci.yml" >"$suite_root/inline-mirror.yml"
expect_failure 'must use the docker-hub-mirror composite' "$suite_root/inline-mirror.yml"

printf 'composite action policy passed; direct setup-go and inline mirror rejected\n'
