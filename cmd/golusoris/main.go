// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command golusoris is the golusoris framework scaffolder.
//
// Usage:
//
//	golusoris init <name>          # scaffold a new golusoris app
//	golusoris add <module>         # add a module to an existing app
//	golusoris bump <version>       # bump golusoris + apply migration notes
//	golusoris --version            # print the release version, commit and date
package main

import (
	"fmt"
	"os"

	"github.com/golusoris/golusoris/cmd/golusoris/internal/scaffold"
	"github.com/golusoris/golusoris/core/clikit"
)

// Release builds set these through -ldflags -X (tools/.goreleaser.yml).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	root := clikit.New("golusoris", "Golusoris framework scaffolder")
	root.Cobra().Version = fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
	root.AddCommand(
		scaffold.InitCmd(),
		scaffold.AddCmd(),
		scaffold.BumpCmd(),
	)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
