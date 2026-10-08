#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-shellcheck-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools"
	install -m 0755 "$repo_root/scripts/ci/shellcheck.sh" \
		"$root/scripts/ci/shellcheck.sh"
	install -m 0644 "$repo_root/tools/tool-versions.env" \
		"$root/tools/tool-versions.env"
	git -C "$root" init -q
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

write_clean_script() {
	local path="$1"
	cat >"$path" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "${1:-clean}"
EOF
}

write_bad_script() {
	local path="$1"
	cat >"$path" <<'EOF'
#!/usr/bin/env bash
name='two words'
printf '%s\n' $name
EOF
}

clean_root="$(new_repo clean)"
write_clean_script "$clean_root/clean.sh"
git -C "$clean_root" add .
bash "$clean_root/scripts/ci/shellcheck.sh" --root "$clean_root" >/dev/null

bare_suppression_root="$(new_repo bare-suppression)"
install -m 0755 \
	"$repo_root/.config/hiss/testdata/HISS-10/shell/positive/unexplained-disable.sh" \
	"$bare_suppression_root/bare.sh"
git -C "$bare_suppression_root" add .
expect_failure 'ShellCheck suppression needs an inline WHY: bare.sh:6' \
	bash "$bare_suppression_root/scripts/ci/shellcheck.sh" --root "$bare_suppression_root"

empty_explanation_root="$(new_repo empty-explanation)"
write_clean_script "$empty_explanation_root/clean.sh"
printf '%s\n' '# shellcheck disable=SC2086 #   ' >>"$empty_explanation_root/clean.sh"
git -C "$empty_explanation_root" add .
expect_failure 'ShellCheck suppression needs an inline WHY: clean.sh:4' \
	bash "$empty_explanation_root/scripts/ci/shellcheck.sh" --root "$empty_explanation_root"

explained_suppression_root="$(new_repo explained-suppression)"
cat >"$explained_suppression_root/clean.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
name='two words'
# shellcheck disable=SC2086 # Deliberate word splitting at this boundary.
printf '%s\n' $name
EOF
git -C "$explained_suppression_root" add .
bash "$explained_suppression_root/scripts/ci/shellcheck.sh" \
	--root "$explained_suppression_root" >/dev/null

workflow_suppression_root="$(new_repo workflow-suppression)"
write_clean_script "$workflow_suppression_root/clean.sh"
mkdir -p "$workflow_suppression_root/.github/workflows"
printf '%s\n' '# shellcheck disable=SC2016' \
	>"$workflow_suppression_root/.github/workflows/check.yml"
git -C "$workflow_suppression_root" add .
expect_failure 'ShellCheck suppression needs an inline WHY: .github/workflows/check.yml:1' \
	bash "$workflow_suppression_root/scripts/ci/shellcheck.sh" --root "$workflow_suppression_root"

positive_root="$(new_repo positive)"
write_bad_script "$positive_root/bad.sh"
git -C "$positive_root" add .
expect_failure 'SC2086' bash "$positive_root/scripts/ci/shellcheck.sh" --root "$positive_root"

untracked_root="$(new_repo untracked)"
write_clean_script "$untracked_root/clean.sh"
git -C "$untracked_root" add .
write_bad_script "$untracked_root/entrypoint"
expect_failure 'entrypoint' bash "$untracked_root/scripts/ci/shellcheck.sh" --root "$untracked_root"

fixture_root="$(new_repo fixtures)"
write_clean_script "$fixture_root/clean.sh"
mkdir -p "$fixture_root/.config/hiss/testdata/HISS-10/shell/positive"
write_bad_script "$fixture_root/.config/hiss/testdata/HISS-10/shell/positive/bad.sh"
git -C "$fixture_root" add .
bash "$fixture_root/scripts/ci/shellcheck.sh" --root "$fixture_root" >/dev/null

binary_root="$(new_repo binary)"
write_clean_script "$binary_root/clean.sh"
printf '\0#!/usr/bin/env bash\0' >"$binary_root/artifact.bin"
git -C "$binary_root" add .
binary_output="$(bash "$binary_root/scripts/ci/shellcheck.sh" --root "$binary_root" 2>&1)"
if [[ "$binary_output" == *'ignored null byte'* ]]; then
	printf 'ShellCheck selector read binary bytes through command substitution\n' >&2
	exit 1
fi

symlink_root="$(new_repo symlink)"
write_clean_script "$suite_root/outside.sh"
ln -s "$suite_root/outside.sh" "$symlink_root/external.sh"
git -C "$symlink_root" add .
expect_failure 'ShellCheck refuses symlink input' \
	bash "$symlink_root/scripts/ci/shellcheck.sh" --root "$symlink_root"

wrong_version_root="$(new_repo wrong-version)"
write_clean_script "$wrong_version_root/clean.sh"
git -C "$wrong_version_root" add .
cat >"$suite_root/shellcheck-wrong" <<'EOF'
#!/usr/bin/env bash
printf 'version: 0.0.0\n'
EOF
chmod +x "$suite_root/shellcheck-wrong"
expect_failure 'ShellCheck version is 0.0.0' env SHELLCHECK_BIN="$suite_root/shellcheck-wrong" \
	bash "$wrong_version_root/scripts/ci/shellcheck.sh" --root "$wrong_version_root"

failure_root="$(new_repo scanner-failure)"
write_clean_script "$failure_root/clean.sh"
git -C "$failure_root" add .
cat >"$suite_root/shellcheck-failure" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == '--version' ]]; then
  printf 'version: 0.11.0\n'
  exit 0
fi
printf 'scanner failure\n' >&2
exit 9
EOF
chmod +x "$suite_root/shellcheck-failure"
expect_failure 'scanner failure' env SHELLCHECK_BIN="$suite_root/shellcheck-failure" \
	bash "$failure_root/scripts/ci/shellcheck.sh" --root "$failure_root"

expect_failure 'SC2086' shellcheck \
	"$repo_root/.config/hiss/testdata/HISS-10/shell/positive/unquoted-expansion.sh"
shellcheck "$repo_root/.config/hiss/testdata/HISS-10/shell/negative/clean-script.sh"

printf 'ShellCheck full-tree policy tests passed\n'
