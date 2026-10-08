#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Offline policy test for install-shellcheck.sh: fake curl and uname serve fixture
# archives, so digest, version, platform and archive-shape failures are proven.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-install-shellcheck.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

readonly version='0.11.0'
readonly asset="shellcheck-v${version}.linux.x86_64.tar.xz"
readonly expected_url="https://github.com/koalaman/shellcheck/releases/download/v${version}/${asset}"

fake_bin="$suite_root/bin"
mkdir -p "$fake_bin" "$suite_root/repo/scripts/ci/lib" "$suite_root/repo/tools"
install -m 0755 "$repo_root/scripts/ci/install-shellcheck.sh" \
	"$suite_root/repo/scripts/ci/install-shellcheck.sh"
install -m 0644 "$repo_root/scripts/ci/lib/verified-download.sh" \
	"$suite_root/repo/scripts/ci/lib/verified-download.sh"

cat >"$fake_bin/curl" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
output=''
url=''
while (($# > 0)); do
	case "$1" in
	--output) output="$2"; shift 2 ;;
	--retry | --connect-timeout | --max-time | --max-filesize | --proto | --proto-redir) shift 2 ;;
	-*) shift ;;
	*) url="$1"; shift ;;
	esac
done
printf '%s\n' "$url" >>"$CURL_LOG"
cp "$FIXTURE_ARCHIVE" "$output"
FAKE
cat >"$fake_bin/uname" <<'FAKE'
#!/usr/bin/env bash
case "$1" in
-s) printf '%s\n' "${FAKE_UNAME_S:-Linux}" ;;
-m) printf '%s\n' "${FAKE_UNAME_M:-x86_64}" ;;
*) exit 2 ;;
esac
FAKE
chmod +x "$fake_bin/curl" "$fake_bin/uname"

# make_archive <name> <reported-version> [symlink]
make_archive() {
	local name="$1" reported="$2" shape="${3:-regular}"
	local stage="$suite_root/stage-$name"
	local dir="$stage/shellcheck-v${version}"
	mkdir -p "$dir"
	if [[ "$shape" == symlink ]]; then
		ln -s /bin/true "$dir/shellcheck"
	else
		printf '#!/bin/sh\nprintf "ShellCheck - shell script analysis tool\\nversion: %s\\nlicense: GPLv3\\n"\n' \
			"$reported" >"$dir/shellcheck"
		chmod +x "$dir/shellcheck"
	fi
	tar -cJf "$suite_root/$name.tar.xz" -C "$stage" "shellcheck-v${version}"
	printf '%s\n' "$suite_root/$name.tar.xz"
}

write_pins() {
	printf 'SHELLCHECK_VERSION=%s\nSHELLCHECK_LINUX_X64_SHA256=%s\n' "$1" "$2" \
		>"$suite_root/repo/tools/tool-versions.env"
}

digest_of() {
	sha256sum "$1" | awk '{ print $1 }'
}

run_installer() {
	local archive="$1" target="$2"
	env PATH="$fake_bin:$PATH" FIXTURE_ARCHIVE="$archive" \
		CURL_LOG="$suite_root/curl.log" \
		bash "$suite_root/repo/scripts/ci/install-shellcheck.sh" "$target"
}

expect_failure() {
	local pattern="$1" target="$2"
	shift 2
	local output status=0
	output="$("$@" 2>&1)" || status=$?
	if ((status == 0)) || [[ "$output" != *"$pattern"* ]]; then
		printf 'expected failure containing %q, got status %d: %s\n' \
			"$pattern" "$status" "$output" >&2
		return 1
	fi
	if [[ -e "$target/shellcheck" ]]; then
		printf 'failed install left a binary behind: %s\n' "$target/shellcheck" >&2
		return 1
	fi
}

good="$(make_archive good "$version")"

# Positive: the pinned digest admits the archive and installs the binary.
write_pins "$version" "$(digest_of "$good")"
run_installer "$good" "$suite_root/install-ok" >/dev/null
reported="$("$suite_root/install-ok/shellcheck" --version | awk '$1 == "version:" { print $2 }')"
[[ "$reported" == "$version" ]] || {
	printf 'installed shellcheck reports %s, want %s\n' "$reported" "$version" >&2
	exit 1
}
[[ "$(tail -n 1 "$suite_root/curl.log")" == "$expected_url" ]] || {
	printf 'installer fetched %s, want %s\n' "$(tail -n 1 "$suite_root/curl.log")" "$expected_url" >&2
	exit 1
}

# Negative: one changed digest byte rejects the same archive.
write_pins "$version" "0$(digest_of "$good" | cut -c2-)"
expect_failure 'archive digest mismatch' "$suite_root/install-digest" \
	run_installer "$good" "$suite_root/install-digest"

# Negative: a malformed pin fails before any download.
write_pins "$version" 'not-a-digest'
expect_failure 'invalid pinned archive digest' "$suite_root/install-pin" \
	run_installer "$good" "$suite_root/install-pin"

# Negative: a verified archive whose binary reports another version.
foreign="$(make_archive foreign 0.9.0)"
write_pins "$version" "$(digest_of "$foreign")"
expect_failure 'reports version 0.9.0' "$suite_root/install-version" \
	run_installer "$foreign" "$suite_root/install-version"

# Boundary: a symlinked binary entry is refused even with a matching digest.
linked="$(make_archive linked "$version" symlink)"
write_pins "$version" "$(digest_of "$linked")"
expect_failure 'regular shellcheck binary' "$suite_root/install-link" \
	run_installer "$linked" "$suite_root/install-link"

# Negative: a host without a pinned archive digest is refused.
write_pins "$version" "$(digest_of "$good")"
export FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64
expect_failure 'no pinned ShellCheck archive digest for Darwin/arm64' "$suite_root/install-host" \
	run_installer "$good" "$suite_root/install-host"
unset FAKE_UNAME_S FAKE_UNAME_M

# Negative: a malformed version pin is refused.
write_pins '0.11' "$(digest_of "$good")"
expect_failure 'invalid SHELLCHECK_VERSION' "$suite_root/install-semver" \
	run_installer "$good" "$suite_root/install-semver"

printf 'shellcheck installer policy tests passed\n'
