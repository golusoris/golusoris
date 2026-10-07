#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly max_c_files=1024
work=''

cleanup() {
	if [[ -n "$work" && -d "$work" ]]; then
		find "$work" -depth -delete
	fi
}

trap cleanup EXIT

usage() {
	printf 'usage: %s [--root PATH] [--write]\n' "${0##*/}" >&2
}

parse_args() {
	root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	write=false
	while (( $# > 0 )); do
		case "$1" in
			--root)
				[[ $# -ge 2 ]] || { usage; return 2; }
				root="$(cd "$2" && pwd)"
				shift 2
			;;
			--write)
				write=true
				shift
			;;
			*)
				usage
				return 2
			;;
		esac
	done
}

collect_files() {
	local path
	while IFS= read -r -d '' path; do
		case "$path" in
			.config/hiss/testdata/* | vendor/* | third_party/*)
				continue
			;;
		esac
		[[ -f "$root/$path" || -L "$root/$path" ]] || continue
		if [[ -L "$root/$path" ]]; then
			printf 'Clang refuses symlink input: %s\n' "$path" >&2
			return 1
		fi
		files+=("$path")
		if (( ${#files[@]} > max_c_files )); then
			printf 'Clang file bound exceeded: %d > %d\n' \
				"${#files[@]}" "$max_c_files" >&2
			return 1
		fi
	done < <(git -C "$root" ls-files -z --cached --others --exclude-standard -- '*.c')
}

resolve_headers() {
	if [[ -n "${CILIUM_EBPF_HEADERS:-}" ]]; then
		(cd "$CILIUM_EBPF_HEADERS" && pwd)
		return
	fi
	local module_dir replacement
	replacement="$(GOFLAGS=-mod=readonly go -C "$root" list -m -f '{{if .Replace}}{{.Replace.Path}}{{end}}' github.com/cilium/ebpf)"
	if [[ -n "$replacement" ]]; then
		printf 'c-quality refuses a replaced github.com/cilium/ebpf module\n' >&2
		return 1
	fi
	module_dir="$(GOFLAGS=-mod=readonly go -C "$root" list -m -f '{{.Dir}}' github.com/cilium/ebpf)"
	(cd "$module_dir/examples/headers" && pwd)
}

run_clang() {
	local source="$1"
	local output="$2"
	# The child Bash expands the quoted script after Docker starts it.
	# shellcheck disable=SC2016 # Child shell expands the quoted script after Docker starts it.
	"$docker_bin" run --rm \
		--platform linux/amd64 \
		--network none \
		--read-only \
		--cap-drop ALL \
		--security-opt no-new-privileges \
		-u "$(id -u):$(id -g)" \
		--tmpfs /tmp:rw,noexec,nosuid,size=64m \
		-e TMPDIR=/tmp \
		-e EXPECTED_CLANG_VERSION="$CLANG_VERSION" \
		-v "$root:/repo:ro" \
		-v "$headers:/tool/include/bpf:ro" \
		-v "$work:/out:rw" \
		-w /repo \
		--entrypoint bash \
		"$image" -euo pipefail -c '
			source=$1
			output=$2
			clang --version | grep -Fq "clang version ${EXPECTED_CLANG_VERSION}"
			common=(-Wall -Wextra -Werror -isystem /tool/include -isystem /usr/include/x86_64-linux-gnu)
			if [[ $source == *.bpf.c ]]; then
				compile=(-O2 -g -target bpf -D__TARGET_ARCH_x86 -ffile-prefix-map=/repo=. -fdebug-prefix-map=/repo=. "${common[@]}")
				clang "${compile[@]}" -c "/repo/$source" -o "/out/$output"
				clang-tidy --quiet -warnings-as-errors="*" "/repo/$source" -- "${compile[@]}"
				llvm-strip -g "/out/$output"
			else
				compile=(-std=c17 "${common[@]}")
				clang "${compile[@]}" -fsyntax-only "/repo/$source"
				clang-tidy --quiet -warnings-as-errors="*" "/repo/$source" -- "${compile[@]}"
			fi
		' _ "$source" "$output"
}

main() {
	parse_args "$@"
	readonly root write
	git -C "$root" rev-parse --is-inside-work-tree >/dev/null
	[[ -f "$root/.clang-tidy" ]] || { printf 'missing Clang-Tidy configuration: %s\n' "$root/.clang-tidy" >&2; return 1; }

	# shellcheck source=/dev/null
	. "$root/tools/tool-versions.env"
	docker_bin="${DOCKER_BIN:-}"
	if [[ -z "$docker_bin" ]] && command -v docker >/dev/null; then
		docker_bin="$(command -v docker)"
	fi
	if [[ -z "$docker_bin" || ! -x "$docker_bin" ]]; then
		printf 'Docker is required for digest-pinned Clang %s\n' "$CLANG_VERSION" >&2
		return 1
	fi

	local -a files=()
	collect_files
	if (( ${#files[@]} == 0 )); then
		printf 'Clang selected no repository C files\n' >&2
		return 1
	fi

	headers="$(resolve_headers)"
	readonly headers
	for header in bpf_helpers.h bpf_helper_defs.h bpf_endian.h; do
		[[ -f "$headers/$header" ]] || { printf 'missing pinned eBPF header: %s\n' "$header" >&2; return 1; }
	done
	work="$(mktemp -d "${TMPDIR:-/tmp}/golusoris-clang.XXXXXX")"
	image="silkeh/clang:${CLANG_IMAGE_TAG}@${CLANG_IMAGE_DIGEST}"
	readonly image docker_bin

	local source object output
	for source in "${files[@]}"; do
		output="${source//\//__}.o"
		run_clang "$source" "$output"
		[[ "$source" == *.bpf.c ]] || continue
		object="${source%.c}.o"
		if [[ "$write" == true ]]; then
			install -m 0644 "$work/$output" "$root/$object"
		elif [[ ! -f "$root/$object" ]] || ! cmp -s "$work/$output" "$root/$object"; then
			printf 'eBPF object differs: %s; run scripts/ci/c-quality.sh --write\n' "$object" >&2
			return 1
		fi
	done
	printf 'Clang/clang-tidy: %d repository C files passed\n' "${#files[@]}"
}

main "$@"
