#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-push: go build + go test -short (no -race) in root and core/. CI owns the race suite.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

need_tool go "https://go.dev/dl/"
cd "$(git rev-parse --show-toplevel)"
# git exports GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE to hooks; tests that run
# git themselves (core/gitx) would otherwise operate on THIS repository
# instead of their temp dirs. Drop the hook environment before go test.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX GIT_COMMON_DIR

for m in . core; do
  note "$m: go build"
  (cd "$m" && go build ./...) || fail "go build failed in $m"
done

for m in . core; do
  note "$m: go test -short"
  (cd "$m" && go test -short -count=1 -timeout=10m ./...) || fail "go test failed in $m"
done
