#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-release-policy.XXXXXX")"
readonly suite_root
trap 'find "$suite_root" -depth -delete' EXIT

expect_failure() {
	local pattern="$1"
	shift
	local output status=0
	output="$("$@" 2>&1)" || status=$?
	if ((status == 0)) || [[ "$output" != *"$pattern"* ]]; then
		printf 'expected failure containing %q: %s\n' "$pattern" "$output" >&2
		return 1
	fi
}

check_tag_release() {
	local path="$1"
	grep -Fq '      - name: Validate canonical release tag' "$path" || {
		printf 'release workflow lacks the canonical-tag failure gate: %s\n' "$path" >&2
		return 1
	}
	if grep -Eq '^  workflow_dispatch:' "$path"; then
		printf 'release publisher exposes ref-ambiguous workflow_dispatch: %s\n' "$path" >&2
		return 1
	fi
	# shellcheck disable=SC2016 # Match the literal GitHub expression in YAML.
	grep -Fq 'ref: ${{ github.sha }}' "$path" || {
		printf 'release checkout is not bound to the event SHA: %s\n' "$path" >&2
		return 1
	}
}

check_release_go() {
	local path="$1"
	check_tag_release "$path" || return 1
	# shellcheck disable=SC2016 # Match literal runtime variables and expressions.
	for contract in \
		'dist/artifacts.json' \
		'group: release-go-${{ github.repository }}-${{ github.ref }}' \
		'release-receipt-parser:start' \
		'release-receipt-parser:end' \
		'release-oci-tag:start' \
		'release-oci-tag:end' \
		'release-tag-binding:start' \
		'release-tag-binding:end' \
		'goreleaser release --config="$GORELEASER_CONFIG" --clean --draft' \
		'      - name: Publish verified release' \
		'"$REMOTE_DIGEST" != "$DIGEST"' \
		'"$remote_sha" != "$SOURCE_SHA"'; do
		grep -Fq -- "$contract" "$path" || {
			printf 'release-go lacks receipt binding %s\n' "$contract" >&2
			return 1
		}
	done
	if grep -Fq 'gh release upload' "$path"; then
		printf 'release-go mutates a release after GoReleaser publication\n' >&2
		return 1
	fi
}

extract_receipt_parser() {
	local workflow="$1"
	awk '/release-receipt-parser:start/{capture=1} capture {print} /release-receipt-parser:end/{capture=0}' \
		"$workflow" | sed 's/^          //'
}

extract_rebuild_resolver() {
	local workflow="$1"
	awk '/rebuild-tag-resolver:start/{capture=1} capture {print} /rebuild-tag-resolver:end/{capture=0}' \
		"$workflow" | sed 's/^          //'
}

extract_tag_binding() {
	local workflow="$1"
	awk '/release-tag-binding:start/{capture=1} capture {print} /release-tag-binding:end/{capture=0}' \
		"$workflow" | sed 's/^          //'
}

extract_oci_tag_validator() {
	local workflow="$1"
	awk '/release-oci-tag:start/{capture=1} capture {print} /release-oci-tag:end/{capture=0}' \
		"$workflow" | sed 's/^          //'
}

run_rebuild_resolver() (
	cd "$1"
	EVENT_SHA="$2" GITHUB_OUTPUT="$3" bash -euo pipefail "$4"
)

run_tag_binding() (
	cd "$1"
	RELEASE_TAG="$2" SOURCE_SHA="$3" bash -euo pipefail "$4"
)

run_oci_tag_validator() {
	RELEASE_REF_NAME="$1" bash -euo pipefail "$2"
}

check_rebuild() {
	local path="$1"
	local qemu_image
	for dead_input in 'go-version-file:' 'goreleaser-config:' 'COSIGN_PASSWORD' 'actions/setup-go@'; do
		if grep -Fq "$dead_input" "$path"; then
			printf 'rebuild exposes dead contract %s\n' "$dead_input" >&2
			return 1
		fi
	done
	# Renovate bumps every Buildx pin together; rebuild must name the release workflow's.
	local buildx release_buildx
	buildx="$(grep -oE 'version: v[0-9]+[.][0-9]+[.][0-9]+' "$path" | head -n 1)"
	release_buildx="$(grep -oE 'version: v[0-9]+[.][0-9]+[.][0-9]+' "$repo_root/.github/workflows/release-go.yml" | head -n 1)"
	[[ -n "$buildx" && "$buildx" == "$release_buildx" ]] || {
		printf 'rebuild does not select the pinned Buildx binary\n' >&2
		return 1
	}
	qemu_image="$(sed -n 's/^[[:space:]]*image: \(docker\.io\/tonistiigi\/binfmt:.*\)$/\1/p' "$path")"
	[[ "$qemu_image" =~ ^docker\.io/tonistiigi/binfmt:qemu-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)@sha256:[0-9a-f]{64}$ ]] || {
		printf 'rebuild does not pin the QEMU installer image by tag and digest\n' >&2
		return 1
	}
	# shellcheck disable=SC2016 # Match literal runtime variables and expressions.
	for contract in \
		'group: rebuild-on-base-${{ github.repository }}-${{ github.ref }}' \
		'rebuild-tag-resolver:start' \
		'rebuild-tag-resolver:end' \
		'"$checkout_sha" != "$EVENT_SHA"' \
		"--match 'v[0-9]*.[0-9]*.[0-9]*'" \
		"printf 'sha=sha-%s\\n'" \
		'org.opencontainers.image.created=${{ steps.tag.outputs.created }}'; do
		grep -Fq -- "$contract" "$path" || {
			printf 'rebuild lacks immutable tag contract %s\n' "$contract" >&2
			return 1
		}
	done
}

check_root_release() {
	local path="$1"
	check_tag_release "$path" || return 1
	# shellcheck disable=SC2016 # Match literal runtime variables in workflow YAML.
	for contract in \
		'      - name: Publish verified immutable release' \
		'      - name: Compose the breaking-change notes header' \
		'scripts/ci/release-notes-breaking.sh notes "$RELEASE_TAG" "$previous"' \
		'args+=(--release-header="$RUNNER_TEMP/release-header.md")' \
		'release-tag-binding:start' \
		'release-tag-binding:end' \
		'"$remote_sha" != "$SOURCE_SHA"' \
		"'\\tfalse\\ttrue'"; do
		grep -Fq -- "$contract" "$path" || {
			printf 'root release lacks publication contract %s\n' "$contract" >&2
			return 1
		}
	done
}

check_release_draft_contract() {
	local config="$1"
	for contract in 'draft: true' 'use_existing_draft: true' \
		'replace_existing_artifacts: true'; do
		grep -Fq "$contract" "$config" || {
			printf 'GoReleaser lacks resumable draft contract %s\n' "$contract" >&2
			return 1
		}
	done
}

check_verifier() {
	local path="$1"
	# shellcheck disable=SC2016 # Match literal runtime variables in workflow YAML.
	for contract in \
		'at least one verification mode must be enabled' \
		'--bundle-from-oci' \
		"gh attestation verify --help | grep -Fq -- '--signer-digest'" \
		'GitHub token login is restricted to matching ghcr.io images' \
		'--signer-workflow' \
		'--signer-digest' \
		'--source-ref' \
		'--source-digest' \
		'--certificate-identity "https://github.com/${SIGNER_WORKFLOW}@${SIGNER_DIGEST}"' \
		'--certificate-github-workflow-repository' \
		'https://slsa.dev/provenance/v1' \
		'https://spdx.dev/Document/v2.3'; do
		grep -Fq -- "$contract" "$path" || {
			printf 'provenance verifier lacks contract %s\n' "$contract" >&2
			return 1
		}
	done
	if grep -Fq 'id-token: write' "$path"; then
		printf 'verification-only workflow requests OIDC minting permission\n' >&2
		return 1
	fi
	if grep -Fq 'certificate-identity-regexp' "$path"; then
		printf 'provenance verifier permits a mutable signer identity regexp\n' >&2
		return 1
	fi
	awk '
		$0 == "      signer-digest:" { in_input = 1; next }
		in_input && $0 ~ /^      [^ ]/ { exit }
		in_input && $0 == "        required: true" { found = 1 }
		END { exit !found }
	' "$path" || {
		printf 'provenance verifier does not require an immutable signer digest\n' >&2
		return 1
	}
}

check_downstream_signer_contract() {
	local path="$1"
	grep -Eq 'uses: golusoris/golusoris/\.github/workflows/release-go\.yml@[0-9a-f]{40}$' \
		"$path" || {
		printf 'downstream release example does not pin the signer commit\n' >&2
		return 1
	}
	grep -Eq 'uses: golusoris/golusoris/\.github/workflows/verify-provenance\.yml@[0-9a-f]{40}$' \
		"$path" || {
		printf 'downstream verifier example is not commit-pinned\n' >&2
		return 1
	}
	grep -Eq '^[[:space:]]+signer-digest: [0-9a-f]{40}$' "$path" || {
		printf 'downstream verifier example lacks the signer digest\n' >&2
		return 1
	}
	for contract in 'dockers_v2:' 'ghcr.io/myorg/myapp' '- "{{ .Tag }}"'; do
		grep -Fq -- "$contract" "$path" || {
			printf 'downstream release example lacks image receipt contract %s\n' \
				"$contract" >&2
			return 1
		}
	done
}

check_caller_permissions() {
	local path="$1"
	for permission in 'contents: write' 'packages: write' 'id-token: write' \
		'attestations: write' 'artifact-metadata: write'; do
		grep -Fq "$permission" "$path" || {
			printf 'release caller lacks permission %s: %s\n' "$permission" "$path" >&2
			return 1
		}
	done
}

release_go="$repo_root/.github/workflows/release-go.yml"
release="$repo_root/.github/workflows/release.yml"
sbom="$repo_root/.github/workflows/sbom.yml"
rebuild="$repo_root/.github/workflows/rebuild-on-base.yml"
verifier="$repo_root/.github/workflows/verify-provenance.yml"
template="$repo_root/template/.github/workflows/release.yml"
downstream_docs="$repo_root/docs/ci-downstream.md"
goreleaser_config="$repo_root/tools/.goreleaser.yml"

check_release_go "$release_go"
check_root_release "$release"
check_tag_release "$sbom"
check_rebuild "$rebuild"
check_verifier "$verifier"
check_caller_permissions "$template"
check_caller_permissions "$downstream_docs"
check_release_draft_contract "$goreleaser_config"
check_release_draft_contract "$downstream_docs"
check_downstream_signer_contract "$downstream_docs"

parser="$suite_root/release-receipt-parser.mjs"
extract_receipt_parser "$release_go" >"$parser"
digest_a='sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
digest_b='sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
receipt="$suite_root/artifacts.json"
printf '[{"name":"ghcr.io/acme/app:v1.2.3","type":"Docker Image","extra":{}},{"name":"ghcr.io/acme/app:v1.2.3","type":"Published Docker Image","extra":{"Digest":"%s"}}]\n' \
	"$digest_a" >"$receipt"
actual="$(ARTIFACTS_JSON="$receipt" EXPECTED_IMAGE_REF='ghcr.io/acme/app:v1.2.3' \
	node "$parser")"
[[ "$actual" == "$digest_a" ]] || {
	printf 'receipt parser returned %s, want %s\n' "$actual" "$digest_a" >&2
	exit 1
}
expect_failure 'missing or invalid digest' env ARTIFACTS_JSON="$receipt" \
	EXPECTED_IMAGE_REF='ghcr.io/acme/app:v9.9.9' node "$parser"
printf '[{"name":"ghcr.io/acme/app:v1.2.3","type":"Docker Image","extra":{"Digest":"%s"}},{"name":"ghcr.io/acme/app:v1.2.3","type":"Docker Manifest","extra":{"Digest":"%s"}}]\n' \
	"$digest_a" "$digest_b" >"$receipt"
expect_failure 'ambiguous digests' env ARTIFACTS_JSON="$receipt" \
	EXPECTED_IMAGE_REF='ghcr.io/acme/app:v1.2.3' node "$parser"

resolver="$suite_root/rebuild-tag-resolver.sh"
extract_rebuild_resolver "$rebuild" >"$resolver"
tag_repo="$suite_root/tag-repo"
mkdir -p "$tag_repo"
git -C "$tag_repo" init -q
git -C "$tag_repo" config user.name 'Release Policy Test'
git -C "$tag_repo" config user.email 'release-policy@example.invalid'
printf 'release policy\n' >"$tag_repo/input.txt"
git -C "$tag_repo" add input.txt
git -C "$tag_repo" commit -qm 'test: release policy fixture'
tag_sha="$(git -C "$tag_repo" rev-parse HEAD)"
git -C "$tag_repo" tag --no-sign -a -m 'root release fixture' 'v1.2.3+build.5'
git -C "$tag_repo" tag --no-sign -a -m 'module release fixture' 'core/v9.9.9'
resolver_output="$suite_root/rebuild-output"
run_rebuild_resolver "$tag_repo" "$tag_sha" "$resolver_output" "$resolver"
grep -Fxq 'tag=v1.2.3_build.5' "$resolver_output"
grep -Fxq "sha=sha-$tag_sha" "$resolver_output"
git -C "$tag_repo" tag -d 'v1.2.3+build.5' >/dev/null
: >"$resolver_output"
run_rebuild_resolver "$tag_repo" "$tag_sha" "$resolver_output" "$resolver"
grep -Fxq "tag=sha-$tag_sha" "$resolver_output"
expect_failure 'does not match the workflow event SHA' run_rebuild_resolver \
	"$tag_repo" '0000000000000000000000000000000000000000' \
	"$resolver_output" "$resolver"

tag_binding="$suite_root/release-tag-binding.sh"
extract_tag_binding "$release_go" >"$tag_binding"
tag_remote="$suite_root/tag-remote.git"
git -C "$tag_repo" tag --no-sign -a -m 'annotated release fixture' 'v1.2.3'
git -C "$tag_repo" tag --no-sign 'v1.2.4'
git init -q --bare "$tag_remote"
git -C "$tag_repo" remote add origin "$tag_remote"
git -C "$tag_repo" push -q origin HEAD:refs/heads/main --tags
run_tag_binding "$tag_repo" 'v1.2.3' "$tag_sha" "$tag_binding"
run_tag_binding "$tag_repo" 'v1.2.4' "$tag_sha" "$tag_binding"
expect_failure 'release tag no longer exists' run_tag_binding \
	"$tag_repo" 'v9.9.9' "$tag_sha" "$tag_binding"
printf 'moved tag\n' >>"$tag_repo/input.txt"
git -C "$tag_repo" add input.txt
git -C "$tag_repo" commit -qm 'test: move release tag fixture'
git -C "$tag_repo" tag --no-sign -f -a -m 'moved release fixture' 'v1.2.3' >/dev/null
git -C "$tag_repo" push -q --force origin refs/tags/v1.2.3
expect_failure 'release tag no longer resolves to the event commit' run_tag_binding \
	"$tag_repo" 'v1.2.3' "$tag_sha" "$tag_binding"

tag_pattern="$(sed -n "s/.*TAG_PATTERN='\(.*\)'/\1/p" "$release_go" | head -n 1)"
for publisher in "$release" "$sbom" "$rebuild"; do
	publisher_pattern="$(sed -n "s/.*TAG_PATTERN='\(.*\)'/\1/p" "$publisher" | head -n 1)"
	[[ "$publisher_pattern" == "$tag_pattern" ]] || {
		printf 'release tag validators diverge: %s\n' "$publisher" >&2
		exit 1
	}
done
for tag in v0.0.0 v1.2.3 v1.2.3-rc.1 v1.2.3-rc.1+build.5; do
	[[ "$tag" =~ $tag_pattern ]] || {
		printf 'canonical tag rejected: %s\n' "$tag" >&2
		exit 1
	}
done
for tag in main 1.2.3 v01.2.3 v1.2.03 v1.2.3-01 v1.2.3-rc..1; do
	if [[ "$tag" =~ $tag_pattern ]]; then
		printf 'noncanonical tag accepted: %s\n' "$tag" >&2
		exit 1
	fi
done

oci_validator="$suite_root/release-oci-tag.sh"
extract_oci_tag_validator "$release_go" >"$oci_validator"
run_oci_tag_validator 'v1.2.3' "$oci_validator"
max_oci_tag="v1.2.3-$(printf '%0121d' 0 | tr '0' a)"
overlong_oci_tag="${max_oci_tag}a"
run_oci_tag_validator "$max_oci_tag" "$oci_validator"
expect_failure 'valid OCI tag' run_oci_tag_validator \
	'v1.2.3+build.5' "$oci_validator"
expect_failure 'valid OCI tag' run_oci_tag_validator \
	"$overlong_oci_tag" "$oci_validator"

negative="$suite_root/negative.yml"
sed '/^on:$/a\  workflow_dispatch:' "$release" >"$negative"
expect_failure 'ref-ambiguous workflow_dispatch' check_tag_release "$negative"
sed '/Validate canonical release tag/d' "$release_go" >"$negative"
expect_failure 'canonical-tag failure gate' check_release_go "$negative"
sed '/Publish verified immutable release/d' "$release" >"$negative"
expect_failure 'publication contract' check_root_release "$negative"
# shellcheck disable=SC2016 # Match literal runtime variables in workflow YAML.
sed '/"$remote_sha" != "$SOURCE_SHA"/d' "$release_go" >"$negative"
# shellcheck disable=SC2016 # Match literal runtime variables in workflow YAML.
expect_failure '"$remote_sha" != "$SOURCE_SHA"' check_release_go "$negative"
sed -E 's/version: v[0-9]+[.][0-9]+[.][0-9]+/version: latest/' "$rebuild" >"$negative"
expect_failure 'pinned Buildx binary' check_rebuild "$negative"
sed 's/@sha256:[0-9a-f]\{64\}//' "$rebuild" >"$negative"
expect_failure 'QEMU installer image' check_rebuild "$negative"
sed '/at least one verification mode must be enabled/d' "$verifier" >"$negative"
expect_failure 'at least one verification mode' check_verifier "$negative"
sed '/--signer-digest/d' "$verifier" >"$negative"
expect_failure '--signer-digest' check_verifier "$negative"
sed '/GitHub token login is restricted/d' "$verifier" >"$negative"
expect_failure 'GitHub token login is restricted' check_verifier "$negative"
sed '/signer-digest:/,/required: true/d' "$verifier" >"$negative"
expect_failure 'immutable signer digest' check_verifier "$negative"
sed '/artifact-metadata: write/d' "$template" >"$negative"
expect_failure 'artifact-metadata: write' check_caller_permissions "$negative"
sed '/attestations: write/d' "$downstream_docs" >"$negative"
expect_failure 'attestations: write' check_caller_permissions "$negative"
sed '/use_existing_draft: true/d' "$goreleaser_config" >"$negative"
expect_failure 'use_existing_draft: true' check_release_draft_contract "$negative"
sed '/^[[:space:]]*signer-digest:/d' "$downstream_docs" >"$negative"
expect_failure 'signer digest' check_downstream_signer_contract "$negative"
sed '/- "{{ \.Tag }}"/d' "$downstream_docs" >"$negative"
expect_failure 'image receipt contract' check_downstream_signer_contract "$negative"

printf 'release workflow policy tests passed\n'
