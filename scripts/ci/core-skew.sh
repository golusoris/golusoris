#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Builds, vets and short-tests a module with every in-repo replace dropped, so
# the root resolves core at the version go.mod requires, as a consumer does.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly MODULE_DIR="${1:-$SCRIPT_DIR/../..}"
readonly TEST_TIMEOUT="${GO_TEST_TIMEOUT:-10m}"
readonly TEST_PARALLELISM="${GO_TEST_PARALLELISM:-4}"
readonly MAX_REPLACES=32

# Prints the old path of every replace whose target is a directory, not a module version.
local_replaces() {
	go mod edit -json | python3 -I -c '
import json, sys
for entry in json.load(sys.stdin).get("Replace") or []:
    if not entry["New"].get("Version"):
        print(entry["Old"]["Path"])
'
}

# Resolves each dropped path, then builds, vets and short-tests the copy.
check_module() {
	local path resolved
	for path in "$@"; do
		resolved="$(go list -m "$path")" || return 1
		printf 'resolved: %s\n' "$resolved"
	done
	go build ./... || return 1
	go vet ./... || return 1
	go test -short -count=1 -p "$TEST_PARALLELISM" -timeout="$TEST_TIMEOUT" ./... || return 1
}

main() {
	local scratch
	scratch="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-core-skew.XXXXXX")"
	# shellcheck disable=SC2064 # expand now: scratch is local to main
	trap "find '$scratch' -depth -delete" EXIT

	# Tracked and untracked-unignored files: local edits count, build output does not.
	git -C "$MODULE_DIR" ls-files -z --cached --others --exclude-standard |
		(cd "$MODULE_DIR" && tar --null -T - -cf -) |
		tar -xf - -C "$scratch"

	cd "$scratch"
	local -a dropped=()
	local path
	while IFS= read -r path; do
		dropped+=("$path")
	done < <(local_replaces)
	if ((${#dropped[@]} > MAX_REPLACES)); then
		printf '::error::%d local replaces exceed the bound of %d\n' "${#dropped[@]}" "$MAX_REPLACES" >&2
		return 1
	fi
	for path in "${dropped[@]}"; do
		go mod edit -dropreplace="$path"
	done

	export GOWORK=off GOFLAGS=-mod=mod
	printf 'dropped replaces: %s\n' "${dropped[*]:-none}"
	printf 'GOPROXY=%s\n' "$(go env GOPROXY)"
	if ! check_module "${dropped[@]}"; then
		printf '::error::module does not build or test against the required versions of: %s\n' \
			"${dropped[*]:-none}" >&2
		return 1
	fi
	printf 'module builds and tests against the required versions\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
