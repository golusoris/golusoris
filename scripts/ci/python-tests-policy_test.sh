#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-python-tests-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/.config/lefthook/scripts"
	install -m 0755 "$repo_root/scripts/ci/python-tests.sh" "$root/scripts/ci/python-tests.sh"
	git -C "$root" init -q
	printf '%s\n' '.workingdir/' >"$root/.gitignore"
	printf '%s\n' "$root"
}

expect_failure() {
	local pattern="$1"
	shift
	local output status=0
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

write_suite() {
	local path="$1"
	local body="$2"
	printf '%s\n' \
		'import unittest' \
		'' \
		'class RequiredTest(unittest.TestCase):' \
		'    def test_contract(self):' \
		"        $body" \
		'' \
		'if __name__ == "__main__":' \
		'    unittest.main()' >"$path"
}

clean_root="$(new_repo clean)"
write_suite "$clean_root/.config/lefthook/scripts/test_checkpoint.py" 'self.assertEqual(2 + 2, 4)'
write_suite "$clean_root/scripts/ci/block_evasion_hook_test.py" 'self.assertTrue(True)'
write_suite "$clean_root/scripts/ci/portability_test.py" 'self.assertTrue(True)'
git -C "$clean_root" add .
bash "$clean_root/scripts/ci/python-tests.sh" --root "$clean_root" >/dev/null

failure_root="$(new_repo failure)"
write_suite "$failure_root/.config/lefthook/scripts/test_checkpoint.py" 'self.assertTrue(False)'
write_suite "$failure_root/scripts/ci/block_evasion_hook_test.py" 'self.assertTrue(True)'
write_suite "$failure_root/scripts/ci/portability_test.py" 'self.assertTrue(True)'
git -C "$failure_root" add .
expect_failure 'FAILED' bash "$failure_root/scripts/ci/python-tests.sh" --root "$failure_root"

missing_root="$(new_repo missing)"
write_suite "$missing_root/.config/lefthook/scripts/test_checkpoint.py" 'self.assertTrue(True)'
git -C "$missing_root" add .
expect_failure 'required Python test is missing' \
	bash "$missing_root/scripts/ci/python-tests.sh" --root "$missing_root"

empty_root="$(new_repo zero-tests)"
printf '%s\n' 'import unittest' 'unittest.main()' \
	>"$empty_root/.config/lefthook/scripts/test_checkpoint.py"
write_suite "$empty_root/scripts/ci/block_evasion_hook_test.py" 'self.assertTrue(True)'
write_suite "$empty_root/scripts/ci/portability_test.py" 'self.assertTrue(True)'
git -C "$empty_root" add .
expect_failure 'NO TESTS RAN' bash "$empty_root/scripts/ci/python-tests.sh" --root "$empty_root"

symlink_root="$(new_repo symlink)"
write_suite "$suite_root/outside_test.py" 'self.assertTrue(True)'
ln -s "$suite_root/outside_test.py" "$symlink_root/.config/lefthook/scripts/test_checkpoint.py"
write_suite "$symlink_root/scripts/ci/block_evasion_hook_test.py" 'self.assertTrue(True)'
write_suite "$symlink_root/scripts/ci/portability_test.py" 'self.assertTrue(True)'
git -C "$symlink_root" add .
expect_failure 'Python tests refuse symlink input' \
	bash "$symlink_root/scripts/ci/python-tests.sh" --root "$symlink_root"

python3 -B "$repo_root/.config/hiss/testdata/HISS-15/python/negative/test_complete_dimensions.py" >/dev/null
python3 -B "$repo_root/.config/hiss/testdata/HISS-15/python/gap/test_positive_only.py" >/dev/null
expect_failure 'FAILED' python3 -B \
	"$repo_root/.config/hiss/testdata/HISS-15/python/positive/test_failing_suite.py"

printf 'Python execution and failure policy tests passed\n'
