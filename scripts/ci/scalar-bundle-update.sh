#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Rebuilds the embedded API-docs bundle from the package registry, or checks its age.
#
#   scalar-bundle-update.sh            take the newest stable release that is at least
#                                      min_age_days old, verify the tarball against the
#                                      registry's sha512, and write the bundle, its version,
#                                      its release date and the docs/upstream pin
#   scalar-bundle-update.sh age-check  fail when the embedded release is older than
#                                      max_age_days, which means the scheduled job is silent

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
readonly root="${SCALAR_ROOT:-$repo_root}"
readonly registry="${SCALAR_REGISTRY_URL:-https://registry.npmjs.org}"
readonly package='@scalar/api-reference'
readonly bundle_member='package/dist/browser/standalone.js'
readonly embed_dir="$root/apidocs/embed"
readonly day_seconds=86400
# A release is adopted once it is three days old (the update rule of the fleet).
readonly min_age_days=3
# The job runs weekly. Upstream's longest gap between two releases in the 24 months to
# 2026-10 was 20 days, so an embedded release older than 20 + 3 (age rule) + 14 (two job
# intervals) + 7 (landing the pull request) = 44 days means the job produced nothing.
readonly max_age_days=45

now_epoch="${SCALAR_NOW_EPOCH:-$(date -u +%s)}"
readonly now_epoch

fail() {
	printf 'scalar bundle: %s\n' "$1" >&2
	exit 1
}

age_check() {
	local released released_epoch age_days
	[[ -f "$embed_dir/SCALAR_RELEASED" ]] || fail 'apidocs/embed/SCALAR_RELEASED is missing'
	released="$(tr -d '\r\n' <"$embed_dir/SCALAR_RELEASED")"
	[[ "$released" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || fail "SCALAR_RELEASED is not a date: ${released:-empty}"
	released_epoch="$(date -u -d "$released" +%s)" || fail "SCALAR_RELEASED is not a calendar date: $released"
	age_days=$(((now_epoch - released_epoch) / day_seconds))
	if ((age_days > max_age_days)); then
		fail "embedded release of $released is $age_days days old, bound $max_age_days: the scheduled bundle job produced nothing; run it or scripts/ci/scalar-bundle-update.sh"
	fi
	printf 'scalar bundle: embedded release of %s is %d days old (bound %d)\n' "$released" "$age_days" "$max_age_days"
}

# newest_eligible META: the highest plain x.y.z version published at or before the age cut-off.
newest_eligible() {
	local cutoff=$((now_epoch - min_age_days * day_seconds))
	jq -r --argjson cutoff "$cutoff" '
		.time as $published
		| [.versions | keys[]
			| select(test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))
			| select(($published[.] | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601) <= $cutoff)]
		| sort_by(split(".") | map(tonumber))
		| last // ""' "$1"
}

# is_newer CANDIDATE CURRENT: true when CANDIDATE sorts after CURRENT.
is_newer() {
	[[ "$1" != "$2" ]] && [[ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n 1)" == "$1" ]]
}

fetch_bundle() {
	local meta="$1" version="$2" work="$3" tarball integrity want got
	tarball="$(jq -r --arg v "$version" '.versions[$v].dist.tarball // ""' "$meta")"
	integrity="$(jq -r --arg v "$version" '.versions[$v].dist.integrity // ""' "$meta")"
	[[ -n "$tarball" && "$integrity" == sha512-* ]] || fail "registry names no tarball or sha512 for $version"
	curl -fsS --max-time 300 "$tarball" -o "$work/pkg.tgz"
	want="${integrity#sha512-}"
	got="$(openssl dgst -sha512 -binary "$work/pkg.tgz" | base64 | tr -d '\n')"
	[[ "$got" == "$want" ]] || fail "tarball of $version does not match the registry's sha512"
	tar -xzf "$work/pkg.tgz" -C "$work" "$bundle_member"
	grep -Fq "$package $version" "$work/$bundle_member" || fail "bundle of $version does not name its own version"
}

write_pin() {
	local current="$1" version="$2" released="$3" work="$4"
	cp "$work/$bundle_member" "$embed_dir/scalar.js"
	printf '%s\n' "$version" >"$embed_dir/SCALAR_VERSION"
	printf '%s\n' "$released" >"$embed_dir/SCALAR_RELEASED"
	# The package name holds a slash, so the row address uses another delimiter.
	sed -i "\\#^| \`$package\` |#s#v${current//./\\.}#v$version#" "$root/docs/upstream/README.md"
	sed -i "s/${current//./\\.}/$version/g" "$root/docs/upstream/scalar/README.md"
}

update() {
	local work meta current version released
	work="$(mktemp -d "${TMPDIR:-/tmp}/scalar-bundle.XXXXXX")"
	# shellcheck disable=SC2064 # expand now: the directory name is fixed here.
	trap "rm -rf '$work'" EXIT
	meta="$work/meta.json"
	curl -fsS --max-time 60 "$registry/@scalar%2Fapi-reference" -o "$meta"
	current="$(tr -d '\r\n' <"$embed_dir/SCALAR_VERSION")"
	version="$(newest_eligible "$meta")"
	if [[ -z "$version" ]] || ! is_newer "$version" "$current"; then
		printf 'scalar bundle: %s is current (newest release at least %d days old: %s)\n' \
			"$current" "$min_age_days" "${version:-none}"
		return
	fi
	fetch_bundle "$meta" "$version" "$work"
	released="$(jq -r --arg v "$version" '.time[$v][0:10]' "$meta")"
	write_pin "$current" "$version" "$released" "$work"
	printf 'scalar bundle: updated %s -> %s (released %s)\n' "$current" "$version" "$released"
}

case "${1:-update}" in
update) update ;;
age-check) age_check ;;
*) fail "unknown command: $1" ;;
esac
