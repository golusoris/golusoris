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
	printf 'fault-recovery policy: ok\n'
}

main "$@"
