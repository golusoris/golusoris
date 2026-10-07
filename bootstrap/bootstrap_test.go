// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bootstrap_test

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/bootstrap"
)

const framework = "github.com/golusoris/golusoris"

// allowedPackage reports whether the lean entry point may link a framework package.
func allowedPackage(importPath string) bool {
	rel, ok := strings.CutPrefix(importPath, framework+"/")
	if !ok {
		return false // the root umbrella itself
	}
	return strings.HasPrefix(rel, "core/") || rel == "httpx/router" || rel == "httpx/server" || rel == "bootstrap"
}

func TestAllowedPackage(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		framework + "/core/config":        true,
		framework + "/httpx/server":       true,
		framework + "/bootstrap":          true,
		framework:                         false,
		framework + "/jobs":               false,
		framework + "/httpx/middleware":   false,
		framework + "/httpx/routerextras": false,
	} {
		if got := allowedPackage(path); got != want {
			t.Errorf("allowedPackage(%q) = %v; want %v", path, got, want)
		}
	}
}

func TestImportGraphStaysLean(t *testing.T) {
	t.Parallel()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH; the import graph needs go list")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, goBin, "list", "-deps", "-f", "{{.ImportPath}}", framework+"/bootstrap").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	seen := 0
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		path := scanner.Text()
		if path != framework && !strings.HasPrefix(path, framework+"/") {
			continue
		}
		seen++
		if !allowedPackage(path) {
			t.Errorf("bootstrap links %s; keep it to core/*, httpx/router and httpx/server", path)
		}
	}
	if seen == 0 {
		t.Fatal("go list reported no framework packages; the check examined nothing")
	}
}

func TestGroupingsValidate(t *testing.T) {
	t.Parallel()
	if err := fx.ValidateApp(bootstrap.Core, bootstrap.HTTP, fx.NopLogger); err != nil {
		t.Fatalf("bootstrap groupings do not form a complete graph: %v", err)
	}
}
