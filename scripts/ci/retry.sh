#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

if (($# == 0)); then
	printf 'usage: retry.sh <command> [args...]\n' >&2
	exit 2
fi

readonly RETRY_ATTEMPTS="${RETRY_ATTEMPTS:-3}"
readonly RETRY_DELAY_SECONDS="${RETRY_DELAY_SECONDS:-5}"

if [[ ! "$RETRY_ATTEMPTS" =~ ^[0-9]+$ ]] || ((RETRY_ATTEMPTS < 1 || RETRY_ATTEMPTS > 10)); then
	printf 'retry.sh: RETRY_ATTEMPTS must be an integer between 1 and 10; got %s\n' "$RETRY_ATTEMPTS" >&2
	exit 2
fi

if [[ ! "$RETRY_DELAY_SECONDS" =~ ^[0-9]+$ ]] || ((RETRY_DELAY_SECONDS < 0 || RETRY_DELAY_SECONDS > 60)); then
	printf 'retry.sh: RETRY_DELAY_SECONDS must be an integer between 0 and 60; got %s\n' "$RETRY_DELAY_SECONDS" >&2
	exit 2
fi

# Only the successful attempt's stdout reaches callers that capture it.
stdout_file="$(mktemp "${TMPDIR:-/tmp}/golusoris-retry.XXXXXX")"
trap 'rm -f -- "$stdout_file"' EXIT

delay="$RETRY_DELAY_SECONDS"
status=0
# Bounded retry loop for network-sensitive tool and module downloads (HISS-02).
for ((attempt = 1; attempt <= RETRY_ATTEMPTS; attempt++)); do
	status=0
	# Closed stdin keeps attempts identical: a failed attempt cannot consume input a retry needs.
	"$@" </dev/null >"$stdout_file" || status=$?
	if ((status == 0)); then
		cat "$stdout_file"
		exit 0
	fi
	cat "$stdout_file" >&2
	if ((attempt < RETRY_ATTEMPTS)); then
		printf '::warning::attempt %d/%d failed with exit status %d: %s\n' \
			"$attempt" "$RETRY_ATTEMPTS" "$status" "$*" >&2
		if ((delay > 0)); then
			sleep "$delay"
		fi
		delay=$((delay * 2))
	else
		printf '::error::attempt %d/%d failed with exit status %d: %s\n' \
			"$attempt" "$RETRY_ATTEMPTS" "$status" "$*" >&2
		exit "$status"
	fi
done

exit "$status"
