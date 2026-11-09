#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init "golusoris-shellcheck-policy"

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
expect_failure_matching 'ShellCheck suppression needs an inline WHY: bare.sh:6' \
	bash "$bare_suppression_root/scripts/ci/shellcheck.sh" --root "$bare_suppression_root"

empty_explanation_root="$(new_repo empty-explanation)"
write_clean_script "$empty_explanation_root/clean.sh"
printf '%s\n' '# shellcheck disable=SC2086 #   ' >>"$empty_explanation_root/clean.sh"
git -C "$empty_explanation_root" add .
expect_failure_matching 'ShellCheck suppression needs an inline WHY: clean.sh:4' \
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
expect_failure_matching 'ShellCheck suppression needs an inline WHY: .github/workflows/check.yml:1' \
	bash "$workflow_suppression_root/scripts/ci/shellcheck.sh" --root "$workflow_suppression_root"

positive_root="$(new_repo positive)"
write_bad_script "$positive_root/bad.sh"
git -C "$positive_root" add .
expect_failure_matching 'SC2086' bash "$positive_root/scripts/ci/shellcheck.sh" --root "$positive_root"

untracked_root="$(new_repo untracked)"
write_clean_script "$untracked_root/clean.sh"
git -C "$untracked_root" add .
write_bad_script "$untracked_root/entrypoint"
expect_failure_matching 'entrypoint' bash "$untracked_root/scripts/ci/shellcheck.sh" --root "$untracked_root"

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
expect_failure_matching 'ShellCheck refuses symlink input' \
	bash "$symlink_root/scripts/ci/shellcheck.sh" --root "$symlink_root"

wrong_version_root="$(new_repo wrong-version)"
write_clean_script "$wrong_version_root/clean.sh"
git -C "$wrong_version_root" add .
cat >"$suite_root/shellcheck-wrong" <<'EOF'
#!/usr/bin/env bash
printf 'version: 0.0.0\n'
EOF
chmod +x "$suite_root/shellcheck-wrong"
expect_failure_matching 'ShellCheck version is 0.0.0' env SHELLCHECK_BIN="$suite_root/shellcheck-wrong" \
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
expect_failure_matching 'scanner failure' env SHELLCHECK_BIN="$suite_root/shellcheck-failure" \
	bash "$failure_root/scripts/ci/shellcheck.sh" --root "$failure_root"

expect_failure_matching 'SC2086' shellcheck \
	"$repo_root/.config/hiss/testdata/HISS-10/shell/positive/unquoted-expansion.sh"
shellcheck "$repo_root/.config/hiss/testdata/HISS-10/shell/negative/clean-script.sh"

path_mode_root="$(new_repo path-mode)"
write_clean_script "$path_mode_root/clean.sh"
write_bad_script "$path_mode_root/tool"
git -C "$path_mode_root" add .

# Path mode: selects an extensionless shebang script
expect_failure_matching 'SC2086' \
	bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- tool

# Path mode: ignores a deleted path
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- deleted.sh clean.sh 2>&1)" || status=$?
if (( status != 0 )) || [[ "$out" != *'1 changed shell scripts passed'* ]]; then
	printf 'expected exit 0 ignoring deleted path, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# Path mode: excludes .config/hiss/testdata/x.sh
mkdir -p "$path_mode_root/.config/hiss/testdata"
write_bad_script "$path_mode_root/.config/hiss/testdata/x.sh"
git -C "$path_mode_root" add .
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- .config/hiss/testdata/x.sh clean.sh 2>&1)" || status=$?
if (( status != 0 )) || [[ "$out" != *'1 changed shell scripts passed'* ]]; then
	printf 'expected exit 0 excluding testdata, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# Path mode: refuses a symlink
ln -s "$suite_root/outside.sh" "$path_mode_root/symlink.sh"
expect_failure_matching 'ShellCheck refuses symlink input: symlink.sh' \
	bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- symlink.sh

# F: non-shell symlink only exits 0 without refusal
touch "$path_mode_root/README.md"
ln -s README.md "$path_mode_root/notes.md"
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- notes.md 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for non-shell symlink in path mode, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# G: --list with only a script, then with only a workflow file
mkdir -p "$path_mode_root/.github/workflows"
touch "$path_mode_root/.github/workflows/list.yml"
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" --list -- clean.sh 2>&1)" || status=$?
if (( status != 0 )) || [[ "$out" != 'clean.sh' ]]; then
	printf -- 'expected exit 0 and clean.sh from --list, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" --list -- .github/workflows/list.yml 2>&1)" || status=$?
if (( status != 0 )) || [[ "$out" != '.github/workflows/list.yml' ]]; then
	printf -- 'expected exit 0 and .github/workflows/list.yml from --list, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# Path mode: --list prints exactly the selected scripts, then the workflow files
write_clean_script "$path_mode_root/ok_tool"
git -C "$path_mode_root" add ok_tool
mkdir -p "$path_mode_root/.github/workflows"
touch "$path_mode_root/.github/workflows/list.yml"
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" --list -- clean.sh ok_tool README.md deleted.sh .config/hiss/testdata/x.sh .github/workflows/list.yml)"
if [[ "$out" != $'clean.sh\nok_tool\n.github/workflows/list.yml' ]]; then
	printf -- '--list did not print exactly the selected scripts and workflow files: %q\n' "$out" >&2
	exit 1
fi

# Path mode: no shell scripts -> exit 0 with message
touch "$path_mode_root/README.md" "$path_mode_root/main.go"
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- README.md main.go 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for no shell scripts, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != 'ShellCheck: no shell scripts among 2 paths' ]]; then
	printf 'unexpected output for no shell scripts: %s\n' "$out" >&2
	exit 1
fi

# Path mode: max_shell_files + 1 paths -> exit 1
declare -a policy_over_paths=()
for ((i = 1; i <= 4097; i++)); do
	policy_over_paths+=("f$i.sh")
done
expect_failure_matching 'ShellCheck file bound exceeded: 4097 > 4096' \
	bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- "${policy_over_paths[@]}"

# Path mode: --list without -- -> exit 2 with usage
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" --list 2>&1)" || status=$?
if (( status != 2 )); then
	printf 'expected exit 2 for --list without --, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'usage:'* ]]; then
	printf 'expected usage in output for --list without --, got: %s\n' "$out" >&2
	exit 1
fi

# Path mode: a workflow-only path list still gets the suppression WHY check.
mkdir -p "$path_mode_root/.github/workflows"
printf '%s\n' '# shellcheck disable=SC2016' >"$path_mode_root/.github/workflows/check.yml"
expect_failure_matching 'ShellCheck suppression needs an inline WHY: .github/workflows/check.yml:1' \
	bash "$path_mode_root/scripts/ci/shellcheck.sh" --root "$path_mode_root" -- .github/workflows/check.yml

# Options: more than three option words -> exit 2 with usage.
status=0
out="$(bash "$path_mode_root/scripts/ci/shellcheck.sh" --list --list --list --list -- clean.sh 2>&1)" || status=$?
if (( status != 2 )) || [[ "$out" != *'usage:'* ]]; then
	printf 'expected exit 2 with usage for repeated options, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

printf 'ShellCheck full-tree policy tests passed\n'
