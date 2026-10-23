#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Offline tests for scalar-bundle-update.sh: a fixture registry on disk, a fixture tree, a fixed clock.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly script="$repo_root/scripts/ci/scalar-bundle-update.sh"
fixture_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-scalar-bundle.XXXXXX")"
readonly fixture_dir
trap 'rm -rf "$fixture_dir"' EXIT

epoch() { date -u -d "$1" +%s; }

# make_tarball OUT NAMED_VERSION: a package tarball whose bundle names NAMED_VERSION.
make_tarball() {
	local out="$1" named="$2" stage
	stage="$(mktemp -d "$fixture_dir/stage.XXXXXX")"
	mkdir -p "$stage/package/dist/browser"
	printf '/* @scalar/api-reference %s */\nconsole.log("bundle");\n' "$named" >"$stage/package/dist/browser/standalone.js"
	tar -czf "$out" -C "$stage" package
}

integrity() { printf 'sha512-%s' "$(openssl dgst -sha512 -binary "$1" | base64 | tr -d '\n')"; }

# make_registry DIR BUNDLE_110_NAMES INTEGRITY_110: 1.0.0 old, 1.1.0 from 2026-01-05, 1.2.0 from 2026-01-09, one pre-release.
make_registry() {
	local dir="$1" named="$2" integrity_110="$3"
	# curl decodes %2F in a file URL, so the metadata lives at @scalar/api-reference.
	mkdir -p "$dir/@scalar"
	make_tarball "$dir/1.1.0.tgz" "$named"
	make_tarball "$dir/1.2.0.tgz" 1.2.0
	if [[ -z "$integrity_110" ]]; then
		integrity_110="$(integrity "$dir/1.1.0.tgz")"
	fi
	jq -n --arg dir "file://$dir" --arg i110 "$integrity_110" --arg i120 "$(integrity "$dir/1.2.0.tgz")" '{
		time: {"1.0.0": "2025-12-01T10:00:00.000Z", "1.1.0": "2026-01-05T10:00:00.000Z",
			"1.2.0": "2026-01-09T10:00:00.000Z", "2.0.0-beta.1": "2025-11-01T10:00:00.000Z"},
		versions: {
			"1.0.0": {dist: {tarball: ($dir + "/1.0.0.tgz"), integrity: "sha512-unused"}},
			"1.1.0": {dist: {tarball: ($dir + "/1.1.0.tgz"), integrity: $i110}},
			"1.2.0": {dist: {tarball: ($dir + "/1.2.0.tgz"), integrity: $i120}},
			"2.0.0-beta.1": {dist: {tarball: ($dir + "/beta.tgz"), integrity: "sha512-unused"}}
		}
	}' >"$dir/@scalar/api-reference"
}

# make_tree DIR: a repository tree that embeds 1.0.0.
make_tree() {
	local dir="$1"
	mkdir -p "$dir/apidocs/embed" "$dir/docs/upstream/scalar"
	printf '/* @scalar/api-reference 1.0.0 */\n' >"$dir/apidocs/embed/scalar.js"
	printf '1.0.0\n' >"$dir/apidocs/embed/SCALAR_VERSION"
	printf '2025-12-01\n' >"$dir/apidocs/embed/SCALAR_RELEASED"
	# shellcheck disable=SC2016 # literal Markdown backticks
	printf '| `go.uber.org/fx` | `v1.0.0` | root | [fx/](fx/README.md) |\n| `@scalar/api-reference` | `v1.0.0` | embedded asset | [scalar/](scalar/README.md) |\n' \
		>"$dir/docs/upstream/README.md"
	printf '# Scalar - v1.0.0 snapshot\n\nPinned: **v1.0.0**\nSource: [published package v1.0.0](https://example.test/v/1.0.0)\n' \
		>"$dir/docs/upstream/scalar/README.md"
}

run_script() {
	local tree="$1" registry="$2" now="$3"
	shift 3
	SCALAR_ROOT="$tree" SCALAR_REGISTRY_URL="file://$registry" SCALAR_NOW_EPOCH="$(epoch "$now")" bash "$script" "$@"
}

expect_output() {
	local want="$1" output
	shift
	output="$("$@" 2>&1)" || {
		printf 'scalar bundle test: command failed, want "%s": %s\n' "$want" "$output" >&2
		exit 1
	}
	if [[ "$output" != *"$want"* ]]; then
		printf 'scalar bundle test: got "%s", want "%s"\n' "$output" "$want" >&2
		exit 1
	fi
}

expect_refusal() {
	local want="$1" output
	shift
	if output="$("$@" 2>&1)"; then
		printf 'scalar bundle test: accepted a case it must refuse: %s\n' "$want" >&2
		exit 1
	fi
	if [[ "$output" != *"$want"* ]]; then
		printf 'scalar bundle test: refused for another reason: got "%s", want "%s"\n' "$output" "$want" >&2
		exit 1
	fi
}

expect_file() {
	if [[ "$(tr -d '\r\n' <"$1")" != "$2" ]]; then
		printf 'scalar bundle test: %s holds "%s", want "%s"\n' "$1" "$(tr -d '\r\n' <"$1")" "$2" >&2
		exit 1
	fi
}

good="$fixture_dir/registry-good"
make_registry "$good" 1.1.0 ''

# Positive: bumped version in SCALAR_VERSION rewrites bundle, release date, and docs rows.
tree="$fixture_dir/tree"
make_tree "$tree"
printf '1.1.0\n' >"$tree/apidocs/embed/SCALAR_VERSION"
expect_output 'updated 1.0.0 -> 1.1.0 (released 2026-01-05)' run_script "$tree" "$good" 2026-01-10 regenerate
expect_file "$tree/apidocs/embed/SCALAR_VERSION" 1.1.0
expect_file "$tree/apidocs/embed/SCALAR_RELEASED" 2026-01-05
grep -Fq '@scalar/api-reference 1.1.0' "$tree/apidocs/embed/scalar.js"
# shellcheck disable=SC2016 # literal Markdown backticks
grep -Fq '| `@scalar/api-reference` | `v1.1.0` |' "$tree/docs/upstream/README.md"
# shellcheck disable=SC2016 # literal Markdown backticks
grep -Fq '| `go.uber.org/fx` | `v1.0.0` |' "$tree/docs/upstream/README.md"
if [[ "$(wc -l <"$tree/docs/upstream/README.md")" -ne 2 ]]; then
	printf 'scalar bundle test: the catalogue gained or lost lines\n' >&2
	exit 1
fi
if grep -Fq '1.0.0' "$tree/docs/upstream/scalar/README.md"; then
	printf 'scalar bundle test: snapshot still names the old version\n' >&2
	exit 1
fi

# Boundary: a second run changes nothing (already current).
before="$(cat "$tree/apidocs/embed/scalar.js" "$tree/docs/upstream/README.md" | cksum)"
expect_output '1.1.0 is current' run_script "$tree" "$good" 2026-01-10 regenerate
[[ "$(cat "$tree/apidocs/embed/scalar.js" "$tree/docs/upstream/README.md" | cksum)" == "$before" ]]

# Boundary: exactly three days old accepted.
exact="$fixture_dir/tree-exact"
make_tree "$exact"
printf '1.1.0\n' >"$exact/apidocs/embed/SCALAR_VERSION"
expect_output 'updated 1.0.0 -> 1.1.0 (released 2026-01-05)' run_script "$exact" "$good" '2026-01-08 10:00:00' regenerate
expect_file "$exact/apidocs/embed/SCALAR_VERSION" 1.1.0
expect_file "$exact/apidocs/embed/SCALAR_RELEASED" 2026-01-05
grep -Fq '@scalar/api-reference 1.1.0' "$exact/apidocs/embed/scalar.js"

# Negative: version younger than three days is refused and files are unchanged.
young="$fixture_dir/tree-young"
make_tree "$young"
printf '1.1.0\n' >"$young/apidocs/embed/SCALAR_VERSION"
expect_refusal 'is younger than 3 days' run_script "$young" "$good" 2026-01-06 regenerate
expect_file "$young/apidocs/embed/SCALAR_RELEASED" 2025-12-01
grep -Fq '@scalar/api-reference 1.0.0' "$young/apidocs/embed/scalar.js"

# Negative: version absent from registry is refused and files are unchanged.
absent="$fixture_dir/tree-absent"
make_tree "$absent"
printf '9.9.9\n' >"$absent/apidocs/embed/SCALAR_VERSION"
expect_refusal 'version 9.9.9 is not in the registry' run_script "$absent" "$good" 2026-01-10 regenerate
grep -Fq '@scalar/api-reference 1.0.0' "$absent/apidocs/embed/scalar.js"

# Negative: sha512 mismatch is refused and files are unchanged.
tampered="$fixture_dir/registry-tampered"
make_registry "$tampered" 1.1.0 'sha512-AAAA'
untouched="$fixture_dir/tree-tampered"
make_tree "$untouched"
printf '1.1.0\n' >"$untouched/apidocs/embed/SCALAR_VERSION"
expect_refusal "does not match the registry's sha512" run_script "$untouched" "$tampered" 2026-01-10 regenerate
grep -Fq '@scalar/api-reference 1.0.0' "$untouched/apidocs/embed/scalar.js"

# Negative: a bundle that names another version is refused and files are unchanged.
misnamed="$fixture_dir/registry-misnamed"
make_registry "$misnamed" 9.9.9 ''
unnamed="$fixture_dir/tree-misnamed"
make_tree "$unnamed"
printf '1.1.0\n' >"$unnamed/apidocs/embed/SCALAR_VERSION"
expect_refusal 'does not name its own version' run_script "$unnamed" "$misnamed" 2026-01-10 regenerate
grep -Fq '@scalar/api-reference 1.0.0' "$unnamed/apidocs/embed/scalar.js"
expect_file "$unnamed/apidocs/embed/SCALAR_RELEASED" 2025-12-01

# Age check: inside the bound, past it, and with an unusable date file.
expect_output 'is 5 days old' run_script "$tree" "$good" 2026-01-10 age-check
expect_refusal 'the Renovate update path produced nothing' run_script "$tree" "$good" 2026-02-20 age-check
printf 'soon\n' >"$tree/apidocs/embed/SCALAR_RELEASED"
expect_refusal 'SCALAR_RELEASED is not a date' run_script "$tree" "$good" 2026-01-10 age-check
rm "$tree/apidocs/embed/SCALAR_RELEASED"
expect_refusal 'SCALAR_RELEASED is missing' run_script "$tree" "$good" 2026-01-10 age-check

printf 'Scalar bundle regenerate and age-check tests passed\n'
