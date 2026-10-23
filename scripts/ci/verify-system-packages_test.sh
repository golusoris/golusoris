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
for tool in awk bash cp dirname env id mkdir timeout; do
	ln -s "$(command -v "$tool")" "$fake_bin/$tool"
done

cat >"$fake_bin/sudo" <<'EOF'
#!/usr/bin/env bash
[[ "$1" == -n ]] || exit 97
shift
exec "$@"
EOF
cat >"$fake_bin/chown" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"${CHOWN_LOG:-/dev/null}"
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
		install | -y | --no-install-recommends | -o | Acquire::* | Dir::Cache::Archives=* | APT::Keep-Downloaded-Packages=true) ;;
		*=*)
			pkg="${arg%%=*}"
			printf '%s %s\n' "$pkg" "${arg#*=}" >>"$INSTALLED"
			;;
		*)
			printf '%s 1.0\n' "$arg" >>"$INSTALLED"
			;;
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
chmod +x "$fake_bin/sudo" "$fake_bin/chown" "$fake_bin/apt-get" "$fake_bin/dpkg-query"

reset_state() {
	: >"$suite_root/apt.log"
	: >"$suite_root/chown.log"
	printf 'libfoo-dev 1.0\n' >"$suite_root/installed"
}

run_in() {
	local path="$1"
	shift
	env -i PATH="$path" APT_LOG="$suite_root/apt.log" \
		CHOWN_LOG="$suite_root/chown.log" \
		INSTALLED="$suite_root/installed" FAIL_APT="${FAIL_APT:-}" \
		APT_ARCHIVE_DIR="${APT_ARCHIVE_DIR:-}" \
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
require_log 'update -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30'
require_log 'install -y --no-install-recommends -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 libbar-dev libbaz-dev=2.1'
run_verifier libbar-dev 'libbaz-dev=2.1'

# Positive: APT_ARCHIVE_DIR set configures Dir::Cache::Archives, keeps debs, creates partial, and chowns.
reset_state
fake_cache="$suite_root/fake-cache"
APT_ARCHIVE_DIR="$fake_cache" run_verifier --install libbar-dev >/dev/null
require_log 'update -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30'
require_log "install -y --no-install-recommends -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 -o Dir::Cache::Archives=$fake_cache -o APT::Keep-Downloaded-Packages=true libbar-dev"
if grep '^update ' "$suite_root/apt.log" | grep -E -q 'Dir::Cache::Archives|APT::Keep-Downloaded-Packages'; then
	printf 'apt-get update unexpectedly received archive options: %s\n' "$(cat "$suite_root/apt.log")" >&2
	exit 1
fi
if [[ ! -d "$fake_cache/partial" ]]; then
	printf 'APT_ARCHIVE_DIR/partial was not created: %s\n' "$fake_cache/partial" >&2
	exit 1
fi
expected_chown="-R $(id -u):$(id -g) $fake_cache"
if ! grep -Fxq -- "$expected_chown" "$suite_root/chown.log"; then
	printf 'chown was not called as: %s\nlog:\n%s\n' "$expected_chown" "$(cat "$suite_root/chown.log")" >&2
	exit 1
fi

# Positive: APT_ARCHIVE_DIR unset passes neither option and performs no chown.
reset_state
APT_ARCHIVE_DIR="" run_verifier --install libbar-dev >/dev/null
require_log 'update -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30'
require_log 'install -y --no-install-recommends -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 libbar-dev'
if grep -E -q 'Dir::Cache::Archives|APT::Keep-Downloaded-Packages' "$suite_root/apt.log"; then
	printf 'apt-get received archive options when APT_ARCHIVE_DIR was unset\n' >&2
	exit 1
fi
if [[ -s "$suite_root/chown.log" ]]; then
	printf 'chown ran although APT_ARCHIVE_DIR was unset: %s\n' "$(cat "$suite_root/chown.log")" >&2
	exit 1
fi

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
require_log 'install -y --no-install-recommends -o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30 libudev-dev libopenal-dev libvorbis-dev libgl-dev libx11-dev libxrandr-dev libxxf86vm-dev libxi-dev libxcursor-dev libxinerama-dev'

check_action_wiring() {
	local actions="$1"
	local action_file="$actions/install-cgo-libs/action.yml"
	if [[ ! -f "$action_file" ]]; then
		printf 'install-cgo-libs action manifest missing: %s\n' "$action_file" >&2
		return 1
	fi
	if ! grep -Fq 'run: bash scripts/ci/install-cgo-libs.sh' "$action_file"; then
		printf 'install-cgo-libs action must run install-cgo-libs.sh\n' >&2
		return 1
	fi
	if ! grep -Fq 'APT_ARCHIVE_DIR:' "$action_file"; then
		printf 'install-cgo-libs action must set APT_ARCHIVE_DIR\n' >&2
		return 1
	fi
	if ! grep -Fq 'uses: actions/cache/restore@' "$action_file"; then
		printf 'install-cgo-libs action must restore apt cache\n' >&2
		return 1
	fi
}

# check_wiring proves the workflows install cgo headers and system packages
# through these scripts, never inline and never from the caller's workspace.
check_wiring() {
	local workflows="$1"
	local actions="${2:-$repo_root/.github/actions}"
	local name
	for name in ci portability release; do
		if ! grep -Fq 'uses: ./.github/actions/install-cgo-libs' "$workflows/$name.yml"; then
			printf '%s.yml does not install cgo headers through ./.github/actions/install-cgo-libs\n' "$name" >&2
			return 1
		fi
	done
	if [[ "$(grep -Fc 'uses: ./.github/actions/install-cgo-libs' "$workflows/ci.yml")" -ne 3 ]]; then
		printf 'ci.yml must install cgo headers in the module sweep, apidiff and fault scan jobs\n' >&2
		return 1
	fi
	if [[ "$(grep -Fc 'uses: ./.github/actions/install-cgo-libs' "$workflows/portability.yml")" -ne 1 ]]; then
		printf 'portability.yml must install cgo headers in the Linux job\n' >&2
		return 1
	fi
	if [[ "$(grep -Fc 'uses: ./.github/actions/install-cgo-libs' "$workflows/release.yml")" -ne 1 ]]; then
		printf 'release.yml must install cgo headers in the apidiff job\n' >&2
		return 1
	fi
	check_action_wiring "$actions" || return 1
	# praetor-api.yml is Praetor's rendering of .standards.yaml api.system_packages,
	# byte-locked by its audit; check_api_packages keeps that list equal to ours.
	if grep -rHn --exclude=praetor-api.yml 'libudev-dev' "$workflows"; then
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

check_wiring "$repo_root/.github/workflows" "$repo_root/.github/actions"

# check_api_packages proves .standards.yaml api.system_packages, which Praetor
# renders into praetor-api.yml, lists exactly the packages install-cgo-libs.sh installs.
check_api_packages() {
	local standards="$1" installer="$2" declared installed
	declared="$(awk '/^api:/{a=1;next} a&&/^  system_packages:/{s=1;next} s&&/^    - /{print $2;next} s{exit}' "$standards" | sort)"
	installed="$(grep -oE 'lib[a-z0-9+.-]+-dev' "$installer" | sort)"
	if [[ -z "$declared" || "$declared" != "$installed" ]]; then
		printf '.standards.yaml api.system_packages differs from install-cgo-libs.sh\n' >&2
		printf 'declared:\n%s\ninstalled:\n%s\n' "$declared" "$installed" >&2
		return 1
	fi
}

check_api_packages "$repo_root/.standards.yaml" "$cgo_installer"
expect_api_packages_rejection() {
	local name="$1" expression="$2"
	sed "$expression" "$repo_root/.standards.yaml" >"$suite_root/$name.yaml"
	if cmp -s "$repo_root/.standards.yaml" "$suite_root/$name.yaml"; then
		printf 'negative control %s did not mutate .standards.yaml\n' "$name" >&2
		exit 1
	fi
	if check_api_packages "$suite_root/$name.yaml" "$cgo_installer" >/dev/null 2>&1; then
		printf 'api package check accepted negative control %s\n' "$name" >&2
		exit 1
	fi
}
expect_api_packages_rejection api-package-missing '/^    - libxinerama-dev$/d'
expect_api_packages_rejection api-package-extra 's/^    - libudev-dev$/    - libudev-dev\n    - libfoo-dev/'
expect_wiring_rejection() {
	local name="$1" file="$2" expression="$3"
	local work_dir="$suite_root/$name"
	mkdir -p "$work_dir/workflows" "$work_dir/actions"
	cp -R "$repo_root/.github/workflows/." "$work_dir/workflows/"
	cp -R "$repo_root/.github/actions/." "$work_dir/actions/"
	local target cmp_src
	if [[ -f "$work_dir/workflows/$file" ]]; then
		target="$work_dir/workflows/$file"
		cmp_src="$repo_root/.github/workflows/$file"
	elif [[ -f "$work_dir/actions/$file" ]]; then
		target="$work_dir/actions/$file"
		cmp_src="$repo_root/.github/actions/$file"
	else
		printf 'negative control %s: file %s not found in workflows or actions\n' "$name" "$file" >&2
		exit 1
	fi
	sed -i "$expression" "$target"
	if cmp -s "$cmp_src" "$target"; then
		printf 'negative control %s did not mutate %s\n' "$name" "$file" >&2
		exit 1
	fi
	if check_wiring "$work_dir/workflows" "$work_dir/actions" >/dev/null 2>&1; then
		printf 'workflow wiring check accepted negative control %s\n' "$name" >&2
		exit 1
	fi
}
expect_wiring_rejection inline-cgo portability.yml \
	's|uses: \./\.github/actions/install-cgo-libs|run: sudo apt-get install -y libudev-dev|'
expect_wiring_rejection sweep-without-cgo ci.yml \
	'0,\|uses: \./\.github/actions/install-cgo-libs|s||run: "true"|'
# shellcheck disable=SC2016 # Plant the literal caller-workspace path.
expect_wiring_rejection caller-script ci-go.yml \
	's|bash "$ci_root/scripts/ci/verify-system-packages.sh"|bash "${GITHUB_WORKSPACE}/scripts/ci/verify-system-packages.sh"|'
# shellcheck disable=SC2016 # Plant against the literal runtime variable.
expect_wiring_rejection install-ungated ci-go.yml \
	's|if \[\[ "$INSTALL_SYSTEM_PACKAGES" == true \]\]; then|if true; then|'
expect_wiring_rejection action-without-restore install-cgo-libs/action.yml \
	'/uses: actions\/cache\/restore@/d'
expect_wiring_rejection action-without-archive-dir install-cgo-libs/action.yml \
	'/APT_ARCHIVE_DIR:/d'

printf 'system package install and verify policy tests passed\n'
