#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# pre-commit / pre-push: ShellCheck repository shell scripts when shell scripts change.

set -euo pipefail

# shellcheck source=scripts/hooks/lib.sh
. "$(dirname "$0")/lib.sh"

root="$(git rev-parse --show-toplevel)"

# shellcheck source=/dev/null
. "$root/tools/tool-versions.env"

declare -a kept=()
for arg in "$@"; do
	# Windows Git passes backslash-separated paths.
	f="${arg//\\//}"
	case "$f" in
		.config/hiss/testdata/* | */.config/hiss/testdata/*)
			;;
		*.sh | *.bash | *.ksh)
			if [ -f "$f" ]; then
				kept+=("$f")
			fi
			;;
	esac
done

((${#kept[@]} > 0)) || skip "no changed shell scripts"

if [ -z "${SHELLCHECK_BIN:-}" ] && ! command -v shellcheck >/dev/null 2>&1; then
	skip "ShellCheck ${SHELLCHECK_VERSION} is not on PATH — install with: bash scripts/ci/install-shellcheck.sh <dir>"
fi

bash "$root/scripts/ci/shellcheck.sh" --root "$root" ||
	fail "ShellCheck reported findings"

note "${#kept[@]} changed shell script(s) passed"
