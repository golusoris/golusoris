#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Rebuilds the embedded API-docs bundle from the package registry, or checks its age.
#
#   scalar-bundle-update.sh regenerate  rebuild the bundle for the version bumped in
#                                       apidocs/embed/SCALAR_VERSION, verify the tarball
#                                       against the registry's sha512, and rewrite
#                                       scalar.js, SCALAR_RELEASED and docs/upstream pins
#   scalar-bundle-update.sh age-check   fail when the embedded release is older than
#                                       max_age_days, which means updates have stalled

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
# Renovate proposes a release once it is min_age_days old. Upstream's longest gap between
# two releases in the 24 months to 2026-10 was 20 days, so an embedded release older than
# 20 + 3 (age rule) + 14 (Renovate and merge-train slack) + 7 (landing the pull request)
# = 44 days means the update path produced nothing.
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
		fail "embedded release of $released is $age_days days old, bound $max_age_days: the Renovate update path produced nothing; check the @scalar/api-reference update pull request"
	fi
	printf 'scalar bundle: embedded release of %s is %d days old (bound %d)\n' "$released" "$age_days" "$max_age_days"
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

regenerate() {
	local version raw_prev previous work meta pub_iso has_version cutoff pub_epoch released
	[[ -f "$embed_dir/SCALAR_VERSION" ]] || fail 'apidocs/embed/SCALAR_VERSION is missing'
	version="$(tr -d '\r\n' <"$embed_dir/SCALAR_VERSION")"
	[[ -n "$version" ]] || fail 'apidocs/embed/SCALAR_VERSION is empty'

	[[ -f "$root/docs/upstream/README.md" ]] || fail 'docs/upstream/README.md is missing'
	raw_prev="$(awk -F '|' -v pkg="\`$package\`" '$2 ~ pkg { gsub(/[ `]/, "", $3); print $3 }' "$root/docs/upstream/README.md")"
	previous="${raw_prev#v}"
	[[ -n "$previous" ]] || fail 'could not determine previous version from docs/upstream/README.md'

	if [[ -f "$embed_dir/scalar.js" ]] && grep -Fq "$package $version" "$embed_dir/scalar.js" && [[ "$previous" == "$version" ]]; then
		printf 'scalar bundle: %s is current\n' "$version"
		exit 0
	fi

	work="$(mktemp -d "${TMPDIR:-/tmp}/scalar-bundle.XXXXXX")"
	# shellcheck disable=SC2064 # expand now: the directory name is fixed here.
	trap "rm -rf '$work'" EXIT
	meta="$work/meta.json"
	curl -fsS --max-time 60 "$registry/@scalar%2Fapi-reference" -o "$meta"

	pub_iso="$(jq -r --arg v "$version" '.time[$v] // ""' "$meta")"
	has_version="$(jq -r --arg v "$version" '.versions[$v] != null' "$meta")"
	if [[ "$has_version" != "true" || -z "$pub_iso" ]]; then
		fail "version $version is not in the registry"
	fi

	cutoff=$((now_epoch - min_age_days * day_seconds))
	pub_epoch="$(jq -rn --arg t "$pub_iso" '($t | sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601)')"
	if ((pub_epoch > cutoff)); then
		fail "release of $version is younger than $min_age_days days"
	fi

	fetch_bundle "$meta" "$version" "$work"
	released="$(jq -r --arg v "$version" '.time[$v][0:10]' "$meta")"
	write_pin "$previous" "$version" "$released" "$work"
	printf 'scalar bundle: updated %s -> %s (released %s)\n' "$previous" "$version" "$released"
}

case "${1:-regenerate}" in
regenerate) regenerate ;;
age-check) age_check ;;
*) fail "unknown command: $1" ;;
esac
