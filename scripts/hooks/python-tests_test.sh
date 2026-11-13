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

main() {
	local root="$suite_root/repo" output path
	mkdir -p "$root/scripts/hooks" "$root/scripts/ci"
	cp "$repo_root/scripts/hooks/python-tests.sh" "$root/scripts/hooks/python-tests.sh"
	cat >"$root/scripts/ci/python-tests.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$#" == 2 && "$1" == --root ]] || exit 97
printf 'runner invoked\n' >"$2/runner.called"
python3 -B "$2/scripts/ci/failing_test.py"
SH
	cat >"$root/scripts/ci/failing_test.py" <<'PY'
import sys
print("planted Python policy failure", file=sys.stderr)
raise SystemExit(23)
PY
	for path in scripts/ci/failing_test.py .config/lefthook/scripts/failing_test.py 'scripts\ci\failing_test.py'; do
		assert_python_failure "$root/scripts/hooks/python-tests.sh" "$path"
	done
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
	# Mutation proves the failure assertion rejects a runner replaced with success.
	python3 - "$root/scripts/hooks/python-tests.sh" "$root/scripts/hooks/mutant.sh" <<'PY'
from pathlib import Path
import sys

source = Path(sys.argv[1]).read_text()
marker = 'bash "$root/scripts/ci/python-tests.sh" --root "$root" || return $?'
if source.count(marker) != 1:
    raise SystemExit("runner mutation requires exactly one call")
Path(sys.argv[2]).write_text(source.replace(marker, "true"))
PY
	expect_failure_matching 'Python failure swallowed: expected exit 23, got 0' \
		assert_python_failure "$root/scripts/hooks/mutant.sh" scripts/ci/failing_test.py
	printf 'Planted runner bypass rejected: %s\n' "$policy_failure_output"
	printf 'Python hook: failure propagation, skip, empty list, planted runner bypass passed\n'
}

main "$@"
