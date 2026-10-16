// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pdf_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/pdf"
	"github.com/golusoris/golusoris/pdf/parse"
)

const concurrentRenderTimeout = 20 * time.Second

func TestRendererConcurrentCallsKeepPageContentIsolated(t *testing.T) {
	t.Parallel()
	chromePath := findChrome(t)
	alphaStarted := make(chan struct{})
	releaseAlpha := make(chan struct{})
	var alphaOnce sync.Once
	var releaseOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/alpha":
			alphaOnce.Do(func() { close(alphaStarted) })
			select {
			case <-releaseAlpha:
			case <-r.Context().Done():
				return
			}
			if _, err := io.WriteString(w, "<title>alpha</title><h1>alpha</h1>"); err != nil {
				return
			}
		case "/beta":
			releaseOnce.Do(func() { close(releaseAlpha) })
			if _, err := io.WriteString(w, "<title>beta</title><h1>beta</h1>"); err != nil {
				return
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	renderer, err := pdf.NewRendererContext(t.Context(), pdf.Options{
		Timeout:    concurrentRenderTimeout,
		NoSandbox:  true,
		DisableGPU: true,
		ChromePath: chromePath,
	})
	if err != nil {
		t.Fatalf("NewRendererContext() error = %v", err)
	}
	t.Cleanup(renderer.Close)

	ctx, cancel := context.WithTimeout(context.Background(), concurrentRenderTimeout)
	t.Cleanup(cancel)
	type result struct {
		name string
		data []byte
		err  error
	}
	results := make(chan result, 2)
	go func() {
		data, renderErr := renderer.RenderURL(ctx, server.URL+"/alpha", pdf.RenderOptions{})
		results <- result{name: "alpha", data: data, err: renderErr}
	}()

	select {
	case <-alphaStarted:
	case got := <-results:
		t.Fatalf("alpha render returned before request: %v", got.err)
	case <-ctx.Done():
		t.Fatalf("alpha request did not start: %v", ctx.Err())
	}
	go func() {
		data, renderErr := renderer.RenderURL(ctx, server.URL+"/beta", pdf.RenderOptions{})
		results <- result{name: "beta", data: data, err: renderErr}
	}()

	for range 2 {
		got := <-results
		if got.err != nil {
			t.Errorf("RenderURL(%s) error = %v", got.name, got.err)
			continue
		}
		info, infoErr := parse.Info(ctx, bytes.NewReader(got.data), got.name+".pdf")
		if infoErr != nil {
			t.Errorf("parse.Info(%s) error = %v", got.name, infoErr)
			continue
		}
		if info.Title != got.name {
			t.Errorf("RenderURL(%s) title = %q", got.name, info.Title)
		}
	}
}

func resolveCandidate(candidate string) (string, error) {
	if candidate == "" {
		return "", errors.New("pdf: empty candidate path")
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		return "", fmt.Errorf("pdf: resolve candidate %q: %w", candidate, err)
	}
	return filepath.Clean(path), nil
}

func findChrome(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("CHROME_PATH"),
		os.Getenv("CHROMIUM_PATH"),
		"/opt/google/chrome/chrome",
	}
	for _, candidate := range candidates {
		if path, err := resolveCandidate(candidate); err == nil {
			return path
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("Chrome/Chromium is required for PDF renderer integration tests")
	return ""
}

func TestResolveCandidate_Positive(t *testing.T) {
	tempDir := t.TempDir()
	binName := "custom-chrome"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	execPath := filepath.Join(tempDir, binName)
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	gotExec, errExec := resolveCandidate(execPath)
	if errExec != nil {
		t.Fatalf("resolveCandidate(%q) error = %v", execPath, errExec)
	}
	if gotExec != execPath {
		t.Fatalf("resolveCandidate(%q) = %q, want %q", execPath, gotExec, execPath)
	}

	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	gotPath, errPath := resolveCandidate(binName)
	if errPath != nil {
		t.Fatalf("resolveCandidate(%q) via PATH error = %v", binName, errPath)
	}
	if gotPath != execPath {
		t.Fatalf("resolveCandidate(%q) = %q, want %q", binName, gotPath, execPath)
	}
}

func TestResolveCandidate_Negative(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("skipping non-executable bit test: Windows does not report exec bits in file mode")
	}
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "not-executable")
	if err := os.WriteFile(filePath, []byte("plain text"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got, err := resolveCandidate(filePath); err == nil {
		t.Fatalf("resolveCandidate(%q) = %q, want error for non-executable file", filePath, got)
	}
}

func TestResolveCandidate_Boundary(t *testing.T) {
	t.Parallel()
	if got, err := resolveCandidate(""); err == nil {
		t.Fatalf("resolveCandidate(\"\") = %q, want error for empty candidate", got)
	}

	tempDir := t.TempDir()
	missingPath := filepath.Join(tempDir, "does-not-exist")
	if got, err := resolveCandidate(missingPath); err == nil {
		t.Fatalf("resolveCandidate(%q) = %q, want error for missing path", missingPath, got)
	}
}

func TestFindChrome_UsesEnvCandidate(t *testing.T) {
	tempDir := t.TempDir()
	binName := "env-chrome"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	execPath := filepath.Join(tempDir, binName)
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("CHROME_PATH", execPath)
	t.Setenv("CHROMIUM_PATH", "")

	got := findChrome(t)
	if got != execPath {
		t.Fatalf("findChrome() = %q, want %q", got, execPath)
	}
}

func TestFindChrome_UsesPathCandidate(t *testing.T) {
	tempDir := t.TempDir()
	binName := "env-chrome-path"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	execPath := filepath.Join(tempDir, binName)
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	t.Setenv("PATH", tempDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHROME_PATH", binName)
	t.Setenv("CHROMIUM_PATH", "")

	got := findChrome(t)
	if got != execPath {
		t.Fatalf("findChrome() = %q, want %q", got, execPath)
	}
}
