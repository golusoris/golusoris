#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/tiny-litert-sidecar-publish-policy.XXXXXX")"
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

check_smoke_before_push() {
	local path="$1"
	local smoke_line push_line
	smoke_line="$(grep -n 'name: Smoke exact publish candidate' "$path" | cut -d: -f1)"
	push_line="$(grep -n 'name: Push tested image' "$path" | cut -d: -f1)"
	if [[ -z "$smoke_line" || -z "$push_line" || "$smoke_line" -ge "$push_line" ]]; then
		printf '%s must smoke the exact candidate image before pushing to registry\n' "$path" >&2
		return 1
	fi
	if ! grep -A 10 'name: Smoke exact publish candidate' "$path" | grep -Fq 'SIDECAR_SKIP_BUILD: '"'1'"; then
		printf '%s must set SIDECAR_SKIP_BUILD=1 when smoking publish candidate\n' "$path" >&2
		return 1
	fi
}

check_workflow() {
	local path="$1"
	local literal
	local -a literals=(
		"environment: tiny-litert-sidecar-publish"
		"vars.TINY_LITERT_SIDECAR_PUBLISH_ENABLED"
		"artifact-metadata: write"
		"attestations: write"
		"contents: read"
		"id-token: write"
		"packages: write"
		"ref: \${{ github.sha }}"
		"- 'tiny-litert-sidecar-v*.*.*'"
		"version=\${REF_NAME#tiny-litert-sidecar-}"
		"expected_tag=\"tiny-litert-sidecar-\$version\""
		"\"\$REF_TYPE\" != tag"
		"\"\$FULL_REF\" != \"refs/tags/\$expected_tag\""
		"checked_out_sha=\$(git rev-parse HEAD)"
		"TESTED_IMAGE_ID: \${{ steps.tested.outputs.image-id }}"
		"\"\$published_id\" != \"\$TESTED_IMAGE_ID\""
		"PUSHED_DIGEST: \${{ steps.push.outputs.digest }}"
		"docker push \"\$IMAGE:\$VERSION\" 2>&1 | tee \"\$push_log\""
		"v?3\\.1\\.4(\\+dirty)?"
		"Generate SPDX SBOM"
	)
	for literal in "${literals[@]}"; do
		if ! require_literal "$path" "$literal"; then
			return 1
		fi
	done
	check_smoke_before_push "$path" || return 1
	if (( $(grep -Fc 'bash scripts/ci/tiny-trainer-verify-image.sh' "$path") != 1 )); then
		printf '%s must verify trusted supply-chain identity after publication\n' "$path" >&2
		return 1
	fi
	if ! grep -Fq 'tiny-litert-sidecar-image.yml tiny-litert-sidecar-' "$path"; then
		printf '%s must pass workflow filename and tag prefix to verifier script\n' "$path" >&2
		return 1
	fi
	if (( $(grep -Fc "tiny-trainer-manifest-identity.py \"\$TESTED_IMAGE_ID\"" "$path") != 2 )); then
		printf '%s must bind one image manifest to the tested config before reuse and signing\n' \
			"$path" >&2
		return 1
	fi
	for step in 'Sign image digest' 'Attest build provenance' 'Attest SBOM' 'Verify published supply-chain identity'; do
		if ! require_unconditional_step "$path" "$step"; then
			return 1
		fi
	done
	if grep -Fq 'cosign attach sbom' "$path"; then
		printf '%s uses deprecated Cosign SBOM attachments\n' "$path" >&2
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

image="ghcr.io/golusoris/tiny-litert-sidecar@sha256:$(printf 'a%.0s' {1..64})"
source_sha="$(printf 'b%.0s' {1..40})"
source_ref='refs/tags/tiny-litert-sidecar-v1.2.3'
call_log="$suite_root/calls"
PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-litert-sidecar-image.yml tiny-litert-sidecar-

require_literal "$call_log" $'cosign\tverify'
require_literal "$call_log" $'--certificate-identity\thttps://github.com/golusoris/golusoris/.github/workflows/tiny-litert-sidecar-image.yml@refs/tags/tiny-litert-sidecar-v1.2.3'
require_literal "$call_log" $'--certificate-github-workflow-sha\tbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
require_literal "$call_log" $'--bundle-from-oci'
require_literal "$call_log" $'--signer-workflow\tgolusoris/golusoris/.github/workflows/tiny-litert-sidecar-image.yml'
require_literal "$call_log" $'--source-ref\trefs/tags/tiny-litert-sidecar-v1.2.3'
require_literal "$call_log" $'--predicate-type\thttps://slsa.dev/provenance/v1'
require_literal "$call_log" $'--predicate-type\thttps://spdx.dev/Document/v2.3'
if (( $(grep -c '^gh' "$call_log") != 2 )); then
	printf 'expected provenance and SBOM verification calls\n' >&2
	exit 1
fi

expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" refs/heads/main \
	tiny-litert-sidecar-image.yml tiny-litert-sidecar-
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" refs/tags/tiny-trainers-v1.2.3 \
	tiny-litert-sidecar-image.yml tiny-litert-sidecar-
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-litert-sidecar-image.yml untrusted-prefix-
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" FAIL_TOOL=cosign \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-litert-sidecar-image.yml tiny-litert-sidecar-
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" FAIL_TOOL=gh \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-litert-sidecar-image.yml tiny-litert-sidecar-

workflow="$repo_root/.github/workflows/tiny-litert-sidecar-image.yml"
check_workflow "$workflow"

# Planted negatives on mutated workflow copies
without_environment="$suite_root/without-environment.yml"
sed '/environment: tiny-litert-sidecar-publish/d' "$workflow" >"$without_environment"
expect_failure check_workflow "$without_environment"

conditional_sign="$suite_root/conditional-sign.yml"
sed '/name: Sign image digest/a \        if: always()' "$workflow" >"$conditional_sign"
expect_failure check_workflow "$conditional_sign"

bad_verifier_prefix="$suite_root/bad-verifier-prefix.yml"
sed 's/tiny-litert-sidecar-image.yml tiny-litert-sidecar-/tiny-litert-sidecar-image.yml bad-prefix-/' \
	"$workflow" >"$bad_verifier_prefix"
expect_failure check_workflow "$bad_verifier_prefix"

without_permission="$suite_root/without-permission.yml"
sed '/artifact-metadata: write/d' "$workflow" >"$without_permission"
expect_failure check_workflow "$without_permission"

without_identity="$suite_root/without-identity.yml"
sed "s/\"\$published_id\" != \"\$TESTED_IMAGE_ID\"/false/" \
	"$workflow" >"$without_identity"
expect_failure check_workflow "$without_identity"

without_smoke="$suite_root/without-smoke.yml"
sed '/name: Smoke exact publish candidate/,+4d' "$workflow" >"$without_smoke"
expect_failure check_workflow "$without_smoke"

printf 'tiny litert sidecar publication policy tests passed\n'
