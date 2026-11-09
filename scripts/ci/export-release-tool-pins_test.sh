#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

# Repo root derived from script directory so invocation from any working directory succeeds.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init "export-release-tool-pins"

# Positive test: GITHUB_ENV gets exactly the 5 lines with live values.
github_env="$suite_root/github_env"
GITHUB_ENV="$github_env" bash "$repo_root/scripts/ci/export-release-tool-pins.sh"

mapfile -t lines < "$github_env"
if (( ${#lines[@]} != 5 )); then
	printf 'expected 5 lines in GITHUB_ENV, got %d\n' "${#lines[@]}" >&2
	exit 1
fi

# shellcheck source=tools/tool-versions.env
. "$repo_root/tools/tool-versions.env"
read -r expected_node < "$repo_root/.node-version"

grep -Fqx "BUILDX_VERSION=$BUILDX_VERSION" "$github_env"
grep -Fqx "COSIGN_VERSION=$COSIGN_VERSION" "$github_env"
grep -Fqx "GORELEASER_VERSION=$GORELEASER_VERSION" "$github_env"
grep -Fqx "NODE_VERSION=$expected_node" "$github_env"
grep -Fqx "SYFT_VERSION=$SYFT_VERSION" "$github_env"

# Negative test: subshell unsets GITHUB_ENV to prove missing environment variable fails closed.
(
	unset GITHUB_ENV
	expect_failure bash "$repo_root/scripts/ci/export-release-tool-pins.sh"
)

# Boundary test: malformed COSIGN_VERSION fails naming COSIGN_VERSION.
malformed_env="$suite_root/malformed.env"
sed 's/^COSIGN_VERSION=.*/COSIGN_VERSION=3.1/' "$repo_root/tools/tool-versions.env" > "$malformed_env"
GITHUB_ENV="$suite_root/github_env_bad" TOOL_VERSIONS_FILE="$malformed_env" \
	expect_failure_matching "COSIGN_VERSION" bash "$repo_root/scripts/ci/export-release-tool-pins.sh"

# Boundary test: malformed NODE_VERSION fails naming NODE_VERSION.
bad_node="$suite_root/bad.node"
printf '24.22\n' > "$bad_node"
GITHUB_ENV="$suite_root/github_env_bad" NODE_VERSION_FILE="$bad_node" \
	expect_failure_matching "NODE_VERSION" bash "$repo_root/scripts/ci/export-release-tool-pins.sh"

# Boundary test: .node-version without trailing newline succeeds and exports NODE_VERSION.
no_newline_node="$suite_root/no_newline.node"
no_newline_env="$suite_root/github_env_no_newline"
printf '24.22.2' > "$no_newline_node"
GITHUB_ENV="$no_newline_env" NODE_VERSION_FILE="$no_newline_node" \
	bash "$repo_root/scripts/ci/export-release-tool-pins.sh"
grep -Fqx 'NODE_VERSION=24.22.2' "$no_newline_env"

# Boundary test: empty .node-version fails naming NODE_VERSION.
empty_node="$suite_root/empty.node"
printf '' > "$empty_node"
GITHUB_ENV="$suite_root/github_env_empty" NODE_VERSION_FILE="$empty_node" \
	expect_failure_matching "NODE_VERSION" bash "$repo_root/scripts/ci/export-release-tool-pins.sh"

printf 'export-release-tool-pins policy tests passed\n'
