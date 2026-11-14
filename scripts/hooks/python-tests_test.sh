#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init golusoris-python-hook

assert_python_failure() {
	local hook="$1" path="$2" output rc=0
	output="$(bash "$hook" --pushed "$path" 2>&1)" || rc=$?
	if (( rc != 23 )); then
		printf 'Python failure swallowed: expected exit 23, got %s (%s)\n' "$rc" "$output" >&2
		return 1
	fi
	[[ "$output" == *'planted Python policy failure'* ]]
}

# Fixtures cannot inherit hook repository variables or user Git configuration.
for git_var in "${!GIT_@}"; do unset "$git_var"; done
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

write_fixture() {
	local root="$1"
	mkdir -p "$root/scripts/hooks" "$root/scripts/ci" "$root/.config/lefthook/scripts"
	cp "$repo_root/scripts/hooks/python-tests.sh" "$root/scripts/hooks/python-tests.sh"
	cat >"$root/scripts/ci/python-tests.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$#" -ge 2 && "$1" == --root ]] || exit 97
[[ ! -v CI_SCOPE_BASE ]] || { printf 'runner inherited CI_SCOPE_BASE\n' >&2; exit 98; }
root="$2"
printf '%s\n' "$@" >"$root/runner.args"
printf 'runner invoked\n' >"$root/runner.called"
shift 2
(( $# <= 128 )) || exit 97
while (( $# > 0 )); do
	[[ "$#" -ge 2 && "$1" == --suite ]] || exit 97
	shift 2
done
python3 -B "$root/scripts/ci/failing_test.py"
SH
	cat >"$root/scripts/ci/failing_test.py" <<'PYF'
import sys
print("planted Python policy failure", file=sys.stderr)
raise SystemExit(23)
PYF
	printf '# fixture\n' >"$root/scripts/ci/new_thing_test.py"
	printf '# fixture\n' >"$root/scripts/ci/helper.py"
	printf '# fixture\n' >"$root/.config/lefthook/scripts/test_added.py"
	git -C "$root" init -q
	git -C "$root" add .
	git -C "$root" -c user.name=policy -c user.email=policy@example.invalid -c commit.gpgsign=false \
		commit -q -m fixture
}

assert_suite_arguments() {
	local hook="$1" root="$2" path="$3" expected="$4"
	assert_python_failure "$hook" "$path" || return 1
	if [[ "$(cat "$root/runner.args")" != "$expected" ]]; then
		printf 'pushed suite arguments mismatch\n' >&2
		return 1
	fi
}

assert_dirty_refused() {
	local hook="$1" root="$2" output rc=0
	rm -f "$root/runner.called"
	output="$(bash "$hook" --pushed scripts/ci/helper.py scripts/ci/new_thing_test.py 2>&1)" || rc=$?
	if [[ "$rc" != 1 || "$output" != *'python-tests: scripts/ci/new_thing_test.py has uncommitted changes; commit them before pushing'* || -e "$root/runner.called" ]]; then
		printf 'dirty pushed Python file was not refused (rc %s): %s\n' "$rc" "$output" >&2
		return 1
	fi
}

# Each mutation removes one defence while leaving the others in place.
write_mutants() {
	python3 - "$1/scripts/hooks/python-tests.sh" "$1/scripts/hooks" <<'PYM'
from pathlib import Path
import sys

source = Path(sys.argv[1]).read_text()
mutations = {
    "bypass": ('env -u CI_SCOPE_BASE bash "$root/scripts/ci/python-tests.sh" --root "$root" "${suites[@]}" || return $?', 'true'),
    "scope": ('env -u CI_SCOPE_BASE bash', 'bash'),
    "suites": ('"${suites[@]}" || return $?', '|| return $?'),
    "dirty": ('if ! git -C "$root" diff --quiet HEAD -- "$path"; then', 'if ! true; then'),
}
for name, (marker, replacement) in mutations.items():
    if source.count(marker) != 1:
        raise SystemExit(f"{name} mutation requires exactly one marker")
    (Path(sys.argv[2]) / f"{name}.sh").write_text(source.replace(marker, replacement))
PYM
}

main() {
	local root="$suite_root/repo" output path expected
	write_fixture "$root"
	export CI_SCOPE_BASE=deadbeef
	for path in scripts/ci/failing_test.py .config/lefthook/scripts/failing_test.py 'scripts\ci\failing_test.py'; do
		assert_python_failure "$root/scripts/hooks/python-tests.sh" "$path"
	done
	expected="$(printf '%s\n' --root "$root" --suite scripts/ci/new_thing_test.py)"
	assert_suite_arguments "$root/scripts/hooks/python-tests.sh" "$root" 'scripts\ci\new_thing_test.py' "$expected"
	expected="$(printf '%s\n' --root "$root" --suite .config/lefthook/scripts/test_added.py)"
	assert_suite_arguments "$root/scripts/hooks/python-tests.sh" "$root" .config/lefthook/scripts/test_added.py "$expected"
	expected="$(printf '%s\n' --root "$root")"
	assert_suite_arguments "$root/scripts/hooks/python-tests.sh" "$root" scripts/ci/helper.py "$expected"
	rm "$root/runner.called"
	for path in README.md scripts/hooks/tool.py; do
		output="$(bash "$root/scripts/hooks/python-tests.sh" --pushed "$path")"
		[[ "$output" == 'python-tests: no Python policy files pushed, skipping' ]]
		[[ ! -e "$root/runner.called" ]]
	done
	output="$(bash "$root/scripts/hooks/python-tests.sh" --pushed)"
	[[ "$output" == 'python-tests: no Python policy files pushed, skipping' ]]
	[[ ! -e "$root/runner.called" ]]
	grep -Fq '      run: bash scripts/hooks/python-tests.sh --pushed {push_files}' "$repo_root/lefthook.yml"
	write_mutants "$root"
	expect_failure_matching 'Python failure swallowed: expected exit 23, got 0' \
		assert_python_failure "$root/scripts/hooks/bypass.sh" scripts/ci/failing_test.py
	expect_failure_matching 'runner inherited CI_SCOPE_BASE' \
		assert_python_failure "$root/scripts/hooks/scope.sh" scripts/ci/failing_test.py
	expected="$(printf '%s\n' --root "$root" --suite scripts/ci/new_thing_test.py)"
	expect_failure_matching 'pushed suite arguments mismatch' \
		assert_suite_arguments "$root/scripts/hooks/suites.sh" "$root" scripts/ci/new_thing_test.py "$expected"
	printf '# dirty\n' >>"$root/scripts/ci/new_thing_test.py"
	assert_dirty_refused "$root/scripts/hooks/python-tests.sh" "$root"
	expect_failure_matching 'dirty pushed Python file was not refused' \
		assert_dirty_refused "$root/scripts/hooks/dirty.sh" "$root"
	git -C "$root" add scripts/ci/new_thing_test.py
	assert_dirty_refused "$root/scripts/hooks/python-tests.sh" "$root"
	assert_python_failure "$root/scripts/hooks/python-tests.sh" scripts/ci/helper.py
	printf 'Python hook: scope isolation, suites, dirty files, failure propagation, skip, mutations passed\n'
}

main "$@"
