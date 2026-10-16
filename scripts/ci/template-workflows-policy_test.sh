#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-template-workflows-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools" "$root/.github/workflows" \
		"$root/template/.github/workflows"
	install -m 0755 "$repo_root/scripts/ci/template-workflows.sh" \
		"$root/scripts/ci/template-workflows.sh"
	install -m 0644 "$repo_root/tools/tool-versions.env" \
		"$root/tools/tool-versions.env"
	install -m 0644 "$repo_root/.github/actionlint.yaml" \
		"$root/.github/actionlint.yaml"
	run_git "$root" init -q
	printf '%s\n' '.workingdir/' >"$root/.gitignore"
	printf '%s\n' "$root"
}

expect_failure() {
	local pattern="$1"
	shift
	local output
	local status=0
	output="$("$@" 2>&1)" || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if [[ "$output" != *"$pattern"* ]]; then
		printf 'missing failure %q in: %s\n' "$pattern" "$output" >&2
		return 1
	fi
}

run_git() {
	local root="$1"
	shift
	local status=0
	git -C "$root" "$@" || status=$?
	if (( status != 0 )); then
		printf 'fixture git invocation failed (%d): git -C %s %s\n' \
			"$status" "$root" "$*" >&2
		return "$status"
	fi
}

write_reuse_workflow() {
	local path="$1"
	cat >"$path" <<'EOF'
name: Reuse
on:
  workflow_call:
    inputs:
      level:
        required: true
        type: string
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: echo "${{ inputs.level }}"
EOF
}

write_no_call_workflow() {
	local path="$1"
	cat >"$path" <<'EOF'
name: Reuse
on:
  push:
    branches: [main]
jobs:
  test:
    runs-on: ubuntu-latest
    timeout-minutes: 5
    steps:
      - run: echo "no workflow call"
EOF
}

write_template_ci() {
	local path="$1"
	local pin="$2"
	local comment="$3"
	local input_key="${4:-level}"
	cat >"$path" <<EOF
name: CI
on:
  push:
    branches: [main]
jobs:
  test:
    uses: golusoris/golusoris/.github/workflows/reuse.yml@${pin} # ${comment}
    with:
      ${input_key}: x
EOF
}

sha_v100=""
sha_v101=""

commit_fixture() {
	local root="$1"
	write_reuse_workflow "$root/.github/workflows/reuse.yml"
	run_git "$root" add .
	run_git "$root" -c user.name=t -c user.email=t@example.invalid commit -m "add reusable workflow" -q
	run_git "$root" -c user.name=t -c user.email=t@example.invalid tag -a v1.0.0 -m "v1.0.0"
	sha_v100="$(run_git "$root" rev-parse HEAD)"

	write_no_call_workflow "$root/.github/workflows/reuse.yml"
	run_git "$root" add .
	run_git "$root" -c user.name=t -c user.email=t@example.invalid commit -m "remove workflow call" -q
	run_git "$root" -c user.name=t -c user.email=t@example.invalid tag -a v1.0.1 -m "v1.0.1"
	sha_v101="$(run_git "$root" rev-parse HEAD)"
}

check_pin_cases() {
	local root="$1"
	local template_ci="$root/template/.github/workflows/ci.yml"

	# Positive case: exact pin, tag match, required input supplied.
	write_template_ci "$template_ci" "$sha_v100" "v1.0.0" "level"
	bash "$root/scripts/ci/template-workflows.sh" --root "$root" >/dev/null

	# Unknown input case: actionlint detects undeclared inputs on workflow_call.
	write_template_ci "$template_ci" "$sha_v100" "v1.0.0" "levle"
	expect_failure 'is not defined' \
		bash "$root/scripts/ci/template-workflows.sh" --root "$root"

	# Missing workflow_call case: actionlint rejects workflows lacking the call trigger.
	write_template_ci "$template_ci" "$sha_v101" "v1.0.1" "level"
	expect_failure 'workflow_call' \
		bash "$root/scripts/ci/template-workflows.sh" --root "$root"

	# Tag mismatch case: comment tag does not resolve to the pinned commit.
	write_template_ci "$template_ci" "$sha_v101" "v1.0.0" "level"
	expect_failure 'resolves to' \
		bash "$root/scripts/ci/template-workflows.sh" --root "$root"

	# Missing commit case: pin sha does not exist in local clone history.
	write_template_ci "$template_ci" "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" "v1.0.0" "level"
	expect_failure 'is not in this clone' \
		bash "$root/scripts/ci/template-workflows.sh" --root "$root"
}

check_selection_cases() {
	local root="$1"
	local template_ci="$root/template/.github/workflows/ci.yml"

	# Unpinned ref case: allow-list regex rejects branch references and non-sha pins.
	cat >"$template_ci" <<'EOF'
name: CI
on:
  push:
    branches: [main]
jobs:
  test:
    uses: golusoris/golusoris/.github/workflows/reuse.yml@main
    with:
      level: x
EOF
	expect_failure 'template/.github/workflows/ci.yml:7' \
		bash "$root/scripts/ci/template-workflows.sh" --root "$root"

	# Unreadable template case: a grep read error must not pass as a file without pins; root reads any mode.
	if (( EUID != 0 )); then
		write_template_ci "$template_ci" "$sha_v100" "v1.0.0" "level"
		chmod 000 "$template_ci"
		expect_failure 'grep failed' \
			bash "$root/scripts/ci/template-workflows.sh" --root "$root"
		chmod 644 "$template_ci"
	fi

	# Empty template case: absence of template workflows triggers fail-closed error.
	rm -f "$template_ci"
	expect_failure 'selected no template workflows' \
		bash "$root/scripts/ci/template-workflows.sh" --root "$root"
}

main() {
	local root
	root="$(new_repo fixture)"
	commit_fixture "$root"
	check_pin_cases "$root"
	check_selection_cases "$root"
	printf 'template-workflows policy tests passed\n'
}

main "$@"
