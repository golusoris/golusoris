#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

tiny_trainer_smoke_tmp=

cleanup_smoke_tmp() {
	if [[ -n "$tiny_trainer_smoke_tmp" ]]; then
		find "$tiny_trainer_smoke_tmp" -depth -delete
	fi
}

trap cleanup_smoke_tmp EXIT

main() {
	if (( $# != 2 )); then
		printf 'usage: %s TRAINER IMAGE\n' "${0##*/}" >&2
		return 2
	fi
	local trainer="$1"
	local image="$2"
	local artifact
	local -a smoke_args=()
	tiny_trainer_smoke_tmp="$(mktemp -d "${TMPDIR:-/tmp}/tiny-${trainer}.XXXXXX")"
	mkdir -p "$tiny_trainer_smoke_tmp/input" "$tiny_trainer_smoke_tmp/output"

	case "$trainer" in
	gemma)
		printf '%s\n' '{"prompt":"ping","response":"pong"}' >"$tiny_trainer_smoke_tmp/input/dataset"
		printf '%s\n' '{"base_model":"gemma3:270m","modality":"text","task_kind":"generate","dataset_fmt":"jsonl","dataset_uri":"file:///work/input/dataset","max_dataset_bytes":1024,"hyperparams":{}}' >"$tiny_trainer_smoke_tmp/input/config.json"
		artifact=adapter.lora.h5
		smoke_args=(--contract-smoke)
		;;
	litert)
		mkdir -p "$tiny_trainer_smoke_tmp/images/cat" "$tiny_trainer_smoke_tmp/images/dog"
		printf '%s' 'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAE0lEQVR4nGP4z8Dwn4EBTPxnAAAd8AP92a2PFgAAAABJRU5ErkJggg==' \
			| base64 --decode >"$tiny_trainer_smoke_tmp/images/cat/one.png"
		cp "$tiny_trainer_smoke_tmp/images/cat/one.png" "$tiny_trainer_smoke_tmp/images/cat/two.png"
		cp "$tiny_trainer_smoke_tmp/images/cat/one.png" "$tiny_trainer_smoke_tmp/images/dog/one.png"
		cp "$tiny_trainer_smoke_tmp/images/cat/one.png" "$tiny_trainer_smoke_tmp/images/dog/two.png"
		tar -C "$tiny_trainer_smoke_tmp/images" -cf "$tiny_trainer_smoke_tmp/input/dataset" cat dog
		printf '%s\n' '{"base_model":"keras:image/mobilenet-v2","modality":"image","task_kind":"classify","dataset_fmt":"tar","dataset_uri":"file:///work/input/dataset","max_dataset_bytes":1048576,"hyperparams":{"epochs":1,"batch_size":2,"validation_fraction":0.25,"do_data_augmentation":false,"do_fine_tuning":false,"pretrained":false}}' >"$tiny_trainer_smoke_tmp/input/config.json"
		artifact=model.tflite
		;;
	*)
		printf 'unsupported trainer: %s\n' "$trainer" >&2
		return 2
		;;
	esac

	if [[ "$(docker image inspect --format '{{.Config.User}}' "$image")" != 65532:65532 ]]; then
		printf 'image must declare UID/GID 65532:65532\n' >&2
		return 1
	fi
	chmod -R a+rX "$tiny_trainer_smoke_tmp/input"
	chmod 0777 "$tiny_trainer_smoke_tmp/output"
	docker run --rm --read-only --network=none --cap-drop=ALL --user 65532:65532 \
		--security-opt=no-new-privileges --pids-limit=256 \
		--ulimit fsize=1073741824:1073741824 \
		--tmpfs /tmp:rw,noexec,nosuid,size=1g,mode=1777 \
		--mount "type=bind,src=$tiny_trainer_smoke_tmp/input,dst=/work/input,readonly" \
		--mount "type=bind,src=$tiny_trainer_smoke_tmp/output,dst=/work/output" \
		"$image" "${smoke_args[@]}"
	test -s "$tiny_trainer_smoke_tmp/output/$artifact"
	test -s "$tiny_trainer_smoke_tmp/output/metrics.json"
	printf 'tiny trainer smoke: %s (%s)\n' "$trainer" "$image"
}

main "$@"
