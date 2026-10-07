// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit_test

import (
	"os"
	"testing"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
)

// helperEnv turns the test binary into the "myapp" CLI so real shells can
// run the generated completion scripts against it.
const helperEnv = "GOLUSORIS_CLIKIT_TEST_CLI"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		if err := buildTree(true).Execute(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func noop(*cobra.Command, []string) error { return nil }

// buildTree is the fixture: aliases, a hidden command, hidden and deprecated
// flags, a persistent flag, nesting, and one enumerated flag. withPort
// toggles one flag so drift tests can remove it.
func buildTree(withPort bool) *cobra.Command {
	root := clikit.New("myapp", "Demo application").Cobra()
	root.PersistentFlags().BoolP("verbose", "v", false, "log more")

	serve := clikit.Command("serve", "Start the server", clikit.WithRunE(noop))
	serve.Aliases = []string{"srv"}
	serve.Long = "Start the HTTP server.\n\n.dot-led line, a back\\slash and 'quotes' -- dashes."
	serve.Example = "  myapp serve --format yaml"
	serve.Flags().StringP("format", "f", "json", "output `format`")
	if withPort {
		serve.Flags().Int("port", 8080, "listen port")
	}
	serve.Flags().String("debug-token", "", "internal only")
	_ = serve.Flags().MarkHidden("debug-token")
	serve.Flags().Bool("legacy", false, "old behaviour")
	_ = serve.Flags().MarkDeprecated("legacy", "use --format")
	if err := clikit.EnumFlag(serve, "format", "json", "yaml", "text"); err != nil {
		panic(err)
	}

	internal := clikit.Command("internal", "Hidden plumbing", clikit.WithRunE(noop))
	internal.Hidden = true

	db := clikit.Command("db", "Database tools")
	db.AddCommand(clikit.Command("migrate", "Apply migrations", clikit.WithRunE(noop)))

	root.AddCommand(serve, internal, db)
	return root
}
