package scaffold

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/clikit"
)

// BumpCmd returns the `golusoris bump <version>` command.
func BumpCmd() *cobra.Command {
	return clikit.Command("bump", "Bump golusoris to a specific version in the current module",
		clikit.WithRunE(func(cmd *cobra.Command, args []string) error {
			version := "latest"
			if len(args) > 0 {
				version = args[0]
			}
			return bumpGolusoris(cmd, version)
		}),
	)
}

func bumpGolusoris(cmd *cobra.Command, version string) error {
	ctx := cmd.Context()
	pkg := "github.com/golusoris/golusoris"
	if version != "latest" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	target := pkg + "@" + version

	fmt.Printf("Running: go get %s\n", target)
	c := exec.CommandContext(ctx, "go", "get", target) //nolint:gosec // G204: version string comes from CLI arg, not user input
	c.Stdout = nil
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go get %s: %w\n%s", target, err, out)
	}

	fmt.Printf("Running: go mod tidy\n")
	tidy := exec.CommandContext(ctx, "go", "mod", "tidy")
	out, err = tidy.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod tidy: %w\n%s", err, out)
	}

	fmt.Printf("\nBumped %s to %s.\n", pkg, version)
	fmt.Printf("Check docs/migrations/ in the framework repo for breaking-change notes.\n")
	return nil
}
