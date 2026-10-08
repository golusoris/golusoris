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
