#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly chart="$repo_root/deploy/helm"
readonly digest="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"
# shellcheck source=scripts/ci/lib/helm.sh
. "$repo_root/scripts/ci/lib/helm.sh"

helm_bin="$(resolve_pinned_helm "$HELM_VERSION")"
readonly helm_bin

common_args=(
	template golusoris "$chart"
	--set-string "image.digest=$digest"
)
backup_args=(
	"${common_args[@]}"
	--set backup.enabled=true
	--set-string backup.image.repository=registry.example.test/platform/postgres-backup
	--set-string "backup.image.digest=$digest"
	--set-string backup.s3.bucket=golusoris-backups
)

"$helm_bin" lint "$chart" --set-string "image.digest=$digest" >/dev/null
"$helm_bin" "${common_args[@]}" >/dev/null
"$helm_bin" "${backup_args[@]}" --set-string backup.dbName=app_db-1.2 >/dev/null

expect_rejected() {
	local label="$1"
	local expected="$2"
	shift 2
	local output
	if output="$($helm_bin "$@" 2>&1)"; then
		printf '%s was accepted unexpectedly\n' "$label" >&2
		exit 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf '%s failed without the expected diagnostic: %s\n' "$label" "$output" >&2
		exit 1
	fi
}

expect_rejected \
	'backup shell metacharacters' \
	'backup.dbName must be a 1-63 character safe object stem' \
	"${backup_args[@]}" --set-string "backup.dbName=\$(id)"
expect_rejected \
	'backup path traversal' \
	'backup.dbName must be a 1-63 character safe object stem' \
	"${backup_args[@]}" --set-string backup.dbName=../escape
expect_rejected \
	'application image YAML injection' \
	'image.repository must be a lowercase OCI repository without a tag or digest' \
	"${common_args[@]}" --set-string $'image.repository=ghcr.io/example/app"\ncommand: [id]'
expect_rejected \
	'backup image tag bypass' \
	'backup.image.repository must be a lowercase OCI repository without a tag or digest' \
	"${backup_args[@]}" --set-string backup.image.repository=registry.example.test/platform/backup:latest

printf 'helm chart positive, negative, and boundary tests passed\n'
