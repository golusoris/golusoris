#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly CHECK="$SCRIPT_DIR/check-gofumpt.sh"
readonly HOOK="$SCRIPT_DIR/../hooks/gofumpt.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-gofumpt-test.XXXXXX")"
cleanup() {
	rm -rf -- "$work"
}
trap cleanup EXIT

readonly formatted=$'package p\n\nfunc f() {\n\tprintln()\n}\n'
# gofmt accepts this file; only gofumpt rejects the empty line that opens the block.
readonly gofumpt_only=$'package p\n\nfunc g() {\n\n\tprintln()\n}\n'

new_repo() {
	local dir="$work/$1"
	mkdir -p "$dir"
	git -C "$dir" init -q
	printf '%s' "$formatted" >"$dir/ok.go"
	git -C "$dir" add ok.go
	printf '%s\n' "$dir"
}

passed=0
expect() {
	local want="$1" label="$2" pattern="$3"
	shift 3
	local status=0 output
	output="$("$@" 2>&1)" || status=$?
	if [[ "$want" == pass && "$status" -ne 0 ]] || [[ "$want" == fail && "$status" -eq 0 ]]; then
		printf 'FAIL %s: want %s, exit %d\n%s\n' "$label" "$want" "$status" "$output" >&2
		exit 1
	fi
	grep -Fq -- "$pattern" <<<"$output" || {
		printf 'FAIL %s: output lacks %q\n%s\n' "$label" "$pattern" "$output" >&2
		exit 1
	}
	passed=$((passed + 1))
}

repo="$(new_repo clean)"
expect pass 'formatted tree' '1 Go files formatted' env GOFUMPT_ROOT="$repo" bash "$CHECK"

repo="$(new_repo tracked)"
printf '%s' "$gofumpt_only" >"$repo/bad.go"
git -C "$repo" add bad.go
expect fail 'tracked gofumpt-only defect' 'bad.go' env GOFUMPT_ROOT="$repo" bash "$CHECK"

repo="$(new_repo untracked)"
printf '%s' "$gofumpt_only" >"$repo/new.go"
expect fail 'untracked file' 'new.go' env GOFUMPT_ROOT="$repo" bash "$CHECK"

repo="$(new_repo testdata)"
mkdir -p "$repo/x/testdata"
printf '%s' "$gofumpt_only" >"$repo/x/testdata/fixture.go"
expect fail 'testdata in the full tree' 'fixture.go' env GOFUMPT_ROOT="$repo" bash "$CHECK"

mkdir -p "$work/empty"
git -C "$work/empty" init -q
expect fail 'no Go files' 'no tracked or untracked Go files' env GOFUMPT_ROOT="$work/empty" bash "$CHECK"

repo="$(new_repo args)"
printf '%s' "$gofumpt_only" >"$repo/bad.go"
expect pass 'named formatted file' '1 Go files formatted' bash "$CHECK" "$repo/ok.go"
expect fail 'named defect' 'bad.go' bash "$CHECK" "$repo/ok.go" "$repo/bad.go"
expect fail 'named missing file' 'not a file' bash "$CHECK" "$repo/gone.go"

# The hook takes paths relative to the repository it runs in, as lefthook passes them.
run_hook() {
	local dir="$1"
	shift
	(cd "$dir" && bash "$HOOK" "$@")
}

repo="$(new_repo hook)"
printf '%s' "$gofumpt_only" >"$repo/bad.go"
mkdir -p "$repo/testdata"
printf '%s' "$gofumpt_only" >"$repo/testdata/fixture.go"
expect fail 'hook on a staged defect' 'bad.go' run_hook "$repo" ok.go bad.go
expect pass 'hook skips staged testdata' 'skip: no Go files' run_hook "$repo" testdata/fixture.go
expect pass 'hook on a formatted file' '1 Go files formatted' run_hook "$repo" ok.go

printf 'gofumpt gate: %d cases ok\n' "$passed"
