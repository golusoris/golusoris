#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

# Bound scanned template files and referenced workflows against runaway inputs.
readonly max_template_files=64
readonly max_uses_per_file=64
readonly allow_regex='^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+golusoris/golusoris/\.github/workflows/([A-Za-z0-9._-]+\.ya?ml)@([0-9a-f]{40})[[:space:]]+#[[:space:]]+(v[0-9]+\.[0-9]+\.[0-9]+)[[:space:]]*$'

temp_dir=""
total_matched_pins=0

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

# locate_tool prints the override path, else the PATH match; it prints nothing when the tool is absent.
locate_tool() {
	local override="$1"
	local name="$2"
	local found
	if [[ -n "$override" ]]; then
		printf '%s\n' "$override"
	elif found="$(command -v "$name")"; then
		printf '%s\n' "$found"
	fi
}

check_actionlint() {
	local actionlint_bin="$1"
	if [[ -z "$actionlint_bin" || ! -x "$actionlint_bin" ]]; then
		printf 'actionlint %s is required; executable is unavailable\n' \
			"$ACTIONLINT_VERSION" >&2
		return 1
	fi
	local actual_actionlint_version
	actual_actionlint_version="$("$actionlint_bin" -version | awk 'NR == 1 { print $1; exit }')"
	if [[ "$actual_actionlint_version" != "$ACTIONLINT_VERSION" ]]; then
		printf 'actionlint version is %s, want %s\n' \
			"${actual_actionlint_version:-unknown}" "$ACTIONLINT_VERSION" >&2
		return 1
	fi
}

check_shellcheck() {
	local shellcheck_bin="$1"
	if [[ -z "$shellcheck_bin" || ! -x "$shellcheck_bin" ]]; then
		printf 'ShellCheck %s is required by actionlint; executable is unavailable\n' \
			"$SHELLCHECK_VERSION" >&2
		return 1
	fi
	local actual_shellcheck_version
	actual_shellcheck_version="$("$shellcheck_bin" --version | awk '$1 == "version:" { print $2; exit }')"
	if [[ "$actual_shellcheck_version" != "$SHELLCHECK_VERSION" ]]; then
		printf 'actionlint ShellCheck version is %s, want %s\n' \
			"${actual_shellcheck_version:-unknown}" "$SHELLCHECK_VERSION" >&2
		return 1
	fi
}

collect_template_files() {
	local root="$1"
	local path
	while IFS= read -r -d '' path; do
		if [[ -L "$root/$path" ]]; then
			printf 'template-workflows refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		[[ -f "$root/$path" ]] || continue
		template_files+=("$path")
		if (( ${#template_files[@]} > max_template_files )); then
			printf 'template-workflows file bound exceeded: %d > %d\n' \
				"${#template_files[@]}" "$max_template_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard -- \
		'template/.github/workflows/*.yml' 'template/.github/workflows/*.yaml')
}

# check_pin requires the pinned commit, a tag comment that resolves to it, and the called workflow at it.
check_pin() {
	local root="$1"
	local where="$2"
	local wf="$3"
	local sha="$4"
	local tag="$5"

	if ! git -C "$root" cat-file -e "${sha}^{commit}" 2>/dev/null; then
		printf 'template-workflows: %s: commit %s is not in this clone (fetch full history and tags)\n' \
			"$where" "$sha" >&2
		return 1
	fi

	local tag_sha=""
	if ! tag_sha="$(git -C "$root" rev-parse -q --verify "refs/tags/${tag}^{commit}" 2>/dev/null)" || [[ "$tag_sha" != "$sha" ]]; then
		printf 'template-workflows: %s: tag %s resolves to %s, pin is %s\n' \
			"$where" "$tag" "${tag_sha:-missing}" "$sha" >&2
		return 1
	fi

	if ! git -C "$root" cat-file -e "${sha}:.github/workflows/${wf}" 2>/dev/null; then
		printf 'template-workflows: %s: .github/workflows/%s does not exist at %s\n' \
			"$where" "$wf" "$sha" >&2
		return 1
	fi
}

validate_template_file() {
	local root="$1"
	local file="$2"

	local uses_text grep_status=0
	uses_text="$(grep -n 'uses:' "$root/$file")" || grep_status=$?
	# grep exits 1 on no match; anything higher is a read error, not an empty file.
	if (( grep_status > 1 )); then
		printf 'template-workflows: %s: grep failed with status %d\n' "$file" "$grep_status" >&2
		return 1
	fi
	local -a uses_lines=()
	if [[ -n "$uses_text" ]]; then
		mapfile -t uses_lines <<< "$uses_text"
	fi
	if (( ${#uses_lines[@]} > max_uses_per_file )); then
		printf 'template-workflows: %s: uses bound exceeded: %d > %d\n' \
			"$file" "${#uses_lines[@]}" "$max_uses_per_file" >&2
		return 1
	fi

	declare -A seen_pins=()
	local -a distinct_pairs=()
	local matched_count=0

	local line line_num line_content wf sha tag
	for line in "${uses_lines[@]}"; do
		line_num="${line%%:*}"
		line_content="${line#*:}"
		if [[ ! "$line_content" =~ $allow_regex ]]; then
			printf 'template-workflows: %s:%s: line does not match allowed reusable workflow pin pattern\n' \
				"$file" "$line_num" >&2
			return 1
		fi
		wf="${BASH_REMATCH[2]}"
		sha="${BASH_REMATCH[3]}"
		tag="${BASH_REMATCH[4]}"
		check_pin "$root" "$file:$line_num" "$wf" "$sha" "$tag" || return 1

		if [[ -n "${seen_pins[$wf]:-}" ]]; then
			if [[ "${seen_pins[$wf]}" != "$sha" ]]; then
				printf 'template-workflows: %s:%s: pins differ for .github/workflows/%s: %s vs %s\n' \
					"$file" "$line_num" "$wf" "${seen_pins[$wf]}" "$sha" >&2
				return 1
			fi
		else
			seen_pins["$wf"]="$sha"
			distinct_pairs+=("$wf:$sha")
		fi
		matched_count=$((matched_count + 1))
	done

	printf 'COUNT:%d\n' "$matched_count"
	local pair
	for pair in "${distinct_pairs[@]}"; do
		printf 'PAIR:%s\n' "$pair"
	done
}

write_reusable_workflows() {
	local root="$1"
	local repo_dir="$2"
	shift 2
	local pair wf sha
	for pair in "$@"; do
		wf="${pair%%:*}"
		sha="${pair#*:}"
		git -C "$root" show "${sha}:.github/workflows/${wf}" > "$repo_dir/.github/workflows/${wf}"
	done
}

lint_rewritten_workflow() {
	local root="$1"
	local file="$2"
	local repo_dir="$3"
	local actionlint_bin="$4"
	local shellcheck_bin="$5"

	local name="${file##*/}"
	local copy_name="check_${name}"

	sed -E 's|^([[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+)golusoris/golusoris/\.github/workflows/([A-Za-z0-9._-]+\.ya?ml)@[0-9a-f]{40}[[:space:]]+#[[:space:]]+v[0-9]+\.[0-9]+\.[0-9]+[[:space:]]*$|\1./.github/workflows/\3|' \
		"$root/$file" > "$repo_dir/.github/workflows/$copy_name"

	local actionlint_status=0
	(
		cd "$repo_dir"
		"$actionlint_bin" \
			-no-color \
			-config-file .github/actionlint.yaml \
			-shellcheck "$shellcheck_bin" \
			-pyflakes= \
			".github/workflows/$copy_name"
	) 2>&1 | sed "s|^\.github/workflows/${copy_name}|$file|" || actionlint_status="${PIPESTATUS[0]}"

	if (( actionlint_status != 0 )); then
		return "$actionlint_status"
	fi
}

# check_template_file validates one template's pins, then lints it against the pinned workflows in repo_dir.
check_template_file() {
	local root="$1"
	local file="$2"
	local repo_dir="$3"
	local actionlint_bin="$4"
	local shellcheck_bin="$5"

	local validation_out line
	validation_out="$(validate_template_file "$root" "$file")"
	local -a distinct_pairs=()
	while IFS= read -r line; do
		if [[ "$line" =~ ^COUNT:([0-9]+)$ ]]; then
			total_matched_pins=$((total_matched_pins + BASH_REMATCH[1]))
		elif [[ "$line" =~ ^PAIR:(.+)$ ]]; then
			distinct_pairs+=("${BASH_REMATCH[1]}")
		fi
	done <<< "$validation_out"

	mkdir -p "$repo_dir/.github/workflows"
	git -C "$repo_dir" init -q
	cp "$root/.github/actionlint.yaml" "$repo_dir/.github/actionlint.yaml"
	if (( ${#distinct_pairs[@]} > 0 )); then
		write_reusable_workflows "$root" "$repo_dir" "${distinct_pairs[@]}"
	fi
	lint_rewritten_workflow "$root" "$file" "$repo_dir" "$actionlint_bin" "$shellcheck_bin"
}

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null
	if [[ ! -f "$root/.github/actionlint.yaml" ]]; then
		printf 'missing actionlint configuration: %s\n' \
			"$root/.github/actionlint.yaml" >&2
		return 1
	fi

	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	local actionlint_bin shellcheck_bin
	actionlint_bin="$(locate_tool "${ACTIONLINT_BIN:-}" actionlint)"
	check_actionlint "$actionlint_bin"
	shellcheck_bin="$(locate_tool "${SHELLCHECK_BIN:-}" shellcheck)"
	check_shellcheck "$shellcheck_bin"

	local -a template_files=()
	collect_template_files "$root"
	if (( ${#template_files[@]} == 0 )); then
		printf 'template-workflows: selected no template workflows\n' >&2
		return 1
	fi

	temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-template-workflows.XXXXXX")"
	trap 'find "$temp_dir" -depth -delete' EXIT

	local file_idx=0
	local file
	for file in "${template_files[@]}"; do
		file_idx=$((file_idx + 1))
		check_template_file "$root" "$file" "$temp_dir/repo_$file_idx" "$actionlint_bin" "$shellcheck_bin"
	done

	printf 'template-workflows: %d template workflows match %d pinned reusable workflows\n' \
		"${#template_files[@]}" "$total_matched_pins"
}

main "$@"
