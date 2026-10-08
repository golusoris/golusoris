# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
#
# Dated gofumpt exceptions shared by scripts/hooks/gofumpt.sh and
# scripts/ci/check-gofumpt.sh (sourced, never executed; callers set strict mode).
# shellcheck shell=bash

# Praetor's byte-locked API gate asset is not gofumpt-clean (cordanaLLM/praetor#842).
readonly gofumpt_locked_gate=tools/apicompat/gate/main.go
readonly gofumpt_locked_gate_expires=20261107

# gofumpt_exempt reports whether the repository-relative path is excepted today;
# after the expiry it is checked again.
gofumpt_exempt() {
	[[ "$1" == "$gofumpt_locked_gate" ]] || return 1
	(($(date +%Y%m%d) <= gofumpt_locked_gate_expires)) || return 1
	printf 'gofumpt skips %s: exception until %s (cordanaLLM/praetor#842)\n' \
		"$1" "$gofumpt_locked_gate_expires" >&2
}
