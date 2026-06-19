package htmltmpl_test

import (
	"bytes"
	"context"
	"html/template"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/htmltmpl"
)

// renderTmpl is a small helper exercising the default funcs through real
// template execution (the only way to reach unexported helpers in an external
// test package).
func renderTmpl(t *testing.T, body string, data any) (string, error) {
	t.Helper()
	files := fstest.MapFS{"p": {Data: []byte(body)}}
	r, err := htmltmpl.New(htmltmpl.Options{Patterns: []string{"*"}},
		testLogger(), clock.NewFake(), files, nil)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if rErr := r.Render(context.Background(), &buf, "p", data); rErr != nil {
		return "", rErr
	}
	return buf.String(), nil
}

func TestDefaultFuncs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		data any
		want string
	}{
		{"upper", `{{ "ab" | upper }}`, nil, "AB"},
		{"lower", `{{ "AB" | lower }}`, nil, "ab"},
		{"title", `{{ title "hello world" }}`, nil, "Hello World"},
		{"hasPrefix true", `{{ hasPrefix "foobar" "foo" }}`, nil, "true"},
		{"hasSuffix false", `{{ hasSuffix "foobar" "foo" }}`, nil, "false"},
		{"default uses fallback on empty", `{{ default "x" "" }}`, nil, "x"},
		{"default keeps value", `{{ default "x" "y" }}`, nil, "y"},
		{"default uses fallback on nil", `{{ default "x" .Nil }}`, map[string]any{"Nil": nil}, "x"},
		{"list+range", `{{ range list "a" "b" }}{{.}}{{ end }}`, nil, "ab"},
		{"join strings", `{{ join "," .V }}`, map[string]any{"V": []string{"a", "b"}}, "a,b"},
		{"join anys", `{{ join "-" .V }}`, map[string]any{"V": []any{1, 2}}, "1-2"},
		{"dict into template", `{{ index (dict "k" "v") "k" }}`, nil, "v"},
		{"nl2br", `{{ nl2br .V }}`, map[string]any{"V": "a\nb"}, "a<br>b"},
		{"now year", `{{ now.Year }}`, nil, "2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := renderTmpl(t, tt.body, tt.data)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultFuncsErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"dict odd args", `{{ dict "k" }}`},
		{"dict non-string key", `{{ dict 1 "v" }}`},
		{"join non-slice", `{{ join "," "notaslice" }}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := renderTmpl(t, tt.body, nil); err == nil {
				t.Fatalf("expected error for %q", tt.body)
			}
		})
	}
}

// TestNl2brEscapes asserts nl2br HTML-escapes the surrounding text so it is safe
// to emit as template.HTML.
func TestNl2brEscapes(t *testing.T) {
	t.Parallel()
	got, err := renderTmpl(t, `{{ nl2br .V }}`, map[string]any{"V": "<script>\nx"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "<script>") {
		t.Fatalf("nl2br leaked raw script: %q", got)
	}
	if !strings.Contains(got, "&lt;script&gt;<br>x") {
		t.Fatalf("nl2br output: %q", got)
	}
}

// TestJSONAttr asserts jsonAttr emits script-safe JSON that cannot break out of
// a <script> context.
func TestJSONAttr(t *testing.T) {
	t.Parallel()
	body := `<script>var d = {{ jsonAttr .V }};</script>`
	got, err := renderTmpl(t, body, map[string]any{"V": map[string]string{"x": "</script>"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "</script><") {
		t.Fatalf("jsonAttr allowed a </script> breakout: %q", got)
	}
	if !strings.Contains(got, `var d =`) {
		t.Fatalf("jsonAttr output: %q", got)
	}
}

// TestSafeURL asserts the scheme allowlist: only safe schemes (and relative
// URLs) survive; everything else collapses to about:blank.
func TestSafeURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"https kept", "https://example.com/x", "https://example.com/x"},
		{"http kept", "http://example.com", "http://example.com"},
		{"mailto kept", "mailto:a@b.com", "mailto:a@b.com"},
		{"tel kept", "tel:123", "tel:123"},
		{"relative kept", "/path?q=1", "/path?q=1"},
		{"javascript blocked", "javascript:alert(1)", "about:blank"},
		{"data blocked", "data:text/html,<x>", "about:blank"},
		{"vbscript blocked", "vbscript:msgbox(1)", "about:blank"},
		{"uppercase javascript blocked", "JavaScript:alert(1)", "about:blank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Render in an href attribute: html/template trusts template.URL
			// verbatim there, so the comparison is exact (no body-context
			// entity-encoding of +, etc.).
			got, err := renderTmpl(t, `<a href="{{ safeURL .V }}">x</a>`, map[string]any{"V": tt.in})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			want := `<a href="` + tt.want + `">x</a>`
			if got != want {
				t.Fatalf("safeURL(%q) rendered %q, want %q", tt.in, got, want)
			}
		})
	}
}

// TestSafeURLInHref asserts safeURL output is trusted by html/template (not
// re-neutralized to #ZgotmplZ) for allowed schemes and blocked for others.
func TestSafeURLInHref(t *testing.T) {
	t.Parallel()
	got, err := renderTmpl(t, `<a href="{{ safeURL .V }}">x</a>`,
		map[string]any{"V": "https://ok.example"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(got, `href="https://ok.example"`) {
		t.Fatalf("safe URL not trusted in href: %q", got)
	}
	if strings.Contains(got, "ZgotmplZ") {
		t.Fatalf("safe URL was re-neutralized: %q", got)
	}
}

// TestWithFuncsOverridesDefault asserts an app-supplied func wins on name clash.
func TestWithFuncsOverridesDefault(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"p": {Data: []byte(`{{ upper "x" }}`)}}
	provider := htmltmpl.WithFuncs(template.FuncMap{
		"upper": func(string) string { return "OVERRIDDEN" },
	})
	r, err := htmltmpl.New(htmltmpl.Options{Patterns: []string{"*"}},
		testLogger(), clock.NewFake(), files, provider)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := render(t, r, "p", nil)
	if got != "OVERRIDDEN" {
		t.Fatalf("app func did not override default: %q", got)
	}
}

// FuzzSafeURL asserts the invariant: safeURL never yields a URL carrying a
// scheme outside the allowlist. Anything with a dangerous/unknown scheme must
// become about:blank.
func FuzzSafeURL(f *testing.F) {
	seeds := []string{
		"https://x", "http://x", "mailto:a@b", "tel:+1", "ftp://x",
		"javascript:alert(1)", "data:text/html,x", "vbscript:x", "/rel",
		"JAVAScript:x", " javascript:x", "\tdata:x", "HTTP://x", "weird:scheme",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	allowed := map[string]bool{"http": true, "https": true, "mailto": true, "tel": true, "ftp": true}
	f.Fuzz(func(t *testing.T, raw string) {
		out, err := renderTmpl(t, `{{ safeURL .V }}`, map[string]any{"V": raw})
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if out == "about:blank" || out == "" {
			return // neutralized — always acceptable
		}
		// If a scheme survived, it must be in the allowlist. Parse the leading
		// scheme by the same rule url.Parse uses: scheme ends at the first ':'.
		if i := strings.IndexByte(out, ':'); i > 0 && !strings.ContainsAny(out[:i], "/?#") {
			scheme := strings.ToLower(out[:i])
			if !allowed[scheme] {
				t.Fatalf("safeURL(%q) leaked disallowed scheme %q: %q", raw, scheme, out)
			}
		}
	})
}
