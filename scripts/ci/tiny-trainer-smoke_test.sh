#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

# Drives tiny-trainer-smoke.sh against a stub docker; CI runs the real images.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly SMOKE="$SCRIPT_DIR/tiny-trainer-smoke.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/tiny-smoke-test.XXXXXX")"
cleanup() {
	rm -rf -- "$work"
}
trap cleanup EXIT

mkdir -p "$work/bin" "$work/tmp"
# The stub answers image inspect with STUB_USER and, for run, logs its arguments, lists the
# input mount and writes STUB_WRITES into the output mount.
cat >"$work/bin/docker" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$1 $2" == 'image inspect' ]]; then
	printf '%s\n' "$STUB_USER"
	exit 0
fi
[[ "$1" == run ]] || exit 64
printf '%s\n' "$@" >"$STUB_LOG"
input='' output=''
for arg in "$@"; do
	case "$arg" in
	type=bind,src=*,dst=/work/input,readonly) input="${arg#type=bind,src=}" input="${input%%,dst=*}" ;;
	type=bind,src=*,dst=/work/output) output="${arg#type=bind,src=}" output="${output%%,dst=*}" ;;
	esac
done
[[ -n "$input" && -n "$output" ]] || exit 65
(cd "$input" && find . -type f | sort) >"$STUB_LOG.input"
tar -tf "$input/dataset" 2>/dev/null | sort >"$STUB_LOG.tar" || true
for name in $STUB_WRITES; do
	printf 'x' >"$output/$name"
done
STUB
chmod +x "$work/bin/docker"

passed=0
smoke() {
	PATH="$work/bin:$PATH" TMPDIR="$work/tmp" STUB_LOG="$work/run.log" bash "$SMOKE" "$@"
}
expect() {
	local want="$1" label="$2" pattern="$3"
	shift 3
	local status=0 output
	output="$("$@" 2>&1)" || status=$?
	if [[ "$status" -ne "$want" ]]; then
		printf 'FAIL %s: want exit %d, got %d\n%s\n' "$label" "$want" "$status" "$output" >&2
		exit 1
	fi
	grep -Fq -- "$pattern" <<<"$output" || {
		printf 'FAIL %s: output lacks %q\n%s\n' "$label" "$pattern" "$output" >&2
		exit 1
	}
	passed=$((passed + 1))
}
logged() {
	grep -Fxq -- "$1" "$work/run.log" || {
		printf 'FAIL docker run lacks %q\n' "$1" >&2
		cat "$work/run.log" >&2
		exit 1
	}
}

export STUB_USER=65532:65532 STUB_WRITES='adapter.lora.h5 metrics.json'
expect 0 'gemma image' 'tiny trainer smoke: gemma (img:gemma)' smoke gemma img:gemma
for flag in --read-only --network=none --cap-drop=ALL --security-opt=no-new-privileges --pids-limit=256 --contract-smoke img:gemma; do
	logged "$flag"
done
logged 65532:65532
if ! { grep -Fxq './config.json' "$work/run.log.input" && grep -Fxq './dataset' "$work/run.log.input"; }; then
	printf 'FAIL gemma input mount lacks config.json or dataset\n' >&2
	exit 1
fi
[[ -z "$(find "$work/tmp" -mindepth 1 -print -quit)" ]] || {
	printf 'FAIL the smoke left its temporary directory behind\n' >&2
	exit 1
}

STUB_WRITES='model.tflite metrics.json' expect 0 'litert image' 'tiny trainer smoke: litert' smoke litert img:litert
if grep -Fxq -- --contract-smoke "$work/run.log"; then
	printf 'FAIL litert must not get --contract-smoke\n' >&2
	exit 1
fi
[[ "$(tr '\n' ' ' <"$work/run.log.tar")" == 'cat/ cat/one.png cat/two.png dog/ dog/one.png dog/two.png ' ]] || {
	printf 'FAIL litert dataset tar: %s\n' "$(tr '\n' ' ' <"$work/run.log.tar")" >&2
	exit 1
}

expect 2 'no arguments' 'usage:' smoke
expect 2 'one argument' 'usage:' smoke gemma
expect 2 'unknown trainer' 'unsupported trainer: other' smoke other img:x
STUB_USER=0:0 expect 1 'root image' 'image must declare UID/GID 65532:65532' smoke gemma img:root
STUB_USER='' expect 1 'image without a user' 'image must declare UID/GID 65532:65532' smoke gemma img:none
STUB_WRITES='metrics.json' expect 1 'no artifact' '' smoke gemma img:gemma
STUB_WRITES='adapter.lora.h5' expect 1 'no metrics' '' smoke gemma img:gemma
STUB_WRITES='model.tflite metrics.json' expect 1 'gemma without its adapter' '' smoke gemma img:gemma

printf 'tiny trainer smoke: %d cases ok\n' "$passed"
