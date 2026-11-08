#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly REPO_ROOT

work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-security-gates-test.XXXXXX")"
trap 'rm -rf -- "$work"' EXIT

fixture="$work/fixture"
mkdir -p "$fixture/scripts/ci" "$fixture/scripts/hiss" "$fixture/tools"
cp "$REPO_ROOT/scripts/ci/security-gates.sh" "$fixture/scripts/ci/security-gates.sh"
chmod +x "$fixture/scripts/ci/security-gates.sh"
readonly CHECK="$fixture/scripts/ci/security-gates.sh"

touch "$fixture/.gitleaks.toml"
printf '# tool versions\n' > "$fixture/tools/tool-versions.env"

call_log="$work/call.log"
export CALL_LOG="$call_log"

cat > "$fixture/scripts/ci/go-modules.sh" << 'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s%s\n' "${0##*/}" "${*:+ $*}" >> "$CALL_LOG"
if [[ "${1:-}" == "gosec" && -n "${STUB_GO_MODULES_GOSEC_EXIT:-}" ]]; then
	exit "$STUB_GO_MODULES_GOSEC_EXIT"
fi
exit 0
EOF
chmod +x "$fixture/scripts/ci/go-modules.sh"

cat > "$fixture/scripts/ci/semgrep-scan.sh" << 'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s%s\n' "${0##*/}" "${*:+ $*}" >> "$CALL_LOG"
if [[ -n "${STUB_SEMGREP_SCAN_EXIT:-}" ]]; then
	exit "$STUB_SEMGREP_SCAN_EXIT"
fi
exit 0
EOF
chmod +x "$fixture/scripts/ci/semgrep-scan.sh"

cat > "$fixture/scripts/hiss/semgrep-fixtures.sh" << 'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s%s\n' "${0##*/}" "${*:+ $*}" >> "$CALL_LOG"
exit 0
EOF
chmod +x "$fixture/scripts/hiss/semgrep-fixtures.sh"

bin_all="$work/bin-all"
bin_no_gitleaks="$work/bin-no-gitleaks"
mkdir -p "$bin_all" "$bin_no_gitleaks"

cat > "$bin_all/docker" << 'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s%s\n' "${0##*/}" "${*:+ $*}" >> "$CALL_LOG"
exit 0
EOF
chmod +x "$bin_all/docker"
cp "$bin_all/docker" "$bin_no_gitleaks/docker"

cat > "$bin_all/gitleaks" << 'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s%s\n' "${0##*/}" "${*:+ $*}" >> "$CALL_LOG"
exit 0
EOF
chmod +x "$bin_all/gitleaks"

# Only bash and dirname, so the no-gitleaks case holds wherever gitleaks is installed.
for tool in bash dirname; do
	ln -s "$(command -v "$tool")" "$bin_no_gitleaks/$tool"
done
export PATH="$bin_all:$PATH"

passed=0
expect() {
	local want="$1" label="$2" pattern="$3"
	shift 3
	local status=0 output
	output="$("$@" 2>&1)" || status=$?
	if [[ "$want" == "nonzero" ]]; then
		if [[ "$status" -eq 0 ]]; then
			printf 'FAIL %s: want nonzero exit, got 0\n%s\n' "$label" "$output" >&2
			exit 1
		fi
	elif [[ "$status" -ne "$want" ]]; then
		printf 'FAIL %s: want exit %d, got %d\n%s\n' "$label" "$want" "$status" "$output" >&2
		exit 1
	fi
	if [[ -n "$pattern" ]]; then
		grep -Fq -- "$pattern" <<<"$output" || {
			printf 'FAIL %s: output lacks %q\n%s\n' "$label" "$pattern" "$output" >&2
			exit 1
		}
	fi
	passed=$((passed + 1))
}

# Positive: all six gates run in order with exact arguments and exit 0.
: > "$call_log"
expect 0 'positive all gates' '' bash "$CHECK"
want_positive=$(printf '%s\n' \
	'go-modules.sh gosec' \
	'go-modules.sh vuln' \
	'gitleaks git . -c .gitleaks.toml --redact --no-banner --verbose' \
	'docker info' \
	'semgrep-scan.sh' \
	'semgrep-fixtures.sh')
got_positive="$(cat "$call_log")"
if [[ "$got_positive" != "$want_positive" ]]; then
	printf 'FAIL positive log mismatch\nwant:\n%s\ngot:\n%s\n' "$want_positive" "$got_positive" >&2
	exit 1
fi

# Negative: gosec failure aborts pipeline without executing later gates.
: > "$call_log"
expect 1 'negative gosec exit' '' env STUB_GO_MODULES_GOSEC_EXIT=1 bash "$CHECK"
want_gosec_fail=$'go-modules.sh gosec'
got_gosec_fail="$(cat "$call_log")"
if [[ "$got_gosec_fail" != "$want_gosec_fail" ]]; then
	printf 'FAIL negative gosec log mismatch\nwant:\n%s\ngot:\n%s\n' "$want_gosec_fail" "$got_gosec_fail" >&2
	exit 1
fi

# Negative: semgrep-scan failure exits non-zero and stops before semgrep-fixtures.
: > "$call_log"
expect 3 'negative semgrep-scan exit' '' env STUB_SEMGREP_SCAN_EXIT=3 bash "$CHECK"
if ! grep -Fq 'semgrep-scan.sh' "$call_log"; then
	printf 'FAIL negative semgrep-scan missing from log\n' >&2
	exit 1
fi
if grep -Fq 'semgrep-fixtures.sh' "$call_log"; then
	printf 'FAIL negative semgrep-fixtures ran after semgrep-scan failure\n' >&2
	exit 1
fi

# Negative: missing gitleaks on PATH fails before semgrep runs.
: > "$call_log"
expect nonzero 'negative no gitleaks on PATH' '' env PATH="$bin_no_gitleaks" bash "$CHECK"
if ! grep -Fq 'go-modules.sh vuln' "$call_log"; then
	printf 'FAIL negative missing earlier gate before gitleaks failure\n' >&2
	exit 1
fi
if grep -Eq 'semgrep-scan\.sh|semgrep-fixtures\.sh' "$call_log"; then
	printf 'FAIL negative semgrep ran after gitleaks failure\n' >&2
	exit 1
fi

# Boundary: extra CLI argument rejected with usage on stderr and exit 2.
: > "$call_log"
expect 2 'boundary extra argument rejected' 'usage: security-gates.sh' bash "$CHECK" unexpected_arg
if [[ -s "$call_log" ]]; then
	printf 'FAIL boundary extra argument produced non-empty call log\n%s\n' "$(cat "$call_log")" >&2
	exit 1
fi

# Boundary: missing tools/tool-versions.env fails before running any gate.
: > "$call_log"
mv "$fixture/tools/tool-versions.env" "$fixture/tools/tool-versions.env.bak"
expect nonzero 'boundary missing tool-versions.env' '' bash "$CHECK"
mv "$fixture/tools/tool-versions.env.bak" "$fixture/tools/tool-versions.env"
if [[ -s "$call_log" ]]; then
	printf 'FAIL boundary missing tool-versions produced non-empty call log\n%s\n' "$(cat "$call_log")" >&2
	exit 1
fi

printf '%d cases passed\n' "$passed"
