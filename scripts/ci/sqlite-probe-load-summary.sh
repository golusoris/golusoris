#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

readonly MAX_LOG_LINES=100000

print_failures() {
	awk -v max="$MAX_LOG_LINES" '
		NR > max { print "load log truncated at " max " lines"; exit }
		/--- FAIL|^FAIL/ && count < 40 {
			if (count++ == 0) print "FAIL lines:"
			print
		}
	' <"$1"
}

print_timeouts() {
	awk -v max="$MAX_LOG_LINES" '
		NR > max { exit }
		/^panic: test timed out after/ {
			if (!seen++) print "Go test timeouts:"
			print
			pending = 1; running = 0; count = 0
			next
		}
		pending && /^[ \t]*running tests:\r?$/ {
			print
			running = 1; pending = 0
			next
		}
		running && /^\t/ {
			if (count < 40) { print; count++ }
			next
		}
		{ running = 0 }
	' <"$1"
}

print_packages() {
	awk -v max="$MAX_LOG_LINES" '
		NR > max { exit }
		/^(ok|FAIL)[ \t]+/ && $3 ~ /^[0-9]+([.][0-9]+)?s\r?$/ {
			duration = $3 + 0; position = 0
			for (slot = 1; slot <= 15; slot++) {
				if (slot > count || duration > durations[slot]) {
					position = slot
					break
				}
			}
			if (!position) next
			for (slot = 15; slot > position; slot--) {
				durations[slot] = durations[slot - 1]
				results[slot] = results[slot - 1]
			}
			durations[position] = duration
			results[position] = $0
			if (count < 15) count++
		}
		END {
			if (count) print "Package results (slowest first):"
			for (slot = 1; slot <= 15 && slot <= count; slot++) print results[slot]
		}
	' <"$1"
}

if [[ $# -ne 1 || ! -f "$1" || ! -r "$1" ]]; then
	printf 'sqlite-probe-load-summary: missing or unreadable load log: %s\n' "${1:-<not supplied>}" >&2
	exit 3
fi

print_failures "$1"
print_timeouts "$1"
print_packages "$1"
