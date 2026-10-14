// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scaffold

import (
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
)

type moduleEntry struct {
	Import string
	FxVar  string
}

// knownModules maps short names to their golusoris import paths and fx vars.
var knownModules = map[string]moduleEntry{
	"db":        {"github.com/golusoris/golusoris", "golusoris.DB"},
	"http":      {"github.com/golusoris/golusoris", "golusoris.HTTP"},
	"otel":      {"github.com/golusoris/golusoris", "golusoris.OTel"},
	"cache":     {"github.com/golusoris/golusoris", "golusoris.CacheMemory"},
	"jobs":      {"github.com/golusoris/golusoris", "golusoris.Jobs"},
	"auth-oidc": {"github.com/golusoris/golusoris", "golusoris.AuthOIDC"},
	"authz":     {"github.com/golusoris/golusoris", "golusoris.Authz"},
	"k8s":       {"github.com/golusoris/golusoris", "golusoris.K8s"},
}

// AddCmd returns the `golusoris add <module>` command.
func AddCmd() *cobra.Command {
	return clikit.Command(
		"add", "Show how to add a golusoris module to an existing app",
		clikit.WithRunE(func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if len(args) < 1 {
				return listModules(out)
			}
			name := args[0]
			m, ok := knownModules[name]
			if !ok {
				return fmt.Errorf("unknown module %q — run 'golusoris add' to list available modules", name)
			}
			return showModule(out, name, m)
		}),
	)
}

func listModules(out io.Writer) error {
	if _, err := fmt.Fprintln(out, "Available modules:"); err != nil {
		return fmt.Errorf("scaffold: add: %w", err)
	}
	for _, name := range slices.Sorted(maps.Keys(knownModules)) {
		m := knownModules[name]
		if _, err := fmt.Fprintf(out, "  %-12s  %s\n", name, m.FxVar); err != nil {
			return fmt.Errorf("scaffold: add: %w", err)
		}
	}
	return nil
}

func showModule(out io.Writer, name string, m moduleEntry) error {
	if _, err := fmt.Fprintf(out, "Add %q to your fx.New() call:\n\n", name); err != nil {
		return fmt.Errorf("scaffold: add: %w", err)
	}
	if _, err := fmt.Fprintf(out, "  import %q\n\n", m.Import); err != nil {
		return fmt.Errorf("scaffold: add: %w", err)
	}
	if _, err := fmt.Fprintf(out, "  fx.New(\n    // ...\n    %s,\n  )\n", m.FxVar); err != nil {
		return fmt.Errorf("scaffold: add: %w", err)
	}
	return nil
}
