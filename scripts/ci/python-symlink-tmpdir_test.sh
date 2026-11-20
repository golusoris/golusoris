#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

# Fixture repositories cannot inherit a push scope or caller Git configuration.
unset CI_SCOPE_BASE
for git_var in "${!GIT_@}"; do unset "$git_var"; done
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

platform="$(uname -s)"
case "$platform" in
	MINGW* | MSYS* | CYGWIN*)
		printf 'python-symlink-tmpdir-test: skip: %s needs symlink privilege; Windows 8.3 short names stay CI-only (Platform Neutrality matrix)\n' "$platform"
		exit 0
		;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init golusoris-symlink-policy
mkdir "$suite_root/tmp"
export TMPDIR="$suite_root/tmp"

# Read required suites from their implementation so fixtures follow future runner additions.
new_repo() {
	local root="$suite_root/$1" script
	mkdir -p "$root/scripts/ci"
	for script in python-symlink-tmpdir.sh python-tests.sh ci_scope.py gomod.py; do
		cp "$repo_root/scripts/ci/$script" "$root/scripts/ci/$script"
	done
	python3 - "$root" <<'PY'
import shlex
import sys
from pathlib import Path

root = Path(sys.argv[1])
source = (root / "scripts/ci/python-tests.sh").read_text()
marker = "local -a tests=(\n"
if source.count(marker) != 1:
    raise SystemExit("required suites need exactly one tests=( block")
block = source.split(marker, 1)[1].split("\n\t)", 1)[0]
suites = shlex.split(block)
if not 0 < len(suites) <= 64:
    raise SystemExit("required suites outside runner bound")
stub = (
    "import unittest\n\n"
    "class PassingTest(unittest.TestCase):\n"
    "    def test_pass(self):\n"
    "        self.assertTrue(True)\n\n"
    "unittest.main()\n"
)
for suite in suites:
    path = root / suite
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(stub)
PY
	git -C "$root" init -q
	printf '%s\n' "$root"
}

# A one-line expectation change isolates the PR 1470 regression from other fixture changes.
write_snapshot() {
	local root="$1" variant="$2"
	python3 - "$repo_root" "$root" "$variant" <<'PY'
import sys
from pathlib import Path

repo, root, variant = sys.argv[1:]
source = (Path(repo) / "scripts/ci/testdata/symlink-tmpdir/snapshot_root_prefix1470.py").read_text()
if variant == "fixed":
    marker = "expected = str(root)  # pre-fix 1470 expectation"
    if source.count(marker) != 1:
        raise SystemExit("fixed fixture requires exactly one pre-fix expectation")
    fixed = source.replace(marker, "expected = str(root.resolve(strict=True))")
    if sum(a != b for a, b in zip(source.splitlines(), fixed.splitlines(), strict=True)) != 1:
        raise SystemExit("fixed fixture must change exactly one line")
    source = fixed
(Path(root) / "scripts/ci/snapshot_root_test.py").write_text(source)
PY
}

# The fixed snapshot and environment assertion must both survive the replay.
assert_positive() {
	local root="$1" output
	output="$(bash "$root/scripts/ci/python-symlink-tmpdir.sh" --root "$root" 2>&1)" || {
		printf '%s\n' "$output" >&2
		return 1
	}
	[[ "$output" == *'suites passed'* ]]
	printf 'fixed 1470 snapshot and symlink TMPDIR: green\n'
}

# A dedicated parent detects cleanup leaks without inspecting unrelated temporary directories.
assert_clean() {
	local -a leftovers
	shopt -s nullglob
	leftovers=("$TMPDIR"/golusoris-symlink-tmpdir.*)
	if (( ${#leftovers[@]} != 0 )); then
		printf 'symlink TMPDIR cleanup leaked: %s\n' "${leftovers[@]}" >&2
		return 1
	fi
}

# Plain execution is a valid control only when its temporary parent has no path alias.
assert_plain_control() {
	local root="$1" output
	if ! python3 - <<'PY'
import os
from pathlib import Path

path = Path(os.environ["TMPDIR"])
raise SystemExit(0 if str(path) == str(path.resolve(strict=True)) else 1)
PY
	then
		printf 'plain 1470 control: skip: TMPDIR resolves to a different path\n'
		return 0
	fi
	output="$(bash "$root/scripts/ci/python-tests.sh" --root "$root" --suite scripts/ci/snapshot_root_test.py 2>&1)" || {
		printf '%s\n' "$output" >&2
		return 1
	}
	[[ "$output" == *'suites passed'* ]]
	printf 'pre-fix 1470 snapshot with plain TMPDIR: green\n'
}

# A copied directory must not masquerade as a symlink on platforms without link privilege.
assert_copy_link_refused() {
	local root stub
	root="$(new_repo copy-link)"
	write_snapshot "$root" planted
	stub="$suite_root/copy-link-stub"
	mkdir "$stub"
	cat >"$stub/ln" <<'SH'
#!/usr/bin/env bash
mkdir "${!#}"
SH
	chmod +x "$stub/ln"
	printf 'copy-link case: checking invalid symlink rejection\n'
	expect_failure_matching 'python-symlink-tmpdir: invalid symlink:' \
		env PATH="$stub:$PATH" bash "$root/scripts/ci/python-symlink-tmpdir.sh" --root "$root"
	assert_clean
}

# Fixture-only runs exercise discovery, regression rejection, and both cleanup paths.
main() {
	local positive negative empty
	positive="$(new_repo positive)"
	write_snapshot "$positive" fixed
	cat >"$positive/scripts/ci/symlink_environment_test.py" <<'PY'
import os
import unittest
from pathlib import Path

class SymlinkEnvironmentTest(unittest.TestCase):
    def test_symlink(self):
        self.assertTrue(Path(os.environ["TMPDIR"]).is_symlink())

if __name__ == "__main__":
    unittest.main()
PY
	assert_positive "$positive"
	assert_clean
	negative="$(new_repo negative)"
	write_snapshot "$negative" planted
	expect_failure_matching 'compare paths against Path.resolve() (macOS /private, Windows 8.3 names)' \
		bash "$negative/scripts/ci/python-symlink-tmpdir.sh" --root "$negative"
	[[ "$policy_failure_output" == *'AssertionError:'* && "$policy_failure_output" == *'snapshot_root_test.py'* ]]
	printf 'pre-fix 1470 snapshot with symlink TMPDIR: red (AssertionError + Path.resolve() hint)\n'
	assert_clean
	assert_plain_control "$negative"
	empty="$(new_repo empty)"
	find "$empty/scripts/ci" -name '*_test.py' -delete
	expect_failure_matching "no scripts/ci/*_test.py suites under $empty" \
		bash "$empty/scripts/ci/python-symlink-tmpdir.sh" --root "$empty"
	assert_clean
	assert_copy_link_refused
	printf 'symlink TMPDIR replay: positive, planted negative, plain control, empty discovery, refused copy-link, cleanup passed\n'
}

main "$@"
