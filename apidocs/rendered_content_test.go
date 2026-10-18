// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/golusoris/golusoris/apidocs"
)

const renderSpec = `openapi: 3.0.3
info:
  title: Rendered Example
  version: "2.3.4"
  description: Docs render probe
paths:
  /widgets/{widgetId}:
    get:
      operationId: getWidget
      summary: fetch one widget
      parameters:
        - name: widgetId
          in: path
          required: true
          description: widget identifier
          schema:
            type: string
      responses:
        '200':
          description: the widget
        '404':
          description: no such widget
  /widgets:
    post:
      operationId: createWidget
      summary: create a widget
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
      responses:
        '201':
          description: created
`

var expectedRenderStrings = []string{
	"Rendered Example",
	"2.3.4",
	"Docs render probe",
	"fetch one widget",
	"create a widget",
	"/widgets/{widgetId}",
	"widgetId",
	"widget identifier",
	"no such widget",
}

func TestRenderedPageShowsMountedDocument(t *testing.T) {
	t.Parallel()
	chrome := findChrome(t)

	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{
		Title: "Docs page title",
		Spec:  []byte(renderSpec),
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	dom, _, _ := renderDocs(t, chrome, r)
	if missing := missingStrings(dom, expectedRenderStrings); len(missing) != 0 {
		dumpPath := filepath.Join(t.TempDir(), "rendered.html")
		if err := os.WriteFile(dumpPath, []byte(dom), 0o600); err != nil {
			t.Errorf("write DOM dump: %v", err)
		}
		t.Fatalf("rendered page missing strings %v (DOM written to %s)", missing, dumpPath)
	}

	// Planted case: rewriting data-url to a missing spec must fail the content check.
	plantedDOM, _, _ := renderDocs(t, chrome, withMissingSpec(t, r))
	if missing := missingStrings(plantedDOM, expectedRenderStrings); len(missing) == 0 {
		t.Fatal("planted 404 spec unexpectedly contained all document strings")
	}
}

// withMissingSpec rewrites data-url to point at a non-existent spec.
func withMissingSpec(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs" {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		page := rec.Body.String()
		changed := strings.Replace(page, `data-url="/openapi.yaml"`, `data-url="/openapi-missing.yaml"`, 1)
		if changed == page {
			t.Error("planted case did not apply: the page carries no data-url attribute")
		}
		w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
		if _, err := w.Write([]byte(changed)); err != nil {
			return
		}
	})
}

// missingStrings returns the elements of want that do not occur in dom.
func missingStrings(dom string, want []string) []string {
	var missing []string
	for _, s := range want {
		if !strings.Contains(dom, s) {
			missing = append(missing, s)
		}
	}
	return missing
}

func TestMissingStrings(t *testing.T) {
	t.Parallel()

	if got := missingStrings("sample dom", nil); got != nil {
		t.Errorf("missingStrings(nil) = %v; want nil", got)
	}
	if got := missingStrings("sample dom", []string{}); got != nil {
		t.Errorf("missingStrings([]) = %v; want nil", got)
	}
	if got := missingStrings("sample dom", []string{"sample", "dom"}); got != nil {
		t.Errorf("missingStrings(all present) = %v; want nil", got)
	}
	got := missingStrings("sample dom", []string{"sample", "absent"})
	if want := []string{"absent"}; !slices.Equal(got, want) {
		t.Errorf("missingStrings(one absent) = %v; want %v", got, want)
	}
}
