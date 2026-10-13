#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
readonly REPO_ROOT
readonly MAX_MODULES=64
readonly MAX_PACKAGES_PER_MODULE=512
readonly MAX_SHARDS=8

readonly MODULE_GROUP="${GO_MODULE_GROUP:-all}"
readonly SHARD_COUNT="${GO_MODULE_SHARD_COUNT:-1}"
readonly SHARD_INDEX="${GO_MODULE_SHARD_INDEX:-0}"
readonly LINT_TIMEOUT="${GOLANGCI_TIMEOUT:-30m}"
readonly TEST_TIMEOUT="${GO_TEST_TIMEOUT:-10m}"
readonly TEST_PARALLELISM="${GO_TEST_PARALLELISM:-4}"

# Gates inspect the declared module graph; they never repair it implicitly.
case " ${GOFLAGS:-} " in
	*' -mod='*) ;;
	*) GOFLAGS="${GOFLAGS:+$GOFLAGS }-mod=readonly" ;;
esac
export GOFLAGS

usage() {
	printf 'usage: %s <list|list-ref|verify|tidy|fix|lint|gosec|vuln|build|test|test-short> [revision]\n' "$0" >&2
}

validate_selector() {
	case "$MODULE_GROUP" in
		all | primary | split) ;;
		*)
			printf 'invalid GO_MODULE_GROUP %q; expected all, primary, or split\n' "$MODULE_GROUP" >&2
			return 2
			;;
	esac

	if ! [[ "$SHARD_COUNT" =~ ^[1-9][0-9]*$ ]] || ((SHARD_COUNT > MAX_SHARDS)); then
		printf 'invalid GO_MODULE_SHARD_COUNT %q; expected 1..%d\n' "$SHARD_COUNT" "$MAX_SHARDS" >&2
		return 2
	fi
	if ! [[ "$SHARD_INDEX" =~ ^[0-9]+$ ]] || ((SHARD_INDEX >= SHARD_COUNT)); then
		printf 'invalid GO_MODULE_SHARD_INDEX %q for %s shard(s)\n' "$SHARD_INDEX" "$SHARD_COUNT" >&2
		return 2
	fi
	if ! [[ "$TEST_PARALLELISM" =~ ^[1-9][0-9]*$ ]]; then
		printf 'invalid GO_TEST_PARALLELISM %q; expected a positive integer\n' "$TEST_PARALLELISM" >&2
		return 2
	fi
}

emit_modules_from_modfiles() {
	local require_files="$1"
	shift
	local -a modfiles=("$@")
	local modfile module
	local root_found=false

	if ((${#modfiles[@]} == 0 || ${#modfiles[@]} > MAX_MODULES)); then
		printf 'discovered %d Go modules; expected 1..%d\n' "${#modfiles[@]}" "$MAX_MODULES" >&2
		return 1
	fi

	for modfile in "${modfiles[@]}"; do
		if [[ "$modfile" != "go.mod" && ! "$modfile" =~ ^[A-Za-z0-9._/-]+/go\.mod$ ]]; then
			printf 'unsupported Go module path: %q\n' "$modfile" >&2
			return 1
		fi
		if [[ "$require_files" == true && ! -f "$REPO_ROOT/$modfile" ]]; then
			printf 'discovered module file is absent: %s\n' "$modfile" >&2
			return 1
		fi
		if [[ "$modfile" == "go.mod" ]]; then
			root_found=true
		fi
	done
	if [[ "$root_found" != true ]]; then
		printf 'discovered module set lacks root go.mod\n' >&2
		return 1
	fi
	printf '.\n'

	for modfile in "${modfiles[@]}"; do
		[[ "$modfile" == "go.mod" ]] && continue
		module="$(dirname "$modfile")"
		printf '%s\n' "$module"
	done
}

discover_modules() {
	local -a modfiles=()
	local modfile_list path

	modfile_list="$(mktemp "${TMPDIR:-/tmp}/golusoris-modules.XXXXXX")"
	if ! git -C "$REPO_ROOT" ls-files --cached --others --exclude-standard -z -- \
		'go.mod' ':(glob)**/go.mod' |
		while IFS= read -r -d '' path; do
			[[ -f "$REPO_ROOT/$path" ]] && printf '%s\0' "$path"
		done | LC_ALL=C sort -z >"$modfile_list"; then
		rm -f -- "$modfile_list"
		printf 'failed to discover Go modules\n' >&2
		return 1
	fi
	mapfile -d '' -t modfiles <"$modfile_list"
	rm -f -- "$modfile_list"

	emit_modules_from_modfiles true "${modfiles[@]}"
}

discover_modules_at_ref() {
	local ref="$1"
	local commit tree_list path
	local overflow=false
	local -a modfiles=()

	if ! commit="$(git -C "$REPO_ROOT" rev-parse --verify --end-of-options "$ref^{commit}")"; then
		printf 'invalid Go module discovery revision: %s\n' "$ref" >&2
		return 1
	fi
	tree_list="$(mktemp "${TMPDIR:-/tmp}/golusoris-module-tree.XXXXXX")"
	if ! git -C "$REPO_ROOT" ls-tree -rz --name-only "$commit" >"$tree_list"; then
		rm -f -- "$tree_list"
		printf 'failed to inspect Go modules at %s\n' "$ref" >&2
		return 1
	fi
	while IFS= read -r -d '' path; do
		case "$path" in
		go.mod | */go.mod)
			modfiles+=("$path")
			if ((${#modfiles[@]} > MAX_MODULES)); then
				overflow=true
				break
			fi
			;;
		esac
	done <"$tree_list"
	rm -f -- "$tree_list"
	if [[ "$overflow" == true ]]; then
		printf 'discovered more than %d Go modules at %s\n' "$MAX_MODULES" "$ref" >&2
		return 1
	fi
	emit_modules_from_modfiles false "${modfiles[@]}"
}

select_modules() {
	local -a discovered=()
	local module discovered_output
	local eligible_index=0

	if ! discovered_output="$(discover_modules)"; then
		return 1
	fi
	mapfile -t discovered <<<"$discovered_output"
	for module in "${discovered[@]}"; do
		case "$MODULE_GROUP:$module" in
			primary:. | primary:core) ;;
			primary:*) continue ;;
			split:. | split:core) continue ;;
		esac

		if ((eligible_index % SHARD_COUNT == SHARD_INDEX)); then
			printf '%s\n' "$module"
		fi
		eligible_index=$((eligible_index + 1))
	done
}

# module_files_sum prints one checksum over go.mod and, when present, go.sum in dir.
module_files_sum() {
	local -a files=("$1/go.mod")
	if [[ -f "$1/go.sum" ]]; then
		files+=("$1/go.sum")
	fi
	cat "${files[@]}" | cksum
}

# verify_module downloads and verifies the module graph in dir. It fails when the
# download rewrote go.mod or go.sum: go mod download repairs a stale requirement
# even under -mod=readonly, which would hide the drift from the tidy phase.
verify_module() {
	local before after
	before="$(module_files_sum "$1")" || return
	(cd "$1" && go mod download && go mod verify) || return
	after="$(module_files_sum "$1")" || return
	if [[ "$after" != "$before" ]]; then
		printf '%s: go mod download rewrote go.mod or go.sum; run go mod tidy and commit the result\n' "$1" >&2
		return 1
	fi
}

# go_fix_check fails when go fix would modernize any package of the module in dir:
# go fix -diff prints the rewrite and exits nonzero.
go_fix_check() {
	(cd "$1" && go fix -diff ./...)
}

run_module() {
	local phase="$1"
	local module="$2"
	local coverage_dir="$3"
	local profile="$4"
	local -a gosec_args=(-exclude-generated -exclude-dir=testdata)

	printf '==> %s: %s\n' "$module" "$phase"
	case "$phase" in
	verify)
			verify_module "$REPO_ROOT/$module"
			;;
		tidy)
			(cd "$REPO_ROOT/$module" && go mod tidy -diff)
			;;
		fix)
			go_fix_check "$REPO_ROOT/$module"
			;;
		lint)
			(cd "$REPO_ROOT/$module" && golangci-lint run \
				--config "$REPO_ROOT/.golangci.yml" --timeout="$LINT_TIMEOUT" ./...)
			;;
		gosec)
			if [[ "$module" == "." ]]; then
				gosec_args+=(-conf "$REPO_ROOT/.gosec.json")
			fi
			if [[ "$module" == "." && -n "${GOSEC_ROOT_SARIF:-}" ]]; then
				(cd "$REPO_ROOT" && gosec "${gosec_args[@]}" \
					-fmt sarif -out "$GOSEC_ROOT_SARIF" -stdout -verbose text ./...)
			else
				(cd "$REPO_ROOT/$module" && gosec -quiet "${gosec_args[@]}" ./...)
			fi
			;;
		vuln)
			(cd "$REPO_ROOT/$module" && "$REPO_ROOT/scripts/ci/govulncheck.sh" ./...)
			;;
		build)
			build_module "$module"
			;;
		test)
			mkdir -p "$coverage_dir"
			(cd "$REPO_ROOT/$module" && go test -race -count=1 -p "$TEST_PARALLELISM" \
				-timeout="$TEST_TIMEOUT" -coverprofile="$profile" -covermode=atomic ./...)
			;;
		test-short)
			(cd "$REPO_ROOT/$module" && go test -short -count=1 -p "$TEST_PARALLELISM" \
				-timeout="$TEST_TIMEOUT" ./...)
			;;
		*)
			usage
			return 2
			;;
	esac
}

build_module() (
	local module="$1"
	local temp_root="${TMPDIR:-/tmp}"
	local build_dir
	local index package packages_output
	local -a packages=()

	build_dir="$(mktemp -d "$temp_root/golusoris-build.XXXXXX")"
	# Invoked indirectly by the EXIT trap below.
	# shellcheck disable=SC2329 # The EXIT trap invokes this cleanup function indirectly.
	cleanup_build_dir() {
		case "$build_dir" in
			"$temp_root"/golusoris-build.*) rm -rf -- "$build_dir" ;;
			*)
				printf 'refusing to remove unexpected build directory: %s\n' "$build_dir" >&2
				return 1
				;;
		esac
	}
	trap cleanup_build_dir EXIT

	cd "$REPO_ROOT/$module"
	if ! packages_output="$(go list ./...)"; then
		printf 'failed to list packages in %s\n' "$module" >&2
		return 1
	fi
	mapfile -t packages <<<"$packages_output"
	if ((${#packages[@]} == 0 || ${#packages[@]} > MAX_PACKAGES_PER_MODULE)); then
		printf 'discovered %d packages in %s; expected 1..%d\n' \
			"${#packages[@]}" "$module" "$MAX_PACKAGES_PER_MODULE" >&2
		return 1
	fi
	for index in "${!packages[@]}"; do
		package="${packages[$index]}"
		go build -o "$build_dir/package-$index" "$package"
	done
	go vet ./...
)

merge_coverage() {
	local coverage_dir="$1"
	shift
	local profile
	local aggregate="$coverage_dir/coverage.out"

	{
		printf 'mode: atomic\n'
		for profile in "$@"; do
			tail -n +2 "$profile"
		done
	} >"$aggregate"
	printf 'coverage: %s\n' "$aggregate"
}

record_primary_coverage_profile() {
	local module="$1"
	local profile="$2"

	case "$module" in
		. | core) primary_profiles+=("$profile") ;;
	esac
}

# run_phase runs one phase in every given module; the test phase also writes the coverage summary
# and merges the primary-module profiles.
run_phase() {
	local phase="$1"
	shift
	local coverage_dir=""
	local coverage_summary=""
	local module slug profile coverage
	local -a primary_profiles=()

	if [[ "$phase" == "test" ]]; then
		coverage_dir="${GO_COVERAGE_DIR:-$REPO_ROOT/.workingdir/cache/go-module-coverage}"
		[[ "$coverage_dir" == /* ]] || coverage_dir="$REPO_ROOT/$coverage_dir"
		mkdir -p "$coverage_dir"
		rm -f -- "$coverage_dir/coverage.out"
		coverage_summary="$coverage_dir/summary.tsv"
		printf 'module\tcoverage\n' >"$coverage_summary"
	fi

	for module in "$@"; do
		slug="${module//\//-}"
		[[ "$slug" == "." ]] && slug="root"
		profile="$coverage_dir/$slug.out"
		run_module "$phase" "$module" "$coverage_dir" "$profile"
		if [[ "$phase" == "test" ]]; then
			record_primary_coverage_profile "$module" "$profile"
			coverage="$(cd "$REPO_ROOT/$module" && go tool cover -func="$profile" \
				| awk '/^total:/ { print $3 }')"
			printf '%s\t%s\n' "$module" "$coverage" >>"$coverage_summary"
		fi
	done

	if [[ "$phase" == "test" ]]; then
		printf 'coverage summary: %s\n' "$coverage_summary"
		if ((${#primary_profiles[@]} > 0)); then
			# The root module requires and replaces core, so their paths share one
			# resolvable module graph. Independent split-module paths do not.
			merge_coverage "$coverage_dir" "${primary_profiles[@]}"
		fi
	fi
}

main() {
	local phase="${1:-}"
	local modules_output
	local -a modules=()

	if [[ "$phase" == "list-ref" ]]; then
		if (($# != 2)); then
			usage
			return 2
		fi
		discover_modules_at_ref "$2"
		return
	fi
	if (($# != 1)); then
		usage
		return 2
	fi

	validate_selector
	if ! modules_output="$(select_modules)"; then
		return 1
	fi
	mapfile -t modules <<<"$modules_output"
	if ((${#modules[@]} == 0)); then
		printf 'selector examined no modules (group=%s shard=%s/%s)\n' \
			"$MODULE_GROUP" "$SHARD_INDEX" "$SHARD_COUNT" >&2
		return 1
	fi

	if [[ "$phase" == "list" ]]; then
		printf '%s\n' "${modules[@]}"
		return
	fi
	case "$phase" in
		verify | tidy | fix | lint | gosec | vuln | build | test | test-short) ;;
		*)
			usage
			return 2
			;;
	esac

	run_phase "$phase" "${modules[@]}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	main "$@"
fi
