#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

if (( $# != 6 )); then
	printf 'usage: %s IMAGE@DIGEST OWNER/REPO SOURCE_SHA SOURCE_REF WORKFLOW_FILE TAG_PREFIX\n' "${0##*/}" >&2
	exit 2
fi

image="$1"
repository="$2"
source_sha="$3"
source_ref="$4"
workflow_file="$5"
tag_prefix="$6"

if [[ ! "$image" =~ ^ghcr\.io/[a-z0-9_.-]+/[a-z0-9_.-]+@sha256:[a-f0-9]{64}$ ]]; then
	printf 'invalid image digest: %q\n' "$image" >&2
	exit 1
fi
if [[ ! "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
	printf 'invalid source repository: %q\n' "$repository" >&2
	exit 1
fi
if [[ ! "$source_sha" =~ ^[a-f0-9]{40}$ ]]; then
	printf 'invalid source SHA: %q\n' "$source_sha" >&2
	exit 1
fi
if [[ ! "$tag_prefix" =~ ^[a-z0-9-]+-$ ]]; then
	printf 'invalid tag prefix: %q\n' "$tag_prefix" >&2
	exit 1
fi
if [[ ! "$workflow_file" =~ ^[a-zA-Z0-9_.-]+\.ya?ml$ ]]; then
	printf 'invalid workflow file: %q\n' "$workflow_file" >&2
	exit 1
fi
if [[ ! "$source_ref" =~ ^refs/tags/${tag_prefix}v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'invalid source ref: %q\n' "$source_ref" >&2
	exit 1
fi

workflow="$repository/.github/workflows/$workflow_file"

identity="https://github.com/$workflow@$source_ref"
cosign verify \
	--certificate-oidc-issuer https://token.actions.githubusercontent.com \
	--certificate-identity "$identity" \
	--certificate-github-workflow-repository "$repository" \
	--certificate-github-workflow-ref "$source_ref" \
	--certificate-github-workflow-sha "$source_sha" \
	"$image" >/dev/null

for predicate in https://slsa.dev/provenance/v1 https://spdx.dev/Document/v2.3; do
	gh attestation verify "oci://$image" \
		--repo "$repository" \
		--bundle-from-oci \
		--signer-workflow "$workflow" \
		--source-digest "$source_sha" \
		--source-ref "$source_ref" \
		--predicate-type "$predicate" >/dev/null
done
