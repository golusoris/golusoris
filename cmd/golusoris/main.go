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
package main

import (
	"os"

	"github.com/golusoris/golusoris/cmd/golusoris/internal/scaffold"
	"github.com/golusoris/golusoris/core/clikit"
)

func main() {
	root := clikit.New("golusoris", "Golusoris framework scaffolder")
	root.AddCommand(
		scaffold.InitCmd(),
		scaffold.AddCmd(),
		scaffold.BumpCmd(),
	)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
