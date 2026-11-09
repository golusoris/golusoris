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
out="$(run_hook "$positive_root" ok.sh 2>&1)" || status=$?
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
out="$(run_hook "$positive_root" 'sub\win.sh' 2>&1)" || status=$?
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
expect_failure_matching 'SC2016' run_hook "$planted_root" planted.sh

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
out="$(run_hook "$suppressed_root" suppressed.sh 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for suppressed script, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi

# Boundary: only README.md and main.go -> exit 0, skip: no changed shell scripts.
boundary_root="$(new_repo boundary)"
touch "$boundary_root/README.md" "$boundary_root/main.go"
git -C "$boundary_root" add .
status=0
out="$(run_hook "$boundary_root" README.md main.go 2>&1)" || status=$?
if (( status != 0 )); then
	printf 'expected exit 0 for non-shell files, got %d: %s\n' "$status" "$out" >&2
	exit 1
fi
if [[ "$out" != *'skip: no changed shell scripts'* ]]; then
	printf 'expected skip: no changed shell scripts, got: %s\n' "$out" >&2
	exit 1
fi

# Boundary: deleted gone.sh arg -> exit 0, skip: no changed shell scripts.
status=0
out="$(run_hook "$boundary_root" gone.sh 2>&1)" || status=$?
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
out="$(run_hook "$boundary_root" .config/hiss/testdata/x.sh 2>&1)" || status=$?
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
		run_hook "$absent_root" clean.sh
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

printf 'ShellCheck hook policy tests passed\n'
