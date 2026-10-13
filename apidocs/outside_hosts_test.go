// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/golusoris/golusoris/apidocs"
)

const (
	renderTimeout = 60 * time.Second
	// vendorFontsHost serves the bundle's default fonts when they are left on.
	vendorFontsHost = "fonts.scalar.com"
)

// netLogURL extracts the URL of one request-start event of Chrome's network log.
var netLogURL = regexp.MustCompile(`"url":"(https?://[^"]+)"`)

func TestServedPageContactsNoOutsideHost(t *testing.T) {
	t.Parallel()
	chrome := findChrome(t)

	if hosts := outsideHosts(t, chrome, docsRouter(t)); len(hosts) != 0 {
		t.Fatalf("the served docs page contacted outside hosts: %v", hosts)
	}

	// Planted case: with the vendor fonts on, the same measurement must see their host.
	planted := outsideHosts(t, chrome, withVendorFonts(t, docsRouter(t)))
	if !slices.Contains(planted, vendorFontsHost) {
		t.Fatalf("with vendor fonts on, outside hosts = %v; want %s among them", planted, vendorFontsHost)
	}
}

// docsRouter mounts the docs for the minimal document, without the MCP endpoint.
func docsRouter(t *testing.T) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{Title: "Example", Spec: []byte(minimalSpec)}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return r
}

// withVendorFonts serves the docs page with the default fonts switched back on.
func withVendorFonts(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs" {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		page := rec.Body.String()
		changed := strings.Replace(page, `"withDefaultFonts":false`, `"withDefaultFonts":true`, 1)
		if changed == page {
			t.Error("planted case did not apply: the page carries no withDefaultFonts setting")
		}
		w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
		if _, err := w.Write([]byte(changed)); err != nil {
			return
		}
	})
}

// outsideHosts renders /docs in headless Chrome with outside name resolution
// blocked and returns the hosts, other than the test server, that the page
// itself asked for. Chrome's own background requests carry no page initiator
// and are left out.
func outsideHosts(t *testing.T, chrome string, handler http.Handler) []string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	netLog := filepath.Join(t.TempDir(), "net.json")

	ctx, cancel := context.WithTimeout(t.Context(), renderTimeout)
	defer cancel()
	//nolint:gosec // G204: the binary is the Chrome found on this machine or named by CHROME_PATH.
	cmd := exec.CommandContext(ctx, chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--user-data-dir="+t.TempDir(),
		"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE 127.0.0.1",
		"--log-net-log="+netLog,
		"--virtual-time-budget=15000",
		"--dump-dom", srv.URL+"/docs",
	)
	dom, err := cmd.Output()
	if err != nil {
		t.Fatalf("render %s/docs in Chrome: %v", srv.URL, err)
	}
	if !strings.Contains(string(dom), "echo an id") {
		t.Fatalf("Chrome did not render the mounted document; the network log would prove nothing")
	}
	return pageRequestHosts(t, netLog, srv.URL)
}

// pageRequestHosts reads the request-start events the page origin initiated.
func pageRequestHosts(t *testing.T, netLog, origin string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(netLog))
	if err != nil {
		t.Fatalf("read Chrome network log: %v", err)
	}
	own, err := url.Parse(origin)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	var hosts []string
	sawOwn := false
	for line := range strings.SplitSeq(string(raw), "\n") {
		if !strings.Contains(line, `"initiator":"`+origin+`"`) {
			continue
		}
		match := netLogURL.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		requested, parseErr := url.Parse(match[1])
		if parseErr != nil {
			t.Fatalf("parse logged URL %q: %v", match[1], parseErr)
		}
		if requested.Host == own.Host {
			sawOwn = true
			continue
		}
		if !slices.Contains(hosts, requested.Hostname()) {
			hosts = append(hosts, requested.Hostname())
		}
	}
	if !sawOwn {
		t.Fatalf("network log shows no page request to the test server; the log format is not the expected one")
	}
	return hosts
}

// findChrome returns a Chrome or Chromium binary, or skips the test.
func findChrome(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{os.Getenv("CHROME_PATH"), os.Getenv("CHROMIUM_PATH")} {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return filepath.Clean(candidate)
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no Chrome or Chromium found on PATH, and CHROME_PATH is not set: the page cannot be rendered here")
	return ""
}
