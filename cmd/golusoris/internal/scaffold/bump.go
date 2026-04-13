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
		clikit.WithRunE(func(_ *cobra.Command, args []string) error {
			version := "latest"
			if len(args) > 0 {
				version = args[0]
			}
			return bumpGolusoris(version)
		}),
	)
}

func bumpGolusoris(version string) error {
	pkg := "github.com/golusoris/golusoris"
	if version != "latest" && !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	target := pkg + "@" + version

	fmt.Printf("Running: go get %s\n", target)
	cmd := exec.Command("go", "get", target) //nolint:gosec // target is constructed from validated version string
	cmd.Stdout = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go get %s: %w\n%s", target, err, out)
	}

	fmt.Printf("Running: go mod tidy\n")
	tidy := exec.Command("go", "mod", "tidy") //nolint:gosec // no user input in args
	out, err = tidy.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go mod tidy: %w\n%s", err, out)
	}

	fmt.Printf("\nBumped %s to %s.\n", pkg, version)
	fmt.Printf("Check docs/migrations/ in the framework repo for breaking-change notes.\n")
	return nil
}
