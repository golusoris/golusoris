#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init "golusoris-semgrep-policy"

new_repo() {
	local name="$1"
	local root="$suite_root/$name"
	mkdir -p "$root/scripts/ci" "$root/tools"
	install -m 0755 "$repo_root/scripts/ci/semgrep-scan.sh" \
		"$root/scripts/ci/semgrep-scan.sh"
	install -m 0644 "$repo_root/scripts/ci/semgrep-report.py" \
		"$root/scripts/ci/semgrep-report.py"
	install -m 0644 "$repo_root/tools/tool-versions.env" \
		"$root/tools/tool-versions.env"
	install -m 0644 "$repo_root/.semgrep.yml" "$root/.semgrep.yml"
	git -C "$root" init -q
	printf '%s\n' '.workingdir/' >"$root/.gitignore"
	printf '%s\n' "$root"
}

# Callers assert failure while rejecting unbound cleanup variables in error paths.
expect_semgrep_failure() {
	expect_failure_matching "$@" || return
	if [[ "$policy_failure_output" == *'unbound variable'* ]]; then
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
expect_semgrep_failure 'no-http-default-client' \
	bash "$positive_root/scripts/ci/semgrep-scan.sh" --root "$positive_root"

untracked_root="$(new_repo untracked)"
write_clean_go "$untracked_root"
git -C "$untracked_root" add .
cat >"$untracked_root/untracked.go" <<'EOF'
package clean

import "net/http"

var client = http.DefaultClient
EOF
expect_semgrep_failure 'untracked.go' \
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

func Unterminated() {
EOF
git -C "$parse_root" add .
expect_semgrep_failure 'Semgrep error:' \
	bash "$parse_root/scripts/ci/semgrep-scan.sh" --root "$parse_root"

write_generic_alias() {
	local path="$1"
	mkdir -p "$(dirname "$path")"
	printf '%s\n' 'package jobs' '' 'type Box[T any] struct{ V T }' '' \
		'type Job[T any] = Box[T]' >"$path"
}

alias_root="$(new_repo generic-alias)"
write_clean_go "$alias_root"
write_generic_alias "$alias_root/jobs/types.go"
git -C "$alias_root" add .
bash "$alias_root/scripts/ci/semgrep-scan.sh" --root "$alias_root" >/dev/null

alias_finding_root="$(new_repo generic-alias-finding)"
write_clean_go "$alias_finding_root"
mkdir -p "$alias_finding_root/jobs"
printf '%s\n' 'package jobs' '' 'import "time"' '' 'type Box[T any] struct{ V T }' '' \
	'type Job[T any] = Box[T]' '' 'func F() { time.Sleep(1) }' >"$alias_finding_root/jobs/types.go"
git -C "$alias_finding_root" add .
expect_semgrep_failure 'no-time-sleep-in-library' \
	bash "$alias_finding_root/scripts/ci/semgrep-scan.sh" --root "$alias_finding_root"

symlink_root="$(new_repo symlink)"
mkdir -p "$suite_root/outside"
write_clean_go "$suite_root/outside"
ln -s "$suite_root/outside/clean.go" "$symlink_root/external.go"
git -C "$symlink_root" add .
expect_semgrep_failure 'Semgrep refuses symlink input' \
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
expect_semgrep_failure 'Semgrep report is unreadable' env DOCKER_BIN="$suite_root/docker-failure" \
	bash "$failure_root/scripts/ci/semgrep-scan.sh" --root "$failure_root"

# xargs splits a long file list over several scanner runs; the report then holds one JSON document per run.
split_root="$(new_repo split-report)"
write_clean_go "$split_root"
printf '%s\n' 'package clean' '' 'func Sub(a, b int) int { return a - b }' >"$split_root/second.go"
git -C "$split_root" add .

# write_fake_docker NAME REPORT...: a docker stand-in that prints the given report documents.
write_fake_docker() {
	local path="$suite_root/$1"
	shift
	{
		printf '%s\n' '#!/usr/bin/env bash' 'cat >/dev/null'
		printf "printf '%%s\\\\n' %q\n" "$@"
	} >"$path"
	chmod +x "$path"
}

# write_fake_docker_exit STATUS NAME REPORT...: a docker stand-in that exits with STATUS.
write_fake_docker_exit() {
	local status="$1"
	local path="$suite_root/$2"
	shift 2
	{
		printf '%s\n' '#!/usr/bin/env bash' 'cat >/dev/null'
		printf "printf '%%s\\\\n' %q\n" "$@"
		printf 'exit %d\n' "$status"
	} >"$path"
	chmod +x "$path"
}

readonly split_first='{"errors":[],"results":[],"paths":{"scanned":["clean.go"]}}'
readonly split_second='{"errors":[],"results":[],"paths":{"scanned":["second.go"]}}'
readonly split_finding='{"errors":[],"results":[{"path":"second.go","check_id":"planted.rule","start":{"line":3},"extra":{"message":"planted"}}],"paths":{"scanned":["second.go"]}}'
readonly split_unscanned='{"errors":[],"results":[],"paths":{"scanned":[]}}'

write_fake_docker docker-split-clean "$split_first" "$split_second"
split_output="$(DOCKER_BIN="$suite_root/docker-split-clean" bash "$split_root/scripts/ci/semgrep-scan.sh" --root "$split_root")"
if [[ "$split_output" != *'2 repository Go files passed'* ]]; then
	printf 'split report was not merged: %s\n' "$split_output" >&2
	exit 1
fi
write_fake_docker docker-split-finding "$split_first" "$split_finding"
expect_semgrep_failure 'second.go:3: planted.rule: planted' env DOCKER_BIN="$suite_root/docker-split-finding" \
	bash "$split_root/scripts/ci/semgrep-scan.sh" --root "$split_root"
write_fake_docker docker-split-unscanned "$split_first" "$split_unscanned"
expect_semgrep_failure 'Semgrep did not scan selected file: second.go' env DOCKER_BIN="$suite_root/docker-split-unscanned" \
	bash "$split_root/scripts/ci/semgrep-scan.sh" --root "$split_root"
write_fake_docker docker-split-garbage "$split_first" 'not json'
expect_semgrep_failure 'Semgrep report is unreadable' env DOCKER_BIN="$suite_root/docker-split-garbage" \
	bash "$split_root/scripts/ci/semgrep-scan.sh" --root "$split_root"

fake_alias_root="$(new_repo fake-alias)"
write_clean_go "$fake_alias_root"
write_generic_alias "$fake_alias_root/jobs/types.go"
git -C "$fake_alias_root" add .

# Negative (fake docker): any PartialParsing error in jobs/types.go -> fails (no tolerance left).
readonly fake_alias_partial_parse='{"errors":[{"code":3,"level":"warn","type":["PartialParsing",[{"path":"jobs/types.go","start":{"line":4}}]],"message":"Syntax error","path":"jobs/types.go","spans":[{"file":"jobs/types.go","start":{"line":4}}]}],"results":[],"paths":{"scanned":["clean.go","jobs/types.go"]}}'
write_fake_docker_exit 123 docker-alias-partial-parse "$fake_alias_partial_parse"
expect_semgrep_failure 'Semgrep error:' env DOCKER_BIN="$suite_root/docker-alias-partial-parse" \
	bash "$fake_alias_root/scripts/ci/semgrep-scan.sh" --root "$fake_alias_root"

# Negative (fake docker): scanner status that is non-zero -> fails.
readonly fake_clean_report='{"errors":[],"results":[],"paths":{"scanned":["clean.go","jobs/types.go"]}}'
write_fake_docker_exit 9 docker-bad-status "$fake_clean_report"
expect_semgrep_failure 'Semgrep scanner failed with exit status 9' env DOCKER_BIN="$suite_root/docker-bad-status" \
	bash "$fake_alias_root/scripts/ci/semgrep-scan.sh" --root "$fake_alias_root"

# Boundary (fake docker): status 123, once tolerated for the alias exception, now fails on a clean report.
write_fake_docker_exit 123 docker-former-tolerated-status "$fake_clean_report"
expect_semgrep_failure 'Semgrep scanner failed with exit status 123' env DOCKER_BIN="$suite_root/docker-former-tolerated-status" \
	bash "$fake_alias_root/scripts/ci/semgrep-scan.sh" --root "$fake_alias_root"

printf 'Semgrep full-tree policy tests passed\n'
