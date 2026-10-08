#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo_root
suite_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-terraform-policy.XXXXXX")"
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
	local root="$suite_root/$name"
	mkdir -p "$root/deploy/terraform/modules/bucket" \
		"$root/deploy/terraform/modules/postgres"
	for module in bucket postgres; do
		printf 'variable "name" {\n  type = string\n}\n' \
			>"$root/deploy/terraform/modules/$module/main.tf"
		: >"$root/deploy/terraform/modules/$module/.terraform.lock.hcl"
	done
	printf '%s\n' "$root"
}

fake="$suite_root/terraform"
cat >"$fake" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == version && "${2:-}" == -json ]]; then
	printf '{"terraform_version":"%s"}\n' "${FAKE_VERSION:-1.16.3}"
	exit 0
fi
printf '%s|%s|%s\n' "$PWD" "${TF_DATA_DIR:-}" "$*" >>"${FAKE_LOG:?}"
if [[ "${1:-}" == "${FAKE_FAIL:-none}" ]]; then
	printf 'synthetic %s failure\n' "$1" >&2
	exit 9
fi
EOF
chmod +x "$fake"

clean_root="$(new_fixture clean)"
fake_log="$suite_root/fake.log"
FAKE_LOG="$fake_log" TERRAFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/terraform-validate.sh" --root "$clean_root" >/dev/null
if [[ "$(wc -l <"$fake_log")" -ne 5 ]] || \
	[[ "$(grep -Fc 'init -backend=false -input=false -lockfile=readonly -no-color' "$fake_log")" -ne 2 ]] || \
	[[ "$(grep -Fc 'validate -no-color' "$fake_log")" -ne 2 ]]; then
	printf 'Terraform wrapper did not run the complete locked command set\n' >&2
	exit 1
fi

wrong_root="$(new_fixture wrong-version)"
expect_failure 'Terraform version is 0.0.0, want 1.16.3' \
	env FAKE_LOG="$fake_log" FAKE_VERSION=0.0.0 TERRAFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/terraform-validate.sh" --root "$wrong_root"

missing_lock_root="$(new_fixture missing-lock)"
find "$missing_lock_root/deploy/terraform/modules/bucket/.terraform.lock.hcl" -delete
expect_failure 'Terraform module lacks dependency lock' \
	env FAKE_LOG="$fake_log" TERRAFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/terraform-validate.sh" --root "$missing_lock_root"

symlink_root="$(new_fixture symlink)"
ln -s main.tf "$symlink_root/deploy/terraform/modules/bucket/alias.tf"
expect_failure 'Terraform validation refuses symlink input' \
	env FAKE_LOG="$fake_log" TERRAFORM_BIN="$fake" \
	bash "$repo_root/scripts/ci/terraform-validate.sh" --root "$symlink_root"

for command in fmt init validate; do
	failure_root="$(new_fixture "failure-$command")"
	expect_failure "synthetic $command failure" \
		env FAKE_LOG="$fake_log" FAKE_FAIL="$command" TERRAFORM_BIN="$fake" \
		bash "$repo_root/scripts/ci/terraform-validate.sh" --root "$failure_root"
done

positive_root="$(new_fixture hiss-positive)"
cp "$repo_root/.config/hiss/testdata/HISS-10/hcl/positive/unformatted.tf" \
	"$positive_root/deploy/terraform/modules/bucket/main.tf"
expect_failure 'main.tf' env -u TERRAFORM_BIN \
	bash "$repo_root/scripts/ci/terraform-validate.sh" --root "$positive_root"
for shape in negative/clean.tf gap/undocumented-variable.tf; do
	hiss_root="$(new_fixture "hiss-${shape%%/*}")"
	cp "$repo_root/.config/hiss/testdata/HISS-10/hcl/$shape" \
		"$hiss_root/deploy/terraform/modules/bucket/main.tf"
	env -u TERRAFORM_BIN bash "$repo_root/scripts/ci/terraform-validate.sh" \
		--root "$hiss_root" >/dev/null
done

printf 'Terraform fail-closed policy and HISS fixtures passed\n'
