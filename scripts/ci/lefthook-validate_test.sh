#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

tmp_dir=""

cleanup() {
	if [[ -n "$tmp_dir" ]]; then
		rm -rf "$tmp_dir"
	fi
}

main() {
	local repo_root valid_cfg bad_cfg plain_run bad_rc=0 bad_out=""
	repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-lefthook-validate.XXXXXX")"
	# Resolve TMPDIR relative paths so LEFTHOOK_CONFIG stays absolute after cd.
	tmp_dir="$(cd "$tmp_dir" && pwd)"
	trap cleanup EXIT

	valid_cfg="$tmp_dir/lefthook.yml"
	cp "$repo_root/lefthook.yml" "$valid_cfg"
	(cd "$repo_root" && LEFTHOOK_CONFIG="$valid_cfg" lefthook validate >/dev/null)

	bad_cfg="$tmp_dir/lefthook-malformed.yml"
	plain_run="      run: if true; then echo ok; else printf 'merge-comments: origin/main not found, skipping\\n' >&2; fi"
	PLAIN_RUN="$plain_run" awk '
		$0 == "    merge-comments:" { in_mc = 1; print; next }
		in_mc && /^    [a-zA-Z0-9_-]+:/ { in_mc = 0 }
		in_mc && /^      run:/ {
			print ENVIRON["PLAIN_RUN"]
			replaced = 1
			next
		}
		{ print }
		END { if (!replaced) exit 1 }
	' "$repo_root/lefthook.yml" >"$bad_cfg"

	bad_out="$(cd "$repo_root" && LEFTHOOK_CONFIG="$bad_cfg" lefthook validate 2>&1)" || bad_rc="$?"
	if ((bad_rc == 0)); then
		printf 'expected lefthook validate to refuse malformed merge-comments run (%s)\n' "$bad_out" >&2
		return 1
	fi

	printf 'lefthook validate tests passed\n'
}

main "$@"
