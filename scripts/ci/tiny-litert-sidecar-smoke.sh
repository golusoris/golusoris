#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

tiny_sidecar_smoke_tmp=
container_id=

cleanup_smoke() {
	if [[ -n "$container_id" ]]; then
		if docker container inspect "$container_id" >/dev/null 2>&1; then
			if ! docker rm -f "$container_id" >/dev/null 2>&1; then
				printf 'warning: failed to remove container %s\n' "$container_id" >&2
			fi
		fi
	fi
	if [[ -n "$tiny_sidecar_smoke_tmp" && -d "$tiny_sidecar_smoke_tmp" ]]; then
		rm -rf "$tiny_sidecar_smoke_tmp"
	fi
}

trap cleanup_smoke EXIT

build_and_verify_image() {
	local image="$1"
	local repo_root="$2"

	if [[ "${SIDECAR_SKIP_BUILD:-0}" == "1" ]]; then
		printf 'skipping sidecar container image build, verifying existing image: %s...\n' "$image"
	else
		printf 'building sidecar container image: %s...\n' "$image"
		docker build -t "$image" "$repo_root/ai/tiny/serve/tflite/sidecar"
	fi

	local uid_gid
	uid_gid="$(docker image inspect --format '{{.Config.User}}' "$image")"
	if [[ "$uid_gid" != "65532:65532" ]]; then
		printf 'FAIL: image must declare UID/GID 65532:65532, got %s\n' "$uid_gid" >&2
		return 1
	fi
}

prepare_smoke_fixtures() {
	local target_dir="$1"
	mkdir -p "$target_dir/models"

	printf 'generating trainer-compatible minimal .tflite model...\n'
	uv run --python 3.13 --with tensorflow==2.21.0 python -c "
import tensorflow as tf
inputs = tf.keras.Input(shape=(160, 160, 3), name='image')
x = tf.keras.layers.GlobalAveragePooling2D()(inputs)
outputs = tf.keras.layers.Dense(2, activation='softmax', name='scores')(x)
model = tf.keras.Model(inputs=inputs, outputs=outputs)
converter = tf.lite.TFLiteConverter.from_keras_model(model)
with open('$target_dir/models/model.tflite', 'wb') as f:
    f.write(converter.convert())
"
	test -s "$target_dir/models/model.tflite"

	# Known test 2x2 PNG
	printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAE0lEQVR4nGP4z8Dwn4EBTPxnAAAd8AP92a2PFgAAAABJRU5ErkJggg==' \
		| base64 --decode >"$target_dir/test.png"

	chmod -R a+rX "$target_dir"
}

resolve_smoke_port() {
	local port="${TFLITE_SMOKE_PORT:-18501}"
	if [[ "$port" == "0" ]] || python3 -c "import socket, sys; s = socket.socket(); s.settimeout(0.2); res = s.connect_ex(('127.0.0.1', int(sys.argv[1]))); s.close(); sys.exit(0 if res == 0 else 1)" "$port" 2>/dev/null; then
		port="$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')"
	fi
	printf '%s\n' "$port"
}

wait_sidecar_ready() {
	local port="$1"
	local cid="$2"
	local attempts=0
	until curl --connect-timeout 2 --max-time 3 -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:${port}/healthz" | grep -E '200|503' >/dev/null; do
		attempts=$((attempts + 1))
		if (( attempts > 30 )); then
			printf 'FAIL: sidecar did not become responsive on port %d\n' "$port" >&2
			if ! docker logs "$cid" >&2; then
				printf 'warning: failed to fetch docker logs for %s\n' "$cid" >&2
			fi
			return 1
		fi
		sleep 0.2
	done
}

run_predictor_integration() {
	local repo_root="$1"
	local port="$2"
	local fixtures_dir="$3"

	printf 'running Go Predictor integration test against sidecar...\n'
	(
		cd "$repo_root"
		TFLITE_SIDECAR_ENDPOINT="http://127.0.0.1:${port}" \
		TFLITE_MODEL_URI="file:///models/model.tflite" \
		TFLITE_MODEL_PATH="$fixtures_dir/models/model.tflite" \
		TFLITE_IMAGE_PATH="$fixtures_dir/test.png" \
		GOFLAGS=-p=4 go test -tags=integration -race -count=1 ./ai/tiny/serve/tflite/...
	)
}

main() {
	local image="${1:-golusoris-litert-sidecar:smoke}"
	local repo_root
	repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

	build_and_verify_image "$image" "$repo_root"

	tiny_sidecar_smoke_tmp="$(mktemp -d "${TMPDIR:-/tmp}/tiny-sidecar.XXXXXX")"
	prepare_smoke_fixtures "$tiny_sidecar_smoke_tmp"

	local port
	port="$(resolve_smoke_port)"
	printf 'using sidecar smoke port: %d\n' "$port"

	container_id="$(docker run -d --rm \
		--read-only --user 65532:65532 \
		--cap-drop=ALL --security-opt=no-new-privileges \
		--pids-limit=256 \
		--tmpfs /tmp:rw,noexec,nosuid,size=1g,mode=1777 \
		-p "127.0.0.1:${port}:8501" \
		--mount "type=bind,src=$tiny_sidecar_smoke_tmp/models,dst=/models,readonly" \
		"$image")"

	wait_sidecar_ready "$port" "$container_id"
	run_predictor_integration "$repo_root" "$port" "$tiny_sidecar_smoke_tmp"

	printf 'tiny litert sidecar smoke: %s PASSED\n' "$image"
}

main "$@"
