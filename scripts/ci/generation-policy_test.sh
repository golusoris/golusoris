#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

touch "$work/sqlc.yaml"
if make -s -C "$work" -f "$repo_root/tools/Makefile.shared" gen \
	SQLC=false OGEN=true MOCKERY=true MOCKERY_CONFIG="$repo_root/tools/mockery.yaml"; then
	printf 'gen ignored a sqlc failure\n' >&2
	exit 1
fi
rm "$work/sqlc.yaml"

touch "$work/openapi.yaml"
if make -s -C "$work" -f "$repo_root/tools/Makefile.shared" gen \
	SQLC=true OGEN=false MOCKERY=true MOCKERY_CONFIG="$repo_root/tools/mockery.yaml"; then
	printf 'gen ignored an ogen failure\n' >&2
	exit 1
fi
rm "$work/openapi.yaml"

if make -s -C "$work" -f "$repo_root/tools/Makefile.shared" gen \
	SQLC=true OGEN=true MOCKERY=false MOCKERY_CONFIG="$repo_root/tools/mockery.yaml"; then
	printf 'gen ignored a Mockery failure\n' >&2
	exit 1
fi

make -s -C "$work" -f "$repo_root/tools/Makefile.shared" gen \
	SQLC=true OGEN=true MOCKERY=true MOCKERY_CONFIG="$repo_root/tools/mockery.yaml"
printf 'generation fail-closed policy tests passed\n'
