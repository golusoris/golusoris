#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Install the pinned gitleaks release binary only after its archive matches the
# SHA-256 recorded in tools/tool-versions.env.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly release_origin='https://github.com/gitleaks/gitleaks/releases/download'
readonly max_archive_bytes=$((32 << 20))
readonly download_timeout_seconds=120

if (($# != 1)) || [[ -z "$1" ]]; then
	printf 'usage: %s <install-dir>\n' "${0##*/}" >&2
	exit 2
fi
install_dir="$1"
readonly install_dir

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"
# shellcheck source=scripts/ci/lib/verified-download.sh
. "$repo_root/scripts/ci/lib/verified-download.sh"

if [[ ! "$GITLEAKS_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'invalid GITLEAKS_VERSION: %s\n' "$GITLEAKS_VERSION" >&2
	exit 1
fi
platform="$(uname -s)/$(uname -m)"
case "$platform" in
Linux/x86_64)
	asset="gitleaks_${GITLEAKS_VERSION}_linux_x64.tar.gz"
	expected_digest="$GITLEAKS_LINUX_X64_SHA256"
	;;
*)
	# CI installs gitleaks on hosted ubuntu-24.04 x64 only; other hosts install it themselves.
	printf 'no pinned gitleaks archive digest for %s\n' "$platform" >&2
	exit 1
	;;
esac
if [[ ! "$expected_digest" =~ ^[0-9a-f]{64}$ ]]; then
	printf 'invalid pinned gitleaks digest: %s\n' "$expected_digest" >&2
	exit 1
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-gitleaks.XXXXXX")"
readonly work_dir
trap 'rm -rf "$work_dir"' EXIT

archive="$work_dir/$asset"
fetch_verified "$release_origin/v${GITLEAKS_VERSION}/$asset" "$expected_digest" \
	"$max_archive_bytes" "$download_timeout_seconds" "$archive"

tar -xzf "$archive" -C "$work_dir" --no-same-owner gitleaks
if [[ -L "$work_dir/gitleaks" || ! -f "$work_dir/gitleaks" ]]; then
	printf 'gitleaks archive does not contain a regular gitleaks binary\n' >&2
	exit 1
fi
installed_version="$("$work_dir/gitleaks" version)"
if [[ "$installed_version" != "$GITLEAKS_VERSION" ]]; then
	printf 'gitleaks reports version %s, want %s\n' "$installed_version" "$GITLEAKS_VERSION" >&2
	exit 1
fi

install -d "$install_dir"
install -m 0755 "$work_dir/gitleaks" "$install_dir/gitleaks"
printf 'installed verified gitleaks %s: %s\n' "$GITLEAKS_VERSION" "$install_dir/gitleaks"
