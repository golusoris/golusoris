#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-govuln-policy.XXXXXX")"
readonly fixture_root
trap 'rm -rf -- "$fixture_root"' EXIT

mkdir -p "$fixture_root/.config" "$fixture_root/scripts/ci" "$fixture_root/tools"
install -m 0755 "$repo_root/scripts/ci/govulncheck.sh" "$fixture_root/scripts/ci/"
install -m 0644 "$repo_root/.config/govulncheck-exceptions.json" "$fixture_root/.config/"
install -m 0644 "$repo_root/tools/tool-versions.env" "$fixture_root/tools/"

fake_scanner="$fixture_root/govulncheck"
readonly fake_scanner
# Keep expansions literal until the generated scanner executes.
# shellcheck disable=SC2016 # Generated scanner expands fixture variables only when invoked.
{
	printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail'
	printf '%s\n' 'if [[ "${1:-}" == -version ]]; then' \
		'  printf "Scanner: govulncheck@v1.8.0\n"' '  exit 0' 'fi'
	printf '%s\n' 'printf '\''%s\n'\'' '\''{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scanner_version":"v1.8.0","db":"https://vuln.go.dev","scan_level":"symbol","scan_mode":"source"}}'\'''
	printf '%s\n' 'case "${GOVULN_FAKE_CASE:-clean}" in'
	printf '%s\n' '  clean) exit 0 ;;'
	printf '%s\n' '  allowed)'
	printf '%s\n' '    printf '\''%s\n'\'' '\''{"finding":{"osv":"GO-2026-6452","trace":[{"module":"github.com/xuri/excelize/v2","version":"v2.11.0","package":"github.com/xuri/excelize/v2","function":"GetCellValue"}]}}'\'''
	printf '%s\n' '    exit 0 ;;'
	printf '%s\n' '  unknown)'
	printf '%s\n' '    printf '\''%s\n'\'' '\''{"finding":{"osv":"GO-2099-9999","trace":[{"module":"example.invalid/vulnerable","version":"v1.0.0","package":"example.invalid/vulnerable","function":"Exploit"}]}}'\'''
	printf '%s\n' '    exit 0 ;;'
	printf '%s\n' '  version-drift)'
	printf '%s\n' '    printf '\''%s\n'\'' '\''{"finding":{"osv":"GO-2026-6452","trace":[{"module":"github.com/xuri/excelize/v2","version":"v2.10.1","package":"github.com/xuri/excelize/v2","function":"GetCellValue"}]}}'\'''
	printf '%s\n' '    exit 0 ;;'
	printf '%s\n' '  module-only)'
	printf '%s\n' '    printf '\''%s\n'\'' '\''{"finding":{"osv":"GO-2026-6452","trace":[{"module":"github.com/xuri/excelize/v2","version":"v2.11.0"}]}}'\'''
	printf '%s\n' '    exit 0 ;;'
	printf '%s\n' '  mixed)'
	printf '%s\n' '    printf '\''%s\n'\'' '\''{"finding":{"osv":"GO-2026-6452","trace":[{"module":"github.com/xuri/excelize/v2","version":"v2.11.0","package":"github.com/xuri/excelize/v2","function":"GetCellValue"}]}}'\'''
	printf '%s\n' '    printf '\''%s\n'\'' '\''{"finding":{"osv":"GO-2099-9999","trace":[{"module":"example.invalid/vulnerable","version":"v1.0.0","package":"example.invalid/vulnerable","function":"Exploit"}]}}'\'''
	printf '%s\n' '    exit 0 ;;'
	printf '%s\n' '  invalid) printf '\''not-json\n'\''; exit 0 ;;'
	printf '%s\n' '  scanner-fail) printf '\''network failure\n'\'' >&2; exit 7 ;;'
	printf '%s\n' '  *) exit 9 ;;' 'esac'
} >"$fake_scanner"
chmod +x "$fake_scanner"

run_case() {
	local case_name="$1"
	shift
	(
		cd "$repo_root"
		GOVULN_FAKE_CASE="$case_name" GOVULNCHECK_BIN="$fake_scanner" \
			bash "$fixture_root/scripts/ci/govulncheck.sh" ./... "$@"
	)
}

expect_failure() {
	local expected="$1" case_name="$2" output
	if output="$(run_case "$case_name" 2>&1)"; then
		printf 'govulncheck policy accepted %s\n' "$case_name" >&2
		exit 1
	fi
	if ! grep -Fq -- "$expected" <<<"$output"; then
		printf 'govulncheck %s failure missed %q; output: %s\n' \
			"$case_name" "$expected" "$output" >&2
		exit 1
	fi
}

run_case clean >/dev/null
run_case allowed | grep -Fq 'verified patched exception GO-2026-6452'
expect_failure 'unexpected reachable finding' unknown
expect_failure 'unexpected reachable finding' version-drift
run_case module-only | grep -Fq 'no reachable vulnerabilities'
expect_failure 'unexpected reachable finding' mixed
expect_failure 'invalid JSON' invalid
expect_failure 'failed with status 7' scanner-fail

fake_module_dir="$fixture_root/excelize"
mkdir -p "$fake_module_dir"
install -m 0644 "$(go list -m -f '{{.Dir}}' github.com/xuri/excelize/v2)/cell.go" "$fake_module_dir/cell.go"
printf '\n// source drift fixture\n' >>"$fake_module_dir/cell.go"

fake_go="$fixture_root/go"
# Keep arguments and environment expansion literal until the generated Go shim executes.
# shellcheck disable=SC2016 # Generated Go shim expands arguments and environment only when invoked.
{
	printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail'
	printf '%s\n' 'if [[ "$1 $2 $3" != "list -m -json" ]]; then exit 9; fi'
	printf '%s\n' 'jq -n --arg dir "$GOVULN_FAKE_MODULE_DIR" '\''{Path:"github.com/xuri/excelize/v2",Version:"v2.11.0",Sum:"h1:HxaEFl6sRN2+8J5a8HaKq+0M4FsjBGMnWWtjOCPSG88=",Dir:$dir}'\'''
} >"$fake_go"
chmod +x "$fake_go"
if output="$(
	cd "$repo_root"
	GOVULN_FAKE_CASE=allowed GOVULNCHECK_BIN="$fake_scanner" \
		GOVULN_FAKE_MODULE_DIR="$fake_module_dir" GO_BIN="$fake_go" \
		bash "$fixture_root/scripts/ci/govulncheck.sh" ./... 2>&1
)"; then
	printf 'govulncheck policy accepted patched-source drift\n' >&2
	exit 1
fi
grep -Fq 'patched-source fingerprint drifted' <<<"$output"

authority_root="$fixture_root/authority-drift"
mkdir -p "$authority_root/.config" "$authority_root/scripts/ci" "$authority_root/tools"
install -m 0755 "$repo_root/scripts/ci/govulncheck.sh" "$authority_root/scripts/ci/"
install -m 0644 "$repo_root/tools/tool-versions.env" "$authority_root/tools/"
jq '.exceptions[0].version = "v2.10.1"' \
	"$repo_root/.config/govulncheck-exceptions.json" >"$authority_root/.config/govulncheck-exceptions.json"
if output="$(
	cd "$repo_root"
	GOVULN_FAKE_CASE=allowed GOVULNCHECK_BIN="$fake_scanner" \
		bash "$authority_root/scripts/ci/govulncheck.sh" ./... 2>&1
)"; then
	printf 'govulncheck policy accepted exception-authority drift\n' >&2
	exit 1
fi
grep -Fq 'invalid exception authority' <<<"$output"

printf 'govulncheck exact-exception and fail-closed policy tests passed\n'
