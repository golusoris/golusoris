#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
readonly RETRY_SCRIPT="$script_dir/retry.sh"

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-retry-test.XXXXXX")"
cleanup() {
	rm -rf -- "$temp_dir"
}
trap cleanup EXIT

# ── test 1: success first try (exit 0, no warning) ──────────────────────────
stderr_file="$temp_dir/test1.stderr"
"$RETRY_SCRIPT" true 2>"$stderr_file"
if [[ -s "$stderr_file" ]]; then
	printf 'test 1: expected empty stderr on immediate success, got: %s\n' \
		"$(cat "$stderr_file")" >&2
	exit 1
fi

# ── test 2: fails twice then succeeds (counter file, RETRY_DELAY_SECONDS=0) ─
counter_file="$temp_dir/test2.counter"
printf '0' >"$counter_file"
flaky_script="$temp_dir/flaky.sh"
cat >"$flaky_script" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
cnt=$(cat "$1")
cnt=$((cnt + 1))
printf '%d' "$cnt" >"$1"
if ((cnt < 3)); then
	exit 1
fi
exit 0
EOF
chmod +x "$flaky_script"

stderr_file="$temp_dir/test2.stderr"
RETRY_ATTEMPTS=3 RETRY_DELAY_SECONDS=0 "$RETRY_SCRIPT" "$flaky_script" "$counter_file" 2>"$stderr_file"
runs=$(cat "$counter_file")
if ((runs != 3)); then
	printf 'test 2: expected 3 runs before success, got %d\n' "$runs" >&2
	exit 1
fi
warnings=$(awk '/::warning::/ { n++ } END { print n+0 }' "$stderr_file")
if ((warnings != 2)); then
	printf 'test 2: expected 2 warning lines, got %d\n' "$warnings" >&2
	exit 1
fi

# ── test 3: always fails with status 7 (exit 7, exactly RETRY_ATTEMPTS runs) 
counter_file="$temp_dir/test3.counter"
printf '0' >"$counter_file"
fail7_script="$temp_dir/fail7.sh"
cat >"$fail7_script" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
cnt=$(cat "$1")
cnt=$((cnt + 1))
printf '%d' "$cnt" >"$1"
exit 7
EOF
chmod +x "$fail7_script"

stderr_file="$temp_dir/test3.stderr"
status=0
RETRY_ATTEMPTS=4 RETRY_DELAY_SECONDS=0 "$RETRY_SCRIPT" "$fail7_script" "$counter_file" 2>"$stderr_file" || status=$?
if ((status != 7)); then
	printf 'test 3: expected exit status 7, got %d\n' "$status" >&2
	exit 1
fi
runs=$(cat "$counter_file")
if ((runs != 4)); then
	printf 'test 3: expected exactly 4 runs, got %d\n' "$runs" >&2
	exit 1
fi
warnings=$(awk '/::warning::/ { n++ } END { print n+0 }' "$stderr_file")
if ((warnings != 3)); then
	printf 'test 3: expected 3 warning lines, got %d\n' "$warnings" >&2
	exit 1
fi
errors=$(awk '/::error::/ { n++ } END { print n+0 }' "$stderr_file")
if ((errors != 1)); then
	printf 'test 3: expected 1 error line, got %d\n' "$errors" >&2
	exit 1
fi

# ── test 4: RETRY_ATTEMPTS=0, 11, abc and no command (exit 2) ───────────────
check_bad_invocation() {
	local desc="$1"
	shift
	local rc=0
	"$@" >/dev/null 2>&1 || rc=$?
	if ((rc != 2)); then
		printf 'test 4: %s expected exit 2, got %d\n' "$desc" "$rc" >&2
		exit 1
	fi
}

check_bad_invocation 'RETRY_ATTEMPTS=0' env RETRY_ATTEMPTS=0 "$RETRY_SCRIPT" true
check_bad_invocation 'RETRY_ATTEMPTS=11' env RETRY_ATTEMPTS=11 "$RETRY_SCRIPT" true
check_bad_invocation 'RETRY_ATTEMPTS=abc' env RETRY_ATTEMPTS=abc "$RETRY_SCRIPT" true
check_bad_invocation 'no command' "$RETRY_SCRIPT"

# ── test 5: stdout passthrough byte-equal ────────────────────────────────────
expected_stdout="$temp_dir/expected.bin"
actual_stdout="$temp_dir/actual.bin"
printf 'line1\nline2 with \x00 null byte\nline3\n' >"$expected_stdout"
"$RETRY_SCRIPT" cat "$expected_stdout" >"$actual_stdout"
if ! cmp -s "$expected_stdout" "$actual_stdout"; then
	printf 'test 5: stdout passthrough was not byte-equal\n' >&2
	exit 1
fi

printf 'retry_test.sh: all tests passed\n'
