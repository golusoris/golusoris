// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package scaffold implements the golusoris scaffolder subcommands.
package scaffold

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
	gerr "github.com/golusoris/golusoris/core/errors"
)

// InitCmd returns the `golusoris init <name>` command.
func InitCmd() *cobra.Command {
	var module string
	cmd := clikit.Command(
		"init", "Scaffold a new golusoris application",
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
		"go.mod":     goModTmpl,
		"main.go":    mainGoTmpl,
		"LICENSE":    eupl12Text,
		"REUSE.toml": reuseTomlTmpl,
	}

	data := struct{ Name, Module, GoVersion, Year string }{
		Name:      name,
		Module:    module,
		GoVersion: "1.27.0",
		Year:      "2026",
	}

	for name, tmpl := range files {
		path := filepath.Join(dir, name)
		if err := writeTemplate(path, tmpl, data); err != nil {
			return err
		}
		fmt.Printf("  created %s\n", path)
	}

	fmt.Printf("\nApp scaffolded in ./%s (EUPL-1.2, REUSE-ready)\n", dir)
	fmt.Printf("Next steps:\n  cd %s\n  go get github.com/golusoris/golusoris/core@latest\n  go mod tidy\n  go run .\n", dir)
	return nil
}

func writeTemplate(path, tmplStr string, data any) (err error) {
	t, err := template.New("").Parse(tmplStr)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	f, err := os.Create(path) // #nosec G304 -- scaffold output path is operator-specified
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer gerr.CloseInto(f, &err, "scaffold: close "+path)
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

// goModTmpl declares no requirement on purpose: "latest" is not valid go.mod
// syntax, so the next-steps `go get` pins a real version.
var goModTmpl = `module {{.Module}}

go {{.GoVersion}}
`

// eupl12Text is the licence every scaffolded app starts under (ADR-0018).
//
//go:embed eupl-1.2.txt
var eupl12Text string

var reuseTomlTmpl = `# SPDX-FileCopyrightText: {{.Year}} {{.Name}} contributors
#
# SPDX-License-Identifier: EUPL-1.2
#
# REUSE (https://reuse.software) annotations. Code is EUPL-1.2, prose is
# CC-BY-SA-4.0; run ` + "`reuse download --all`" + ` to fetch LICENSES/ texts and
# ` + "`reuse lint`" + ` to verify.
version = 1
SPDX-PackageName = "{{.Name}}"
SPDX-PackageDownloadLocation = "https://{{.Module}}"

[[annotations]]
path = ["**"]
precedence = "aggregate"
SPDX-FileCopyrightText = "{{.Year}} {{.Name}} contributors"
SPDX-License-Identifier = "EUPL-1.2"

[[annotations]]
path = ["README.md", "docs/**", "**/AGENTS.md"]
precedence = "aggregate"
SPDX-FileCopyrightText = "{{.Year}} {{.Name}} contributors"
SPDX-License-Identifier = "CC-BY-SA-4.0"
`

var mainGoTmpl = `// SPDX-FileCopyrightText: {{.Year}} {{.Name}} contributors
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/log"
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
