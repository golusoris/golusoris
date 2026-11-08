#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# One gate sequence for the Gitea CI job and the weekly Gitea security scan.
set -euo pipefail

usage() {
	printf 'usage: %s\n' "${0##*/}" >&2
}

main() {
	if [[ $# -ne 0 ]]; then
		usage
		return 2
	fi
	local root
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	cd "$root"
	# Fail before the slow gosec pass when the pin file is missing.
	# shellcheck source=tools/tool-versions.env disable=SC1091
	. tools/tool-versions.env
	scripts/ci/go-modules.sh gosec
	scripts/ci/go-modules.sh vuln
	gitleaks git . -c .gitleaks.toml --redact --no-banner --verbose
	docker info
	bash scripts/ci/semgrep-scan.sh
	bash scripts/hiss/semgrep-fixtures.sh
}

main "$@"
