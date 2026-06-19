package htmltmpl

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"strings"
	"time"
)

// defaultFuncs is the curated, SSR-safe default FuncMap. It deliberately omits
// env/os/exec/network funcs (the sprig-class SSTI/secrets-leak vector). The only
// helpers that emit pre-escaped values (jsonAttr, safeURL) validate their input
// first; everything else flows through html/template's auto-escaper untouched.
func (r *Renderer) defaultFuncs() template.FuncMap {
	clk := r.clk
	return template.FuncMap{
		"dict":      dict,
		"list":      list,
		"default":   deflt,
		"hasPrefix": strings.HasPrefix,
		"hasSuffix": strings.HasSuffix,
		"lower":     strings.ToLower,
		"upper":     strings.ToUpper,
		"title":     title,
		"join":      join,
		"nl2br":     nl2br,
		"jsonAttr":  jsonAttr,
		"safeURL":   safeURL,
		"now":       func() time.Time { return clk.Now() },
	}
}

// dict builds a map from alternating key/value args, for passing structured data
// into a {{template}} call: {{template "x" (dict "k" .V)}}.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("htmltmpl: dict needs an even number of args, got %d", len(kv))
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("htmltmpl: dict key %d is not a string", i)
		}
		m[key] = kv[i+1]
	}
	return m, nil
}

// list collects its args into a slice for ranging in templates.
func list(items ...any) []any { return items }

// deflt returns fallback when value is nil or a zero-length string.
func deflt(fallback, value any) any {
	if value == nil {
		return fallback
	}
	if s, ok := value.(string); ok && s == "" {
		return fallback
	}
	return value
}

// title upper-cases the first rune of each space-separated word without the
// deprecated strings.Title's Unicode-title-case caveats for ASCII content.
func title(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// join concatenates a []string (or []any of strings) with sep.
func join(sep string, items any) (string, error) {
	switch v := items.(type) {
	case []string:
		return strings.Join(v, sep), nil
	case []any:
		parts := make([]string, len(v))
		for i, it := range v {
			parts[i] = fmt.Sprint(it)
		}
		return strings.Join(parts, sep), nil
	default:
		return "", fmt.Errorf("htmltmpl: join wants a slice, got %T", items)
	}
}

// nl2br converts newlines to <br> in already-trusted text. It HTML-escapes the
// surrounding text itself so it is safe to mark the result as template.HTML.
func nl2br(s string) template.HTML {
	escaped := template.HTMLEscapeString(s)
	return template.HTML(strings.ReplaceAll(escaped, "\n", "<br>")) // #nosec G203 -- input is HTML-escaped above before <br> insertion
}

// jsonAttr marshals v to JSON for embedding in a <script> context. The result
// is template.JS so html/template keeps it in JS-string state; json.Marshal's
// HTML escaping of <, >, & defeats </script> breakouts.
func jsonAttr(v any) (template.JS, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("htmltmpl: jsonAttr marshal: %w", err)
	}
	return template.JS(b), nil // #nosec G203 -- json.Marshal escapes <,>,& for script-context safety
}

// safeURL validates a URL against an allowlist of schemes and returns it as
// template.URL only when safe; anything else (javascript:, data:, vbscript:, a
// malformed URL) collapses to "about:blank". Relative URLs are allowed.
func safeURL(raw string) template.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return template.URL("about:blank")
	}
	if u.Scheme == "" { // relative URL — no scheme to abuse
		return template.URL(raw) // #nosec G203 -- relative URL, validated as scheme-less above
	}
	if _, ok := safeURLSchemes[strings.ToLower(u.Scheme)]; !ok {
		return template.URL("about:blank")
	}
	return template.URL(raw) // #nosec G203 -- scheme validated against safeURLSchemes allowlist
}
