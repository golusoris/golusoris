#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly max_patterns=512
scan_temp_dir=

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

fail() {
	printf 'govulncheck policy: %s\n' "$1" >&2
	return 1
}

print_findings() {
	local findings_file="$1"
	jq -r '
		.[0:20][] |
		"  \(.osv) \(.trace[0].module // "unknown")@\(.trace[0].version // "unknown") " +
		"\(.trace[0].package // "unknown").\(.trace[0].function // "unknown")"
	' "$findings_file" >&2
}

validate_patterns() {
	local pattern
	if (($# == 0 || $# > max_patterns)); then
		printf 'usage: %s <go-package-pattern>...\n' "${0##*/}" >&2
		return 2
	fi
	for pattern in "$@"; do
		if [[ -z "$pattern" || "$pattern" == -* ]]; then
			fail "package patterns must be non-empty positional arguments"
			return
		fi
	done
}

require_scanner() {
	local govulncheck_bin="$1"
	local version_output
	if ! command -v "$govulncheck_bin" >/dev/null 2>&1; then
		fail "govulncheck executable not found: $govulncheck_bin"
		return
	fi
	if ! version_output="$("$govulncheck_bin" -version 2>&1)" ||
		! grep -Fxq "Scanner: govulncheck@${GOVULNCHECK_VERSION}" <<<"$version_output"; then
		fail "expected govulncheck ${GOVULNCHECK_VERSION}"
		return
	fi
}

# scan_symbol_findings runs govulncheck over the patterns, validates its configuration record and
# writes the reachable symbol-level findings to findings_file.
scan_symbol_findings() {
	local govulncheck_bin="$1"
	local findings_file="$2"
	shift 2
	local report_file="$scan_temp_dir/report.json"
	local stderr_file="$scan_temp_dir/stderr.log"
	local scanner_status
	set +e
	"$govulncheck_bin" -format=json "$@" >"$report_file" 2>"$stderr_file"
	scanner_status=$?
	set -e
	if ((scanner_status != 0)); then
		printf 'govulncheck failed with status %d\n' "$scanner_status" >&2
		sed -n '1,40p' "$stderr_file" >&2
		return 1
	fi
	if ! jq -se --arg scanner_version "$GOVULNCHECK_VERSION" '
		[.[] | .config? | select(. != null)] as $configs |
		($configs | length) == 1 and
		$configs[0].protocol_version == "v1.0.0" and
		$configs[0].scanner_name == "govulncheck" and
		$configs[0].scanner_version == $scanner_version and
		$configs[0].db == "https://vuln.go.dev" and
		$configs[0].scan_level == "symbol" and
		$configs[0].scan_mode == "source"
	' "$report_file" >/dev/null; then
		fail "scanner emitted invalid JSON or unexpected govulncheck configuration"
		return
	fi
	# The govulncheck protocol requires an OSV id and at least one trace frame on every finding.
	if ! jq -se '
		all(.[] | .finding? | select(. != null);
			((.osv? | type) == "string" and (.osv | length) > 0) and
			((.trace? | type) == "array" and (.trace | length) > 0) and
			((.trace[0] | type) == "object"))
	' "$report_file" >/dev/null; then
		fail "scanner finding stream is invalid: finding without OSV id or trace"
		return
	fi
	if ! jq -sc '[.[] | .finding? | select(. != null) |
		select((.trace[0].function? // "") | length > 0)]' \
		"$report_file" >"$findings_file"; then
		fail "scanner finding stream is invalid"
		return
	fi
}

main() {
	validate_patterns "$@"
	command -v jq >/dev/null || fail "jq is required"

	local govulncheck_bin
	govulncheck_bin="${GOVULNCHECK_BIN:-govulncheck}"
	require_scanner "$govulncheck_bin"

	local findings_file finding_count
	scan_temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-govulncheck.XXXXXX")"
	findings_file="$scan_temp_dir/symbol-findings.json"
	trap 'rm -rf -- "$scan_temp_dir"' EXIT

	scan_symbol_findings "$govulncheck_bin" "$findings_file" "$@"
	finding_count="$(jq -er 'length' "$findings_file")"
	if ((finding_count == 0)); then
		printf 'govulncheck: no reachable vulnerabilities\n'
		return
	fi
	printf 'govulncheck reported %d reachable finding(s):\n' "$finding_count" >&2
	print_findings "$findings_file"
	return 1
}

main "$@"
