#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/tiny-trainer-publish-policy.XXXXXX")"
helper="$repo_root/scripts/ci/tiny-trainer-manifest-identity.py"
readonly repo_root suite_root helper
trap 'find "$suite_root" -depth -delete' EXIT

sha256_digest() {
	python3 -I -c 'import hashlib,sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
}

expect_failure() {
	local status=0
	"$@" >/dev/null 2>&1 || status=$?
	if (( status == 0 )); then
		printf 'expected failure: %s\n' "$*" >&2
		return 1
	fi
}

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
		line="$(grep -n -F "      - name: $name" "$path" | sed -n '1p' | cut -d: -f1)" || status=$?
		if [[ -z "$line" ]] || (( line <= prev_line )); then
			printf '%s must order publish steps: %s before %s\n' \
				"$path" "$prev_name" "$name" >&2
			return 1
		fi
		prev_name="$name"
		prev_line="$line"
	done
}

check_manifest_json() {
	local json="$1"
	shift
	printf '%s' "$json" | python3 -B "$helper" "$@"
}

check_config_json() {
	local json="$1"
	shift
	python3 -B "$helper" --config "$@" <<<"$json"
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
		"artifact-metadata: write"
		"ref: \${{ github.sha }}"
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
	if (( $(grep -Fc 'bash scripts/ci/tiny-trainer-verify-image.sh' "$path") != 1 )); then
		printf '%s must verify trusted supply-chain identity after publication\n' "$path" >&2
		return 1
	fi
	if (( $(grep -Fc 'tiny-trainer-manifest-identity.py' "$path") != 4 )); then
		printf '%s must verify manifest identity before reuse and signing\n' \
			"$path" >&2
		return 1
	fi
	if (( $(grep -Fc -- '--index' "$path") < 2 )); then
		printf '%s must validate multi-arch manifest list in both reuse and push paths\n' \
			"$path" >&2
		return 1
	fi
	for step in 'Sign image digest' 'Attest build provenance' 'Attest SBOM' \
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
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-trainer-images.yml tiny-trainers-

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

# shellcheck source=tools/tool-versions.env
. "$repo_root/tools/tool-versions.env"
cosign_version_pattern="^GitVersion:[[:space:]]+v?${COSIGN_VERSION//./\\.}(\\+dirty)?[[:space:]]*$"
grep -Eq "$cosign_version_pattern" <<<"GitVersion: v${COSIGN_VERSION}+dirty"
if grep -Eq "$cosign_version_pattern" <<<"GitVersion: v99.99.99"; then
	printf 'Cosign version check accepted a foreign release\n' >&2
	exit 1
fi
if grep -Eq "$cosign_version_pattern" <<<"GitVersion: v${COSIGN_VERSION}+dirty.untrusted"; then
	printf 'Cosign version check accepted foreign build metadata\n' >&2
	exit 1
fi

config_digest="sha256:$(printf 'c%.0s' {1..64})"
check_manifest_json \
	"{\"schemaVersion\":2,\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"config\":{\"digest\":\"$config_digest\"}}" \
	"$config_digest" >/dev/null
check_manifest_json \
	"{\"schemaVersion\":2,\"mediaType\":\"application/vnd.docker.distribution.manifest.v2+json\",\"config\":{\"digest\":\"$config_digest\"}}" \
	"$config_digest" >/dev/null
expect_failure check_manifest_json '{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}' "$config_digest"

valid_index='{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:'"$(printf 'e%.0s' {1..64})"'","platform":{"architecture":"amd64","os":"linux"}},{"digest":"sha256:'"$(printf 'f%.0s' {1..64})"'","platform":{"architecture":"arm64","os":"linux"}}]}'
valid_index_digest="sha256:$(printf '%s' "$valid_index" | sha256_digest)"
check_manifest_json "$valid_index" "$valid_index_digest" --index >/dev/null
expect_failure check_manifest_json "$valid_index" "$config_digest" --index
amd64_only_index='{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:'"$(printf 'e%.0s' {1..64})"'","platform":{"architecture":"amd64","os":"linux"}}]}'
amd64_only_digest="sha256:$(printf '%s' "$amd64_only_index" | sha256_digest)"
expect_failure check_manifest_json "$amd64_only_index" "$amd64_only_digest" --index
empty_index='{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}'
empty_index_digest="sha256:$(printf '%s' "$empty_index" | sha256_digest)"
expect_failure check_manifest_json "$empty_index" "$empty_index_digest" --index
expect_failure python3 -B "$helper" --index
bad_platform_index='{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:'"$(printf 'e%.0s' {1..64})"'","platform":"not-a-dict"}]}'
bad_platform_digest="sha256:$(printf '%s' "$bad_platform_index" | sha256_digest)"
expect_failure check_manifest_json "$bad_platform_index" "$bad_platform_digest" --index

wrong_digest="sha256:$(printf 'd%.0s' {1..64})"
expect_failure check_manifest_json "{\"mediaType\":\"application/vnd.oci.image.manifest.v1+json\",\"config\":{\"digest\":\"$wrong_digest\"}}" "$config_digest"
containerd_manifest=$(printf '{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"digest":"%s"}}' "$config_digest")
containerd_digest="sha256:$(printf '%s' "$containerd_manifest" | sha256_digest)"
check_manifest_json "$containerd_manifest" "$containerd_digest" >/dev/null
different_digest="sha256:$(printf '%s ' "$containerd_manifest" | sha256_digest)"
expect_failure check_manifest_json "$containerd_manifest" "$different_digest"
index_manifest='{"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}'
index_digest="sha256:$(printf '%s' "$index_manifest" | sha256_digest)"
expect_failure check_manifest_json "$index_manifest" "$index_digest"
if head -c 1048577 /dev/zero | \
	python3 -B "$helper" "$config_digest" \
	>/dev/null 2>&1; then
	printf 'oversized remote manifest accepted\n' >&2
	exit 1
fi

expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" refs/heads/main \
	tiny-trainer-images.yml tiny-trainers-
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" FAIL_TOOL=cosign \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-trainer-images.yml tiny-trainers-
expect_failure env PATH="$suite_root/bin:$PATH" CALL_LOG="$call_log" FAIL_TOOL=gh \
	bash "$repo_root/scripts/ci/tiny-trainer-verify-image.sh" \
	"$image" golusoris/golusoris "$source_sha" "$source_ref" \
	tiny-trainer-images.yml tiny-trainers-

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

test_source="https://github.com/golusoris/golusoris"
test_rev="$(printf 'a%.0s' {1..40})"
test_ver="v1.2.3"
valid_amd64_config=$(printf '{"architecture":"amd64","os":"linux","config":{"Labels":{"org.opencontainers.image.source":"%s","org.opencontainers.image.revision":"%s","org.opencontainers.image.version":"%s"}}}' \
	"$test_source" "$test_rev" "$test_ver")
valid_arm64_config=$(printf '{"architecture":"arm64","os":"linux","config":{"Labels":{"org.opencontainers.image.source":"%s","org.opencontainers.image.revision":"%s","org.opencontainers.image.version":"%s"}}}' \
	"$test_source" "$test_rev" "$test_ver")

# Valid amd64 and arm64 configs pass
check_config_json "$valid_amd64_config" linux/amd64 "$test_source" "$test_rev" "$test_ver" >/dev/null
check_config_json "$valid_arm64_config" linux/arm64 "$test_source" "$test_rev" "$test_ver" >/dev/null

# arm64 revision mismatch FAIL
mismatch_rev="$(printf 'b%.0s' {1..40})"
expect_failure_msg "revision label mismatch" \
	check_config_json "$valid_arm64_config" linux/arm64 "$test_source" "$mismatch_rev" "$test_ver"

# missing Labels FAIL
missing_labels_config='{"architecture":"amd64","os":"linux","config":{}}'
expect_failure_msg "Labels is missing or not an object" \
	check_config_json "$missing_labels_config" linux/amd64 "$test_source" "$test_rev" "$test_ver"

# Labels not dict FAIL
labels_not_dict_config='{"architecture":"amd64","os":"linux","config":{"Labels":"not-a-dict"}}'
expect_failure_msg "Labels is missing or not an object" \
	check_config_json "$labels_not_dict_config" linux/amd64 "$test_source" "$test_rev" "$test_ver"

# platform mismatch (amd64 config checked as linux/arm64) FAIL
expect_failure_msg "platform mismatch" \
	check_config_json "$valid_amd64_config" linux/arm64 "$test_source" "$test_rev" "$test_ver"

# unknown platform linux/s390x FAIL
expect_failure_msg "unsupported platform" \
	check_config_json "$valid_amd64_config" linux/s390x "$test_source" "$test_rev" "$test_ver"

# 4 or 6 args -> rc 2
status_4=0
python3 -B "$helper" \
	--config linux/amd64 "$test_source" "$test_rev" >/dev/null 2>&1 || status_4=$?
if (( status_4 != 2 )); then
	printf 'expected rc 2 for 4 args, got %d\n' "$status_4" >&2
	exit 1
fi
status_6=0
python3 -B "$helper" \
	--config linux/amd64 "$test_source" "$test_rev" "$test_ver" "extra" >/dev/null 2>&1 || status_6=$?
if (( status_6 != 2 )); then
	printf 'expected rc 2 for 6 args, got %d\n' "$status_6" >&2
	exit 1
fi

# revision not 40-hex FAIL
expect_failure_msg "revision is not a 40-character hex SHA-1" \
	check_config_json "$valid_amd64_config" linux/amd64 "$test_source" 'not-a-40-hex-revision' "$test_ver"

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

deprecated_sbom="$suite_root/deprecated-sbom.yml"
cp "$workflow" "$deprecated_sbom"
printf '%s\n' '      # cosign attach sbom' >>"$deprecated_sbom"
expect_failure check_workflow "$deprecated_sbom"

skipped_resume="$suite_root/skipped-resume.yml"
sed '/- name: Sign image digest/a\        if: steps.existing.outputs.exists != '\''true'\''' \
	"$workflow" >"$skipped_resume"
expect_failure check_workflow "$skipped_resume"

with_hardcoded_repo="$suite_root/with-hardcoded-repo.yml"
sed 's/image: tiny-gemma-trainer/image: ghcr.io\/golusoris\/tiny-gemma-trainer/' \
	"$workflow" >"$with_hardcoded_repo"
expect_failure check_workflow "$with_hardcoded_repo"

printf 'tiny trainer publication policy tests passed\n'
