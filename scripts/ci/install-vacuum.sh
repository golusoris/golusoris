#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Install the pinned vacuum OpenAPI linter only after its module zip matches the
# go.sum hash recorded in tools/tool-versions.env.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly module='github.com/daveshanley/vacuum'

if (($# != 1)) || [[ -z "$1" ]]; then
	printf 'usage: %s <install-dir>\n' "${0##*/}" >&2
	exit 2
fi
install_dir="$(mkdir -p "$1" && cd "$1" && pwd)"
readonly install_dir

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

if [[ ! "$VACUUM_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'invalid VACUUM_VERSION: %s\n' "$VACUUM_VERSION" >&2
	exit 1
fi
if [[ ! "$VACUUM_MODULE_SUM" =~ ^h1:[A-Za-z0-9+/]{43}=$ ]]; then
	printf 'invalid pinned module sum: %s\n' "$VACUUM_MODULE_SUM" >&2
	exit 1
fi

# Outside any module, so the caller's go.mod cannot redirect the download.
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-vacuum.XXXXXX")"
readonly work_dir
trap 'rm -rf "$work_dir"' EXIT
cd "$work_dir"

download="$(GOFLAGS=-mod=mod GO111MODULE=on go mod download -json "$module@$VACUUM_VERSION")"
module_sum="$(jq -r '.Sum // ""' <<<"$download")"
if [[ "$module_sum" != "$VACUUM_MODULE_SUM" ]]; then
	printf 'module sum mismatch for %s@%s: got %s, want %s\n' \
		"$module" "$VACUUM_VERSION" "${module_sum:-none}" "$VACUUM_MODULE_SUM" >&2
	exit 1
fi

GOBIN="$work_dir/bin" go install "$module@$VACUUM_VERSION"
binary="$work_dir/bin/vacuum"
installed_version="$("$binary" version)"
if [[ "$installed_version" != "$VACUUM_VERSION" ]]; then
	printf 'vacuum reports version %s, want %s\n' "$installed_version" "$VACUUM_VERSION" >&2
	exit 1
fi

install -m 0755 "$binary" "$install_dir/vacuum"
printf 'installed verified vacuum %s: %s\n' "$VACUUM_VERSION" "$install_dir/vacuum"
