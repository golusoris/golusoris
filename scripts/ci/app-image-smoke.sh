#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

smoke_work=""
smoke_container=""
smoke_image=""

cleanup() {
	if [[ -n "$smoke_container" ]]; then
		docker rm -f "$smoke_container" >/dev/null
	fi
	if [[ -n "$smoke_image" ]]; then
		docker image rm -f "$smoke_image" >/dev/null
	fi
	if [[ -n "$smoke_work" ]]; then
		find "$smoke_work" -depth -delete
	fi
}

trap cleanup EXIT

require_tools() {
	local tool
	for tool in docker go git; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			printf 'app-image-smoke: required command %q is not installed\n' "$tool" >&2
			return 1
		fi
	done
}

scaffold_app() {
	local repo_root="$1"
	local work="$2"
	local go_version core_version app_dir
	go_version="$(awk '$1 == "go" { print $2; exit }' "$repo_root/go.mod")"
	core_version="$(cd "$repo_root" && GOWORK=off go list -m -f '{{.Version}}' github.com/golusoris/golusoris/core)"
	printf 'go %s\nuse (\n\t%s\n\t%s/core\n)\n' "$go_version" "$repo_root" "$repo_root" >"$work/cli.work"
	(
		cd "$work"
		GOWORK="$work/cli.work" go run "$repo_root/cmd/golusoris" init smokeapp
	)
	app_dir="$work/smokeapp"
	test -s "$app_dir/go.mod"
	test -s "$app_dir/main.go"
	# Until #876: tools/Dockerfile.template compiles ./cmd/app while golusoris init writes main.go at module root.
	mkdir -p "$app_dir/cmd/app"
	mv "$app_dir/main.go" "$app_dir/cmd/app/main.go"
	(
		cd "$app_dir"
		export GOWORK=off
		go mod edit \
			-require="github.com/golusoris/golusoris/core@${core_version}" \
			-replace="github.com/golusoris/golusoris/core=${repo_root}/core"
		go mod tidy
		go mod vendor
		# tools/Dockerfile.template runs `go mod download` before `COPY . .`, so drop the local replace after vendoring.
		go mod edit -dropreplace=github.com/golusoris/golusoris/core
	)
	sed -i -E \
		-e 's|^(# github\.com/golusoris/golusoris/core v[^[:space:]]+)[[:space:]]+=>[[:space:]]+.*$|\1|' \
		-e '/^# github\.com\/golusoris\/golusoris\/core[[:space:]]+=>[[:space:]]+/d' \
		"$app_dir/vendor/modules.txt"
}

verify_container_lifecycle() {
	local platform="$1"
	local image="$2"
	local max_polls=50
	local poll status="" logs="" started=false exit_code mod
	smoke_container="$(docker run -d \
		--platform "$platform" \
		--read-only \
		--network=none \
		--cap-drop=ALL \
		--security-opt=no-new-privileges \
		"$image")"
	for ((poll = 1; poll <= max_polls; poll++)); do
		status="$(docker inspect --format '{{.State.Status}}' "$smoke_container")"
		logs="$(docker logs "$smoke_container" 2>&1)"
		if [[ "$status" == running ]] && grep -Fq '[Fx] RUNNING' <<<"$logs"; then
			started=true
			break
		fi
		if [[ "$status" == exited || "$status" == dead ]]; then
			break
		fi
		sleep 0.2
	done
	if [[ "$started" != true ]]; then
		exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$smoke_container")"
		printf 'app-image-smoke: container did not reach [Fx] RUNNING (status=%s exit=%s):\n%s\n' \
			"$status" "$exit_code" "$logs" >&2
		return 1
	fi
	for mod in 'from module "golusoris.config"' 'from module "golusoris.log"'; do
		if ! grep -Fq "$mod" <<<"$logs"; then
			printf 'app-image-smoke: startup log lacks %s:\n%s\n' "$mod" "$logs" >&2
			return 1
		fi
	done
	docker stop -t 10 "$smoke_container" >/dev/null
	exit_code="$(docker inspect --format '{{.State.ExitCode}}' "$smoke_container")"
	logs="$(docker logs "$smoke_container" 2>&1)"
	if [[ "$exit_code" != 0 ]] || ! grep -Fq '[Fx] TERMINATED' <<<"$logs"; then
		printf 'app-image-smoke: container did not terminate cleanly on SIGTERM (exit=%s):\n%s\n' \
			"$exit_code" "$logs" >&2
		return 1
	fi
}

main() {
	if (($# != 1)); then
		printf 'usage: %s <platform-tag>\n' "${0##*/}" >&2
		return 2
	fi
	local platform="$1"
	case "$platform" in
		linux/amd64 | linux/arm64) ;;
		*)
			printf 'app-image-smoke: unsupported platform %q (want linux/amd64 or linux/arm64)\n' "$platform" >&2
			return 2
			;;
	esac
	require_tools
	local repo_root commit_sha tag image_platform image_user
	repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
	smoke_work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/golusoris-app-smoke.XXXXXX")"
	mkdir -p "$smoke_work/docker-config"
	# Avoid unreadable host ~/.docker/config.json warnings on unauthenticated public pulls.
	export DOCKER_CONFIG="${DOCKER_CONFIG:-$smoke_work/docker-config}"
	if ! docker info >/dev/null 2>&1; then
		printf 'app-image-smoke: docker daemon is not reachable\n' >&2
		return 1
	fi
	scaffold_app "$repo_root" "$smoke_work"
	commit_sha="$(git -C "$repo_root" rev-parse --short=12 HEAD)"
	tag="local/golusoris-app-smoke:${platform#linux/}-${commit_sha}"
	docker build \
		--progress=plain \
		--platform "$platform" \
		--build-arg "VERSION=smoke" \
		--build-arg "COMMIT=${commit_sha}" \
		-f "$repo_root/tools/Dockerfile.template" \
		-t "$tag" \
		"$smoke_work/smokeapp"
	smoke_image="$tag"
	image_platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$smoke_image")"
	if [[ "$image_platform" != "$platform" ]]; then
		printf 'app-image-smoke: image platform is %q, want %q\n' "$image_platform" "$platform" >&2
		return 1
	fi
	image_user="$(docker image inspect --format '{{.Config.User}}' "$smoke_image")"
	if [[ "$image_user" != "nonroot:nonroot" ]]; then
		printf 'app-image-smoke: image user is %q, want "nonroot:nonroot"\n' "$image_user" >&2
		return 1
	fi
	verify_container_lifecycle "$platform" "$smoke_image"
	printf 'app-image-smoke: verified %s (%s)\n' "$smoke_image" "$platform"
}

main "$@"
