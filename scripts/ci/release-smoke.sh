#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Runs every linux/amd64 CLI binary goreleaser built and checks it reports
# the release tag. Run from the directory goreleaser ran in.
# usage: release-smoke.sh <tag>
set -euo pipefail

readonly expected_binaries=(golusoris golusoris-mcp)
readonly artifacts=dist/artifacts.json

if (($# != 1)); then
	printf 'usage: %s <tag>\n' "$0" >&2
	exit 2
fi
readonly want="${1#v}"
if [[ -z "$want" ]]; then
	printf 'release-smoke: empty version in tag %q\n' "$1" >&2
	exit 2
fi
if [[ ! -s "$artifacts" ]]; then
	printf 'release-smoke: %s missing or empty\n' "$artifacts" >&2
	exit 1
fi

failures=0
for name in "${expected_binaries[@]}"; do
	mapfile -t paths < <(jq -r --arg name "$name" \
		'.[] | select(.type == "Binary" and .goos == "linux" and .goarch == "amd64" and .extra.ID == $name) | .path' \
		"$artifacts")
	if ((${#paths[@]} != 1)); then
		printf 'release-smoke: %s: expected one linux/amd64 binary, found %d\n' "$name" "${#paths[@]}" >&2
		failures=$((failures + 1))
		continue
	fi
	if ! out="$(timeout 30 "${paths[0]}" --version 2>&1)"; then
		printf 'release-smoke: %s --version failed: %s\n' "${paths[0]}" "$out" >&2
		failures=$((failures + 1))
		continue
	fi
	if [[ "$out" != "$name version $want" && "$out" != "$name version $want "* ]]; then
		printf 'release-smoke: %s reports %q, want "%s version %s"\n' "${paths[0]}" "$out" "$name" "$want" >&2
		failures=$((failures + 1))
		continue
	fi
	printf 'release-smoke: %s\n' "$out"
done
if ((failures > 0)); then
	printf 'release-smoke: %d of %d binaries failed\n' "$failures" "${#expected_binaries[@]}" >&2
	exit 1
fi
