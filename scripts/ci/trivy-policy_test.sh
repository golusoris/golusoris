#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
fixture_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-trivy-policy.XXXXXX")"
readonly fixture_dir
trap 'rm -rf "$fixture_dir"' EXIT

grpc_version="$(go -C "$repo_root" list -m -f '{{.Version}}' google.golang.org/grpc)"
if ! grep -Fq "pkg:golang/google.golang.org/grpc@${grpc_version}" \
	"$repo_root/.trivy/openvex.json"; then
	printf 'OpenVEX gRPC product does not match resolved module version %s\n' "$grpc_version" >&2
	exit 1
fi
root_dependencies="$(go -C "$repo_root" list -deps ./...)"
if grep -Fxq 'google.golang.org/grpc/xds' <<<"$root_dependencies"; then
	printf 'OpenVEX exception is stale: vulnerable gRPC xDS server package is reachable\n' >&2
	exit 1
fi

# An npm exception is stale once the lock moves off the named version, and fails once its date passed.
check_npm_exception() {
	local vex="$1" lock="$2" package="$3" today="$4"
	local locked product expires
	locked="$(jq -r --arg key "node_modules/$package" '.packages[$key].version // ""' "$lock")"
	product="pkg:npm/${package}@${locked}"
	if ! jq -e --arg id "$product" \
		'any(.statements[]; any(.products[]; .["@id"] == $id))' "$vex" >/dev/null; then
		printf 'OpenVEX %s product does not match locked version %s\n' "$package" "${locked:-none}" >&2
		return 1
	fi
	expires="$(jq -r --arg id "$product" \
		'first(.statements[] | select(any(.products[]; .["@id"] == $id))) | .status_notes // "" | sub("^expires: "; "")' \
		"$vex")"
	if [[ ! "$expires" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
		printf 'OpenVEX %s exception names no expiry date\n' "$package" >&2
		return 1
	fi
	if [[ "$today" > "$expires" ]]; then
		printf 'OpenVEX %s exception expired on %s\n' "$package" "$expires" >&2
		return 1
	fi
}

expect_refusal() {
	local want="$1" output
	shift
	if output="$(check_npm_exception "$@" 2>&1)"; then
		printf 'OpenVEX policy accepted a case it must refuse: %s\n' "$want" >&2
		exit 1
	fi
	if [[ "$output" != *"$want"* ]]; then
		printf 'OpenVEX policy refused for another reason: got "%s", want "%s"\n' "$output" "$want" >&2
		exit 1
	fi
}

readonly vex_file="$repo_root/.trivy/openvex.json"
readonly spectral_lock_file="$repo_root/tools/spectral/package-lock.json"
policy_date="$(date -u +%F)"
readonly policy_date
check_npm_exception "$vex_file" "$spectral_lock_file" braces "$policy_date"
expect_refusal 'expired on' "$vex_file" "$spectral_lock_file" braces 9999-12-31
jq '.packages["node_modules/braces"].version = "3.0.4"' "$spectral_lock_file" >"$fixture_dir/moved-lock.json"
expect_refusal 'does not match locked version 3.0.4' "$vex_file" "$fixture_dir/moved-lock.json" braces "$policy_date"
jq 'del(.statements[].status_notes)' "$vex_file" >"$fixture_dir/undated-vex.json"
expect_refusal 'names no expiry date' "$fixture_dir/undated-vex.json" "$spectral_lock_file" braces "$policy_date"

write_fake() {
	local body="$1"
	local dollar='$'
	{
		printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail'
		printf '%s\n' "if [[ \"${dollar}{1:-}\" == \"--version\" ]]; then" \
			'  printf "Version: 0.74.0\\n"' '  exit 0' 'fi'
		printf '%s\n' "$body"
	} >"$fixture_dir/trivy"
	chmod +x "$fixture_dir/trivy"
}

write_fake 'printf "clean scan\\n"'
TRIVY_BIN="$fixture_dir/trivy" bash "$repo_root/scripts/ci/trivy-scan.sh" >/dev/null

write_fake 'printf "%s\\n" "WARN [helm scanner] Skipping chart" >&2'
if TRIVY_BIN="$fixture_dir/trivy" bash "$repo_root/scripts/ci/trivy-scan.sh" >/dev/null 2>&1; then
	printf 'Trivy policy accepted a skipped Helm chart\n' >&2
	exit 1
fi

write_fake 'exit 9'
if TRIVY_BIN="$fixture_dir/trivy" bash "$repo_root/scripts/ci/trivy-scan.sh" >/dev/null 2>&1; then
	printf 'Trivy policy hid scanner failure\n' >&2
	exit 1
fi

printf 'Trivy fail-closed policy controls passed\n'
