#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

# Repo root derived from script directory so invocation from any working directory succeeds.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

if [[ -z "${GITHUB_ENV:-}" ]]; then
	printf '::error::GITHUB_ENV is unset\n' >&2
	exit 1
fi

tool_versions_file="${TOOL_VERSIONS_FILE:-$repo_root/tools/tool-versions.env}"
node_version_file="${NODE_VERSION_FILE:-$repo_root/.node-version}"

if [[ ! -f "$tool_versions_file" ]]; then
	printf '::error::tool-versions file not found: %s\n' "$tool_versions_file" >&2
	exit 1
fi

if [[ ! -f "$node_version_file" ]]; then
	printf '::error::node-version file not found: %s\n' "$node_version_file" >&2
	exit 1
fi

# shellcheck source=/dev/null
. "$tool_versions_file"
read -r NODE_VERSION < "$node_version_file"

semver_pattern='^[0-9]+\.[0-9]+\.[0-9]+$'
for var_name in COSIGN_VERSION SYFT_VERSION GORELEASER_VERSION BUILDX_VERSION NODE_VERSION; do
	val="${!var_name:-}"
	if [[ ! "$val" =~ $semver_pattern ]]; then
		printf '::error::%s is invalid: %s\n' "$var_name" "$val" >&2
		exit 1
	fi
done

{
	printf 'BUILDX_VERSION=%s\n' "$BUILDX_VERSION"
	printf 'COSIGN_VERSION=%s\n' "$COSIGN_VERSION"
	printf 'GORELEASER_VERSION=%s\n' "$GORELEASER_VERSION"
	printf 'NODE_VERSION=%s\n' "$NODE_VERSION"
	printf 'SYFT_VERSION=%s\n' "$SYFT_VERSION"
} >> "$GITHUB_ENV"
