#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-semgrep-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools"
	install -m 0755 "$repo_root/scripts/ci/semgrep-scan.sh" \
		"$root/scripts/ci/semgrep-scan.sh"
	install -m 0644 "$repo_root/tools/tool-versions.env" \
		"$root/tools/tool-versions.env"
	install -m 0644 "$repo_root/.semgrep.yml" "$root/.semgrep.yml"
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
	if [[ "$output" == *'unbound variable'* ]]; then
		printf 'Semgrep failure path leaked an unbound cleanup variable\n' >&2
		return 1
	fi
}

write_clean_go() {
	local root="$1"
	printf '%s\n' 'package clean' '' 'func Add(a, b int) int { return a + b }' \
		>"$root/clean.go"
}

clean_root="$(new_repo clean)"
write_clean_go "$clean_root"
git -C "$clean_root" add .
bash "$clean_root/scripts/ci/semgrep-scan.sh" --root "$clean_root" >/dev/null

positive_root="$(new_repo positive)"
cat >"$positive_root/bad.go" <<'EOF'
package bad

import "net/http"

var client = http.DefaultClient
EOF
git -C "$positive_root" add .
expect_failure 'no-http-default-client' \
	bash "$positive_root/scripts/ci/semgrep-scan.sh" --root "$positive_root"

untracked_root="$(new_repo untracked)"
write_clean_go "$untracked_root"
git -C "$untracked_root" add .
cat >"$untracked_root/untracked.go" <<'EOF'
package clean

import "net/http"

var client = http.DefaultClient
EOF
expect_failure 'untracked.go' \
	bash "$untracked_root/scripts/ci/semgrep-scan.sh" --root "$untracked_root"

fixture_root="$(new_repo fixtures)"
write_clean_go "$fixture_root"
mkdir -p "$fixture_root/.config/hiss/testdata/HISS-08/go/positive"
cp "$positive_root/bad.go" \
	"$fixture_root/.config/hiss/testdata/HISS-08/go/positive/bad.go"
git -C "$fixture_root" add .
bash "$fixture_root/scripts/ci/semgrep-scan.sh" --root "$fixture_root" >/dev/null

parse_root="$(new_repo parse-error)"
cat >"$parse_root/broken.go" <<'EOF'
package broken

import "github.com/riverqueue/river"

type Args struct{}

type Broken struct {
	river.WorkerDefaults[Args]
}
EOF
git -C "$parse_root" add .
expect_failure 'Semgrep error:' \
	bash "$parse_root/scripts/ci/semgrep-scan.sh" --root "$parse_root"

symlink_root="$(new_repo symlink)"
mkdir -p "$suite_root/outside"
write_clean_go "$suite_root/outside"
ln -s "$suite_root/outside/clean.go" "$symlink_root/external.go"
git -C "$symlink_root" add .
expect_failure 'Semgrep refuses symlink input' \
	bash "$symlink_root/scripts/ci/semgrep-scan.sh" --root "$symlink_root"

failure_root="$(new_repo scanner-failure)"
write_clean_go "$failure_root"
git -C "$failure_root" add .
cat >"$suite_root/docker-failure" <<'EOF'
#!/usr/bin/env bash
cat >/dev/null
exit 9
EOF
chmod +x "$suite_root/docker-failure"
expect_failure 'Semgrep report is unreadable' env DOCKER_BIN="$suite_root/docker-failure" \
	bash "$failure_root/scripts/ci/semgrep-scan.sh" --root "$failure_root"

printf 'Semgrep full-tree policy tests passed\n'
