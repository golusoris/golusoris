#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-kubeconform-policy.XXXXXX")"
readonly suite_root
trap 'rm -rf "$suite_root"' EXIT

expect_failure() {
	local expected="$1"
	shift
	local output
	if output="$("$@" 2>&1)"; then
		printf 'expected failure: %s\n' "$*" >&2
		exit 1
	fi
	if [[ "$output" != *"$expected"* ]]; then
		printf 'missing diagnostic %q in: %s\n' "$expected" "$output" >&2
		exit 1
	fi
}

new_fixture() {
	local name="$1"
	local shape="$2"
	local root="$suite_root/$name"
	mkdir -p "$root/deploy/helm/templates"
	printf '%s\n' 'apiVersion: v2' 'name: fixture' 'version: 0.1.0' \
		>"$root/deploy/helm/Chart.yaml"
	cat >"$root/deploy/helm/templates/configmap.yaml" <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: chart-fixture
EOF
	cp "$repo_root/.config/hiss/testdata/HISS-10/kubernetes-yaml/$shape" \
		"$root/deploy/static.yaml"
	git -C "$root" init -q
	git -C "$root" add deploy
	printf '%s\n' "$root"
}

fake="$suite_root/kubeconform"
cat >"$fake" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == -v ]]; then
	printf '%s\n' "${FAKE_VERSION:-v0.8.0}"
	exit 0
fi
printf '%s\n' "$*" >"${FAKE_LOG:?}"
if [[ "${FAKE_FAIL:-false}" == true ]]; then
	printf 'synthetic kubeconform failure\n' >&2
	exit 9
fi
EOF
chmod +x "$fake"

clean_root="$(new_fixture fake-clean negative/clean-configmap.yaml)"
fake_log="$suite_root/fake.log"
FAKE_LOG="$fake_log" KUBECONFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/kubeconform.sh" --root "$clean_root" >/dev/null
for required in \
	'-strict' \
	'-kubernetes-version 1.37.1' \
	'a6f9a32d2ccb64b6e4f5b41419b9c2e8ee0cce18' \
	'ad3b08c5045129d7bb1eeffd8e61719b2c8dd1e2' \
	'-skip AppStack'; do
	if ! grep -Fq -- "$required" "$fake_log"; then
		printf 'kubeconform contract lacks: %s\n' "$required" >&2
		exit 1
	fi
done

wrong_root="$(new_fixture wrong-version negative/clean-configmap.yaml)"
expect_failure 'kubeconform version is v0.0.0, want v0.8.0' \
	env FAKE_LOG="$fake_log" FAKE_VERSION=v0.0.0 KUBECONFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/kubeconform.sh" --root "$wrong_root"

failure_root="$(new_fixture scanner-failure negative/clean-configmap.yaml)"
expect_failure 'synthetic kubeconform failure' \
	env FAKE_LOG="$fake_log" FAKE_FAIL=true KUBECONFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/kubeconform.sh" --root "$failure_root"

symlink_root="$(new_fixture symlink negative/clean-configmap.yaml)"
ln -s static.yaml "$symlink_root/deploy/alias.yaml"
expect_failure 'kubeconform refuses symlink input' \
	env FAKE_LOG="$fake_log" KUBECONFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/kubeconform.sh" --root "$symlink_root"

positive_root="$(new_fixture hiss-positive positive/unknown-field.yaml)"
expect_failure "additional properties 'unknownField' not allowed" \
	bash "$repo_root/scripts/ci/kubeconform.sh" --root "$positive_root"
for shape in negative/clean-configmap.yaml gap/local-appstack.yaml; do
	hiss_root="$(new_fixture "hiss-${shape%%/*}" "$shape")"
	bash "$repo_root/scripts/ci/kubeconform.sh" --root "$hiss_root" >/dev/null
done

printf 'kubeconform pin, schema, selection, failure, and HISS policy tests passed\n'
