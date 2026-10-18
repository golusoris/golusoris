#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Offline policy test for install-vacuum.sh: a fake go serves the module sum and
# the built binary, so sum, version and pin-shape failures are proven.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-install-vacuum.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

readonly version='v0.33.1'
readonly good_sum='h1:dlKjyVR7XeJfAGtseGARiJZpD9d6wYWaPTAeW0AgMDU='
readonly other_sum='h1:PQ/1K4axoQon95CUSkTzsHvafj/Go2kjUu2pir7VojY='

fake_bin="$suite_root/bin"
mkdir -p "$fake_bin" "$suite_root/repo/scripts/ci" "$suite_root/repo/tools"
install -m 0755 "$repo_root/scripts/ci/install-vacuum.sh" \
	"$suite_root/repo/scripts/ci/install-vacuum.sh"

cat >"$fake_bin/go" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$GO_LOG"
case "$1 $2" in
"mod download") printf '{"Path":"github.com/daveshanley/vacuum","Sum":"%s"}\n' "$FAKE_SUM" ;;
"install "*)
	mkdir -p "$GOBIN"
	printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$FAKE_REPORTED" >"$GOBIN/vacuum"
	chmod +x "$GOBIN/vacuum"
	;;
*) exit 2 ;;
esac
FAKE
chmod +x "$fake_bin/go"

write_pins() {
	printf 'VACUUM_VERSION=%s\nVACUUM_MODULE_SUM=%s\n' "$1" "$2" \
		>"$suite_root/repo/tools/tool-versions.env"
}

run_installer() {
	local sum="$1" reported="$2" target="$3"
	env PATH="$fake_bin:$PATH" FAKE_SUM="$sum" FAKE_REPORTED="$reported" \
		GO_LOG="$suite_root/go.log" \
		bash "$suite_root/repo/scripts/ci/install-vacuum.sh" "$target"
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
	if [[ -e "$target/vacuum" ]]; then
		printf 'failed install left a binary behind: %s\n' "$target/vacuum" >&2
		return 1
	fi
}

# Positive: the pinned sum admits the module and installs the binary.
write_pins "$version" "$good_sum"
run_installer "$good_sum" "$version" "$suite_root/install-ok" >/dev/null
[[ "$("$suite_root/install-ok/vacuum")" == "$version" ]] || {
	printf 'installed vacuum does not report %s\n' "$version" >&2
	exit 1
}
grep -Fxq "install github.com/daveshanley/vacuum@$version" "$suite_root/go.log" || {
	printf 'installer did not build the pinned module version\n' >&2
	exit 1
}

# Negative: another module sum rejects the download before any build.
: >"$suite_root/go.log"
expect_failure 'module sum mismatch' "$suite_root/install-sum" \
	run_installer "$other_sum" "$version" "$suite_root/install-sum"
if grep -q '^install ' "$suite_root/go.log"; then
	printf 'installer built a module whose sum did not match\n' >&2
	exit 1
fi

# Negative: a missing sum in the download record is a mismatch, not a pass.
expect_failure 'got none' "$suite_root/install-nosum" \
	run_installer '' "$version" "$suite_root/install-nosum"

# Negative: a malformed sum pin fails before any download.
write_pins "$version" 'sha256:not-a-sum'
expect_failure 'invalid pinned module sum' "$suite_root/install-pin" \
	run_installer "$good_sum" "$version" "$suite_root/install-pin"

# Negative: a verified module whose binary reports another version.
write_pins "$version" "$good_sum"
expect_failure 'reports version v0.33.0' "$suite_root/install-version" \
	run_installer "$good_sum" v0.33.0 "$suite_root/install-version"

# Boundary: a version pin without the v prefix is refused.
write_pins '0.33.1' "$good_sum"
expect_failure 'invalid VACUUM_VERSION' "$suite_root/install-semver" \
	run_installer "$good_sum" "$version" "$suite_root/install-semver"

printf 'vacuum installer policy tests passed\n'
