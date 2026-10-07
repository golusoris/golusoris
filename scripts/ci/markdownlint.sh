#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_markdown_files=4096

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

is_linted_path() {
	local path="$1"
	case "$path" in
		AGENTS.md | */AGENTS.md | CLAUDE.md | */CLAUDE.md | CHANGELOG.md | */CHANGELOG.md | \
		.agents/* | .claude/* | .codex/* | .gemini/* | .paperclip/* | \
		.github/agents/* | .github/copilot-instructions.md | \
		.config/hiss/testdata/* | docs/upstream/* | vendor/* | third_party/* | node_modules/*)
			return 1
		;;
	esac
	[[ "$path" == *.md ]]
}

collect_files() {
	local root="$1"
	local path
	while IFS= read -r -d '' path; do
		is_linted_path "$path" || continue
		if [[ -L "$root/$path" ]]; then
			printf 'markdownlint refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		[[ -f "$root/$path" ]] || continue
		files+=(":$path")
		if (( ${#files[@]} > max_markdown_files )); then
			printf 'markdownlint file bound exceeded: %d > %d\n' \
				"${#files[@]}" "$max_markdown_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard -- '*.md')
}

main() {
	local root
	root="$(resolve_root "$@")"
	readonly root
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null

	if [[ ! -f "$root/.markdownlint-cli2.jsonc" ]]; then
		printf 'missing Markdownlint configuration: %s\n' "$root/.markdownlint-cli2.jsonc" >&2
		return 1
	fi
	if [[ ! -f "$root/tools/markdownlint/package.json" ]]; then
		printf 'missing Markdownlint package manifest\n' >&2
		return 1
	fi
	if [[ ! -f "$root/tools/markdownlint/package-lock.json" ]]; then
		printf 'missing Markdownlint package lock\n' >&2
		return 1
	fi
	command -v node >/dev/null
	command -v npm >/dev/null
	local node_major
	node_major="$(node -p 'Number(process.versions.node.split(".")[0])')"
	if (( node_major < 22 )); then
		printf 'locked markdownlint-cli2 requires Node >=22; found %s\n' \
			"$(node --version)" >&2
		return 1
	fi

	local -a files=()
	collect_files "$root"
	if (( ${#files[@]} == 0 )); then
		printf 'markdownlint: no public Markdown files selected\n'
		return
	fi

	(
		local install_root
		install_root="$(mktemp -d)"
		readonly install_root
		trap 'find "$install_root" -depth -delete' EXIT
		install -m 0600 "$root/tools/markdownlint/package.json" \
			"$root/tools/markdownlint/package-lock.json" "$install_root/"
		npm ci --prefix "$install_root" --ignore-scripts --no-audit --no-fund
		cd "$root"
		"$install_root/node_modules/.bin/markdownlint-cli2" \
			--config .markdownlint-cli2.jsonc "${files[@]}"
	)
	printf 'markdownlint: %d public Markdown files passed\n' "${#files[@]}"
}

main "$@"
