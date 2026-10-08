#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
spectral_bin="$repo_root/tools/spectral/node_modules/.bin/spectral"
ruleset="$repo_root/tools/spectral/oas.yaml"

if [[ ! -x "$spectral_bin" ]]; then
	printf 'locked Spectral executable is unavailable: %s\n' "$spectral_bin" >&2
	exit 1
fi
if [[ ! -f "$ruleset" || -L "$ruleset" ]]; then
	printf 'locked default OAS ruleset is unavailable: %s\n' "$ruleset" >&2
	exit 1
fi

printf '%s\n' \
	'openapi: 3.0.3' \
	'info:' \
	'  title: Default ruleset contract' \
	'  version: 1.0.0' \
	'  description: Validates the reusable workflow default.' \
	'  contact:' \
	'    name: Golusoris' \
	'  license:' \
	'    name: EUPL-1.2' \
	'servers:' \
	'  - url: https://example.invalid' \
	'paths: {}' | "$spectral_bin" lint \
	--ruleset "$ruleset" \
	--fail-severity warn \
	--stdin-filepath openapi.yaml
