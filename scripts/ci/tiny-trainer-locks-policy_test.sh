#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/tiny-trainer-locks-policy.XXXXXX")"
readonly repo_root suite_root
trap 'find "$suite_root" -depth -delete' EXIT

expect_failure() {
	local status=0
	"$@" >/dev/null 2>&1 || status=$?
	if ((status == 0)); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
}

require_error_matches() {
	local path="$1"
	shift
	local status=0 output
	output="$("$@" 2>&1)" || status=$?
	if ((status == 0)); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
	if ! grep -Fq -- "$path" <<<"$output"; then
		printf 'expected error to name %s, got: %s\n' "$path" "$output" >&2
		return 1
	fi
}

# shellcheck disable=SC1091 # Source path is dynamically resolved from repo_root at runtime.
source "$repo_root/scripts/ci/tiny-trainer-locks.sh"

# positive (3.11.16)
positive_fixture="$suite_root/positive.Dockerfile"
cat >"$positive_fixture" <<'EOF'
# syntax=docker/dockerfile:1.28
ARG PYTHON_IMAGE="python:3.11.16-slim-bookworm@sha256:4b4c524dc3dce996864e030c7bd9c6b0e517597189fee48f48e05b499442444b"
FROM ${PYTHON_IMAGE}
EOF
extracted="$(extract_dockerfile_python_version "$positive_fixture")"
if [[ "$extracted" != "3.11.16" ]]; then
	printf 'positive fixture: expected 3.11.16, got %s\n' "$extracted" >&2
	exit 1
fi

# negative: no ARG line
no_arg_fixture="$suite_root/no_arg.Dockerfile"
cat >"$no_arg_fixture" <<'EOF'
# syntax=docker/dockerfile:1.28
FROM debian:bookworm-slim
ARG VCS_REF="unknown"
EOF
require_error_matches "$no_arg_fixture" extract_dockerfile_python_version "$no_arg_fixture"

# negative: two ARG lines
two_arg_fixture="$suite_root/two_arg.Dockerfile"
cat >"$two_arg_fixture" <<'EOF'
# syntax=docker/dockerfile:1.28
ARG PYTHON_IMAGE="python:3.11.16-slim-bookworm@sha256:4b4c524dc3dce996864e030c7bd9c6b0e517597189fee48f48e05b499442444b"
FROM ${PYTHON_IMAGE}
ARG PYTHON_IMAGE="python:3.14.8-slim-trixie@sha256:fbecd461fc72d21b2143c6eaba25143738079154a6d0ea3da3aec724f50eed4a"
EOF
require_error_matches "$two_arg_fixture" extract_dockerfile_python_version "$two_arg_fixture"

# negative: python:3.14-slim without patch
no_patch_fixture="$suite_root/no_patch.Dockerfile"
cat >"$no_patch_fixture" <<'EOF'
# syntax=docker/dockerfile:1.28
ARG PYTHON_IMAGE="python:3.14-slim-bookworm@sha256:4b4c524dc3dce996864e030c7bd9c6b0e517597189fee48f48e05b499442444b"
FROM ${PYTHON_IMAGE}
EOF
require_error_matches "$no_patch_fixture" extract_dockerfile_python_version "$no_patch_fixture"

# boundary: digest present
boundary_digest_fixture="$suite_root/boundary_digest.Dockerfile"
cat >"$boundary_digest_fixture" <<'EOF'
ARG PYTHON_IMAGE="python:3.11.16-slim-bookworm@sha256:4b4c524dc3dce996864e030c7bd9c6b0e517597189fee48f48e05b499442444b"
FROM ${PYTHON_IMAGE}
EOF
extracted="$(extract_dockerfile_python_version "$boundary_digest_fixture")"
if [[ "$extracted" != "3.11.16" ]]; then
	printf 'boundary digest fixture: expected 3.11.16, got %s\n' "$extracted" >&2
	exit 1
fi

# boundary: digest absent
boundary_no_digest_fixture="$suite_root/boundary_no_digest.Dockerfile"
cat >"$boundary_no_digest_fixture" <<'EOF'
ARG PYTHON_IMAGE="python:3.11.16-slim-bookworm"
FROM ${PYTHON_IMAGE}
EOF
extracted="$(extract_dockerfile_python_version "$boundary_no_digest_fixture")"
if [[ "$extracted" != "3.11.16" ]]; then
	printf 'boundary no digest fixture: expected 3.11.16, got %s\n' "$extracted" >&2
	exit 1
fi

# repository Dockerfiles: each lock target yields a version (values move with Renovate, so none is pinned here)
for target in ai/tiny/trainers/gemma ai/tiny/trainers/litert ai/tiny/serve/tflite/sidecar; do
	extract_dockerfile_python_version "$repo_root/$target/Dockerfile" >/dev/null
done

printf 'tiny trainer locks policy tests passed\n'
