#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Regenerate the dated complexity exceptions in .golangci.yml from a measured lint run (#1017).
# Needs golangci-lint, jq and praetorctl on PATH; the engine audit judges the result.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly BURN_DOWN_ISSUE=1017

cd "$REPO_ROOT"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# The stripped copy drops the generated block, so every current finding is measured again.
praetorctl linters exceptions strip-config -config .golangci.yml -out "$work/golangci.yml"
n=0
while IFS= read -r module; do
	n=$((n + 1))
	(cd "$REPO_ROOT/$module" && golangci-lint run --config="$work/golangci.yml" \
		--enable-only=funlen,gocyclo,gocognit,cyclop --issues-exit-code=0 \
		--max-issues-per-linter=0 --max-same-issues=0 --uniq-by-line=false \
		--output.text.path=stderr --output.json.path="$work/report-$n.json" ./...) 2>/dev/null
done < <(bash "$SCRIPT_DIR/go-modules.sh" list)
jq -s '{Issues: (map(.Issues // []) | add)}' "$work"/report-*.json >"$work/report.json"
printf 'measured %s findings in %s modules\n' "$(jq '.Issues | length' "$work/report.json")" "$n"
praetorctl linters exceptions generate -config .golangci.yml -report "$work/report.json" \
	-issue "$BURN_DOWN_ISSUE" -category C -write
