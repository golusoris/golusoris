#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

readonly helm_scan_digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
trivy_log="$(mktemp "${TMPDIR:-/tmp}/golusoris-trivy.XXXXXX.log")"
readonly trivy_log
trap 'rm -f "$trivy_log"' EXIT

scan_args=(
	fs
	--scanners "vuln,secret,misconfig"
	--severity "HIGH,CRITICAL"
	--ignore-unfixed
	--vex .trivy/openvex.json
	--helm-set-string "image.digest=$helm_scan_digest"
	--tf-exclude-downloaded-modules
	--skip-dirs .git
	--skip-dirs .terraform
	--skip-dirs .workingdir
	--skip-dirs .workingdir2
	--skip-dirs node_modules
	--skip-version-check
	--exit-code 1
	.
)

run_trivy() {
	local -a statuses
	set +e
	"$@" 2>&1 | tee "$trivy_log"
	statuses=("${PIPESTATUS[@]}")
	set -e
	if ((statuses[0] != 0)); then
		return "${statuses[0]}"
	fi
	if ((statuses[1] != 0)); then
		return "${statuses[1]}"
	fi
	if grep -Fq '[helm scanner] Skipping chart' "$trivy_log"; then
		printf 'Trivy skipped Helm chart; supply every required render value\n' >&2
		return 1
	fi
}

installed_version=""
trivy_bin="${TRIVY_BIN:-}"
if [[ -z "$trivy_bin" ]] && command -v trivy >/dev/null 2>&1; then
	trivy_bin="$(command -v trivy)"
fi
if [[ -n "$trivy_bin" && -x "$trivy_bin" ]]; then
	version_output="$($trivy_bin --version)"
	installed_version="$(awk '$1 == "Version:" { version = $2 } END { print version }' <<<"$version_output")"
fi

if [[ "$installed_version" == "$TRIVY_VERSION" ]]; then
	cd "$repo_root"
	run_trivy "$trivy_bin" "${scan_args[@]}"
	exit
fi

if ! command -v docker >/dev/null 2>&1; then
	printf 'Trivy %s or Docker is required; installed Trivy is %s\n' \
		"$TRIVY_VERSION" "${installed_version:-missing}" >&2
	exit 1
fi

image="aquasec/trivy:${TRIVY_VERSION}@${TRIVY_IMAGE_DIGEST}"
set +e
tar -C "$repo_root" \
	--exclude=.git \
	--exclude=.terraform \
	--exclude=.workingdir \
	--exclude=.workingdir2 \
	--exclude=node_modules \
	-cf - . \
	| docker run --rm -i \
		-v golusoris-trivy-cache:/var/cache/trivy \
		-e "HELM_SCAN_DIGEST=$helm_scan_digest" \
		--entrypoint sh "$image" -ec '
		mkdir -p /scan
		tar -xf - -C /scan
		cd /scan
		exec trivy --cache-dir /var/cache/trivy fs \
			--scanners vuln,secret,misconfig \
			--severity HIGH,CRITICAL \
			--ignore-unfixed \
			--vex .trivy/openvex.json \
			--helm-set-string "image.digest=$HELM_SCAN_DIGEST" \
			--tf-exclude-downloaded-modules \
			--skip-dirs .git \
			--skip-dirs .terraform \
			--skip-dirs .workingdir \
			--skip-dirs .workingdir2 \
			--skip-dirs node_modules \
			--skip-version-check \
			--exit-code 1 \
			.
	' 2>&1 | tee "$trivy_log"
statuses=("${PIPESTATUS[@]}")
set -e
if ((statuses[0] != 0 || statuses[1] != 0 || statuses[2] != 0)); then
	exit 1
fi
if grep -Fq '[helm scanner] Skipping chart' "$trivy_log"; then
	printf 'Trivy skipped Helm chart; supply every required render value\n' >&2
	exit 1
fi
