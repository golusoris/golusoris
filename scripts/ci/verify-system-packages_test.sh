#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Offline policy test for verify-system-packages.sh and install-cgo-libs.sh:
# fake apt-get, dpkg-query and sudo on a closed PATH prove the install and
# verify paths, and the workflow wiring checks prove their callers.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-system-packages.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

readonly verifier="$repo_root/scripts/ci/verify-system-packages.sh"
readonly cgo_installer="$repo_root/scripts/ci/install-cgo-libs.sh"
fake_bin="$suite_root/bin"
mkdir -p "$fake_bin"
for tool in awk bash dirname env timeout; do
	ln -s "$(command -v "$tool")" "$fake_bin/$tool"
done

cat >"$fake_bin/sudo" <<'EOF'
#!/usr/bin/env bash
[[ "$1" == -n ]] || exit 97
shift
exec "$@"
EOF
cat >"$fake_bin/apt-get" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$APT_LOG"
case "$1" in
update) [[ "${FAIL_APT:-}" != update ]] ;;
install)
	[[ "${FAIL_APT:-}" != install ]] || exit 100
	for arg in "$@"; do
		case "$arg" in
		install | -y | --no-install-recommends | -o | Acquire::Retries=*) ;;
		*=*) printf '%s %s\n' "${arg%%=*}" "${arg#*=}" >>"$INSTALLED" ;;
		*) printf '%s 1.0\n' "$arg" >>"$INSTALLED" ;;
		esac
	done
	;;
*) exit 98 ;;
esac
EOF
cat >"$fake_bin/dpkg-query" <<'EOF'
#!/usr/bin/env bash
package="${*: -1}"
version="$(awk -v p="$package" '$1 == p { v = $2 } END { print v }' "$INSTALLED")"
[[ -n "$version" ]] || exit 1
printf 'installed %s' "$version"
EOF
chmod +x "$fake_bin/sudo" "$fake_bin/apt-get" "$fake_bin/dpkg-query"

reset_state() {
	: >"$suite_root/apt.log"
	printf 'libfoo-dev 1.0\n' >"$suite_root/installed"
}

run_in() {
	local path="$1"
	shift
	env -i PATH="$path" APT_LOG="$suite_root/apt.log" \
		INSTALLED="$suite_root/installed" FAIL_APT="${FAIL_APT:-}" \
		"$BASH" "$@"
}

run_verifier() {
	run_in "$fake_bin" "$verifier" "$@"
}

expect_failure() {
	local pattern="$1"
	shift
	local output status=0
	output="$("$@" 2>&1)" || status=$?
	if ((status == 0)) || [[ "$output" != *"$pattern"* ]]; then
		printf 'expected failure containing %q, got status %d: %s\n' \
			"$pattern" "$status" "$output" >&2
		return 1
	fi
}

require_log() {
	if ! grep -Fxq -- "$1" "$suite_root/apt.log"; then
		printf 'apt-get was not called as: %s\nlog:\n%s\n' "$1" \
			"$(cat "$suite_root/apt.log")" >&2
		return 1
	fi
}

require_no_apt() {
	if [[ -s "$suite_root/apt.log" ]]; then
		printf 'apt-get ran although it must not: %s\n' "$(cat "$suite_root/apt.log")" >&2
		return 1
	fi
}

# Positive: verify-only accepts an installed package, bare and version-pinned.
reset_state
run_verifier libfoo-dev 'libfoo-dev=1.0'
require_no_apt

# Negative: verify-only rejects a missing package and a different pinned version.
expect_failure 'required system package is not installed: libbar-dev' run_verifier libbar-dev
expect_failure 'libfoo-dev is version 1.0, declared 2.0' run_verifier 'libfoo-dev=2.0'
require_no_apt

# Positive: --install updates, installs with bounded retries, then verifies.
reset_state
run_verifier --install libbar-dev 'libbaz-dev=2.1' >/dev/null
require_log 'update -o Acquire::Retries=3'
require_log 'install -y --no-install-recommends -o Acquire::Retries=3 libbar-dev libbaz-dev=2.1'
run_verifier libbar-dev 'libbaz-dev=2.1'

# Negative: a failing apt-get install fails the step.
reset_state
FAIL_APT=install expect_failure '' run_verifier --install libbar-dev

# Negative: an invalid declaration is refused before apt-get runs.
reset_state
expect_failure "invalid system package declaration: bad;rm" \
	run_verifier --install libbar-dev 'bad;rm'
expect_failure 'usage:' run_verifier --unknown libbar-dev
require_no_apt

# Boundary: no packages is a no-op; 64 pass validation; 65 are refused.
reset_state
run_verifier --install
require_no_apt
mapfile -t many < <(seq -f 'libpkg%g-dev' 1 64)
run_verifier --install "${many[@]}" >/dev/null
reset_state
many+=('libpkg65-dev')
expect_failure 'expected at most 64' run_verifier --install "${many[@]}"
require_no_apt

# Negative: hosts without apt-get or dpkg-query fail with a stated reason.
no_apt="$suite_root/no-apt"
mkdir -p "$no_apt"
for tool in awk bash env timeout sudo dpkg-query; do
	ln -s "$fake_bin/$tool" "$no_apt/$tool"
done
no_dpkg="$suite_root/no-dpkg"
mkdir -p "$no_dpkg"
for tool in awk bash env timeout sudo apt-get; do
	ln -s "$fake_bin/$tool" "$no_dpkg/$tool"
done
expect_failure 'requires a Debian-family runner with apt-get' \
	run_in "$no_apt" "$verifier" --install libbar-dev
expect_failure 'require a Debian-family runner with dpkg-query' \
	run_in "$no_dpkg" "$verifier" libfoo-dev

# Positive: the cgo wrapper installs exactly the declared header set.
reset_state
run_in "$fake_bin" "$cgo_installer" >/dev/null
require_log 'install -y --no-install-recommends -o Acquire::Retries=3 libudev-dev libopenal-dev libvorbis-dev libgl-dev libx11-dev libxrandr-dev libxxf86vm-dev libxi-dev libxcursor-dev libxinerama-dev'

# check_wiring proves the workflows install cgo headers and system packages
# through these scripts, never inline and never from the caller's workspace.
check_wiring() {
	local workflows="$1"
	local name
	for name in ci portability; do
		if ! grep -Fq 'run: bash scripts/ci/install-cgo-libs.sh' "$workflows/$name.yml"; then
			printf '%s.yml does not install cgo headers through install-cgo-libs.sh\n' "$name" >&2
			return 1
		fi
	done
	if [[ "$(grep -Fc 'run: bash scripts/ci/install-cgo-libs.sh' "$workflows/ci.yml")" -ne 2 ]]; then
		printf 'ci.yml must install cgo headers in the module sweep and apidiff jobs\n' >&2
		return 1
	fi
	if grep -rHn 'libudev-dev' "$workflows"; then
		printf 'workflow inlines the cgo header list\n' >&2
		return 1
	fi
	local reusable="$workflows/ci-go.yml"
	# shellcheck disable=SC2016 # Match literal runtime variables in workflow YAML.
	if grep -Fq '"${GITHUB_WORKSPACE}/scripts/ci/' "$reusable"; then
		printf 'ci-go.yml runs a script from the caller workspace\n' >&2
		return 1
	fi
	local users checkouts runners
	# shellcheck disable=SC2016 # Match literal expressions and runtime variables in workflow YAML.
	users="$(grep -Fc 'if: ${{ inputs.system-packages' "$reusable")"
	checkouts="$(grep -Fc 'name: Check out golusoris system-package script' "$reusable")"
	# shellcheck disable=SC2016 # Match literal runtime variables in workflow YAML.
	runners="$(grep -Fc 'bash "$ci_root/scripts/ci/verify-system-packages.sh" "${mode[@]}" "${packages[@]}"' "$reusable")"
	if ((users != 12 || checkouts != 6 || runners != 6)); then
		printf 'ci-go.yml system-package steps drifted: %d guards, %d checkouts, %d runs\n' \
			"$users" "$checkouts" "$runners" >&2
		return 1
	fi
	# shellcheck disable=SC2016 # Match the literal runtime variable in workflow YAML.
	if ! grep -Fq 'if [[ "$INSTALL_SYSTEM_PACKAGES" == true ]]; then' "$reusable"; then
		printf 'ci-go.yml does not gate installation on install-system-packages\n' >&2
		return 1
	fi
}

check_wiring "$repo_root/.github/workflows"
expect_wiring_rejection() {
	local name="$1" file="$2" expression="$3"
	cp -R "$repo_root/.github/workflows" "$suite_root/$name"
	sed -i "$expression" "$suite_root/$name/$file"
	if cmp -s "$repo_root/.github/workflows/$file" "$suite_root/$name/$file"; then
		printf 'negative control %s did not mutate %s\n' "$name" "$file" >&2
		exit 1
	fi
	if check_wiring "$suite_root/$name" >/dev/null 2>&1; then
		printf 'workflow wiring check accepted negative control %s\n' "$name" >&2
		exit 1
	fi
}
expect_wiring_rejection inline-cgo portability.yml \
	's|run: bash scripts/ci/install-cgo-libs.sh|run: sudo apt-get install -y libudev-dev|'
expect_wiring_rejection sweep-without-cgo ci.yml \
	'0,\|run: bash scripts/ci/install-cgo-libs.sh|s||run: "true"|'
# shellcheck disable=SC2016 # Plant the literal caller-workspace path.
expect_wiring_rejection caller-script ci-go.yml \
	's|bash "$ci_root/scripts/ci/verify-system-packages.sh"|bash "${GITHUB_WORKSPACE}/scripts/ci/verify-system-packages.sh"|'
# shellcheck disable=SC2016 # Plant against the literal runtime variable.
expect_wiring_rejection install-ungated ci-go.yml \
	's|if \[\[ "$INSTALL_SYSTEM_PACKAGES" == true \]\]; then|if true; then|'

printf 'system package install and verify policy tests passed\n'
