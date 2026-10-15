#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly MAX_IMAGES=32
# Bounds are measured, not guessed. All seven at once split the link so
# thinly that five expired together at a 300 s bound; two at a time each take
# ~220 s. 420 s clears the worst healthy pull by ~2x; the retry covers a wedged
# first contact with the registry (HISS-02).
readonly PULL_CONCURRENCY=2
readonly PULL_TIMEOUT_SECONDS=420
readonly PULL_ATTEMPTS=3

pull_one() {
	local image=$1 attempt
	for ((attempt = 1; attempt <= PULL_ATTEMPTS; attempt++)); do
		if timeout "$PULL_TIMEOUT_SECONDS" docker pull --quiet "$image"; then
			return 0
		fi
		echo "::warning::docker pull ${image}: attempt ${attempt}/${PULL_ATTEMPTS} failed or exceeded ${PULL_TIMEOUT_SECONDS}s"
	done
	return 1
}

main() {
	if (($# > MAX_IMAGES)); then
		echo "::error::pre-pull received $# images; maximum ${MAX_IMAGES}"
		return 1
	fi
	local -a images=("$@") failed=() batch=() pids=()
	local i j image
	for ((i = 0; i < ${#images[@]}; i += PULL_CONCURRENCY)); do
		batch=("${images[@]:i:PULL_CONCURRENCY}")
		pids=()
		for image in "${batch[@]}"; do
			pull_one "$image" &
			pids+=("$!")
		done
		# Matching indices identify failures even when pulls finish out of order.
		for j in "${!pids[@]}"; do
			wait "${pids[$j]}" || failed+=("${batch[$j]}")
		done
	done
	if ((${#failed[@]} != 0)); then
		for image in "${failed[@]}"; do
			echo "::error::docker pull failed after ${PULL_ATTEMPTS} attempts: ${image}"
		done
		echo "::error::pre-pull incomplete -- the test step would otherwise pull under contention"
		return 1
	fi
}

main "$@"
