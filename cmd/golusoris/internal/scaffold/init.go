// Package scaffold implements the golusoris scaffolder subcommands.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/clikit"
)

// InitCmd returns the `golusoris init <name>` command.
func InitCmd() *cobra.Command {
	var module string
	cmd := clikit.Command("init", "Scaffold a new golusoris application",
		clikit.WithRunE(func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("usage: golusoris init <app-name>")
			}
			name := args[0]
			if err := validateName(name); err != nil {
				return err
			}
			if module == "" {
				module = "github.com/example/" + name
			}
			return initApp(name, module)
		}),
	)
	cmd.Args = cobra.MaximumNArgs(1)
	cmd.Flags().StringVar(&module, "module", "", "Go module path (default: github.com/example/<name>)")
	return cmd
}

func initApp(name, module string) error {
	dir := name
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	files := map[string]string{
		"go.mod":  goModTmpl,
		"main.go": mainGoTmpl,
	}

	data := struct{ Name, Module, GoVersion string }{
		Name:      name,
		Module:    module,
		GoVersion: "1.24",
	}

	for name, tmpl := range files {
		path := filepath.Join(dir, name)
		if err := writeTemplate(path, tmpl, data); err != nil {
			return err
		}
		fmt.Printf("  created %s\n", path)
	}

	fmt.Printf("\nApp scaffolded in ./%s\n", dir)
	fmt.Printf("Next steps:\n  cd %s\n  go mod tidy\n  go run .\n", dir)
	return nil
}

func writeTemplate(path, tmplStr string, data any) error {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return t.Execute(f, data) //nolint:wrapcheck // template error is descriptive
}

// validateName checks that a name is a valid Go identifier-style string.
func validateName(name string) error {
	if name == "" {
		return errors.New("name must not be empty")
	}
	if strings.ContainsAny(name, " /\\:*?\"<>|") {
		return fmt.Errorf("name %q contains invalid characters", name)
	}
	return nil
}

var goModTmpl = `module {{.Module}}

go {{.GoVersion}}

require github.com/golusoris/golusoris latest
`

var mainGoTmpl = `package main

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/log"
)

func main() {
	fx.New(
		config.Module,
		log.Module,
		// Add more modules here:
		// golusoris.DB,
		// golusoris.HTTP,
	).Run()
}
`
