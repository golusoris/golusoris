#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-push: go build + go test -short (no -race) in root and core/. CI owns the race suite.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

need_tool go "https://go.dev/dl/"
cd "$(git rev-parse --show-toplevel)"

# Packages whose dependencies only compile on Linux (the CI target); excluded on other hosts.
linux_only='/storage/scan(/|$)'

pkgs_for() { # pkgs_for <module-dir> — "./..." on Linux, the portable subset elsewhere
  if [ "$(go env GOOS)" = linux ]; then
    echo ./...
  else
    (cd "$1" && go list ./... | grep -vE "$linux_only" | tr '\n' ' ')
  fi
}

for m in . core; do
  pkgs=$(pkgs_for "$m")
  note "$m: go build"
  # shellcheck disable=SC2086 # package list is intentionally word-split
  (cd "$m" && go build $pkgs) || fail "go build failed in $m"
done

for m in . core; do
  pkgs=$(pkgs_for "$m")
  note "$m: go test -short"
  # shellcheck disable=SC2086 # package list is intentionally word-split
  (cd "$m" && go test -short -count=1 -timeout=10m $pkgs) || fail "go test failed in $m"
done
