#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly exception_file="$repo_root/.config/govulncheck-exceptions.json"
readonly max_patterns=512
readonly allowed_id="GO-2026-6452"
readonly allowed_module="github.com/xuri/excelize/v2"
readonly allowed_version="v2.11.0"
readonly allowed_module_sum="h1:HxaEFl6sRN2+8J5a8HaKq+0M4FsjBGMnWWtjOCPSG88="
readonly allowed_source_path="cell.go"
readonly allowed_source_sha256="52dca8b3104978c5a711351e28b5ea9351e5b403896e46cf82e219efb32aa1ac"
readonly allowed_source_contains="if xlsxSI < 0 || xlsxSI >= len(d.SI) {"
readonly allowed_advisory="https://github.com/qax-os/excelize/security/advisories/GHSA-fx5j-qcqg-grpf"
readonly allowed_database_issue_1="https://github.com/golang/vulndb/issues/6510"
readonly allowed_database_issue_2="https://github.com/golang/vulndb/issues/6532"
scan_temp_dir=

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

fail() {
	printf 'govulncheck policy: %s\n' "$1" >&2
	return 1
}

validate_exception_config() {
	[[ -f "$exception_file" ]] || fail "missing exception authority: $exception_file"
	jq -e \
		--arg id "$allowed_id" \
		--arg module "$allowed_module" \
		--arg version "$allowed_version" \
		--arg module_sum "$allowed_module_sum" \
		--arg source_path "$allowed_source_path" \
		--arg source_sha256 "$allowed_source_sha256" \
		--arg source_contains "$allowed_source_contains" \
		--arg advisory "$allowed_advisory" \
		--arg database_issue_1 "$allowed_database_issue_1" \
		--arg database_issue_2 "$allowed_database_issue_2" '
		.schema_version == 1 and
		.exceptions == [{
			id: $id,
			module: $module,
			version: $version,
			module_sum: $module_sum,
			source_path: $source_path,
			source_sha256: $source_sha256,
			source_contains: $source_contains,
			advisory: $advisory,
			database_issues: [$database_issue_1, $database_issue_2]
		}]
	' "$exception_file" >/dev/null || fail "invalid exception authority"
}

verify_patched_module() {
	local go_bin="$1"
	local module_json resolved_dir resolved_path resolved_sum resolved_version source_contains
	local source_hash source_path source_sha256
	local exception_module exception_sum exception_version

	exception_module="$(jq -er '.exceptions[0].module' "$exception_file")"
	exception_version="$(jq -er '.exceptions[0].version' "$exception_file")"
	exception_sum="$(jq -er '.exceptions[0].module_sum' "$exception_file")"
	source_path="$(jq -er '.exceptions[0].source_path' "$exception_file")"
	source_sha256="$(jq -er '.exceptions[0].source_sha256' "$exception_file")"
	source_contains="$(jq -er '.exceptions[0].source_contains' "$exception_file")"

	if ! module_json="$("$go_bin" list -m -json "$exception_module" 2>/dev/null)"; then
		fail "exempted module is absent from the active module graph: $exception_module"
		return
	fi
	resolved_path="$(jq -er '.Path' <<<"$module_json")"
	resolved_version="$(jq -er '.Version' <<<"$module_json")"
	resolved_sum="$(jq -er '.Sum' <<<"$module_json")"
	resolved_dir="$(jq -er '.Dir' <<<"$module_json")"
	if ! jq -e '(.Replace? // null) == null' <<<"$module_json" >/dev/null; then
		fail "exempted module uses a replacement: $exception_module"
		return
	fi
	if [[ "$resolved_path" != "$exception_module" || "$resolved_version" != "$exception_version" ||
		"$resolved_sum" != "$exception_sum" ]]; then
		fail "exempted module identity drifted: ${exception_module}@${resolved_version} ${resolved_sum}"
		return
	fi
	if [[ ! -f "$resolved_dir/$source_path" || -L "$resolved_dir/$source_path" ]]; then
		fail "exempted module source is absent or a symlink: $source_path"
		return
	fi
	if ! grep -Fq -- "$source_contains" "$resolved_dir/$source_path"; then
		fail "exempted module lacks the verified patched-source fingerprint"
		return
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		source_hash="$(sha256sum -- "$resolved_dir/$source_path" | awk '{print $1}')"
	elif command -v shasum >/dev/null 2>&1; then
		source_hash="$(shasum -a 256 -- "$resolved_dir/$source_path" | awk '{print $1}')"
	else
		fail "sha256sum or shasum is required"
		return
	fi
	if [[ "$source_hash" != "$source_sha256" ]]; then
		fail "exempted module patched-source fingerprint drifted: $source_path"
		return
	fi
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
	local go_bin="$1"
	local govulncheck_bin="$2"
	local version_output
	if ! command -v "$go_bin" >/dev/null 2>&1; then
		fail "go executable not found: $go_bin"
		return
	fi
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
	if ! jq -sc '[.[] | .finding? | select(. != null) |
		select((.trace[0].function? // "") | length > 0)]' \
		"$report_file" >"$findings_file"; then
		fail "scanner finding stream is invalid"
		return
	fi
}

# check_exception_findings accepts the findings only when every one is the pinned exception and the
# resolved module still carries the verified patched source.
check_exception_findings() {
	local go_bin="$1"
	local findings_file="$2"
	local finding_count="$3"
	local exception_id exception_module exception_version allowed_count
	exception_id="$(jq -er '.exceptions[0].id' "$exception_file")"
	exception_module="$(jq -er '.exceptions[0].module' "$exception_file")"
	exception_version="$(jq -er '.exceptions[0].version' "$exception_file")"
	allowed_count="$(jq -er --arg id "$exception_id" --arg module "$exception_module" \
		--arg version "$exception_version" '
		[.[] | select(
			.osv == $id and
			.trace[0].module == $module and
			.trace[0].version == $version
		)] | length
	' "$findings_file")"
	if ((allowed_count != finding_count)); then
		printf 'govulncheck reported %d unexpected reachable finding(s):\n' \
			"$((finding_count - allowed_count))" >&2
		print_findings "$findings_file"
		return 1
	fi
	verify_patched_module "$go_bin"
	printf 'govulncheck: verified patched exception %s for %s@%s (%d trace(s))\n' \
		"$exception_id" "$exception_module" "$exception_version" "$allowed_count"
}

main() {
	validate_patterns "$@"
	command -v jq >/dev/null || fail "jq is required"
	validate_exception_config

	local go_bin govulncheck_bin
	go_bin="${GO_BIN:-go}"
	govulncheck_bin="${GOVULNCHECK_BIN:-govulncheck}"
	require_scanner "$go_bin" "$govulncheck_bin"

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
	check_exception_findings "$go_bin" "$findings_file" "$finding_count"
}

main "$@"
