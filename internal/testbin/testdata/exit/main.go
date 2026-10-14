// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command exit is the testbin fixture: it prints its linker-set version and
// exits with code 3 when its first argument is "fail".
package main

import "os"

var version = "unset"

func main() {
	_, _ = os.Stdout.WriteString(version + "\n")
	_, _ = os.Stderr.WriteString("stderr line\n")
	if len(os.Args) > 1 && os.Args[1] == "fail" {
		os.Exit(3)
	}
}
