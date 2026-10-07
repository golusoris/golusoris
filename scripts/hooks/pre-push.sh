#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# pre-push: primary modules build + short tests. CI owns all-module race suite.
set -euo pipefail
. "$(dirname "$0")/lib.sh"

need_tool go "https://go.dev/dl/"
cd "$(git rev-parse --show-toplevel)"
# git exports GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE to hooks; tests that run
# git themselves (core/gitx) would otherwise operate on THIS repository
# instead of their temp dirs. Drop the hook environment before go test.
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX GIT_COMMON_DIR

note "primary modules: build + vet"
GO_MODULE_GROUP=primary scripts/ci/go-modules.sh build || fail "primary build failed"

note "primary modules: go test -short"
GO_MODULE_GROUP=primary scripts/ci/go-modules.sh test-short || fail "primary short tests failed"
