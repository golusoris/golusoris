#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

set -euo pipefail

print_failures() {
	awk '
		/--- FAIL|^FAIL/ && count < 40 {
			if (count++ == 0) print "FAIL lines:"
			print
		}
	' <"$1"
}

print_timeouts() {
	awk '
		/^panic: test timed out after/ {
			if (!seen++) print "Go test timeouts:"
			print
			pending = 1; running = 0; count = 0
			next
		}
		pending && /^running tests:\r?$/ {
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
	awk '
		/^(ok|FAIL)[ \t]+/ && $3 ~ /^[0-9]+([.][0-9]+)?s\r?$/ {
			printf "%f\t%s\n", $3 + 0, $0
		}
	' <"$1" | LC_ALL=C sort -k1,1nr | awk '
		NR == 1 { print "Package results (slowest first):" }
		NR <= 15 { sub(/^[^\t]*\t/, ""); print }
	'
}

if [[ $# -ne 1 || ! -f "$1" || ! -r "$1" ]]; then
	printf 'sqlite-probe-load-summary: missing or unreadable load log: %s\n' "${1:-<not supplied>}" >&2
	exit 2
fi

print_failures "$1"
print_timeouts "$1"
print_packages "$1"
