#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root

# shellcheck source=/dev/null
. "$repo_root/tools/tool-versions.env"
# shellcheck source=/dev/null
. "$repo_root/scripts/ci/lib/verified-download.sh"

if [[ "${CI:-}" != "true" && "${ALLOW_HOST_KIND:-}" != "true" ]]; then
	printf 'cnpg-restore: error: running Kubernetes clusters on the host is disallowed by fleet rules. Run in CI or with ALLOW_HOST_KIND=true in an isolated VM.\n' >&2
	exit 1
fi

bin_dir="${RUNNER_TEMP:-/tmp}/bin"
while [[ $# -gt 0 ]]; do
	case "$1" in
	--bin-dir)
		if [[ $# -lt 2 ]]; then
			printf 'error: --bin-dir requires an argument\n' >&2
			exit 2
		fi
		bin_dir="$2"
		shift 2
		;;
	*)
		printf 'error: unknown argument: %s\n' "$1" >&2
		exit 2
		;;
	esac
done
readonly bin_dir

install_tools() {
	mkdir -p "$bin_dir"
	local kind_bin="$bin_dir/kind"
	local kubectl_bin="$bin_dir/kubectl"

	local kind_url="https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-amd64"
	local kubectl_url="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl"

	if [[ ! -x "$kind_bin" ]] || [[ "$(sha256sum "$kind_bin" | awk '{print $1}')" != "$KIND_LINUX_X64_SHA256" ]]; then
		fetch_verified "$kind_url" "$KIND_LINUX_X64_SHA256" "$((64 << 20))" 120 "$kind_bin"
		chmod 0755 "$kind_bin"
	fi
	if [[ ! -x "$kubectl_bin" ]] || [[ "$(sha256sum "$kubectl_bin" | awk '{print $1}')" != "$KUBECTL_LINUX_X64_SHA256" ]]; then
		fetch_verified "$kubectl_url" "$KUBECTL_LINUX_X64_SHA256" "$((128 << 20))" 120 "$kubectl_bin"
		chmod 0755 "$kubectl_bin"
	fi

	export PATH="$bin_dir:$PATH"
}

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-cnpg-restore.XXXXXX")"
readonly work_dir
cluster_name="cnpg-restore-$$"
readonly cluster_name
export KUBECONFIG="$work_dir/kubeconfig"

# diag runs one diagnostic command; its failure is reported, never fatal.
diag() {
	if ! "$@" >&2; then
		printf 'cnpg-restore: diagnostic failed: %s\n' "$*" >&2
	fi
}

dump_diagnostics() {
	printf '=== DIAGNOSTICS: CNPG RESTORE FAILURE ===\n' >&2
	if command -v kubectl >/dev/null 2>&1 && [[ -f "${KUBECONFIG:-}" ]]; then
		printf '--- kubectl get cluster,backup,pods -A -o wide ---\n' >&2
		diag kubectl get cluster,backup,pods -A -o wide
		printf '--- operator logs ---\n' >&2
		diag kubectl logs -n cnpg-system -l app.kubernetes.io/name=cloudnative-pg --tail=200
		printf '--- describe source cluster ---\n' >&2
		diag kubectl describe cluster pg-source -n cnpg-restore
		printf '--- describe recovery cluster ---\n' >&2
		diag kubectl describe cluster pg-recovery -n cnpg-restore
		printf '--- describe backups ---\n' >&2
		diag kubectl describe backup -n cnpg-restore
		printf '--- versitygw logs ---\n' >&2
		diag kubectl logs -n cnpg-restore -l app=versitygw --tail=100
	fi
	printf '=== END DIAGNOSTICS ===\n' >&2
}

cleanup() {
	local exit_code=$?
	if ((exit_code != 0)); then
		dump_diagnostics
	fi
	if [[ -n "${cluster_name:-}" ]] && command -v kind >/dev/null 2>&1; then
		if ! kind delete cluster --name "$cluster_name" >/dev/null 2>&1; then
			printf 'cnpg-restore: warning: kind delete cluster %s failed\n' "$cluster_name" >&2
		fi
	fi
	rm -rf "$work_dir"
	exit "$exit_code"
}
trap cleanup EXIT

apply_with_retry() {
	local manifest="$1"
	local attempts=15 i
	for i in $(seq 1 "$attempts"); do
		if kubectl apply -f "$manifest"; then
			return 0
		fi
		printf 'kubectl apply %s failed (attempt %d/%d), retrying in 2s...\n' "$manifest" "$i" "$attempts" >&2
		sleep 2
	done
	return 1
}

readonly ns=cnpg-restore
readonly manifest_dir="$repo_root/deploy/ci/cnpg-restore"

# render writes a manifest with its @...@ image tokens replaced by the pins.
render() {
	local name="$1" out="$work_dir/$1"
	sed -e "s|@CNPG_POSTGRES_IMAGE@|${CNPG_POSTGRES_IMAGE}|g" -e "s|@VERSITY_IMAGE@|${versity_tag}|g" \
		"$manifest_dir/$name" >"$out"
	if grep -q '@[A-Z_]*@' "$out"; then
		printf 'cnpg-restore: unrendered token left in %s\n' "$name" >&2
		exit 1
	fi
	printf '%s\n' "$out"
}

psql_value() {
	kubectl exec -n "$ns" "$1" -c postgres -- psql -U postgres -d app -v ON_ERROR_STOP=1 -t -A -c "$2"
}

wait_pg() {
	local pod="$1" i
	for i in $(seq 1 30); do
		if kubectl exec -n "$ns" "$pod" -c postgres -- pg_isready -U postgres -d app >/dev/null 2>&1; then
			return 0
		fi
		printf 'cnpg-restore: %s not ready (attempt %d/30)\n' "$pod" "$i"
		sleep 2
	done
	printf 'cnpg-restore: %s database app not ready after 60s\n' "$pod" >&2
	exit 1
}

wait_cluster() {
	kubectl wait --for=condition=Ready "cluster/$1" -n "$ns" --timeout=300s
	kubectl wait --for=condition=Ready "pod/$1-1" -n "$ns" --timeout=120s
	wait_pg "$1-1"
}

create_cluster() {
	local versity_image
	printf 'cnpg-restore: creating kind cluster %s with node image %s\n' "$cluster_name" "$KIND_NODE_IMAGE"
	kind create cluster --name "$cluster_name" --image "$KIND_NODE_IMAGE" --kubeconfig "$KUBECONFIG" --wait 5m
	versity_image="$(sed -n 's/^[[:space:]]*VersityGW[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$repo_root/internal/testimages/images.go")"
	if [[ -z "$versity_image" ]]; then
		printf 'cnpg-restore: VersityGW image constant not found in images.go\n' >&2
		exit 1
	fi
	# kind loads by tag; the tag points at the digest-pinned pull and pods never pull it.
	versity_tag="${versity_image%@*}"
	docker pull "$versity_image"
	docker tag "$versity_image" "$versity_tag"
	kind load docker-image "$versity_tag" --name "$cluster_name"
}

install_operator() {
	local manifest="$work_dir/cnpg-operator.yaml"
	local image="ghcr.io/cloudnative-pg/cloudnative-pg:${CNPG_CRD_VERSION#v}"
	fetch_verified "https://github.com/cloudnative-pg/cloudnative-pg/releases/download/${CNPG_CRD_VERSION}/cnpg-${CNPG_CRD_VERSION#v}.yaml" \
		"$CNPG_OPERATOR_SHA256" "$((8 << 20))" 120 "$manifest"
	sed -i "s|${image}\$|${image}@${CNPG_OPERATOR_IMAGE_DIGEST}|" "$manifest"
	if ! grep -qF "${image}@${CNPG_OPERATOR_IMAGE_DIGEST}" "$manifest"; then
		printf 'cnpg-restore: operator image %s not found in manifest; digest pin not applied\n' "$image" >&2
		exit 1
	fi
	kubectl apply --server-side -f "$manifest"
	kubectl wait --for=condition=Available deployment/cnpg-controller-manager -n cnpg-system --timeout=180s
}

seed_source() {
	kubectl create namespace "$ns"
	kubectl apply -f "$(render storage.yaml)"
	kubectl wait --for=condition=Available deployment/versitygw -n "$ns" --timeout=120s
	apply_with_retry "$(render source.yaml)"
	wait_cluster pg-source
	psql_value pg-source-1 "CREATE TABLE restore_test (id INT PRIMARY KEY, content TEXT NOT NULL);
INSERT INTO restore_test (id, content) SELECT i, 'row-' || i || '-' || md5(i::text) FROM generate_series(1, 1000) AS i;"
	psql_value pg-source-1 "SELECT pg_switch_wal();"
}

backup_and_restore() {
	apply_with_retry "$(render backup.yaml)"
	kubectl wait --for=jsonpath='{.status.phase}'=completed backup/pg-source-backup -n "$ns" --timeout=300s
	kubectl wait --for=condition=LastBackupSucceeded cluster/pg-source -n "$ns" --timeout=120s
	apply_with_retry "$(render recovery.yaml)"
	wait_cluster pg-recovery
}

compare_data() {
	local query_count="SELECT count(*) FROM restore_test;"
	local query_sum="SELECT md5(string_agg(id::text || ':' || content, ',' ORDER BY id)) FROM restore_test;"
	local src_count src_sum rec_count rec_sum
	src_count="$(psql_value pg-source-1 "$query_count")"
	src_sum="$(psql_value pg-source-1 "$query_sum")"
	rec_count="$(psql_value pg-recovery-1 "$query_count")"
	rec_sum="$(psql_value pg-recovery-1 "$query_sum")"
	if [[ "$src_count" != 1000 || "$rec_count" != "$src_count" || "$rec_sum" != "$src_sum" ]]; then
		printf 'cnpg-restore: data mismatch: source=%s/%s recovery=%s/%s\n' "$src_count" "$src_sum" "$rec_count" "$rec_sum" >&2
		exit 1
	fi
	printf 'cnpg-restore: data parity confirmed: rows=%s checksum=%s\n' "$rec_count" "$rec_sum"
}

main() {
	install_tools
	create_cluster
	install_operator
	seed_source
	backup_and_restore
	compare_data
	(
		cd "$repo_root"
		CNPG_TEST_NAMESPACE="$ns" CNPG_SOURCE_CLUSTER=pg-source CNPG_RECOVERY_CLUSTER=pg-recovery \
			GOMAXPROCS=4 go test -p 4 -v -count=1 -tags cnpgkind -run '^TestKind_' ./k8s/cnpg/
	)
	printf 'cnpg-restore: restore verified and kind tests passed\n'
}

versity_tag=""
main "$@"
