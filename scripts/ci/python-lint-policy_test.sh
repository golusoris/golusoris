#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail
# Fixture repositories lack the pull request base commit; run them unscoped.
unset CI_SCOPE_BASE

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-python-lint-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools"
	install -m 0755 "$repo_root/scripts/ci/python-lint.sh" "$root/scripts/ci/python-lint.sh"
	install -m 0755 "$repo_root/scripts/ci/ci_scope.py" "$root/scripts/ci/ci_scope.py"
	install -m 0644 "$repo_root/tools/tool-versions.env" "$root/tools/tool-versions.env"
	install -m 0644 "$repo_root/ruff.toml" "$root/ruff.toml"
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

clean_root="$(new_repo clean)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/python/negative/clean-module.py" \
	"$clean_root/clean.py"
git -C "$clean_root" add .
bash "$clean_root/scripts/ci/python-lint.sh" --root "$clean_root" >/dev/null

positive_root="$(new_repo positive)"
install -m 0644 "$repo_root/.config/hiss/testdata/HISS-10/python/positive/unused-import.py" \
	"$positive_root/bad.py"
git -C "$positive_root" add .
expect_failure 'F401' bash "$positive_root/scripts/ci/python-lint.sh" --root "$positive_root"

suppression_root="$(new_repo suppression)"
install -m 0644 \
	"$repo_root/.config/hiss/testdata/HISS-10/python/positive/unexplained-noqa.py" \
	"$suppression_root/bad.py"
git -C "$suppression_root" add .
expect_failure 'Ruff noqa needs specific codes and an inline WHY: bad.py:7' \
	bash "$suppression_root/scripts/ci/python-lint.sh" --root "$suppression_root"

explained_root="$(new_repo explained)"
printf '%s\n' 'import json  # noqa: F401 - imported only to exercise policy.' \
	>"$explained_root/clean.py"
git -C "$explained_root" add .
bash "$explained_root/scripts/ci/python-lint.sh" --root "$explained_root" >/dev/null

empty_reason_root="$(new_repo empty-reason)"
printf '%s\n' 'import json  # noqa: F401 -   ' >"$empty_reason_root/bad.py"
git -C "$empty_reason_root" add .
expect_failure 'Ruff noqa needs specific codes and an inline WHY: bad.py:1' \
	bash "$empty_reason_root/scripts/ci/python-lint.sh" --root "$empty_reason_root"

file_level_root="$(new_repo file-level)"
printf '%s\n' '# ruff: noqa: F401' 'import json' >"$file_level_root/bad.py"
git -C "$file_level_root" add .
expect_failure 'Ruff file-level noqa is forbidden' \
	bash "$file_level_root/scripts/ci/python-lint.sh" --root "$file_level_root"

untracked_root="$(new_repo untracked)"
printf 'value = 1\n' >"$untracked_root/clean.py"
git -C "$untracked_root" add .
printf 'import json\n' >"$untracked_root/untracked.py"
expect_failure 'untracked.py' bash "$untracked_root/scripts/ci/python-lint.sh" --root "$untracked_root"

fixture_root="$(new_repo fixtures)"
printf 'value = 1\n' >"$fixture_root/clean.py"
mkdir -p "$fixture_root/.config/hiss/testdata/HISS-10/python/positive"
printf 'import json\n' >"$fixture_root/.config/hiss/testdata/HISS-10/python/positive/bad.py"
git -C "$fixture_root" add .
bash "$fixture_root/scripts/ci/python-lint.sh" --root "$fixture_root" >/dev/null

if grep -Eq -- '--(select|extend-select|ignore|isolated)\b' "$repo_root/scripts/ci/python-lint.sh"; then
	printf 'python-lint.sh must take Ruff rules only from ruff.toml\n' >&2
	exit 1
fi

untrusted_call='import subprocess\n\n\ndef run(cmd: str) -> None:\n    subprocess.run([cmd], check=False)\n'
rule_source_root="$(new_repo rule-source)"
# shellcheck disable=SC2059 # The format string is the fixture body.
printf "$untrusted_call" >"$rule_source_root/bad.py"
git -C "$rule_source_root" add .
expect_failure 'S603' bash "$rule_source_root/scripts/ci/python-lint.sh" --root "$rule_source_root"

nested_root="$(new_repo nested-config)"
mkdir -p "$nested_root/sub"
printf '[lint]\nselect = ["E"]\n' >"$nested_root/sub/ruff.toml"
# shellcheck disable=SC2059 # The format string is the fixture body.
printf "$untrusted_call" >"$nested_root/sub/bad.py"
git -C "$nested_root" add .
expect_failure 'S603' bash "$nested_root/scripts/ci/python-lint.sh" --root "$nested_root"

missing_config_root="$(new_repo missing-config)"
rm "$missing_config_root/ruff.toml"
printf 'value = 1\n' >"$missing_config_root/clean.py"
git -C "$missing_config_root" add .
expect_failure 'Ruff rule source missing' \
	bash "$missing_config_root/scripts/ci/python-lint.sh" --root "$missing_config_root"

scoped_root="$(new_repo missing-config-scoped)"
rm "$scoped_root/ruff.toml"
printf 'note\n' >"$scoped_root/README"
git -C "$scoped_root" add .
scoped_tree="$(git -C "$scoped_root" write-tree)"
scoped_base="$(GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.invalid GIT_COMMITTER_NAME=t \
	GIT_COMMITTER_EMAIL=t@example.invalid git -C "$scoped_root" commit-tree -m base "$scoped_tree")"
git -C "$scoped_root" update-ref HEAD "$scoped_base"
expect_failure 'Ruff rule source missing' env CI_SCOPE_BASE="$scoped_base" \
	bash "$scoped_root/scripts/ci/python-lint.sh" --root "$scoped_root"

symlink_root="$(new_repo symlink)"
printf 'value = 1\n' >"$suite_root/outside.py"
ln -s "$suite_root/outside.py" "$symlink_root/external.py"
git -C "$symlink_root" add .
expect_failure 'Ruff refuses symlink input' \
	bash "$symlink_root/scripts/ci/python-lint.sh" --root "$symlink_root"

fake="$suite_root/docker"
fake_log="$suite_root/docker.log"
# The generated fake expands its own argv and log path.
# shellcheck disable=SC2016 # Generated fake expands its argv and log path only when invoked.
printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$*" >"${FAKE_LOG:?}"\nexit 9\n' >"$fake"
chmod +x "$fake"
expect_failure '' env DOCKER_BIN="$fake" FAKE_LOG="$fake_log" \
	bash "$clean_root/scripts/ci/python-lint.sh" --root "$clean_root"
for required in \
	'--network none' \
	'--read-only' \
	'--cap-drop ALL' \
	'ghcr.io/astral-sh/ruff:0.16.8@sha256:ec3c84a063840fc9a678004f4d958ea4e1e79de1ee3f94dc0f0df3a4a3a1f1a7' \
	'--config ruff.toml' \
	'--no-cache'; do
	if ! grep -Fq -- "$required" "$fake_log"; then
		printf 'Ruff container contract lacks: %s\n' "$required" >&2
		exit 1
	fi
done

printf 'Ruff full-tree policy tests passed\n'
