#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
script="$repo_root/scripts/verify-upstream-pins.sh"
readonly script
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-upstream-pins.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

fail() {
	printf 'upstream-pins policy: %s\n' "$*" >&2
	exit 1
}

# A go command that stalls must fail the gate and name the command that ran out of time.
mkdir -p "$suite_root/bin"
printf '#!/usr/bin/env bash\nsleep 10\n' >"$suite_root/bin/go"
chmod +x "$suite_root/bin/go"
rc=0
PATH="$suite_root/bin:$PATH" UPSTREAM_PINS_COMMAND_TIMEOUT=1s \
	bash "$script" >"$suite_root/out" 2>"$suite_root/err" || rc=$?
((rc != 0)) || fail "a stalled go command passed the gate"
grep -Fq 'timed out after 1s: go -C' "$suite_root/err" ||
	fail "timeout did not name the command: $(<"$suite_root/err")"

echo "upstream pin verification policy tests passed"
