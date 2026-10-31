#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly hook="$repo_root/scripts/hooks/gci.sh"
command -v golangci-lint >/dev/null
work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-gci-test.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT

git -C "$work" init -q
cp "$repo_root/.golangci.yml" "$work/.golangci.yml"
mkdir -p "$work/core" "$work/testdata"
printf 'module github.com/golusoris/golusoris\n' >"$work/go.mod"
printf 'module github.com/golusoris/golusoris/core\n' >"$work/core/go.mod"

# Unused imports distinguish gci from goimports, which the config also enables.
cat >"$work/good.go" <<'EOF'
package fixture

import (
	"fmt"

	"example.net/other"

	"github.com/golusoris/golusoris/core/clock"
)
EOF
cat >"$work/bad.go" <<'EOF'
package fixture

import (
	"github.com/golusoris/golusoris/core/clock"

	"example.net/other"

	"fmt"
)
EOF
cp "$work/good.go" "$work/core/good.go"
cp "$work/bad.go" "$work/core/bad.go"
cp "$work/bad.go" "$work/testdata/bad.go"

expect_hook() (
	local want="$1" label="$2" directory="$3"
	shift 3
	cd "$directory"
	local before after output status=0
	before="$(cksum "$@")"
	output="$(bash "$hook" "$@" 2>&1)" || status=$?
	after="$(cksum "$@")"
	if [[ "$status" -ne "$want" || "$before" != "$after" ]]; then
		printf 'FAIL %s: expected exit %d, got %d; files unchanged: %s\n%s\n' \
			"$label" "$want" "$status" "$([[ "$before" == "$after" ]] && echo yes || echo no)" "$output" >&2
		exit 1
	fi
	printf 'PASS %s: exit %d; files unchanged\n' "$label" "$status"
)

expect_hook 0 'root good; only selected file and gci checked' "$work" good.go
expect_hook 1 'root misordered imports' "$work" bad.go
expect_hook 0 'core good' "$work" core/good.go
expect_hook 1 'core misordered imports' "$work" core/bad.go
expect_hook 0 'both modules good' "$work" good.go core/good.go
expect_hook 1 'mixed good and bad' "$work" good.go core/bad.go
expect_hook 0 'testdata skipped' "$work" testdata/bad.go

# A changed prefix must take effect without editing the hook.
for path in .golangci.yml go.mod core/go.mod good.go core/good.go bad.go core/bad.go; do
	sed 's|github.com/golusoris/golusoris|example.test/fixture|g' "$work/$path" >"$work/replaced"
	mv "$work/replaced" "$work/$path"
done
expect_hook 0 'configured prefix in both modules' "$work" good.go core/good.go
expect_hook 1 'configured prefix misordered imports' "$work" bad.go core/bad.go
expect_hook 0 'core working directory uses root config' "$work/core" good.go

printf 'gci hook: 10 cases ok\n'
