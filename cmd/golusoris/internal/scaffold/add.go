package scaffold

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/clikit"
)

// knownModules maps short names to their golusoris import paths and fx vars.
var knownModules = map[string]struct {
	Import string
	FxVar  string
}{
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
	return clikit.Command("add", "Show how to add a golusoris module to an existing app",
		clikit.WithRunE(func(_ *cobra.Command, args []string) error {
			if len(args) < 1 {
				fmt.Println("Available modules:")
				for name, m := range knownModules {
					fmt.Printf("  %-12s  %s\n", name, m.FxVar)
				}
				return nil
			}
			name := args[0]
			m, ok := knownModules[name]
			if !ok {
				return fmt.Errorf("unknown module %q — run 'golusoris add' to list available modules", name)
			}
			fmt.Printf("Add %q to your fx.New() call:\n\n", name)
			fmt.Printf("  import %q\n\n", m.Import)
			fmt.Printf("  fx.New(\n    // ...\n    %s,\n  )\n", m.FxVar)
			return nil
		}),
	)
}
