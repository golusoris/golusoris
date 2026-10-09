#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-govuln-policy.XXXXXX")"
readonly fixture_root
trap 'rm -rf -- "$fixture_root"' EXIT

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"
export GOVULN_FAKE_VERSION="$GOVULNCHECK_VERSION"

mkdir -p "$fixture_root/scripts/ci" "$fixture_root/tools"
install -m 0755 "$repo_root/scripts/ci/govulncheck.sh" "$fixture_root/scripts/ci/"
install -m 0644 "$repo_root/tools/tool-versions.env" "$fixture_root/tools/"

fake_scanner="$fixture_root/govulncheck"
readonly fake_scanner
cat >"$fake_scanner" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
version="${GOVULN_FAKE_VERSION:?}"
if [[ "${1:-}" == -version ]]; then
	printf 'Scanner: govulncheck@%s\n' "${GOVULN_FAKE_SCANNER_VERSION:-$version}"
	exit 0
fi
config() {
	printf '{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scanner_version":"%s","db":"https://vuln.go.dev","scan_level":"symbol","scan_mode":"source"}}\n' "${1:-$version}"
}
finding() { printf '{"finding":%s}\n' "$1"; }
excelize='{"osv":"GO-2026-6452","trace":[{"module":"github.com/xuri/excelize/v2","version":"v2.11.0","package":"github.com/xuri/excelize/v2","function":"GetCellValue"}]}'
unknown='{"osv":"GO-2099-9999","trace":[{"module":"example.invalid/vulnerable","version":"v1.0.0","package":"example.invalid/vulnerable","function":"Exploit"}]}'
module_only='{"osv":"GO-2099-9998","trace":[{"module":"example.invalid/imported","version":"v1.0.0"}]}'
package_only='{"osv":"GO-2099-9997","trace":[{"module":"example.invalid/imported","version":"v1.0.0","package":"example.invalid/imported"}]}'
case "${GOVULN_FAKE_CASE:-clean}" in
clean) config ;;
module-only) config && finding "$module_only" ;;
package-only) config && finding "$package_only" ;;
excelize) config && finding "$excelize" ;;
unknown) config && finding "$unknown" ;;
mixed) config && finding "$module_only" && finding "$unknown" ;;
no-trace) config && finding '{"osv":"GO-2099-9996"}' ;;
empty-trace) config && finding '{"osv":"GO-2099-9996","trace":[]}' ;;
null-frame) config && finding '{"osv":"GO-2099-9996","trace":[null]}' ;;
no-osv) config && finding '{"trace":[{"module":"example.invalid/vulnerable","version":"v1.0.0","package":"example.invalid/vulnerable","function":"Exploit"}]}' ;;
invalid) printf 'not-json\n' ;;
empty) ;;
no-config) finding "$unknown" ;;
duplicate-config) config && config ;;
config-drift) config v0.0.0 ;;
scanner-fail) printf 'network failure\n' >&2 && exit 7 ;;
*) exit 9 ;;
esac
FAKE
chmod +x "$fake_scanner"

run_case() {
	local case_name="$1"
	shift
	(
		cd "$fixture_root"
		GOVULN_FAKE_CASE="$case_name" GOVULNCHECK_BIN="${GOVULN_TEST_BIN:-$fake_scanner}" \
			bash "$fixture_root/scripts/ci/govulncheck.sh" "$@"
	)
}

expect_success() {
	local expected="$1" case_name="$2" output
	shift 2
	if ! output="$(run_case "$case_name" "$@" 2>&1)"; then
		printf 'govulncheck policy rejected %s: %s\n' "$case_name" "$output" >&2
		exit 1
	fi
	if ! grep -Fq -- "$expected" <<<"$output"; then
		printf 'govulncheck %s success missed %q; output: %s\n' \
			"$case_name" "$expected" "$output" >&2
		exit 1
	fi
}

expect_failure() {
	local expected="$1" case_name="$2" output
	shift 2
	if output="$(run_case "$case_name" "$@" 2>&1)"; then
		printf 'govulncheck policy accepted %s\n' "$case_name" >&2
		exit 1
	fi
	if ! grep -Fq -- "$expected" <<<"$output"; then
		printf 'govulncheck %s failure missed %q; output: %s\n' \
			"$case_name" "$expected" "$output" >&2
		exit 1
	fi
}

# Positive: a clean stream and findings that are imported but never called pass.
expect_success 'no reachable vulnerabilities' clean ./...
expect_success 'no reachable vulnerabilities' module-only ./...
expect_success 'no reachable vulnerabilities' package-only ./...

# Negative: every reachable finding fails and names its OSV id, with no exemption.
expect_failure 'GO-2026-6452 github.com/xuri/excelize/v2@v2.11.0' excelize ./...
expect_failure 'GO-2099-9999 example.invalid/vulnerable@v1.0.0' unknown ./...
expect_failure 'reported 1 reachable finding(s)' mixed ./...
expect_failure 'failed with status 7' scanner-fail ./...
GOVULN_FAKE_SCANNER_VERSION=v0.0.0 expect_failure "expected govulncheck $GOVULNCHECK_VERSION" clean ./...
GOVULN_TEST_BIN="$fixture_root/absent-govulncheck" expect_failure 'executable not found' clean ./...

# Boundary: malformed, empty or protocol-violating streams and arguments fail closed.
expect_failure 'invalid JSON or unexpected govulncheck configuration' invalid ./...
expect_failure 'invalid JSON or unexpected govulncheck configuration' empty ./...
expect_failure 'invalid JSON or unexpected govulncheck configuration' no-config ./...
expect_failure 'invalid JSON or unexpected govulncheck configuration' duplicate-config ./...
expect_failure 'invalid JSON or unexpected govulncheck configuration' config-drift ./...
expect_failure 'finding without OSV id or trace' no-trace ./...
expect_failure 'finding without OSV id or trace' empty-trace ./...
expect_failure 'finding without OSV id or trace' null-frame ./...
expect_failure 'finding without OSV id or trace' no-osv ./...
expect_failure 'usage:' clean
expect_failure 'package patterns must be non-empty' clean -tags=x

printf 'govulncheck fail-closed policy tests passed\n'
