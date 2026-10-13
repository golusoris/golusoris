#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# check-attribution.sh fails when a commit message or pull request text credits a
# model or coding tool: a Co-authored-by / Generated-by / Assisted-by trailer, or a
# "Generated with <tool>" footer. People and bots that are not coding tools pass.
#
#   check-attribution.sh BASE HEAD       every commit in BASE..HEAD, bot commits included
#   check-attribution.sh --message FILE  one commit-message file (comment lines dropped)
#   check-attribution.sh --text          text on standard input (a pull request body)

set -euo pipefail

# A line is judged only when it opens like a trailer or a footer.
readonly lead='^[[:space:]]*((co-authored-by|generated-by|assisted-by):|([^[:alnum:][:space:]]+[[:space:]]*)?(generated|created|written)[[:space:]]+(with|by)[[:space:]:])'
# Names need a non-alphanumeric character on both sides, so "Claude Coderre" and "Devin Smith" pass.
readonly names='claude[[:space:]-]*(code|opus|sonnet|haiku|fable|[0-9][0-9.]*)|codex|chatgpt|gpt-[0-9][a-z0-9.]*|gemini|antigravity|copilot|cursor|windsurf|aider|devin[[:space:]-]ai|devin-ai-integration|opencode'
readonly domains='anthropic\.com|openai\.com|cursor\.com|aider\.chat'
readonly refused="${lead}((.*[^[:alnum:]])?(${names})([^[:alnum:]]|\$)|.*@([a-z0-9-]+\\.)*(${domains})([^[:alnum:].-]|\$))"

usage() {
	printf 'usage: check-attribution.sh BASE HEAD | --message FILE | --text\n' >&2
	exit 2
}

# report PREFIX reads text on standard input and prints one finding per refused line.
report() {
	local prefix="$1" hits status=0
	hits="$(tr -d '\r' | grep -aiE "$refused")" || status=$?
	if ((status > 1)); then
		printf 'check-attribution.sh: the pattern search failed with exit %d\n' "$status" >&2
		exit 2
	fi
	[[ -n "$hits" ]] || return 0
	printf '%s\n' "$hits" | awk -v prefix="$prefix" \
		'{ print "::error::" prefix "model or coding-tool attribution is not allowed: " $0 }'
	return 1
}

check_range() {
	local base="$1" head="$2" commits sha found=0
	commits="$(git rev-list --no-merges "$base..$head")"
	for sha in $commits; do
		git show -s --format=%B "$sha" | report "commit $sha: " || found=1
	done
	return "$found"
}

main() {
	case "$#:${1:-}" in
		2:--message)
			[[ -f "$2" ]] || usage
			awk '!/^#/' "$2" | report ''
			;;
		1:--text) report '' ;;
		2:--*) usage ;;
		2:*) check_range "$1" "$2" ;;
		*) usage ;;
	esac
}

main "$@"
