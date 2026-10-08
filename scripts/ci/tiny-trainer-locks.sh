#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

# renovate: datasource=docker depName=ghcr.io/astral-sh/uv
UV_VERSION=0.12.17-debian-slim
UV_DIGEST=sha256:1c97a153d9c2308c89ca4fb19766483db3256a86313806fed99b1c59a9767b6e
UV_IMAGE="ghcr.io/astral-sh/uv:${UV_VERSION}@${UV_DIGEST}"
# renovate: datasource=pypi depName=pip-audit
PIP_AUDIT_VERSION=2.10.1
MAX_TRAINERS=2
MAX_LOCK_CONTEXTS=3
AUDIT_PYTHON_VERSION=3.11.16
# Resolve as of this upload cutoff so --check compares the lock with its inputs,
# not with whatever PyPI published since; move it forward deliberately with --write.
LOCK_EXCLUDE_NEWER=2026-10-08T00:00:00Z
tiny_trainer_lock_tmp=

cleanup_lock_tmp() {
	if [[ -n "$tiny_trainer_lock_tmp" ]]; then
		find "$tiny_trainer_lock_tmp" -depth -delete
	fi
}

trap cleanup_lock_tmp EXIT

compile_lock() {
	local source="$1"
	local python_version="$2"
	local work="$3"
	mkdir -p "$work"
	install -m 0644 "$source" "$work/requirements.in"
	docker run --rm \
		--user "$(id -u):$(id -g)" \
		--env HOME=/src \
		--env UV_CACHE_DIR=/src/.uv-cache \
		--env UV_PYTHON_INSTALL_DIR=/src/.uv-python \
		--mount "type=bind,src=$work,dst=/src" \
		--workdir /src \
		"$UV_IMAGE" uv pip compile --quiet --generate-hashes --no-emit-index-url \
		--exclude-newer "$LOCK_EXCLUDE_NEWER" \
		--python-version "$python_version" --output-file requirements.lock requirements.in
}

add_spdx_header() {
	local generated="$1"
	local destination="$2"
	{
		printf '%s\n' '# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>'
		printf '# %s-%s: %s\n' SPDX License-Identifier EUPL-1.2
		printf '%s\n' '#'
		cat "$generated"
	} >"$destination"
}

materialize_lock() {
	local mode="$1"
	local generated="$2"
	local committed="$3"
	local candidate="${generated%.lock}.with-spdx.lock"
	add_spdx_header "$generated" "$candidate"
	if [[ "$mode" == --write ]]; then
		install -m 0644 "$candidate" "$committed"
	elif ! cmp -s "$candidate" "$committed"; then
		# diff exits 1 for the difference cmp found and 2 when the committed lock is unreadable;
		# both outcomes mean the lock is stale, so the diff only informs the report below.
		local diff_status=0
		diff -u "$committed" "$candidate" || diff_status=$?
		if ((diff_status > 1)); then
			printf 'cannot diff committed lock (diff exit %d)\n' "$diff_status" >&2
		fi
		printf '%s is stale; run scripts/ci/tiny-trainer-locks.sh --write\n' \
			"${committed#"$PWD"/}" >&2
		return 1
	fi
}

audit_locks() {
	local root="$1"
	local audit_home="$tiny_trainer_lock_tmp/audit"
	mkdir -p "$audit_home"
	docker run --rm \
		--user "$(id -u):$(id -g)" \
		--env HOME=/audit \
		--env UV_CACHE_DIR=/audit/.uv-cache \
		--env UV_PYTHON_INSTALL_DIR=/audit/.uv-python \
		--env "AUDIT_PYTHON_VERSION=$AUDIT_PYTHON_VERSION" \
		--mount "type=bind,src=$audit_home,dst=/audit" \
		--mount "type=bind,src=$root,dst=/src,readonly" \
		"$UV_IMAGE" sh -eu -c '
			uv venv --python "$AUDIT_PYTHON_VERSION" /audit/venv
			uv pip install --python /audit/venv/bin/python --require-hashes \
				--no-deps --no-build --requirement /src/scripts/ci/tiny-trainer-pip-audit.lock
			for lock in \
				/src/scripts/ci/tiny-trainer-pip-audit.lock \
				/src/ai/tiny/trainers/gemma/requirements.lock \
				/src/ai/tiny/trainers/litert/requirements.lock
			do
				/audit/venv/bin/pip-audit --strict --require-hashes --disable-pip \
					--progress-spinner off --requirement "$lock"
			done
		'
}

main() {
	local mode="${1:---check}"
	if [[ "$mode" != --check && "$mode" != --write && "$mode" != --audit ]]; then
		printf 'usage: %s [--check|--write|--audit]\n' "${0##*/}" >&2
		return 2
	fi
	local root index trainer python_version work
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	tiny_trainer_lock_tmp="$(mktemp -d "${TMPDIR:-/tmp}/tiny-trainer-locks.XXXXXX")"
	local -a trainers=(gemma litert)
	local -a versions=(3.11.16 3.11.16)
	if (( ${#trainers[@]} != MAX_TRAINERS || ${#versions[@]} != MAX_TRAINERS )); then
		printf 'trainer lock authority must contain exactly %d entries\n' "$MAX_TRAINERS" >&2
		return 1
	fi
	if [[ "$mode" == --audit ]]; then
		audit_locks "$root"
		printf 'tiny trainer locks: audit (%d contexts)\n' "$MAX_LOCK_CONTEXTS"
		return
	fi
	for index in "${!trainers[@]}"; do
		trainer="${trainers[$index]}"
		python_version="${versions[$index]}"
		work="$tiny_trainer_lock_tmp/$trainer"
		compile_lock "$root/ai/tiny/trainers/$trainer/requirements.in" "$python_version" "$work"
		materialize_lock "$mode" "$work/requirements.lock" \
			"$root/ai/tiny/trainers/$trainer/requirements.lock"
	done
	printf 'pip-audit==%s\n' "$PIP_AUDIT_VERSION" >"$tiny_trainer_lock_tmp/pip-audit.in"
	work="$tiny_trainer_lock_tmp/pip-audit"
	compile_lock "$tiny_trainer_lock_tmp/pip-audit.in" "$AUDIT_PYTHON_VERSION" "$work"
	materialize_lock "$mode" "$work/requirements.lock" \
		"$root/scripts/ci/tiny-trainer-pip-audit.lock"
	printf 'tiny trainer locks: %s (%d contexts)\n' "${mode#--}" "$MAX_LOCK_CONTEXTS"
}

main "$@"
