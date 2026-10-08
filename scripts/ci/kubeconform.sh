#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
scan_root="$repo_root"
if [[ "${1:-}" == --root ]]; then
	if [[ $# -ne 2 ]]; then
		printf 'usage: %s [--root PATH]\n' "$0" >&2
		exit 2
	fi
	scan_root="$(cd "$2" && pwd)"
elif [[ $# -ne 0 ]]; then
	printf 'usage: %s [--root PATH]\n' "$0" >&2
	exit 2
fi
readonly repo_root scan_root

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"
# shellcheck source=scripts/ci/lib/helm.sh
. "$repo_root/scripts/ci/lib/helm.sh"

if [[ ! -d "$scan_root/deploy" || ! -d "$scan_root/deploy/helm" ]]; then
	printf 'Kubernetes validation requires deploy/ and deploy/helm/: %s\n' "$scan_root" >&2
	exit 1
fi

mapfile -d '' candidates < <(
	git -C "$scan_root" ls-files -z --cached --others --exclude-standard -- \
		'deploy/*.yaml' 'deploy/*.yml' 'deploy/**/*.yaml' 'deploy/**/*.yml' | sort -z -u
)
static_files=()
for relative in "${candidates[@]}"; do
	case "$relative" in
	deploy/helm/*) continue ;;
	esac
	path="$scan_root/$relative"
	if [[ -L "$path" ]]; then
		printf 'kubeconform refuses symlink input: %s\n' "$relative" >&2
		exit 1
	fi
	[[ -f "$path" ]] || continue
	if grep -Eq '^apiVersion:[[:space:]]*[^[:space:]]+' "$path" && \
		grep -Eq '^kind:[[:space:]]*[^[:space:]]+' "$path"; then
		static_files+=("$relative")
	fi
done
if ((${#static_files[@]} == 0 || ${#static_files[@]} > 1024)); then
	printf 'static Kubernetes manifest count outside bound 1..1024: %d\n' \
		"${#static_files[@]}" >&2
	exit 1
fi

work_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-kubeconform.XXXXXX")"
readonly work_root
trap 'rm -rf "$work_root"' EXIT
stage="$work_root/manifests"
cache="$work_root/cache"
mkdir -p "$stage/static" "$stage/rendered" "$cache"
for relative in "${static_files[@]}"; do
	destination="$stage/static/$relative"
	mkdir -p "$(dirname "$destination")"
	install -m 0644 "$scan_root/$relative" "$destination"
done

helm_bin="$(resolve_pinned_helm "$HELM_VERSION")"
chart="$scan_root/deploy/helm"
digest='sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
common_args=(
	template golusoris "$chart"
	--set-string "image.digest=$digest"
)
"$helm_bin" "${common_args[@]}" >"$stage/rendered/default.yaml"
"$helm_bin" "${common_args[@]}" \
	--set ingress.enabled=true \
	--set autoscaling.enabled=true \
	--set serviceMonitor.enabled=true \
	--set backup.enabled=true \
	--set-string backup.image.repository=registry.example.test/platform/postgres-backup \
	--set-string "backup.image.digest=$digest" \
	--set-string backup.s3.bucket=golusoris-backups \
	>"$stage/rendered/features.yaml"

core_schema="https://raw.githubusercontent.com/yannh/kubernetes-json-schema/${KUBERNETES_SCHEMA_COMMIT}/{{.NormalizedKubernetesVersion}}-standalone{{.StrictSuffix}}/{{.ResourceKind}}{{.KindSuffix}}.json"
crd_schema="https://raw.githubusercontent.com/datreeio/CRDs-catalog/${CRD_SCHEMA_COMMIT}/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json"
args=(
	-strict
	-summary
	-exit-on-error
	-n 4
	-kubernetes-version "${KUBERNETES_SCHEMA_VERSION#v}"
	-schema-location "$core_schema"
	-schema-location "$crd_schema"
	-skip AppStack
	"$stage"
)

# kubeconform_version prints the first word of the version probe; a failed probe prints nothing.
kubeconform_version() {
	local probe
	probe="$("$1" -v 2>&1)" || return
	awk 'NR == 1 { print $1; exit }' <<<"$probe"
}

kubeconform_bin="${KUBECONFORM_BIN:-}"
if [[ -n "$kubeconform_bin" ]]; then
	if [[ ! -x "$kubeconform_bin" ]]; then
		printf 'KUBECONFORM_BIN is not executable: %s\n' "$kubeconform_bin" >&2
		exit 1
	fi
	if ! actual_version="$(kubeconform_version "$kubeconform_bin")" || \
		[[ "$actual_version" != "$KUBECONFORM_VERSION" ]]; then
		printf 'kubeconform version is %s, want %s\n' \
			"${actual_version:-unknown}" "$KUBECONFORM_VERSION" >&2
		exit 1
	fi
elif ambient_bin="$(command -v kubeconform)" && \
	ambient_version="$(kubeconform_version "$ambient_bin")" && \
	[[ "$ambient_version" == "$KUBECONFORM_VERSION" ]]; then
	# An ambient binary is used only at the pinned version; otherwise the pinned image runs.
	kubeconform_bin="$ambient_bin"
fi

if [[ -n "$kubeconform_bin" ]]; then
	"$kubeconform_bin" -cache "$cache" "${args[@]}"
else
	docker_bin="${DOCKER_BIN:-}"
	if [[ -z "$docker_bin" ]] && command -v docker >/dev/null; then
		docker_bin="$(command -v docker)"
	fi
	if [[ -z "$docker_bin" || ! -x "$docker_bin" ]]; then
		printf 'kubeconform %s or Docker is required\n' "$KUBECONFORM_VERSION" >&2
		exit 1
	fi
	image="ghcr.io/yannh/kubeconform:${KUBECONFORM_VERSION}@${KUBECONFORM_IMAGE_DIGEST}"
	docker_args=(
		run --rm
		--read-only
		--cap-drop ALL
		--security-opt no-new-privileges
		-u "$(id -u):$(id -g)"
		--tmpfs '/tmp:rw,noexec,nosuid,size=32m'
		-v "$stage:/manifests:ro"
		-v "$cache:/cache"
		"$image"
		-cache /cache
		"${args[@]:0:${#args[@]}-1}"
		/manifests
	)
	"$docker_bin" "${docker_args[@]}"
fi

printf 'kubeconform %s: %d static files plus 2 Helm renders passed Kubernetes %s schemas\n' \
	"$KUBECONFORM_VERSION" "${#static_files[@]}" "$KUBERNETES_SCHEMA_VERSION"
