#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/tiny-trainer-publish-policy.XXXXXX")"
readonly repo_root suite_root
trap 'find "$suite_root" -depth -delete' EXIT

expect_failure() {
	local status=0
	"$@" >/dev/null 2>&1 || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
}

require_literal() {
	local path="$1"
	local literal="$2"
	if ! grep -Fq -- "$literal" "$path"; then
		printf '%s lacks required publication control: %s\n' "$path" "$literal" >&2
		return 1
	fi
}

require_unconditional_step() {
	local path="$1"
	local step="$2"
	local block
	block="$(awk -v marker="      - name: $step" '
		$0 == marker { active = 1; next }
		active && /^      - name:/ { exit }
		active { print }
	' "$path")"
	if [[ -z "$block" ]] || grep -Eq '^        if:' <<<"$block"; then
		printf '%s must run %s on new and resumed publication\n' "$path" "$step" >&2
		return 1
	fi
}

check_workflow() {
	local path="$1"
	local literal
	local -a literals=(
		"artifact-metadata: write"
		"ref: \${{ github.sha }}"
		"\"\$REF_TYPE\" != tag"
		"\"\$FULL_REF\" != \"refs/tags/\$expected_tag\""
		"checked_out_sha=\$(git rev-parse HEAD)"
		"TESTED_IMAGE_ID: \${{ steps.tested.outputs.image-id }}"
		"\"\$published_id\" != \"\$TESTED_IMAGE_ID\""
		"PUSHED_DIGEST: \${{ steps.push.outputs.digest }}"
		"docker push \"\$IMAGE:\$VERSION\" 2>&1 | tee \"\$push_log\""
		"v?3\\.1\\.3(\\+dirty)?"
		"Generate SPDX SBOM"
	)
	for literal in "${literals[@]}"; do
		if ! require_literal "$path" "$literal"; then
			return 1
		fi
	done
	if (( $(grep -Fc 'bash scripts/ci/tiny-trainer-verify-image.sh' "$path") != 1 )); then
		printf '%s must verify trusted supply-chain identity after publication\n' "$path" >&2
		return 1
	fi
	if (( $(grep -Fc "tiny-trainer-manifest-identity.py \"\$TESTED_IMAGE_ID\"" "$path") != 2 )); then
		printf '%s must bind one image manifest to the tested config before reuse and signing\n' \
			"$path" >&2
		return 1
	fi
	for step in 'Sign image digest' 'Attest build provenance' 'Attest SBOM'; do
		if ! require_unconditional_step "$path" "$step"; then
			return 1
		fi
	done
	if grep -Fq 'cosign attach sbom' "$path"; then
		printf '%s uses deprecated Cosign SBOM attachments\n' "$path" >&2
		return 1
	fi
}

check_audit_toolchain() {
	local path="$1"
	local lock="$2"
	local audit_version
	local literal
	local -a literals=(
		'/src/scripts/ci/tiny-trainer-pip-audit.lock'
		'--require-hashes'
		'--no-deps'
		'--no-build'
	)
	for literal in "${literals[@]}"; do
		if ! require_literal "$path" "$literal"; then
			return 1
		fi
	done
	if grep -Eq '(^|[[:space:]])(uvx|pipx)([[:space:]]|$)' "$path"; then
		printf '%s resolves the vulnerability-audit environment at runtime\n' "$path" >&2
		return 1
	fi
	audit_version="$(sed -n 's/^PIP_AUDIT_VERSION=//p' "$path")"
	if [[ -z "$audit_version" ]] || ! grep -Fq "pip-audit==$audit_version" "$lock"; then
		printf '%s does not lock its declared pip-audit release\n' "$lock" >&2
		return 1
	fi
}

mkdir -p "$suite_root/bin"
cat >"$suite_root/bin/cosign" <<'EOF'
#!/usr/bin/env bash
{
	printf 'cosign'
	printf '\t%s' "$@"
	printf '\n'
} >>"$CALL_LOG"
[[ "${FAIL_TOOL:-}" != cosign ]]
EOF
cat >"$suite_root/bin/gh" <<'EOF'
#!/usr/bin/env bash
{
	printf 'gh'
	printf '\t%s' "$@"
	printf '\n'
} >>"$CALL_LOG"
[[ "${FAIL_TOOL:-}" != gh ]]
EOF
chmod +x "$suite_root/bin/cosign" "$suite_root/bin/gh"

image="ghcr.io/golusoris/tiny-gemma-trainer@sha256:$(printf 'a%.0s' {1..64})"
source_sha="$(printf 'b%.0s' {1..40})"
source_ref='refs/tags/tiny-trainers-v1.2.3'
call_log="$suite_root/calls"
PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref"

require_literal "$call_log" $'cosign\tverify'
require_literal "$call_log" $'--certificate-identity\thttps://github.com/golusoris/golusoris/.github/workflows/tiny-trainer-images.yml@refs/tags/tiny-trainers-v1.2.3'
require_literal "$call_log" $'--certificate-github-workflow-sha\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
require_literal "$call_log" $'--bundle-from-oci'
require_literal "$call_log" $'--source-ref\trefs/tags/tiny-trainers-v1.2.3'
require_literal "$call_log" $'--predicate-type\thttps://slsa.dev/provenance/v1'
require_literal "$call_log" $'--predicate-type\thttps://spdx.dev/Document/v2.3'
if (( $(grep -c '^gh' "$call_log") != 2 )); then
	printf 'expected provenance and SBOM verification calls\n' >&2
	exit 1
fi

cosign_version_pattern='^GitVersion:[[:space:]]+v?3\.1\.3(\+dirty)?[[:space:]]*$'
printf 'GitVersion: v3.1.3+dirty\n' | grep -Eq "$cosign_version_pattern"
if printf 'GitVersion: v3.1.4\n' | grep -Eq "$cosign_version_pattern"; then
	printf 'Cosign version check accepted a foreign release\n' >&2
	exit 1
fi
if printf 'GitVersion: v3.1.3+dirty.untrusted\n' | grep -Eq "$cosign_version_pattern"; then
	printf 'Cosign version check accepted foreign build metadata\n' >&2
	exit 1
fi

config_digest="sha256:$(printf 'c%.0s' {1..64})"
printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"%s"}}' \
	"$config_digest" | python3 -B "$repo_root/scripts/ci/tiny-trainer-manifest-identity.py" \
	"$config_digest" >/dev/null
printf '{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json","config":{"digest":"%s"}}' \
	"$config_digest" | python3 -B "$repo_root/scripts/ci/tiny-trainer-manifest-identity.py" \
	"$config_digest" >/dev/null
expect_failure bash -c "printf '%s' '{\"mediaType\":\"application/vnd.oci.image.index.v1+json\",\"manifests\":[]}' | python3 -B '$repo_root/scripts/ci/tiny-trainer-manifest-identity.py' '$config_digest'"
wrong_digest="sha256:$(printf 'd%.0s' {1..64})"
expect_failure bash -c "printf '%s' '{\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"config\":{\"digest\":\"$wrong_digest\"}}' | python3 -B '$repo_root/scripts/ci/tiny-trainer-manifest-identity.py' '$config_digest'"
if head -c 1048577 /dev/zero | \
	python3 -B "$repo_root/scripts/ci/tiny-trainer-manifest-identity.py" "$config_digest" \
	>/dev/null 2>&1; then
	printf 'oversized remote manifest accepted\n' >&2
	exit 1
fi

expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" refs/heads/main
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" FAIL_TOOL=cosign \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref"
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" FAIL_TOOL=gh \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref"

workflow="$repo_root/.github/workflows/tiny-trainer-images.yml"
check_workflow "$workflow"
check_audit_toolchain "$repo_root/scripts/ci/tiny-trainer-locks.sh" \
	"$repo_root/scripts/ci/tiny-trainer-pip-audit.lock"

mutable_audit="$suite_root/mutable-audit.sh"
cp "$repo_root/scripts/ci/tiny-trainer-locks.sh" "$mutable_audit"
printf '%s\n' 'uvx pip-audit' >>"$mutable_audit"
expect_failure check_audit_toolchain "$mutable_audit" \
	"$repo_root/scripts/ci/tiny-trainer-pip-audit.lock"

unlocked_audit="$suite_root/unlocked-audit.sh"
sed 's/^PIP_AUDIT_VERSION=.*/PIP_AUDIT_VERSION=9.9.9/' \
	"$repo_root/scripts/ci/tiny-trainer-locks.sh" >"$unlocked_audit"
expect_failure check_audit_toolchain "$unlocked_audit" \
	"$repo_root/scripts/ci/tiny-trainer-pip-audit.lock"

without_permission="$suite_root/without-permission.yml"
sed '/artifact-metadata: write/d' "$workflow" >"$without_permission"
expect_failure check_workflow "$without_permission"

without_identity="$suite_root/without-identity.yml"
sed "s/\"\$published_id\" != \"\$TESTED_IMAGE_ID\"/false/" \
	"$workflow" >"$without_identity"
expect_failure check_workflow "$without_identity"

deprecated_sbom="$suite_root/deprecated-sbom.yml"
cp "$workflow" "$deprecated_sbom"
printf '%s\n' '      # cosign attach sbom' >>"$deprecated_sbom"
expect_failure check_workflow "$deprecated_sbom"

skipped_resume="$suite_root/skipped-resume.yml"
sed '/- name: Sign image digest/a\        if: steps.existing.outputs.exists != '\''true'\''' \
	"$workflow" >"$skipped_resume"
expect_failure check_workflow "$skipped_resume"

printf 'tiny trainer publication policy tests passed\n'
