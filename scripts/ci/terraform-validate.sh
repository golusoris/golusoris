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

terraform_root="$scan_root/deploy/terraform"
modules_root="$terraform_root/modules"
if [[ ! -d "$modules_root" ]]; then
	printf 'missing Terraform module root: %s\n' "$modules_root" >&2
	exit 1
fi
if symlink="$(find "$terraform_root" -type l -print -quit)" && [[ -n "$symlink" ]]; then
	printf 'Terraform validation refuses symlink input: %s\n' "$symlink" >&2
	exit 1
fi

mapfile -d '' modules < <(
	find "$modules_root" -mindepth 1 -maxdepth 1 -type d -print0 | sort -z
)
if ((${#modules[@]} == 0)); then
	printf 'Terraform validation selected no modules\n' >&2
	exit 1
fi
if ((${#modules[@]} > 64)); then
	printf 'Terraform module bound exceeded: %d > 64\n' "${#modules[@]}" >&2
	exit 1
fi
for module in "${modules[@]}"; do
	if ! compgen -G "$module/*.tf" >/dev/null; then
		printf 'Terraform module has no configuration: %s\n' "$module" >&2
		exit 1
	fi
	if [[ ! -f "$module/.terraform.lock.hcl" ]]; then
		printf 'Terraform module lacks dependency lock: %s\n' "$module" >&2
		exit 1
	fi
done

# terraform_version prints the version the probe reports; a failed probe prints nothing.
terraform_version() {
	local probe
	probe="$("$1" version -json 2>/dev/null)" || return
	python3 -c 'import json,sys; print(json.load(sys.stdin)["terraform_version"])' \
		2>/dev/null <<<"$probe"
}

terraform_bin="${TERRAFORM_BIN:-}"
if [[ -n "$terraform_bin" ]]; then
	if [[ ! -x "$terraform_bin" ]]; then
		printf 'TERRAFORM_BIN is not executable: %s\n' "$terraform_bin" >&2
		exit 1
	fi
	if ! actual_version="$(terraform_version "$terraform_bin")" || \
		[[ "$actual_version" != "$TERRAFORM_VERSION" ]]; then
		printf 'Terraform version is %s, want %s\n' \
			"${actual_version:-unknown}" "$TERRAFORM_VERSION" >&2
		exit 1
	fi
elif ambient_bin="$(command -v terraform)" && \
	ambient_version="$(terraform_version "$ambient_bin")" && \
	[[ "$ambient_version" == "$TERRAFORM_VERSION" ]]; then
	# An ambient binary is used only at the pinned version; otherwise the pinned image runs.
	terraform_bin="$ambient_bin"
fi

work_root="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-terraform.XXXXXX")"
readonly work_root
trap 'rm -rf "$work_root"' EXIT

run_terraform() {
	local working_dir="$1"
	local data_dir="$2"
	shift 2
	mkdir -p "$data_dir"
	if [[ -n "$terraform_bin" ]]; then
		(cd "$working_dir" && TF_DATA_DIR="$data_dir" "$terraform_bin" "$@")
		return
	fi
	if ! command -v docker >/dev/null 2>&1; then
		printf 'Terraform %s or Docker is required\n' "$TERRAFORM_VERSION" >&2
		return 1
	fi
	local relative_dir
	relative_dir="$(realpath --relative-to="$scan_root" "$working_dir")"
	if [[ "$relative_dir" == .. || "$relative_dir" == ../* ]]; then
		printf 'Terraform working directory escapes scan root: %s\n' "$working_dir" >&2
		return 1
	fi
	local image="hashicorp/terraform:${TERRAFORM_VERSION}@${TERRAFORM_IMAGE_DIGEST}"
	docker run --rm \
		-u "$(id -u):$(id -g)" \
		-v "$scan_root:/workspace:ro" \
		-v "$data_dir:/terraform-data" \
		-w "/workspace/$relative_dir" \
		-e TF_DATA_DIR=/terraform-data \
		"$image" "$@"
}

run_terraform "$terraform_root" "$work_root/fmt" fmt -check -recursive -diff .
for module in "${modules[@]}"; do
	module_name="$(basename "$module")"
	data_dir="$work_root/$module_name"
	run_terraform "$module" "$data_dir" \
		init -backend=false -input=false -lockfile=readonly -no-color
	run_terraform "$module" "$data_dir" validate -no-color
done

printf 'Terraform %s: format, locked init, and validate passed for %d modules\n' \
	"$TERRAFORM_VERSION" "${#modules[@]}"
