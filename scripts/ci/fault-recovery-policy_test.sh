#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly TEST_DIR
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-fault-recovery-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -type d -exec chmod u+w {} + && find "$suite_root" -depth -delete' EXIT

export GOFLAGS='' GOWORK=off GOTOOLCHAIN=local

# shellcheck source=scripts/ci/fault-recovery.sh
source "$TEST_DIR/fault-recovery.sh"

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

fail() {
	printf 'fault-recovery policy: %s\n' "$*" >&2
	exit 1
}

main() {
	make_fixture "$suite_root/fixture"
	mark_goroot "$suite_root/goroot"
	local report
	report="$(scan_fixture "$suite_root/goroot" '^TestFault$')"
	[[ "$report" == *"example.com/fixture (seen in TestFault)"* ]] ||
		fail "recovered fault not reported: ${report:-<empty>}"
	report="$(scan_fixture "$suite_root/goroot" '^TestPlainPanic$')"
	[[ -z "$report" ]] || fail "ordinary panic reported as fault: $report"

	# A runtime without the patched functions must stop the gate, not pass it.
	printf 'package runtime\n' >"$suite_root/bare_panic.go"
	if mark_panic_go "$suite_root/bare_panic.go" 2>/dev/null; then
		fail "mark_panic_go accepted a runtime without panicmem"
	fi

	local ro_goroot="$suite_root/ro-goroot" mode
	local ro_panic="$ro_goroot/src/runtime/panic.go"
	mkdir -p "$ro_goroot/src/runtime"
	printf 'func panicmem() {\n}\nfunc panicmemAddr(addr uintptr) {\n}\n' >"$suite_root/ro-panic.go"
	cp "$suite_root/ro-panic.go" "$ro_panic"
	chmod 0444 "$ro_panic"
	chmod 0555 "$ro_goroot" "$ro_goroot/src" "$ro_goroot/src/runtime"
	if ! (
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

	local scratch_tmp scratch_unset scratch invalid_scratch='' system_tmp=/tmp
	scratch_tmp="$(export TMPDIR=/tmp; make_scratch)"
	scratch_unset="$(unset TMPDIR; make_scratch)"
	for scratch in "$scratch_tmp" "$scratch_unset"; do
		if [[ ! -d "$scratch" ||
			"$scratch" != "$REPO_ROOT/.workingdir/cache/fault-recovery."* ||
			"$scratch" == "$system_tmp/"* ]]; then
			invalid_scratch="$scratch"
		fi
	done
	rmdir "$scratch_tmp" "$scratch_unset"
	[[ -z "$invalid_scratch" ]] || fail "make_scratch used an unsafe directory: $invalid_scratch"
	printf 'fault-recovery policy: ok\n'
}

main "$@"
