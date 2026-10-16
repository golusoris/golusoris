// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scaffold

import (
	"archive/zip"
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // Mutates environment and process working directory.
func TestBump_positive(t *testing.T) {
	requireGoTool(t)
	proxyURL := setupBumpProxy(t)
	consumerDir := setupConsumerModule(t, "")
	configureBumpEnv(t, proxyURL, consumerDir)

	err := executeBump(t, "v0.2.0")
	require.NoError(t, err)
	assertBumpedToV020(t)
}

//nolint:paralleltest // Mutates environment and process working directory.
func TestBump_boundaryNormalize(t *testing.T) {
	requireGoTool(t)
	proxyURL := setupBumpProxy(t)
	consumerDir := setupConsumerModule(t, "")
	configureBumpEnv(t, proxyURL, consumerDir)

	err := executeBump(t, "0.2.0")
	require.NoError(t, err)
	assertBumpedToV020(t)
}

//nolint:paralleltest // Mutates environment and process working directory.
func TestBump_boundaryLatest(t *testing.T) {
	requireGoTool(t)
	proxyURL := setupBumpProxy(t)
	consumerDir := setupConsumerModule(t, "")
	configureBumpEnv(t, proxyURL, consumerDir)

	err := executeBump(t, "latest")
	require.NoError(t, err)
	assertBumpedToV020(t)
}

//nolint:paralleltest // Mutates environment and process working directory.
func TestBump_negativeMissingVersion(t *testing.T) {
	requireGoTool(t)
	proxyURL := setupBumpProxy(t)
	consumerDir := setupConsumerModule(t, "")
	configureBumpEnv(t, proxyURL, consumerDir)

	initialMod, err := os.ReadFile("go.mod")
	require.NoError(t, err)

	err = executeBump(t, "v0.9.9")
	require.Error(t, err)
	require.ErrorContains(t, err, "go get github.com/golusoris/golusoris@v0.9.9")

	currentMod, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	require.Equal(t, initialMod, currentMod)
	require.Contains(t, string(currentMod), "github.com/golusoris/golusoris v0.1.0")
}

//nolint:paralleltest // Mutates environment and process working directory.
func TestBump_negativeTidyFailure(t *testing.T) {
	requireGoTool(t)
	proxyURL := setupBumpProxy(t)
	consumerDir := setupConsumerModule(t, "github.com/golusoris/golusoris/missing")
	configureBumpEnv(t, proxyURL, consumerDir)

	err := executeBump(t, "v0.2.0")
	require.Error(t, err)
	require.ErrorContains(t, err, "go mod tidy")
}

func requireGoTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go executable not found on PATH")
	}
}

func setupBumpProxy(t *testing.T) string {
	t.Helper()
	proxyDir := filepath.Join(t.TempDir(), "proxy")
	modDir := filepath.Join(proxyDir, "github.com", "golusoris", "golusoris", "@v")
	require.NoError(t, os.MkdirAll(modDir, 0o750))

	require.NoError(t, os.WriteFile(filepath.Join(modDir, "list"), []byte("v0.1.0\nv0.2.0\n"), 0o600))
	for _, v := range []string{"v0.1.0", "v0.2.0"} {
		info := `{"Version":"` + v + `","Time":"2026-01-01T00:00:00Z"}` + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(modDir, v+".info"), []byte(info), 0o600))
		mod := "module github.com/golusoris/golusoris\n\ngo 1.22\n"
		require.NoError(t, os.WriteFile(filepath.Join(modDir, v+".mod"), []byte(mod), 0o600))
		zipBytes, err := createModuleZip(v)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(modDir, v+".zip"), zipBytes, 0o600))
	}
	path := filepath.ToSlash(proxyDir)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func createModuleZip(version string) ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	prefix := "github.com/golusoris/golusoris@" + version + "/"
	modContent := "module github.com/golusoris/golusoris\n\ngo 1.22\n"
	pkgContent := "package golusoris\n\nconst Version = \"" + version + "\"\n"
	if err := writeZipEntry(zw, prefix+"go.mod", modContent); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, prefix+"golusoris.go", pkgContent); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeZipEntry(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte(content))
	return err
}

func setupConsumerModule(t *testing.T, extraImport string) string {
	t.Helper()
	consumerDir := filepath.Join(t.TempDir(), "consumer")
	require.NoError(t, os.MkdirAll(consumerDir, 0o750))

	modContent := "module example.com/consumer\n\ngo 1.22\n\nrequire github.com/golusoris/golusoris v0.1.0\n"
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "go.mod"), []byte(modContent), 0o600))

	mainContent := "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/golusoris/golusoris\"\n"
	if extraImport != "" {
		mainContent += "\t_ \"" + extraImport + "\"\n"
	}
	mainContent += ")\n\nfunc main() {\n\tfmt.Println(golusoris.Version)\n}\n"
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "main.go"), []byte(mainContent), 0o600))
	return consumerDir
}

func configureBumpEnv(t *testing.T, proxyURL, consumerDir string) {
	t.Helper()
	modCache := filepath.Join(t.TempDir(), "modcache")
	t.Setenv("GOPROXY", proxyURL)
	t.Setenv("GOFLAGS", "-mod=mod -modcacherw")
	t.Setenv("GOMODCACHE", modCache)
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GOPRIVATE", "")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "off")
	t.Chdir(consumerDir)
}

func executeBump(t *testing.T, version string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := BumpCmd()
	cmd.SetContext(ctx)
	return bumpGolusoris(cmd, version)
}

func assertBumpedToV020(t *testing.T) {
	t.Helper()
	modData, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	require.Contains(t, string(modData), "github.com/golusoris/golusoris v0.2.0")

	sumData, err := os.ReadFile("go.sum")
	require.NoError(t, err)
	sumStr := string(sumData)
	require.Contains(t, sumStr, "github.com/golusoris/golusoris v0.2.0 h1:")
	require.Contains(t, sumStr, "github.com/golusoris/golusoris v0.2.0/go.mod h1:")
}
