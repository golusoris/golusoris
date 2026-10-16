#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Fails when any short test recovers a nil-dereference fault. On Windows hosts
# with large exception contexts such a recovery corrupts the heap
# (golang/go#81238), so code must check for nil instead of recovering.
# Runs the tests on a copy of GOROOT whose runtime marks every fault panic.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly MARKER=GOLUSORIS_RECOVERED_FAULT
readonly TEST_TIMEOUT="${GO_TEST_TIMEOUT:-10m}"
readonly TEST_PARALLELISM="${GO_TEST_PARALLELISM:-4}"
readonly MAX_MODULES=64

# Makes the directories under $1 writable; a module-cache GOROOT is read-only.
# Files keep their mode: they may be hard links into the source GOROOT.
writable_dirs() {
	[[ -e "$1" ]] || return 0
	find "$1" -type d -exec chmod u+w {} +
}

# Copies GOROOT to $1 with a marked runtime.
mark_goroot() {
	local target="$1" source
	source="$(go env GOROOT)"
	# Hard links when GOROOT shares the file system, a full copy otherwise.
	if ! cp -al "$source" "$target" 2>/dev/null; then
		writable_dirs "$target"
		find "$target" -depth -delete
		cp -a "$source" "$target"
	fi
	writable_dirs "$target"
	local panic_go="$target/src/runtime/panic.go"
	# Replace the hard link before editing so the source GOROOT stays untouched.
	cp --remove-destination "$source/src/runtime/panic.go" "$panic_go"
	mark_panic_go "$panic_go"
}

# Makes panicmem and panicmemAddr in the runtime source file $1 print the marker.
mark_panic_go() {
	python3 -I - "$1" "$MARKER" <<'PY'
import sys
path, marker = sys.argv[1], sys.argv[2]
text = open(path, encoding="utf-8").read()
heads = ("func panicmem() {\n", "func panicmemAddr(addr uintptr) {\n")
for head in heads:
    if text.count(head) != 1:
        sys.exit(f"fault-recovery: runtime/panic.go has no single {head.strip()!r}")
    text = text.replace(head, f'{head}\tprint("{marker}\\n")\n')
open(path, "w", encoding="utf-8").write(text)
PY
}

# Prints "package (seen in test)" for every marker line in the go test -json stream on stdin.
report_faults() {
	python3 -I -c '
import json, sys
marker, hits = sys.argv[1], set()
for line in sys.stdin:
    try:
        event = json.loads(line)
    except ValueError:
        continue
    if event.get("Action") == "output" and marker in event.get("Output", ""):
        hits.add((event.get("Package", "?"), event.get("Test", "<package>")))
for package, test in sorted(hits):
    print(f"{package} (seen in {test})")
' "$MARKER"
}

# Short-tests each module with the marked GOROOT; JSON events go to $1.
run_modules() {
	local events="$1" module count=0 status=0
	local -a modules=()
	mapfile -t modules < <(bash "$SCRIPT_DIR/go-modules.sh" list)
	if ((${#modules[@]} > MAX_MODULES)); then
		printf '::error::%d modules exceed the bound of %d\n' "${#modules[@]}" "$MAX_MODULES" >&2
		return 1
	fi
	for module in "${modules[@]}"; do
		count=$((count + 1))
		printf 'fault-recovery: [%d/%d] %s\n' "$count" "${#modules[@]}" "$module" >&2
		(cd "$REPO_ROOT/$module" && go test -short -count=1 -json \
			-p "$TEST_PARALLELISM" -timeout="$TEST_TIMEOUT" ./... >>"$events") || {
			printf '::error::%s: short tests failed\n' "$module" >&2
			status=1
		}
	done
	return "$status"
}

main() {
	local scratch
	scratch="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-fault-recovery.XXXXXX")"
	# shellcheck disable=SC2064 # expand now: scratch is local to main
	trap "find '$scratch' -type d -exec chmod u+w {} + && find '$scratch' -depth -delete" EXIT
	mark_goroot "$scratch/goroot"
	export GOROOT="$scratch/goroot" GOTOOLCHAIN=local
	export PATH="$GOROOT/bin:$PATH"

	local tests_status=0 faults
	run_modules "$scratch/events.jsonl" || tests_status=$?
	faults="$(report_faults <"$scratch/events.jsonl")"
	if [[ -n "$faults" ]]; then
		printf '::error::tests recovered a nil-dereference fault (golang/go#81238):\n%s\n' "$faults" >&2
		return 1
	fi
	if ((tests_status != 0)); then
		printf '::error::short tests failed under the marked runtime\n' >&2
		return 1
	fi
	printf 'fault-recovery: no recovered fault in %s\n' "$(go version)"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
