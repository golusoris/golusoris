#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TEST_DIR

export GOFLAGS='' GOWORK=off GOTOOLCHAIN=local

# shellcheck source=scripts/ci/fault-recovery.sh
source "$TEST_DIR/fault-recovery.sh"

scratch_dirs=()
empty_dirs=()

# Removes registered artifacts in reverse order, then their newly created empty bases.
cleanup() {
	local index dir
	for ((index = ${#scratch_dirs[@]} - 1; index >= 0; index--)); do
		remove_scratch "${scratch_dirs[index]}"
	done
	for dir in "${empty_dirs[@]}"; do
		[[ ! -d "$dir" ]] || rmdir "$dir"
	done
}
trap cleanup EXIT
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-fault-recovery-policy.XXXXXX")"
readonly suite_root
scratch_dirs+=("$suite_root")

# Module whose tests recover a nil-dereference fault and an ordinary panic.
make_fixture() {
	local dir="$1"
	mkdir -p "$dir"
	printf 'module example.com/fixture\n\ngo 1.24\n' >"$dir/go.mod"
	cat >"$dir/fixture_test.go" <<'GO'
package fixture

import "testing"

type node struct{ next *node }

func recovered(f func()) (ok bool) {
	defer func() { ok = recover() != nil }()
	f()
	return false
}

func TestFault(t *testing.T) {
	var n *node
	if !recovered(func() { _ = n.next }) {
		t.Fatal("nil dereference did not panic")
	}
}

func TestPlainPanic(t *testing.T) {
	if !recovered(func() { panic("plain") }) {
		t.Fatal("panic was not recovered")
	}
}
GO
}

# Runs the fixture tests named by $2 on the marked GOROOT $1 and prints the report.
scan_fixture() {
	local goroot="$1" run="$2"
	(cd "$suite_root/fixture" &&
		GOROOT="$goroot" PATH="$goroot/bin:$PATH" go test -count=1 -json -run "$run" .) |
		report_faults
}

# Reports a broken policy case and exits non-zero.
fail() {
	printf 'fault-recovery policy: %s\n' "$*" >&2
	exit 1
}

# Checks recovered faults and ordinary panics on the marked runtime.
check_fault_report() {
	make_fixture "$suite_root/fixture"
	mark_goroot "$suite_root/goroot"
	local report
	report="$(scan_fixture "$suite_root/goroot" '^TestFault$')"
	[[ "$report" == *"example.com/fixture (seen in TestFault)"* ]] ||
		fail "recovered fault not reported: ${report:-<empty>}"
	report="$(scan_fixture "$suite_root/goroot" '^TestPlainPanic$')"
	[[ -z "$report" ]] || fail "ordinary panic reported as fault: $report"
}

# Checks rejection of a runtime without the patched functions.
check_bare_runtime() {
	# A runtime without the patched functions must stop the gate, not pass it.
	printf 'package runtime\n' >"$suite_root/bare_panic.go"
	if mark_panic_go "$suite_root/bare_panic.go" 2>/dev/null; then
		fail "mark_panic_go accepted a runtime without panicmem"
	fi
}

# Checks copying a read-only GOROOT without changing its source.
check_readonly_goroot() {
	local ro_goroot="$suite_root/ro-goroot" mode
	local ro_panic="$ro_goroot/src/runtime/panic.go"
	mkdir -p "$ro_goroot/src/runtime"
	printf 'func panicmem() {\n}\nfunc panicmemAddr(addr uintptr) {\n}\n' >"$suite_root/ro-panic.go"
	cp "$suite_root/ro-panic.go" "$ro_panic"
	chmod 0444 "$ro_panic"
	chmod 0555 "$ro_goroot" "$ro_goroot/src" "$ro_goroot/src/runtime"
	if ! (
		# Returns only the planted read-only GOROOT for the copy test.
		go() {
			[[ "$#" -eq 2 && "$1" == env && "$2" == GOROOT ]] || return 1
			printf '%s\n' "$ro_goroot"
		}
		mark_goroot "$suite_root/ro-marked"
	); then
		fail "mark_goroot rejected a read-only GOROOT"
	fi
	grep -q "$MARKER" "$suite_root/ro-marked/src/runtime/panic.go" ||
		fail "read-only GOROOT copy missing recovered-fault marker"
	cmp -s "$suite_root/ro-panic.go" "$ro_panic" ||
		fail "mark_goroot changed source panic.go content"
	mode="$(stat -c %a "$ro_panic" 2>/dev/null || stat -f %Lp "$ro_panic")"
	[[ "$mode" == 444 ]] || fail "mark_goroot changed source panic.go mode: $mode"
}

# Checks default scratch bases with TMPDIR set and unset.
check_default_bases() {
	local scratch_tmp scratch_unset scratch physical_scratch system_tmp=/tmp physical_tmp
	physical_tmp="$(cd -P "$system_tmp" && pwd)"
	scratch_tmp="$(export TMPDIR=/tmp; make_scratch "${SCRATCH_BASES[@]}")" ||
		fail "make_scratch failed with TMPDIR=/tmp"
	scratch_dirs+=("$scratch_tmp")
	scratch_unset="$(unset TMPDIR; make_scratch "${SCRATCH_BASES[@]}")" ||
		fail "make_scratch failed with TMPDIR unset"
	scratch_dirs+=("$scratch_unset")
	for scratch in "$scratch_tmp" "$scratch_unset"; do
		[[ -d "$scratch" ]] || fail "make_scratch returned no directory: $scratch"
		physical_scratch="$(cd -P "$scratch" && pwd)"
		[[ "$physical_scratch" != "$physical_tmp" && "$physical_scratch" != "$physical_tmp/"* ]] ||
			fail "make_scratch used an unsafe directory: $scratch"
	done
}

# Checks fallback from a checkout under /tmp to /var/tmp.
check_tmp_checkout() {
	local scratch physical_scratch system_tmp=/tmp
	local checkout="$system_tmp/golusoris-checkout-$$" fallback
	[[ ! -e "$checkout" ]] || fail "checkout fixture already exists: $checkout"
	scratch_dirs+=("$checkout")
	scratch="$(make_scratch "$checkout/.workingdir/cache" /var/tmp)" ||
		fail "make_scratch failed to fall back from a checkout under /tmp"
	scratch_dirs+=("$scratch")
	[[ ! -e "$checkout/.workingdir/cache" ]] || fail "make_scratch created the skipped /tmp base"
	fallback="$(cd -P /var/tmp && pwd)"
	[[ -d "$scratch" ]] || fail "make_scratch fallback returned no directory: $scratch"
	physical_scratch="$(cd -P "$scratch" && pwd)"
	[[ "$physical_scratch" == "$fallback/"fault-recovery.?????? ]] ||
		fail "make_scratch did not use the /var/tmp fallback directly: $scratch"
}

# Checks rejection of a scratch base symlinked into /tmp.
check_symlink_base() {
	local scratch system_tmp=/tmp
	local outside error_file="$suite_root/scratch-error" accepted=0
	outside="$(mktemp -d /var/tmp/golusoris-scratch-policy.XXXXXX)"
	scratch_dirs+=("$outside")
	ln -s "$system_tmp" "$outside/link"
	[[ -e "$outside/link/cache" ]] || empty_dirs+=("$system_tmp/cache")
	if scratch="$(make_scratch "$outside/link/cache" 2>"$error_file")"; then
		accepted=1
		scratch_dirs+=("$scratch")
	fi
	((accepted == 0)) || fail "make_scratch accepted a symlink into /tmp"
	[[ "$(cat "$error_file")" == "fault-recovery: no scratch directory outside /tmp: $outside/link/cache" ]] ||
		fail "make_scratch did not explain the rejected symlink base"
}

# Checks EXIT trap cleanup with an apostrophe in the scratch path.
check_trap_quoting() {
	local quoted_dir="$suite_root/it's a dir"
	mkdir -p "$quoted_dir/read-only"
	printf 'fixture\n' >"$quoted_dir/read-only/file"
	chmod 0555 "$quoted_dir/read-only"
	(
		# shellcheck disable=SC2064 # WHY: expand the local scratch path when registering the trap.
		trap "$(scratch_trap "$quoted_dir")" EXIT
	) || fail "scratch_trap failed for an apostrophe in the path"
	[[ ! -e "$quoted_dir" ]] || fail "scratch_trap left its read-only directory behind"
}

# Runs the policy cases; exits non-zero through fail on the first broken one.
main() {
	check_fault_report
	check_bare_runtime
	check_readonly_goroot
	check_default_bases
	check_tmp_checkout
	check_symlink_base
	check_trap_quoting
	printf 'fault-recovery policy: ok\n'
}

main "$@"
