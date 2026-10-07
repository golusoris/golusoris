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

# expect_render asserts one render contains (+text) or omits (-text) each fragment.
expect_render() {
	local label="$1"
	shift
	local -a render_args=()
	while (($# > 0)) && [[ "$1" != -- ]]; do
		render_args+=("$1")
		shift
	done
	shift
	local output
	if ! output="$($helm_bin "${common_args[@]}" "${render_args[@]}" 2>&1)"; then
		printf '%s failed to render: %s\n' "$label" "$output" >&2
		exit 1
	fi
	local check fragment
	for check in "$@"; do
		fragment="${check:1}"
		case "$check" in
		+*) [[ "$output" == *"$fragment"* ]] || {
			printf '%s is missing %q\n' "$label" "$fragment" >&2
			exit 1
		} ;;
		-*) [[ "$output" != *"$fragment"* ]] || {
			printf '%s unexpectedly contains %q\n' "$label" "$fragment" >&2
			exit 1
		} ;;
		*)
			printf '%s has a malformed check %q\n' "$label" "$check" >&2
			exit 1
			;;
		esac
	done
}

readonly prestop_sleep=$'preStop:\n              sleep:\n                seconds: 5'
expect_render 'drain default: preStop sleep, no in-app wait' -- \
	'+terminationGracePeriodSeconds: 30' "+$prestop_sleep" \
	$'+name: APP_HEALTH_DRAIN_DELAY\n              value: "0s"' \
	$'+name: APP_HTTP_TIMEOUTS_SHUTDOWN\n              value: "10s"'
expect_render 'drain without preStop waits in the app' --set drain.preStop=false -- \
	'-lifecycle:' $'+name: APP_HEALTH_DRAIN_DELAY\n              value: "5s"'
expect_render 'drain delay zero renders no hook' --set drain.delaySeconds=0 -- \
	'-lifecycle:' $'+name: APP_HEALTH_DRAIN_DELAY\n              value: "0s"'
expect_render 'grace one second above the drain budget' --set terminationGracePeriodSeconds=16 -- \
	'+terminationGracePeriodSeconds: 16'
expect_render 'drain env prefix follows the app config prefix' --set-string drain.envPrefix=SVC_ -- \
	'+name: SVC_HEALTH_DRAIN_DELAY' '+name: SVC_HTTP_TIMEOUTS_SHUTDOWN' '-APP_HEALTH_DRAIN_DELAY'

readonly grace_error='terminationGracePeriodSeconds must be an integer greater than drain.delaySeconds + drain.shutdownSeconds (15)'
expect_rejected 'grace equal to the drain budget' "$grace_error" \
	"${common_args[@]}" --set terminationGracePeriodSeconds=15
expect_rejected 'non-integer grace' "$grace_error" \
	"${common_args[@]}" --set-string terminationGracePeriodSeconds=30s
expect_rejected 'negative drain delay' 'drain.delaySeconds must be an integer from 0 to 9999' \
	"${common_args[@]}" --set drain.delaySeconds=-1
expect_rejected 'fractional drain delay' 'drain.delaySeconds must be an integer from 0 to 9999' \
	"${common_args[@]}" --set drain.delaySeconds=2.5
expect_rejected 'zero shutdown budget' 'drain.shutdownSeconds must be an integer from 1 to 9999' \
	"${common_args[@]}" --set drain.shutdownSeconds=0
expect_rejected 'string preStop flag' 'drain.preStop must be a boolean' \
	"${common_args[@]}" --set-string drain.preStop=yes
expect_rejected 'lowercase env prefix' 'drain.envPrefix must be empty or an uppercase prefix ending in _' \
	"${common_args[@]}" --set-string drain.envPrefix=app_
expect_rejected 'env override of the drain delay' \
	'env.APP_HEALTH_DRAIN_DELAY is set by drain.*; configure drain.delaySeconds or drain.shutdownSeconds instead' \
	"${common_args[@]}" --set-string env.APP_HEALTH_DRAIN_DELAY=1s

printf 'helm chart positive, negative, and boundary tests passed\n'
