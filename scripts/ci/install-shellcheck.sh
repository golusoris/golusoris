#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Install the pinned ShellCheck release binary only after its archive matches the
# SHA-256 recorded in tools/tool-versions.env. Hosted runners ship an older one.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly release_origin='https://github.com/koalaman/shellcheck/releases/download'
readonly max_archive_bytes=$((16 << 20))
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

if [[ ! "$SHELLCHECK_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'invalid SHELLCHECK_VERSION: %s\n' "$SHELLCHECK_VERSION" >&2
	exit 1
fi
platform="$(uname -s)/$(uname -m)"
case "$platform" in
Linux/x86_64)
	asset="shellcheck-v${SHELLCHECK_VERSION}.linux.x86_64.tar.xz"
	expected_digest="$SHELLCHECK_LINUX_X64_SHA256"
	;;
*)
	# CI installs ShellCheck on hosted ubuntu-24.04 x64 only; other hosts install it themselves.
	printf 'no pinned ShellCheck archive digest for %s\n' "$platform" >&2
	exit 1
	;;
esac

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-shellcheck.XXXXXX")"
readonly work_dir
trap 'rm -rf "$work_dir"' EXIT

archive="$work_dir/$asset"
fetch_verified "$release_origin/v${SHELLCHECK_VERSION}/$asset" "$expected_digest" \
	"$max_archive_bytes" "$download_timeout_seconds" "$archive"

member="shellcheck-v${SHELLCHECK_VERSION}/shellcheck"
tar -xJf "$archive" -C "$work_dir" --no-same-owner "$member"
binary="$work_dir/$member"
if [[ -L "$binary" || ! -f "$binary" ]]; then
	printf 'ShellCheck archive does not contain a regular shellcheck binary\n' >&2
	exit 1
fi
installed_version="$("$binary" --version | awk '$1 == "version:" { print $2 }')"
if [[ "$installed_version" != "$SHELLCHECK_VERSION" ]]; then
	printf 'shellcheck reports version %s, want %s\n' "$installed_version" "$SHELLCHECK_VERSION" >&2
	exit 1
fi

install -d "$install_dir"
install -m 0755 "$binary" "$install_dir/shellcheck"
printf 'installed verified ShellCheck %s: %s\n' "$SHELLCHECK_VERSION" "$install_dir/shellcheck"
