#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"

for value in \
	"$GOFUMPT_VERSION" \
	"$GOLANGCI_LINT_VERSION" \
	"$GOSEC_VERSION" \
	"$GOVULNCHECK_VERSION" \
	"$GO_COMPAT_TOOL_VERSION" \
	"$LEFTHOOK_VERSION" \
	"$GCI_VERSION" \
	"$MOCKERY_VERSION" \
	"$AIR_VERSION" \
	"$SQLC_VERSION" \
	"$OGEN_VERSION" \
	"$MIGRATE_VERSION" \
	"$CONTROLLER_GEN_VERSION" \
	"$GO_MUTESTING_VERSION" \
	"$HELM_VERSION" \
	"v$SHELLCHECK_VERSION" \
	"$ACTIONLINT_VERSION" \
	"v$GORELEASER_VERSION" \
	"v$SYFT_VERSION" \
	"v$TERRAFORM_VERSION" \
	"v$MKDOCS_MATERIAL_VERSION" \
	"$KUBECONFORM_VERSION" \
	"$KUBERNETES_SCHEMA_VERSION" \
	"v$REUSE_VERSION" \
	"v$SEMGREP_VERSION" \
	"v$TRIVY_VERSION" \
	"v$RUFF_VERSION" \
	"v$CLANG_VERSION"; do
	if [[ ! "$value" =~ ^v[0-9] ]]; then
		printf 'invalid repository tool version: %s\n' "$value" >&2
		exit 1
	fi
done

if [[ ! "$SEMGREP_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid Semgrep image digest: %s\n' "$SEMGREP_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ ! "$TERRAFORM_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid Terraform image digest: %s\n' "$TERRAFORM_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ ! "$MKDOCS_MATERIAL_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid MkDocs Material image digest: %s\n' \
		"$MKDOCS_MATERIAL_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ ! "$KUBECONFORM_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid kubeconform image digest: %s\n' "$KUBECONFORM_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ ! "$REUSE_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid REUSE image digest: %s\n' "$REUSE_IMAGE_DIGEST" >&2
	exit 1
fi
for schema_commit in "$KUBERNETES_SCHEMA_COMMIT" "$CRD_SCHEMA_COMMIT"; do
	if [[ ! "$schema_commit" =~ ^[0-9a-f]{40}$ ]]; then
		printf 'invalid schema commit: %s\n' "$schema_commit" >&2
		exit 1
	fi
done
if [[ ! "$TRIVY_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid Trivy image digest: %s\n' "$TRIVY_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ ! "$RUFF_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid Ruff image digest: %s\n' "$RUFF_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ ! "$CLANG_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]; then
	printf 'invalid Clang image digest: %s\n' "$CLANG_IMAGE_DIGEST" >&2
	exit 1
fi
if [[ "$CLANG_IMAGE_TAG" != 22-bookworm ]]; then
	printf 'unexpected Clang image tag: %s\n' "$CLANG_IMAGE_TAG" >&2
	exit 1
fi

if git -C "$repo_root" grep -nE 'go (install|run) [^[:space:]]+@latest' -- . \
	':(exclude)docs/upstream/**' \
	':(exclude).config/hiss/testdata/**'; then
	printf 'tracked executable guidance bypasses repository tool pins\n' >&2
	exit 1
fi

for variable in \
	LEFTHOOK_VERSION \
	GCI_VERSION \
	MOCKERY_VERSION \
	AIR_VERSION \
	SQLC_VERSION \
	OGEN_VERSION \
	MIGRATE_VERSION \
	CONTROLLER_GEN_VERSION \
	GO_MUTESTING_VERSION \
	HELM_VERSION \
	ACTIONLINT_VERSION; do
	if ! grep -Fq "\$(${variable})" "$repo_root/Makefile"; then
		printf 'tools-bootstrap does not consume %s\n' "$variable" >&2
		exit 1
	fi
done

if ! grep -Fq "github.com/vektra/mockery/v3@\$(MOCKERY_VERSION)" "$repo_root/Makefile"; then
	printf 'tools-bootstrap does not install the Mockery v3 authority\n' >&2
	exit 1
fi
if [[ "$(grep -Fc "\$(MOCKERY) --config \$(MOCKERY_CONFIG)" "$repo_root/tools/Makefile.shared")" -ne 2 ]]; then
	printf 'generation targets do not consume the canonical Mockery config\n' >&2
	exit 1
fi
if grep -Eq '^[[:space:]]*-\$\((SQLC|OGEN)\)|\|\| true' "$repo_root/tools/Makefile.shared"; then
	printf 'generation target suppresses generator failure\n' >&2
	exit 1
fi

direct_workflows=(
	"$repo_root/.github/workflows/ci.yml"
	"$repo_root/.gitea/workflows/ci.yml"
	"$repo_root/.gitea/workflows/security-scan.yml"
)
if grep -En 'go install [^[:space:]]+@v[0-9]' "${direct_workflows[@]}"; then
	printf 'direct workflow bypasses tools/tool-versions.env\n' >&2
	exit 1
fi

cosign_version_pattern='^GitVersion:[[:space:]]+v?3\.1\.3(\+dirty)?[[:space:]]*$'
cosign_version_guard="cosign version | grep -Eq '$cosign_version_pattern'"
cosign_version_workflows=(
	"$repo_root/.github/workflows/rebuild-on-base.yml"
	"$repo_root/.github/workflows/release-go.yml"
	"$repo_root/.github/workflows/release-tool-contract.yml"
	"$repo_root/.github/workflows/release.yml"
	"$repo_root/.github/workflows/tiny-trainer-images.yml"
	"$repo_root/.github/workflows/verify-provenance.yml"
)
for workflow in "${cosign_version_workflows[@]}"; do
	if ! grep -Fq "$cosign_version_guard" "$workflow"; then
		printf 'workflow bypasses the exact Cosign runner contract: %s\n' \
			"$workflow" >&2
		exit 1
	fi
done
if grep -rHnF 'cosign version' \
	"$repo_root/.github/workflows" "$repo_root/scripts" | \
	grep -Fv "$repo_root/scripts/ci/tool-versions_test.sh:" | \
	grep -Fv "$cosign_version_guard"; then
	printf 'Cosign version guard diverges from the exact runner contract\n' >&2
	exit 1
fi
for version in 'v3.1.3' 'v3.1.3+dirty'; do
	if ! printf 'GitVersion: %s\n' "$version" | grep -Eq "$cosign_version_pattern"; then
		printf 'Cosign runner contract rejects supported version: %s\n' "$version" >&2
		exit 1
	fi
done
for version in \
	'v3.1.4' \
	'v3.1.3+other' \
	'v3.1.3+dirty.extra' \
	'v3.1.3+dirty+other' \
	'v3.1.3-dirty' \
	'v3.1.30'; do
	if printf 'GitVersion: %s\n' "$version" | grep -Eq "$cosign_version_pattern"; then
		printf 'Cosign runner contract accepts unsupported version: %s\n' "$version" >&2
		exit 1
	fi
done

goreleaser_version_pattern="GitVersion:[[:space:]]+v?${GORELEASER_VERSION//./\\.}([[:space:]]|$)"
syft_version_pattern="Version:[[:space:]]+${SYFT_VERSION//./\\.}([[:space:]]|$)"
goreleaser_version_guard="goreleaser --version | grep -Eq '$goreleaser_version_pattern'"
syft_version_guard="syft version | grep -Eq '$syft_version_pattern'"
goreleaser_version_workflows=(
	"$repo_root/.github/workflows/release-go.yml"
	"$repo_root/.github/workflows/release-tool-contract.yml"
	"$repo_root/.github/workflows/release.yml"
)
syft_version_workflows=(
	"$repo_root/.github/workflows/rebuild-on-base.yml"
	"$repo_root/.github/workflows/release-go.yml"
	"$repo_root/.github/workflows/release-tool-contract.yml"
	"$repo_root/.github/workflows/release.yml"
	"$repo_root/.github/workflows/tiny-trainer-images.yml"
)
for workflow in "${goreleaser_version_workflows[@]}"; do
	if ! grep -Fq "$goreleaser_version_guard" "$workflow"; then
		printf 'workflow bypasses the locked GoReleaser runner contract: %s\n' \
			"$workflow" >&2
		exit 1
	fi
done
for workflow in "${syft_version_workflows[@]}"; do
	if ! grep -Fq "$syft_version_guard" "$workflow"; then
		printf 'workflow bypasses the locked Syft runner contract: %s\n' \
			"$workflow" >&2
		exit 1
	fi
done
if grep -rHnF 'goreleaser --version' \
	"$repo_root/.github/workflows" "$repo_root/scripts" | \
	grep -Fv "$repo_root/scripts/ci/tool-versions_test.sh:" | \
	grep -Fv "$goreleaser_version_guard"; then
	printf 'GoReleaser version guard diverges from the locked runner contract\n' >&2
	exit 1
fi
if grep -rHnF 'syft version' \
	"$repo_root/.github/workflows" "$repo_root/scripts" | \
	grep -Fv "$repo_root/scripts/ci/tool-versions_test.sh:" | \
	grep -Fv "$syft_version_guard"; then
	printf 'Syft version guard diverges from the locked runner contract\n' >&2
	exit 1
fi
for version in "$GORELEASER_VERSION" "v$GORELEASER_VERSION"; do
	if ! printf 'GitVersion: %s\n' "$version" | grep -Eq "$goreleaser_version_pattern"; then
		printf 'GoReleaser runner contract rejects supported version: %s\n' \
			"$version" >&2
		exit 1
	fi
done
if printf 'GitVersion: %s\n' "${GORELEASER_VERSION}0" | \
	grep -Eq "$goreleaser_version_pattern"; then
	printf 'GoReleaser runner contract accepts a version suffix\n' >&2
	exit 1
fi
if ! printf 'Version: %s\n' "$SYFT_VERSION" | grep -Eq "$syft_version_pattern"; then
	printf 'Syft runner contract rejects the locked version\n' >&2
	exit 1
fi
if printf 'Version: %s\n' "${SYFT_VERSION}0" | grep -Eq "$syft_version_pattern"; then
	printf 'Syft runner contract accepts a version suffix\n' >&2
	exit 1
fi

sbom_attestation_workflows=(
	"$repo_root/.github/workflows/rebuild-on-base.yml"
	"$repo_root/.github/workflows/release-go.yml"
	"$repo_root/.github/workflows/tiny-trainer-images.yml"
)
for workflow in "${sbom_attestation_workflows[@]}"; do
	if ! grep -Fq 'artifact-metadata: write' "$workflow" || \
		! grep -Fq 'actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6' "$workflow" || \
		! grep -Fq 'sbom-path:' "$workflow" || \
		! grep -Fq 'push-to-registry: true' "$workflow"; then
		printf 'workflow does not publish a signed OCI SBOM attestation: %s\n' \
			"$workflow" >&2
		exit 1
	fi
done
attestation_workflow_paths=(
	'.github/workflows/rebuild-on-base.yml'
	'.github/workflows/release-go.yml'
	'.github/workflows/release.yml'
	'.github/workflows/sbom.yml'
	'.github/workflows/tiny-trainer-images.yml'
)
for relative_workflow in "${attestation_workflow_paths[@]}"; do
	workflow="$repo_root/$relative_workflow"
	if ! grep -Fq 'artifact-metadata: write' "$workflow"; then
		printf 'attestation workflow lacks artifact metadata permission: %s\n' \
			"$workflow" >&2
		exit 1
	fi
done
mapfile -t discovered_attestation_workflows < <(
	cd "$repo_root"
	rg -l 'uses:[[:space:]]+actions/attest(-build-provenance)?@' \
		.github/workflows -g '*.yml' | sort
)
if [[ "${discovered_attestation_workflows[*]}" != "${attestation_workflow_paths[*]}" ]]; then
	printf 'attestation workflow inventory changed: found %s, expected %s\n' \
		"${discovered_attestation_workflows[*]}" "${attestation_workflow_paths[*]}" >&2
	exit 1
fi
if git -C "$repo_root" grep -nF 'cosign attach sbom' -- \
	'.github/workflows/*.yml' '.gitea/workflows/*.yml' 'scripts/**/*.sh' \
	':(exclude)scripts/ci/tool-versions_test.sh' \
	':(exclude)scripts/ci/tiny-trainer-publish-policy_test.sh'; then
	printf 'workflow uses deprecated Cosign SBOM attachment instead of attestation\n' >&2
	exit 1
fi

custom_semgrep_workflows=(
	"$repo_root/.github/workflows/ci.yml"
	"$repo_root/.gitea/workflows/ci.yml"
	"$repo_root/.gitea/workflows/security-scan.yml"
)
for workflow in "${custom_semgrep_workflows[@]}"; do
	if ! grep -Fq 'scripts/ci/semgrep-scan.sh' "$workflow"; then
		printf 'workflow bypasses the digest-pinned custom Semgrep wrapper: %s\n' \
			"$workflow" >&2
		exit 1
	fi
done
if ! grep -Fq 'SEMGREP_IMAGE_DIGEST' "$repo_root/.github/workflows/security-scan.yml"; then
	printf 'registry Semgrep workflow does not consume the image digest authority\n' >&2
	exit 1
fi
if git -C "$repo_root" grep -nE 'semgrep(==|/semgrep:)[0-9]' -- \
	'.github/workflows/*.yml' '.gitea/workflows/*.yml' 'scripts/**/*.sh'; then
	printf 'tracked Semgrep command bypasses tools/tool-versions.env\n' >&2
	exit 1
fi
if git -C "$repo_root" grep -nF 'pipx run' -- \
	'.github/workflows/*.yml' '.gitea/workflows/*.yml' 'scripts/**/*.sh' | grep -F semgrep; then
	printf 'Semgrep uses an unlocked pipx dependency graph\n' >&2
	exit 1
fi
if git -C "$repo_root" grep -nE '(pip(x|3)?[^[:cntrl:]]+(install|run)[^[:cntrl:]]+reuse|python3?[[:space:]]+-m[[:space:]]+reuse)' -- \
	'.github/workflows/*.yml' '.gitea/workflows/*.yml' 'scripts/**/*.sh' \
	':(exclude)scripts/ci/tool-versions_test.sh'; then
	printf 'REUSE bypasses the immutable image authority\n' >&2
	exit 1
fi
if git -C "$repo_root" grep -nE 'pip(3)?[[:space:]]+install[^[:cntrl:]]+mkdocs-material' -- \
	'.github/workflows/*.yml' '.gitea/workflows/*.yml' 'scripts/**/*.sh' \
	':(exclude)scripts/ci/tool-versions_test.sh'; then
	printf 'MkDocs uses an unlocked pip dependency graph\n' >&2
	exit 1
fi
if ! grep -Fq 'scripts/ci/mkdocs-build.sh --site-dir site' \
	"$repo_root/.github/workflows/docs.yml"; then
	printf 'Pages workflow bypasses the immutable MkDocs authority\n' >&2
	exit 1
fi

if ! grep -Fq 'scripts/ci/trivy-scan.sh' "$repo_root/.github/workflows/security-scan.yml" || \
	! grep -Fq 'scripts/ci/trivy-scan.sh' "$repo_root/.gitea/workflows/security-scan.yml" || \
	! grep -Fq 'trivy-scan' "$repo_root/Makefile"; then
	printf 'Trivy authority is not consumed by local and hosted security gates\n' >&2
	exit 1
fi

for gate in semgrep-scan shellcheck actionlint terraform-validate mkdocs-build kubeconform reuse-lint; do
	if ! grep -Fq "scripts/ci/${gate}.sh" "$repo_root/.github/workflows/ci.yml" || \
		! grep -Fq "scripts/ci/${gate}.sh" "$repo_root/.gitea/workflows/ci.yml" || \
		! grep -Fq "$gate" "$repo_root/Makefile"; then
		printf '%s authority is not consumed by local and hosted gates\n' "$gate" >&2
		exit 1
	fi
done

for gate in python-lint python-tests c-quality; do
	if ! grep -Fq "scripts/ci/${gate}.sh" "$repo_root/.github/workflows/ci.yml" || \
		! grep -Fq "scripts/ci/${gate}.sh" "$repo_root/.gitea/workflows/ci.yml" || \
		! grep -Fq "$gate" "$repo_root/Makefile"; then
		printf '%s authority is not consumed by local and hosted gates\n' "$gate" >&2
		exit 1
	fi
done

for wrapper in python-lint c-quality; do
	if ! grep -Fq -- '--network none' "$repo_root/scripts/ci/${wrapper}.sh" || \
		! grep -Fq -- '--read-only' "$repo_root/scripts/ci/${wrapper}.sh" || \
		! grep -Fq -- '--cap-drop ALL' "$repo_root/scripts/ci/${wrapper}.sh"; then
		printf '%s does not harden its immutable image authority\n' "$wrapper" >&2
		exit 1
	fi
done
if git -C "$repo_root" grep -nE '(pip(x|3)?[^[:cntrl:]]+(install|run)[^[:cntrl:]]+ruff|ruff(:|==)[0-9]|silkeh/clang:[^$[:space:]]+@sha256:)' -- \
	'.github/workflows/*.yml' '.gitea/workflows/*.yml' 'scripts/**/*.sh' 'Makefile' \
	':(exclude)scripts/ci/tool-versions_test.sh' \
	':(exclude)scripts/ci/python-lint-policy_test.sh' \
	':(exclude)scripts/ci/c-quality-policy_test.sh'; then
	printf 'Python or C gate bypasses tools/tool-versions.env\n' >&2
	exit 1
fi

apidiff_workflows=(
	"$repo_root/.github/workflows/ci.yml"
	"$repo_root/.github/workflows/ci-go.yml"
	"$repo_root/.gitea/workflows/ci.yml"
)
if grep -Fq 'golang.org/x/exp/cmd/apidiff' "${apidiff_workflows[@]}"; then
	printf 'workflow still uses export-bundle apidiff\n' >&2
	exit 1
fi
for workflow in "${apidiff_workflows[@]}"; do
	if ! grep -Fq "github.com/joelanford/go-apidiff@\${GO_COMPAT_TOOL_VERSION}" "$workflow"; then
		printf 'workflow does not install the repository API checker pin: %s\n' "$workflow" >&2
		exit 1
	fi
done

direct_apidiff_workflows=(
	"$repo_root/.github/workflows/ci.yml"
	"$repo_root/.gitea/workflows/ci.yml"
)
for workflow in "${direct_apidiff_workflows[@]}"; do
	if ! grep -Fq 'scripts/ci/go-apidiff.sh' "$workflow"; then
		printf 'direct workflow bypasses multi-module API coverage: %s\n' "$workflow" >&2
		exit 1
	fi
done

if ! grep -Fq "exit \"\$status\"" "$repo_root/.github/workflows/ci-go.yml"; then
	printf 'reusable workflow does not fail closed on API checker errors\n' >&2
	exit 1
fi
if ! grep -Fq -- "- apidiff" "$repo_root/.github/workflows/ci.yml" || \
	! grep -Fq "APIDIFF: \${{ needs.apidiff.result }}" "$repo_root/.github/workflows/ci.yml"; then
	printf 'GitHub aggregate gate does not require API compatibility execution\n' >&2
	exit 1
fi
if grep -A3 '^  apidiff:' "$repo_root/.github/workflows/ci.yml" | grep -Fq "renovate[bot]"; then
	printf 'GitHub API compatibility job is skipped for Renovate\n' >&2
	exit 1
fi

for input in golangci-version gofumpt-version gosec-version govulncheck-version apidiff-version apidiff-base-ref; do
	if ! grep -Fq "inputs.${input}" "$repo_root/.github/workflows/ci-go.yml"; then
		printf 'reusable CI input is not consumed: %s\n' "$input" >&2
		exit 1
	fi
done

spectral_version="$(jq -r '.dependencies["@stoplight/spectral-cli"]' "$repo_root/tools/spectral/package.json")"
workflow_spectral_version="$(awk '
	$1 == "spectral-version:" { in_spectral = 1; next }
	in_spectral && $1 == "default:" { print $2; exit }
' "$repo_root/.github/workflows/ci-go.yml")"
if [[ -z "$spectral_version" || "$spectral_version" == "null" || "$workflow_spectral_version" != "$spectral_version" ]]; then
	printf 'reusable Spectral version %s does not match package authority %s\n' \
		"${workflow_spectral_version:-missing}" "${spectral_version:-missing}" >&2
	exit 1
fi
spectral_lock="$repo_root/tools/spectral/package-lock.json"
if [[ "$(jq -er '.lockfileVersion' "$spectral_lock")" != 3 ]] || \
	[[ "$(jq -er '.packages[""].dependencies["@stoplight/spectral-cli"]' "$spectral_lock")" != "$spectral_version" ]] || \
	[[ "$(jq -er '.packages["node_modules/@stoplight/spectral-cli"].version' "$spectral_lock")" != "$spectral_version" ]] || \
	! jq -e '[.packages | to_entries[] | select(.key | startswith("node_modules/")) | select(.value.link != true) | select((.value.integrity // "") == "")] | length == 0' \
		"$spectral_lock" >/dev/null; then
	printf 'Spectral lock authority lacks complete package integrity\n' >&2
	exit 1
fi
reusable_workflow="$repo_root/.github/workflows/ci-go.yml"
# shellcheck disable=SC2016 # Match literal GitHub expressions in workflow YAML.
for marker in \
	'JOB_CONTEXT: ${{ toJSON(job) }}' \
	'job.workflow_repository' \
	'job.workflow_sha' \
	'repository: ${{ steps.workflow-source.outputs.repository }}' \
	'ref: ${{ steps.workflow-source.outputs.sha }}' \
	'path: .golusoris-ci' \
	'scripts/ci/spectral-default-test.sh' \
	'npm ci --prefix .golusoris-ci/tools/spectral --ignore-scripts --no-audit --no-fund' \
	'.golusoris-ci/scripts/ci/spectral-default-test.sh' \
	'ARGS+=(--ruleset .golusoris-ci/tools/spectral/oas.yaml)' \
	'.golusoris-ci/tools/spectral/node_modules/.bin/spectral'; do
	if ! grep -Fq "$marker" "$reusable_workflow"; then
		printf 'reusable Spectral job bypasses its workflow-revision lock authority: %s\n' \
			"$marker" >&2
		exit 1
	fi
done
if [[ "$(sed -n '/^extends:/p' "$repo_root/tools/spectral/oas.yaml")" != \
	'extends: ["spectral:oas"]' ]]; then
	printf 'reusable Spectral default ruleset does not extend locked OAS rules\n' >&2
	exit 1
fi
for direct_workflow in \
	"$repo_root/.github/workflows/ci.yml" \
	"$repo_root/.gitea/workflows/ci.yml"; do
	if ! grep -Fq 'npm ci --prefix tools/spectral --ignore-scripts --no-audit --no-fund' \
		"$direct_workflow"; then
		printf 'direct Spectral install permits lifecycle scripts: %s\n' "$direct_workflow" >&2
		exit 1
	fi
done
if grep -Eq 'npx[^[:cntrl:]]*@stoplight/spectral-cli|SPECTRAL_VERSION:' \
	"$reusable_workflow"; then
	printf 'reusable Spectral job resolves a caller-controlled npm graph\n' >&2
	exit 1
fi

printf 'tool-version authority tests passed\n'
