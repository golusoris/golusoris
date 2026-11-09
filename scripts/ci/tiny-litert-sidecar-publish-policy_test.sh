#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=scripts/ci/lib/policy-test.sh
. "$repo_root/scripts/ci/lib/policy-test.sh"
policy_suite_init "tiny-litert-sidecar-publish-policy"

expect_failure_msg() {
	local want="$1"
	shift
	local err status=0
	err="$("$@" 2>&1 >/dev/null)" || status=$?
	(( status != 0 )) || {
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	}
	grep -Fq -- "$want" <<<"$err" || {
		printf 'wrong failure for %s: %s\n' "$*" "$err" >&2
		return 1
	}
}

check_step_order() {
	local path="$1"
	shift
	local prev_name="workflow" prev_line=0 name line status=0
	for name in "$@"; do
		line="$(grep -n -F "      - name: $name" "$path" | head -n 1 | cut -d: -f1)" || status=$?
		if [[ -z "$line" ]] || (( line <= prev_line )); then
			printf '%s must order publish steps: %s before %s\n' \
				"$path" "$prev_name" "$name" >&2
			return 1
		fi
		prev_name="$name"
		prev_line="$line"
	done
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

check_smoke_skips_build() {
	local path="$1"
	if ! grep -A 10 'name: Smoke exact publish candidate' "$path" | grep -Fq 'SIDECAR_SKIP_BUILD: '"'1'"; then
		printf '%s must set SIDECAR_SKIP_BUILD=1 when smoking publish candidate\n' "$path" >&2
		return 1
	fi
}

check_manifest_identity() {
	local path="$1"
	if (( $(grep -Fc 'bash scripts/ci/tiny-trainer-verify-image.sh' "$path") != 1 )); then
		printf '%s must verify trusted supply-chain identity after publication\n' "$path" >&2
		return 1
	fi
	if ! grep -Fq 'tiny-litert-sidecar-image.yml tiny-litert-sidecar-' "$path"; then
		printf '%s must pass workflow filename and tag prefix to verifier script\n' "$path" >&2
		return 1
	fi
	if (( $(grep -Fc 'tiny-trainer-manifest-identity.py' "$path") != 4 )); then
		printf '%s must verify manifest identity before reuse and signing\n' "$path" >&2
		return 1
	fi
	if (( $(grep -Fc -- '--index' "$path") < 2 )); then
		printf '%s must validate multi-arch manifest list in both reuse and push paths\n' "$path" >&2
		return 1
	fi
}

check_publish_job_shape() {
	local path="$1"
	local publish_job push_step
	publish_job="$(awk '
		/^  publish:/ { active = 1; next }
		active && /^  [a-z0-9_-]+:/ { exit }
		active { print }
	' "$path")"
	if grep -Eq '^[[:space:]]+load:[[:space:]]*true' <<<"$publish_job"; then
		printf '%s publish job must not retain load: true\n' "$path" >&2
		return 1
	fi
	push_step="$(awk '
		/^      - name: Build and push multi-arch candidate by digest/ { active = 1; next }
		active && /^      - name:/ { exit }
		active { print }
	' "$path")"
	if [[ -z "$push_step" ]] || grep -Eq '^[[:space:]]+tags:' <<<"$push_step"; then
		printf '%s push step must not specify tags: before validation\n' "$path" >&2
		return 1
	fi
}

check_workflow() {
	local path="$1"
	local literal
	# shellcheck disable=SC2016 # Match the literal workflow expression; it must not expand here.
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
		"push-by-digest=true"
		"docker buildx imagetools create -t \"\$IMAGE:\$VERSION\" \"\$IMAGE@\$digest\""
		"--config \"\$platform\""
		"docker pull --platform linux/amd64 \"\$IMAGE@\$digest\""
		"PUSHED_DIGEST: \${{ steps.push.outputs.digest }}"
		"platforms: linux/amd64,linux/arm64"
		"for platform in linux/amd64 linux/arm64; do"
		'v?${COSIGN_VERSION//./\\.}(\+dirty)?'
		"Generate SPDX SBOM"
	)
	for literal in "${literals[@]}"; do
		if ! require_literal "$path" "$literal"; then
			return 1
		fi
	done
	check_publish_job_shape "$path" || return 1
	check_step_order "$path" \
		"Select candidate digest" "Smoke exact publish candidate" \
		"Verify release identity of every platform" "Tag verified candidate" \
		"Verify immutable published image" "Sign image digest" || return 1
	check_smoke_skips_build "$path" || return 1
	check_manifest_identity "$path" || return 1
	for step in 'Sign image digest' 'Attest build provenance' 'Attest SBOM' \
		'Verify published supply-chain identity' \
		'Smoke exact publish candidate' 'Verify release identity of every platform'; do
		if ! require_unconditional_step "$path" "$step"; then
			return 1
		fi
	done
	if grep -Fq 'cosign attach sbom' "$path"; then
		printf '%s uses deprecated Cosign SBOM attachments\n' "$path" >&2
		return 1
	fi
	if grep -Fq 'ghcr.io/golusoris/' "$path"; then
		printf '%s must derive repository dynamically instead of hardcoding namespace\n' "$path" >&2
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

without_config="$suite_root/without-config.yml"
sed '/--config/d' "$workflow" >"$without_config"
# shellcheck disable=SC2016 # Flag and variable pattern match literal string.
expect_failure_msg '--config "$platform"' check_workflow "$without_config"

conditional_smoke="$suite_root/conditional-smoke.yml"
sed '/- name: Smoke exact publish candidate/a\        if: steps.existing.outputs.exists != '\''true'\''' \
	"$workflow" >"$conditional_smoke"
expect_failure_msg 'must run Smoke exact publish candidate on new and resumed publication' \
	check_workflow "$conditional_smoke"

with_load_true="$suite_root/with-load-true.yml"
sed '/name: Build and push multi-arch candidate by digest/a\          load: true' \
	"$workflow" >"$with_load_true"
expect_failure_msg 'publish job must not retain load: true' check_workflow "$with_load_true"

without_push_by_digest="$suite_root/without-push-by-digest.yml"
sed 's/push-by-digest=true/push-by-digest=false/' "$workflow" >"$without_push_by_digest"
expect_failure_msg 'push-by-digest=true' check_workflow "$without_push_by_digest"

tag_before_identity="$suite_root/tag-before-identity.yml"
awk '
	/^      - name: Tag verified candidate/ { in_tag = 1 }
	in_tag && /^      - name:/ && !/^      - name: Tag verified candidate/ { in_tag = 0 }
	in_tag { tag_block = tag_block $0 "\n"; next }
	{ lines[++n] = $0 }
	END {
		for (i = 1; i <= n; i++) {
			if (lines[i] ~ /^      - name: Verify release identity of every platform/) {
				printf "%s", tag_block
			}
			print lines[i]
		}
	}
' "$workflow" >"$tag_before_identity"
expect_failure_msg 'must order publish steps' check_workflow "$tag_before_identity"

amd64_only_identity="$suite_root/amd64-only-identity.yml"
sed 's#for platform in linux/amd64 linux/arm64; do#for platform in linux/amd64; do#' \
	"$workflow" >"$amd64_only_identity"
expect_failure_msg 'linux/amd64 linux/arm64' check_workflow "$amd64_only_identity"

without_smoke="$suite_root/without-smoke.yml"
sed '/name: Smoke exact publish candidate/,+4d' "$workflow" >"$without_smoke"
expect_failure check_workflow "$without_smoke"

with_hardcoded_repo="$suite_root/with-hardcoded-repo.yml"
# shellcheck disable=SC2016 # The planted line keeps $GITHUB_ENV literal.
sed 's/echo "IMAGE_REPO=.*/echo "IMAGE_REPO=ghcr.io\/golusoris\/tiny-litert-sidecar" >> "$GITHUB_ENV"/' \
	"$workflow" >"$with_hardcoded_repo"
expect_failure check_workflow "$with_hardcoded_repo"

printf 'tiny litert sidecar publication policy tests passed\n'
