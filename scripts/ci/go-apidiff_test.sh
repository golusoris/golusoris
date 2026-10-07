#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
readonly api_gate="$script_dir/go-apidiff.sh"

temp_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-apidiff-test.XXXXXX")"
readonly temp_root
cleanup() {
	local status=$?
	case "$temp_root" in
	"${TMPDIR:-/tmp}"/golusoris-apidiff-test.*) rm -rf -- "$temp_root" ;;
	*) printf 'refusing to remove unexpected test directory: %s\n' "$temp_root" >&2 ;;
	esac
	return "$status"
}
trap cleanup EXIT

repo="$temp_root/repo"
mkdir -p "$repo/scripts/ci" "$repo/core" "$temp_root/bin"
cp "$api_gate" "$repo/scripts/ci/go-apidiff.sh"
cp "$script_dir/go-modules.sh" "$repo/scripts/ci/go-modules.sh"

git -C "$repo" init -q -b main
git -C "$repo" config user.name fixture
git -C "$repo" config user.email fixture@example.invalid
printf 'module example.test/root\n\ngo 1.27.1\n' >"$repo/go.mod"
printf 'package root\n\nfunc Root() {}\n' >"$repo/root.go"
printf 'module example.test/core\n\ngo 1.27.1\n' >"$repo/core/go.mod"
printf 'package core\n\nfunc Core() {}\n' >"$repo/core/core.go"
git -C "$repo" add .
git -C "$repo" commit -q -m old
old_ref="$(git -C "$repo" rev-parse HEAD)"
printf 'fixture\n' >"$repo/README.md"
git -C "$repo" add README.md
git -C "$repo" commit -q -m new
new_ref="$(git -C "$repo" rev-parse HEAD)"

fake_apidiff="$temp_root/bin/go-apidiff"
cat >"$fake_apidiff" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
module="${PWD#"$GO_APIDIFF_REPO_ROOT"/}"
[[ "$PWD" == "$GO_APIDIFF_REPO_ROOT" ]] && module=.
printf '%s\t%s\n' "$module" "$*" >>"$GO_APIDIFF_LOG"
if [[ "$module" == "${GO_APIDIFF_FAIL_MODULE:-}" ]]; then
	exit "${GO_APIDIFF_FAIL_STATUS:-2}"
fi
EOF
chmod +x "$fake_apidiff"

log="$temp_root/calls.log"
run_gate() {
	local base_ref="${1:-$old_ref}"
	GO_APIDIFF_BIN="$fake_apidiff" \
		GO_APIDIFF_LOG="$log" \
		GO_APIDIFF_REPO_ROOT="$repo" \
		bash "$repo/scripts/ci/go-apidiff.sh" "$base_ref" HEAD
}

run_gate >/dev/null
[[ "$(wc -l <"$log")" -eq 2 ]]
grep -Fqx $'.\t--repo-path='"$repo"$' '"$old_ref"$' '"$new_ref"$' --print-compatible' "$log"
grep -Fqx $'core\t--repo-path='"$repo"$' '"$old_ref"$' '"$new_ref"$' --print-compatible' "$log"

: >"$log"
if GO_APIDIFF_FAIL_MODULE=core GO_APIDIFF_FAIL_STATUS=2 run_gate >/dev/null 2>&1; then
	printf 'API gate accepted checker execution failure\n' >&2
	exit 1
fi

: >"$log"
GO_APIDIFF_FAIL_MODULE=core GO_APIDIFF_FAIL_STATUS=1 run_gate >/dev/null
if GO_APIDIFF_ENFORCE_INCOMPATIBLE=true \
	GO_APIDIFF_FAIL_MODULE=core GO_APIDIFF_FAIL_STATUS=1 run_gate >/dev/null 2>&1; then
	printf 'enforced API gate accepted incompatible change\n' >&2
	exit 1
fi

mkdir -p "$repo/extra"
printf 'module example.test/extra\n\ngo 1.27.1\n' >"$repo/extra/go.mod"
printf 'package extra\n\nfunc Extra() {}\n' >"$repo/extra/extra.go"
git -C "$repo" rm -q core/core.go core/go.mod
git -C "$repo" add extra
git -C "$repo" commit -q -m 'change module set'
: >"$log"
module_change_output="$(run_gate "$new_ref" 2>&1)"
grep -Fq 'API module coverage: checked=1 added=1 removed=1 incompatible=1 current=2' \
	<<<"$module_change_output"
[[ "$(wc -l <"$log")" -eq 1 ]]
grep -Fqx $'.\t--repo-path='"$repo"$' '"$new_ref"$' '"$(git -C "$repo" rev-parse HEAD)"$' --print-compatible' "$log"
if GO_APIDIFF_ENFORCE_INCOMPATIBLE=true run_gate "$new_ref" >/dev/null 2>&1; then
	printf 'enforced API gate accepted a removed module\n' >&2
	exit 1
fi

printf 'multi-module API compatibility policy tests passed\n'
