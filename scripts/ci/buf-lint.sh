#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Lints every tracked protobuf file with the pinned buf and checks the vendored
# KEDA external scaler contract against the upstream commit it was copied from.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
scan_root="${BUF_SCAN_ROOT:-$repo_root}"
readonly repo_root scan_root

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

readonly module=k8s/keda/internal/externalscalerpb
# Must match the commit named in $module/buf.gen.yaml.
readonly keda_commit=626ded5d783108bedf647e20bbc83f8e458e02e4
against="${BUF_AGAINST:-https://github.com/kedacore/keda.git#ref=${keda_commit},subdir=pkg/scalers/externalscaler}"
readonly against

if ! grep -Fq "$keda_commit" "$scan_root/$module/buf.gen.yaml"; then
	printf 'buf: %s/buf.gen.yaml does not name the upstream commit %s\n' "$module" "$keda_commit" >&2
	exit 1
fi

mapfile -d '' protos < <(git -C "$scan_root" ls-files -z -- '*.proto')
if ((${#protos[@]} == 0 || ${#protos[@]} > 256)); then
	printf 'buf: tracked .proto count outside bound 1..256: %d\n' "${#protos[@]}" >&2
	exit 1
fi
for proto in "${protos[@]}"; do
	if [[ "$proto" != "$module/"* ]]; then
		printf 'buf: %s lies outside the linted module %s; give it a buf module and lint it here\n' \
			"$proto" "$module" >&2
		exit 1
	fi
done

buf_cmd=()
if [[ -n "${BUF_BIN:-}" ]]; then
	buf_cmd=("$BUF_BIN")
elif command -v buf >/dev/null 2>&1; then
	buf_cmd=("$(command -v buf)")
fi
installed=""
if ((${#buf_cmd[@]} > 0)); then
	installed="v$("${buf_cmd[@]}" --version)"
fi
if [[ "$installed" != "$BUF_VERSION" ]]; then
	if [[ -n "${BUF_BIN:-}" ]]; then
		printf 'buf: BUF_BIN is %s, %s required\n' "${installed:-unreadable}" "$BUF_VERSION" >&2
		exit 1
	fi
	printf 'buf: installed buf is %s; running buf %s through go run\n' \
		"${installed:-missing}" "$BUF_VERSION" >&2
	buf_cmd=(go run "github.com/bufbuild/buf/cmd/buf@${BUF_VERSION}")
fi

cd "$scan_root/$module"
"${buf_cmd[@]}" lint --timeout 2m
"${buf_cmd[@]}" breaking --timeout 2m --against "$against"
