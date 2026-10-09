#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
script="$repo_root/scripts/ci/docker-hub-mirror.sh"
readonly script
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-docker-hub-mirror.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

fail() {
	printf 'docker-hub-mirror policy: %s\n' "$*" >&2
	exit 1
}

# The hosted runner's daemon.json carries cgroup settings that must survive.
runner="$suite_root/runner.json"
printf '{"exec-opts":["native.cgroupdriver=cgroupfs"],"cgroup-parent":"/actions_job"}' >"$runner"
merged="$(bash "$script" --print "$runner")"
[[ "$(jq -r '."cgroup-parent"' <<<"$merged")" == /actions_job ]] || fail "existing keys were dropped: $merged"
[[ "$(jq -c '."exec-opts"' <<<"$merged")" == '["native.cgroupdriver=cgroupfs"]' ]] || fail "exec-opts changed: $merged"
[[ "$(jq -c '."registry-mirrors"' <<<"$merged")" == '["https://mirror.gcr.io"]' ]] || fail "mirror not added: $merged"

# A second run adds nothing, and existing mirrors are kept.
twice="$suite_root/twice.json"
printf '%s' "$merged" >"$twice"
again="$(bash "$script" --print "$twice")"
[[ "$(jq -c '."registry-mirrors"' <<<"$again")" == '["https://mirror.gcr.io"]' ]] || fail "not idempotent: $again"
other="$suite_root/other.json"
printf '{"registry-mirrors":["https://other.example"]}' >"$other"
kept="$(bash "$script" --print "$other")"
[[ "$(jq -c '."registry-mirrors"' <<<"$kept")" == '["https://mirror.gcr.io","https://other.example"]' ]] || fail "existing mirror lost: $kept"

# Missing or empty daemon.json starts from an empty object.
empty="$(bash "$script" --print "$suite_root/absent.json")"
[[ "$(jq -c . <<<"$empty")" == '{"registry-mirrors":["https://mirror.gcr.io"]}' ]] || fail "missing file: $empty"

# The mirror is configurable.
custom="$(DOCKER_HUB_MIRROR=https://mirror.example bash "$script" --print "$suite_root/absent.json")"
[[ "$(jq -c '."registry-mirrors"' <<<"$custom")" == '["https://mirror.example"]' ]] || fail "override ignored: $custom"

# A malformed daemon.json fails instead of being overwritten.
broken="$suite_root/broken.json"
printf '{"exec-opts": [' >"$broken"
if bash "$script" --print "$broken" >/dev/null 2>&1; then
	fail "malformed daemon.json accepted"
fi

# Wrong usage is refused.
if bash "$script" --print >/dev/null 2>&1; then
	fail "--print without a file accepted"
fi
if bash "$script" extra >/dev/null 2>&1; then
	fail "unknown argument accepted"
fi

printf 'Docker Hub mirror policy tests passed\n'
