// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pdf_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

	renderer, err := pdf.NewRenderer(pdf.Options{
		Timeout:    concurrentRenderTimeout,
		NoSandbox:  true,
		DisableGPU: true,
		ChromePath: chromePath,
	})
	if err != nil {
		t.Fatalf("NewRenderer() error = %v", err)
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

func findChrome(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("CHROME_PATH"),
		os.Getenv("CHROMIUM_PATH"),
		"/opt/google/chrome/chrome",
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return filepath.Clean(candidate)
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
