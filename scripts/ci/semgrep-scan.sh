#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_go_files=16384

# Exception (one file, all rules): Semgrep's Go parser rejects generic type
# aliases while the ruleset holds a for-range pattern (semgrep/semgrep#11972),
# and jobs/types.go re-exports River's generic types that way. After the expiry
# Semgrep scans the file again and the gate fails until the parser is fixed.
readonly generic_alias_exception=jobs/types.go
readonly generic_alias_exception_expires=20261107
scan_work=''

cleanup() {
	if [[ -n "$scan_work" && -d "$scan_work" ]]; then
		find "$scan_work" -depth -delete
	fi
}

trap cleanup EXIT

usage() {
	printf 'usage: %s [--root PATH]\n' "${0##*/}" >&2
}

resolve_root() {
	local root
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	if [[ $# -eq 0 ]]; then
		printf '%s\n' "$root"
		return
	fi
	if [[ $# -ne 2 || "$1" != --root ]]; then
		usage
		return 2
	fi
	(cd "$2" && pwd)
}

collect_files() {
	local root="$1"
	local path
	while IFS= read -r -d '' path; do
		case "$path" in
			.config/hiss/testdata/* | .config/hiss/semgrep/testdata/* | \
			vendor/* | third_party/*)
				continue
			;;
		esac
		if [[ -L "$root/$path" ]]; then
			printf 'Semgrep refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		# SEMGREP_EXCEPTION_DATE (YYYYMMDD) lets the policy test move past the expiry.
		if [[ "$path" == "$generic_alias_exception" ]] &&
			((${SEMGREP_EXCEPTION_DATE:-$(date -u +%Y%m%d)} <= generic_alias_exception_expires)); then
			printf 'Semgrep skips %s: exception until %s (semgrep/semgrep#11972)\n' \
				"$path" "$generic_alias_exception_expires" >&2
			continue
		fi
		[[ -f "$root/$path" ]] || continue
		files+=("$path")
		if (( ${#files[@]} > max_go_files )); then
			printf 'Semgrep file bound exceeded: %d > %d\n' \
				"${#files[@]}" "$max_go_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard -- '*.go')
}

validate_report() {
	local report="$1"
	local expected="$2"
	python3 - "$report" "$expected" <<'PY'
import json
import pathlib
import sys

report_path, expected_path = map(pathlib.Path, sys.argv[1:])
if not report_path.is_file():
    print("Semgrep did not produce its JSON report", file=sys.stderr)
    raise SystemExit(1)

try:
    payload = json.loads(report_path.read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError) as error:
    print(f"Semgrep report is unreadable: {error}", file=sys.stderr)
    raise SystemExit(1) from error
errors = payload.get("errors", [])
results = payload.get("results", [])
expected = {
    pathlib.Path(raw.decode("utf-8")).as_posix()
    for raw in expected_path.read_bytes().split(b"\0")
    if raw
}
scanned = {
    pathlib.Path(path).as_posix()
    for path in payload.get("paths", {}).get("scanned", [])
}

for error in errors[:5]:
    print(f"Semgrep error: {error}", file=sys.stderr)
for result in results[:10]:
    path = result.get("path", "unknown")
    check = result.get("check_id", "unknown")
    line = result.get("start", {}).get("line", 0)
    message = result.get("extra", {}).get("message", "finding")
    print(f"{path}:{line}: {check}: {message}", file=sys.stderr)

missing = sorted(expected - scanned)
if missing:
    for path in missing[:10]:
        print(f"Semgrep did not scan selected file: {path}", file=sys.stderr)
if errors or results or missing:
    raise SystemExit(1)
print(f"Semgrep custom rules: {len(scanned)} repository Go files passed")
PY
}

# snapshot_sources copies the configuration and main's selected files into stage and lists them.
snapshot_sources() {
	local root="$1"
	local stage="$2"
	mkdir -p "$stage"
	local -a copy_statuses
	set +e
	tar -C "$root" -cf - .semgrep.yml "${files[@]}" | tar -C "$stage" -xf -
	copy_statuses=("${PIPESTATUS[@]}")
	set -e
	if (( copy_statuses[0] != 0 || copy_statuses[1] != 0 )); then
		printf 'Semgrep source snapshot failed\n' >&2
		return 1
	fi
	printf '%s\0' "${files[@]}" >"$stage/.semgrep-files"
}

# scan_snapshot runs the digest-pinned Semgrep image over stage and validates its report.
scan_snapshot() {
	local docker_bin="$1"
	local stage="$2"
	local report="$3"
	local expected="$stage/.semgrep-files"
	local image="semgrep/semgrep:${SEMGREP_VERSION}@${SEMGREP_IMAGE_DIGEST}"
	local -a scan_statuses
	set +e
	tar -C "$stage" -cf - . \
		| "$docker_bin" run --rm -i --entrypoint sh "$image" -ec '
			mkdir -p /src
			tar -xf - -C /src
			cd /src
			xargs -0 semgrep scan \
				--config .semgrep.yml \
				--error \
				--strict \
				--metrics=off \
				--disable-version-check \
				--oss-only \
				--no-git-ignore \
				--jobs 1 \
				--json < .semgrep-files
		' >"$report"
	scan_statuses=("${PIPESTATUS[@]}")
	set -e
	if ! validate_report "$report" "$expected"; then
		return 1
	fi
	if (( scan_statuses[0] != 0 || scan_statuses[1] != 0 )); then
		printf 'Semgrep scan pipeline failed: tar=%d docker=%d\n' \
			"${scan_statuses[0]}" "${scan_statuses[1]}" >&2
		return 1
	fi
}

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null
	if [[ ! -f "$root/.semgrep.yml" ]]; then
		printf 'missing Semgrep configuration: %s\n' "$root/.semgrep.yml" >&2
		return 1
	fi

	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	local docker_bin="${DOCKER_BIN:-}"
	if [[ -z "$docker_bin" ]] && command -v docker >/dev/null; then
		docker_bin="$(command -v docker)"
	fi
	if [[ -z "$docker_bin" || ! -x "$docker_bin" ]]; then
		printf 'Docker is required for digest-pinned Semgrep %s\n' "$SEMGREP_VERSION" >&2
		return 1
	fi

	local -a files=()
	collect_files "$root"
	if (( ${#files[@]} == 0 )); then
		printf 'Semgrep selected no repository Go files\n' >&2
		return 1
	fi

	scan_work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-semgrep.XXXXXX")"
	snapshot_sources "$root" "$scan_work/tree"
	scan_snapshot "$docker_bin" "$scan_work/tree" "$scan_work/report.json"
}

main "$@"
