#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
cd "$repo_root"

# Capture selection separately so discovery failures cannot become an empty successful pull.
modules="$(bash scripts/ci/go-modules.sh list)"
selected="$(awk 'NR == FNR { modules[$0] = 1; next } $1 in modules { print $2 }' \
	<(printf '%s\n' "$modules") .github/testcontainers-split-images.txt | LC_ALL=C sort -u)"
images=()
if [[ -n "$selected" ]]; then
	mapfile -t images <<<"$selected"
fi
bash scripts/ci/pull-test-images.sh "${images[@]}"
