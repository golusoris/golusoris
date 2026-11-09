#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init "golusoris-shellcheck-hook-policy"

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/scripts/hooks" "$root/tools"
	install -m 0755 "$repo_root/scripts/ci/shellcheck.sh" \
		"$root/scripts/ci/shellcheck.sh"
	install -m 0755 "$repo_root/scripts/hooks/shellcheck.sh" \
		"$root/scripts/hooks/shellcheck.sh"
	install -m 0644 "$repo_root/scripts/hooks/lib.sh" \
		"$root/scripts/hooks/lib.sh"
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

run_hook() {
	local target_root="$1"
	shift
	(
		cd "$target_root"
		bash scripts/hooks/shellcheck.sh "$@"
	)
}

# Positive: clean ok.sh passed as arg -> exit 0, output names the pass.
positive_root="$(new_repo positive)"
write_clean_script "$positive_root/ok.sh"
git -C "$positive_root" add .
status=0
out="$(run_hook "$positive_root" --staged ok.sh 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for positive test, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'passed'* ]]; then
	printf 'positive hook run did not name pass: %s\n' "$out" >&2
	exit 1
fi

# Boundary: a Windows backslash path to an existing script is kept, not skipped.
mkdir -p "$positive_root/sub"
write_clean_script "$positive_root/sub/win.sh"
git -C "$positive_root" add .
status=0
out="$(run_hook "$positive_root" --staged 'sub\win.sh' 2>&1)" || status=$?
if (( status != 0 )) || [[ "$out" != *'1 changed shell script(s) passed'* ]]; then
	printf 'backslash path was not kept (exit %d): %s\n' "$status" "$out" >&2
	exit 1
fi

# Planted negative: unsuppressed single-quoted '${COSIGN_VERSION}' literal (SC2016).
planted_root="$(new_repo planted)"
cat >"$planted_root/planted.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '${COSIGN_VERSION}'
EOF
git -C "$planted_root" add .
expect_failure_matching 'SC2016' run_hook "$planted_root" --staged planted.sh

# Suppressed: same line with inline-why suppression above -> exit 0.
suppressed_root="$(new_repo suppressed)"
cat >"$suppressed_root/suppressed.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
# shellcheck disable=SC2016 # Literal text; never expanded.
printf '%s\n' '${COSIGN_VERSION}'
EOF
git -C "$suppressed_root" add .
status=0
out="$(run_hook "$suppressed_root" --staged suppressed.sh 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for suppressed script, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# Boundary: only README.md and main.go -> exit 0, skip: no changed shell scripts.
boundary_root="$(new_repo boundary)"
touch "$boundary_root/README.md" "$boundary_root/main.go"
git -C "$boundary_root" add .
status=0
out="$(run_hook "$boundary_root" --staged README.md main.go 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for non-shell files, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'skip: no changed shell scripts'* ]]; then
	printf 'expected skip: no changed shell scripts, got: %s\n' "$out" >&2
	exit 1
fi

# Boundary: deleted gone.sh arg (never in index) -> exit 0, skip: no changed shell scripts.
status=0
out="$(run_hook "$boundary_root" --staged gone.sh 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for deleted file, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'skip: no changed shell scripts'* ]]; then
	printf 'expected skip: no changed shell scripts for deleted file, got: %s\n' "$out" >&2
	exit 1
fi

# Boundary: .config/hiss/testdata/x.sh only -> exit 0, skip: no changed shell scripts.
mkdir -p "$boundary_root/.config/hiss/testdata"
touch "$boundary_root/.config/hiss/testdata/x.sh"
status=0
out="$(run_hook "$boundary_root" --staged .config/hiss/testdata/x.sh 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for testdata script, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'skip: no changed shell scripts'* ]]; then
	printf 'expected skip: no changed shell scripts for testdata, got: %s\n' "$out" >&2
	exit 1
fi

# Tool absent: PATH with only bash, git, dirname, basename; SHELLCHECK_BIN unset -> exit 0, skip:.
absent_root="$(new_repo tool-absent)"
write_clean_script "$absent_root/clean.sh"
git -C "$absent_root" add .

tool_bin="$suite_root/tool-absent-bin"
mkdir -p "$tool_bin"
for cmd in bash git dirname basename; do
	ln -s "$(command -v "$cmd")" "$tool_bin/$cmd"
done

status=0
out="$(
	(
		unset SHELLCHECK_BIN
		export PATH="$tool_bin"
		run_hook "$absent_root" --staged clean.sh
	) 2>&1
)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 when tool absent, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'skip:'* ]]; then
	printf 'expected skip: in output when tool absent, got: %s\n' "$out" >&2
	exit 1
fi

# Unrelated finding: committed bad script other.sh does not block clean staged ok.sh.
unrelated_root="$(new_repo unrelated)"
cat >"$unrelated_root/other.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '${COSIGN_VERSION}'
EOF
git -C "$unrelated_root" add other.sh
git -C "$unrelated_root" -c user.name=test -c user.email=test@example.com commit -m 'commit bad script' -q
write_clean_script "$unrelated_root/ok.sh"
git -C "$unrelated_root" add ok.sh
status=0
out="$(run_hook "$unrelated_root" --staged ok.sh 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for unrelated finding, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# Extensionless: staged tool shebang script with SC2016 fails matching SC2016.
extensionless_root="$(new_repo extensionless)"
cat >"$extensionless_root/tool" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '${COSIGN_VERSION}'
EOF
chmod +x "$extensionless_root/tool"
git -C "$extensionless_root" add tool
expect_failure_matching 'SC2016' run_hook "$extensionless_root" --staged tool

# Staged divergence: staged plant overwritten with clean worktree copy fails matching unstaged.
staged_div_root="$(new_repo staged-divergence)"
cat >"$staged_div_root/planted.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '${COSIGN_VERSION}'
EOF
git -C "$staged_div_root" add planted.sh
write_clean_script "$staged_div_root/planted.sh"
expect_failure_matching 'unstaged' run_hook "$staged_div_root" --staged planted.sh

# Pushed divergence: committed plant fixed only in worktree copy fails matching uncommitted.
pushed_div_root="$(new_repo pushed-divergence)"
cat >"$pushed_div_root/planted.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' '${COSIGN_VERSION}'
EOF
git -C "$pushed_div_root" add planted.sh
git -C "$pushed_div_root" -c user.name=test -c user.email=test@example.com commit -m 'commit plant' -q
write_clean_script "$pushed_div_root/planted.sh"
expect_failure_matching 'uncommitted' run_hook "$pushed_div_root" --pushed planted.sh

# Staged then removed: staged script deleted from worktree fails naming the file.
removed_root="$(new_repo staged-removed)"
write_clean_script "$removed_root/gone.sh"
git -C "$removed_root" add gone.sh
rm "$removed_root/gone.sh"
expect_failure_matching 'gone.sh' run_hook "$removed_root" --staged gone.sh

# Mode validation: missing mode and invalid mode fail with usage.
expect_failure_matching 'usage:' run_hook "$positive_root"
expect_failure_matching 'usage:' run_hook "$positive_root" --invalid ok.sh

# Bound: 4097 path arguments triggers fallback to repository-wide check.
bound_root="$(new_repo bound)"
write_clean_script "$bound_root/clean.sh"
git -C "$bound_root" add clean.sh
declare -a over_paths=()
for ((i = 1; i <= 4097; i++)); do
	over_paths+=("dummy$i.sh")
done
status=0
out="$(run_hook "$bound_root" --staged "${over_paths[@]}" 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for bound test, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'repository shell scripts passed'* ]]; then
	printf 'expected repository-wide run in output, got: %s\n' "$out" >&2
	exit 1
fi

# check_lefthook_wiring validates shellcheck hook registration in pre-commit and pre-push.
check_lefthook_wiring() {
	local file="$1"
	awk '
		BEGIN {
			section = ""
			job = ""
			pre_commit_ok = 0
			pre_push_ok = 0
			has_error = 0
		}
		/^[a-zA-Z0-9_-]+:/ {
			section = $1
			job = ""
			next
		}
		section == "pre-commit:" && /^    [a-zA-Z0-9_-]+:/ {
			job = $1
			next
		}
		section == "pre-push:" && /^    [a-zA-Z0-9_-]+:/ {
			job = $1
			next
		}
		section == "pre-commit:" && job == "shellcheck:" {
			if (/^[ ]+glob:/) {
				has_error = 1
			}
			if ($0 ~ /^[ ]+run:[ ]+bash scripts\/hooks\/shellcheck\.sh --staged \{staged_files\}$/) {
				pre_commit_ok = 1
			}
		}
		section == "pre-push:" && job == "shellcheck:" {
			if (/^[ ]+glob:/) {
				has_error = 1
			}
			if ($0 ~ /^[ ]+run:[ ]+bash scripts\/hooks\/shellcheck\.sh --pushed \{push_files\}$/) {
				pre_push_ok = 1
			}
		}
		END {
			if (has_error || !pre_commit_ok || !pre_push_ok) {
				exit 1
			}
		}
	' "$file"
}

if ! check_lefthook_wiring "$repo_root/lefthook.yml"; then
	printf 'lefthook.yml must run the shellcheck hook with --staged {staged_files} in pre-commit and --pushed {push_files} in pre-push, without glob\n' >&2
	exit 1
fi

# Negative wiring: job removed
wiring_tmp="$suite_root/lefthook-no-job.yml"
grep -v 'shellcheck:' "$repo_root/lefthook.yml" >"$wiring_tmp"
expect_failure check_lefthook_wiring "$wiring_tmp"

# Negative wiring: glob re-added
wiring_glob_tmp="$suite_root/lefthook-glob.yml"
sed '/bash scripts\/hooks\/shellcheck\.sh --pushed/i \      glob: "*.sh"' \
	"$repo_root/lefthook.yml" >"$wiring_glob_tmp"
expect_failure check_lefthook_wiring "$wiring_glob_tmp"

# Negative wiring: --pushed swapped to --staged
wiring_swap_tmp="$suite_root/lefthook-swap.yml"
sed 's/--pushed {push_files}/--staged {staged_files}/g' \
	"$repo_root/lefthook.yml" >"$wiring_swap_tmp"
expect_failure check_lefthook_wiring "$wiring_swap_tmp"

printf 'ShellCheck hook policy tests passed\n'
