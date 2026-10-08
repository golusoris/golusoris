#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-reuse-policy.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

expect_failure() {
	local expected="$1"
	shift
	local output
	if output="$("$@" 2>&1)"; then
		printf 'expected failure: %s\n' "$*" >&2
		exit 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing diagnostic %q in: %s\n' "$expected" "$output" >&2
		exit 1
	fi
}

fixture="$suite_root/fixture"
mkdir -p "$fixture/LICENSES"
: >"$fixture/REUSE.toml"

fake="$suite_root/docker"
cat >"$fake" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >"${FAKE_LOG:?}"
if [[ "${FAKE_FAIL:-false}" == true ]]; then
	printf 'synthetic REUSE failure\n' >&2
	exit 9
fi
EOF
chmod +x "$fake"

fake_log="$suite_root/fake.log"
FAKE_LOG="$fake_log" DOCKER_BIN="$fake" \
	bash "$repo_root/scripts/ci/reuse-lint.sh" --root "$fixture"
for required in \
	'--network none' \
	'--read-only' \
	'fsfe/reuse:6.2.0@sha256:85462a75c0f8efda09ddd190b92816b70e7662577c8427429e11e1b9f25a992e' \
	'lint'; do
	if ! grep -Fq -- "$required" "$fake_log"; then
		printf 'REUSE container contract lacks: %s\n' "$required" >&2
		exit 1
	fi
done

expect_failure 'synthetic REUSE failure' \
	env FAKE_LOG="$fake_log" FAKE_FAIL=true DOCKER_BIN="$fake" \
	bash "$repo_root/scripts/ci/reuse-lint.sh" --root "$fixture"

missing="$suite_root/missing"
mkdir -p "$missing"
expect_failure 'REUSE lint requires REUSE.toml and LICENSES/' \
	env FAKE_LOG="$fake_log" DOCKER_BIN="$fake" \
	bash "$repo_root/scripts/ci/reuse-lint.sh" --root "$missing"

printf 'REUSE immutable-image and failure policy tests passed\n'
