#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
scan_root="$repo_root"
if [[ "${1:-}" == --root ]]; then
	if [[ $# -ne 2 ]]; then
		printf 'usage: %s [--root PATH]\n' "$0" >&2
		exit 2
	fi
	scan_root="$(cd "$2" && pwd)"
elif [[ $# -ne 0 ]]; then
	printf 'usage: %s [--root PATH]\n' "$0" >&2
	exit 2
fi
readonly repo_root scan_root

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

if [[ ! -f "$scan_root/REUSE.toml" || ! -d "$scan_root/LICENSES" ]]; then
	printf 'REUSE lint requires REUSE.toml and LICENSES/: %s\n' "$scan_root" >&2
	exit 1
fi

docker_bin="${DOCKER_BIN:-}"
if [[ -z "$docker_bin" ]] && command -v docker >/dev/null; then
	docker_bin="$(command -v docker)"
fi
if [[ -z "$docker_bin" || ! -x "$docker_bin" ]]; then
	printf 'Docker is required for the digest-pinned REUSE lint\n' >&2
	exit 1
fi

image="fsfe/reuse:${REUSE_VERSION}@${REUSE_IMAGE_DIGEST}"
"$docker_bin" run --rm \
	--network none \
	--read-only \
	--cap-drop ALL \
	--security-opt no-new-privileges \
	-u "$(id -u):$(id -g)" \
	--tmpfs /tmp:rw,noexec,nosuid,size=32m \
	-v "$scan_root:/data:ro" \
	-w /data \
	"$image" lint
