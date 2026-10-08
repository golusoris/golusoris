// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// maxYAMLDocuments bounds the documents read from one rules file.
const maxYAMLDocuments = 256

// dashboardJSON is the slice of the Grafana classic JSON model promcheck reads.
type dashboardJSON struct {
	Dashboard  *dashboardJSON `json:"dashboard"` // API export wrapper
	Panels     []panelJSON    `json:"panels"`
	Templating struct {
		List []variableJSON `json:"list"`
	} `json:"templating"`
	Annotations struct {
		List []annotationJSON `json:"list"`
	} `json:"annotations"`
}

type panelJSON struct {
	Title      string          `json:"title"`
	Datasource json.RawMessage `json:"datasource"`
	Targets    []targetJSON    `json:"targets"`
	Panels     []panelJSON     `json:"panels"` // collapsed row children
}

type targetJSON struct {
	Expr       string          `json:"expr"`
	RefID      string          `json:"refId"` //nolint:tagliatelle // Grafana JSON model is camelCase.
	Datasource json.RawMessage `json:"datasource"`
}

type variableJSON struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Query      json.RawMessage `json:"query"`
	Datasource json.RawMessage `json:"datasource"`
}

type annotationJSON struct {
	Name       string          `json:"name"`
	Expr       string          `json:"expr"`
	Datasource json.RawMessage `json:"datasource"`
	Target     *targetJSON     `json:"target"`
}

// DashboardExprs extracts the PromQL of every Prometheus panel target
// (including collapsed rows), query variable and annotation of a Grafana
// dashboard. Targets of other datasource types (Loki, Tempo) are skipped.
// label_values(sel, label) becomes `count by (label) (sel)` so the label is
// checked too. source prefixes each [Expr.Source].
func DashboardExprs(raw []byte, source string) ([]Expr, error) {
	var d dashboardJSON
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("promcheck: %s: %w", source, err)
	}
	if d.Dashboard != nil {
		d = *d.Dashboard
	}
	var out []Expr
	for _, p := range d.Panels {
		out = append(out, panelExprs(p, source)...)
		for _, child := range p.Panels {
			out = append(out, panelExprs(child, source)...)
		}
	}
	for _, v := range d.Templating.List {
		if e, ok := variableExpr(v, source); ok {
			out = append(out, e)
		}
	}
	for _, a := range d.Annotations.List {
		out = append(out, annotationExprs(a, source)...)
	}
	return out, nil
}

func panelExprs(p panelJSON, source string) []Expr {
	out := make([]Expr, 0, len(p.Targets))
	for _, t := range p.Targets {
		if t.Expr == "" || !isPrometheus(t.Datasource, p.Datasource) {
			continue
		}
		out = append(out, Expr{Source: fmt.Sprintf("%s: panel %q target %s", source, p.Title, t.RefID), Query: t.Expr})
	}
	return out
}

func annotationExprs(a annotationJSON, source string) []Expr {
	exprs := []string{a.Expr}
	if a.Target != nil {
		exprs = append(exprs, a.Target.Expr)
	}
	var out []Expr
	for _, e := range exprs {
		if e != "" && isPrometheus(a.Datasource) {
			out = append(out, Expr{Source: fmt.Sprintf("%s: annotation %q", source, a.Name), Query: e})
		}
	}
	return out
}

// isPrometheus reports whether the first datasource that names a type is
// Prometheus; references without a type (legacy names, variables) count.
func isPrometheus(refs ...json.RawMessage) bool {
	for _, raw := range refs {
		var ref struct {
			Type string `json:"type"`
		}
		if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &ref) != nil || ref.Type == "" {
			continue
		}
		return ref.Type == "prometheus"
	}
	return true
}

func variableExpr(v variableJSON, source string) (Expr, bool) {
	if v.Type != "query" || !isPrometheus(v.Datasource) {
		return Expr{}, false
	}
	query := variableQuery(v.Query)
	src := fmt.Sprintf("%s: variable $%s", source, v.Name)
	if inner, ok := call(query, "query_result"); ok {
		return Expr{Source: src, Query: inner}, true
	}
	inner, ok := call(query, "label_values")
	if !ok {
		return Expr{}, false
	}
	sel, label, ok := splitLastArg(inner)
	if !ok {
		return Expr{}, false // label_values(label) lists all series: nothing to check.
	}
	return Expr{Source: src, Query: "count by (" + label + ") (" + sel + ")"}, true
}

// variableQuery reads the query string or the {"query": …} object form.
func variableQuery(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var obj struct {
		Query string `json:"query"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Query)
	}
	return ""
}

// call returns the argument text of fn(…) when s is exactly that call.
func call(s, fn string) (string, bool) {
	if !strings.HasPrefix(s, fn+"(") || !strings.HasSuffix(s, ")") {
		return "", false
	}
	return s[len(fn)+1 : len(s)-1], true
}

// splitLastArg splits "a, b" at the last comma outside quotes and brackets.
func splitLastArg(s string) (string, string, bool) {
	var sc argScanner
	cut := -1
	for i := range len(s) {
		if sc.topLevelComma(s, i) {
			cut = i
		}
	}
	if cut < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:cut]), strings.TrimSpace(s[cut+1:]), true
}

// argScanner tracks quote and bracket nesting while scanning arguments.
type argScanner struct {
	depth int
	quote byte
}

// topLevelComma advances over s[i] and reports a comma at nesting depth 0.
func (sc *argScanner) topLevelComma(s string, i int) bool {
	ch := s[i]
	if sc.quote != 0 {
		sc.quote = closeQuote(s, i, sc.quote)
		return false
	}
	switch ch {
	case '"', '\'', '`':
		sc.quote = ch
	case '(', '{', '[':
		sc.depth++
	case ')', '}', ']':
		sc.depth--
	case ',':
		return sc.depth == 0
	}
	return false
}

// rulesYAML reads both a PrometheusRule (spec.groups) and a plain rule file.
type rulesYAML struct {
	Groups []ruleGroupYAML `json:"groups"`
	Spec   struct {
		Groups []ruleGroupYAML `json:"groups"`
	} `json:"spec"`
}

type ruleGroupYAML struct {
	Name  string     `json:"name"`
	Rules []ruleYAML `json:"rules"`
}

type ruleYAML struct {
	Alert       string            `json:"alert"`
	Record      string            `json:"record"`
	Expr        exprValue         `json:"expr"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// exprValue accepts a string or a bare number (`expr: 1`).
type exprValue string

func (e *exprValue) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*e = exprValue(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("promcheck: expr is neither string nor number: %w", err)
	}
	*e = exprValue(n.String())
	return nil
}

// RuleExprs extracts every rule of a PrometheusRule manifest or plain
// Prometheus rule file; multi-document YAML (`---`) is accepted. Alerts
// carry their {{ $labels.x }} references and static labels.
func RuleExprs(raw []byte, source string) ([]Expr, error) {
	docs, err := splitYAMLDocuments(raw)
	if err != nil {
		return nil, fmt.Errorf("promcheck: %s: %w", source, err)
	}
	var out []Expr
	for i, doc := range docs {
		var f rulesYAML
		if err := yaml.Unmarshal(doc, &f); err != nil {
			return nil, fmt.Errorf("promcheck: %s document %d: %w", source, i, err)
		}
		for _, g := range slices.Concat(f.Groups, f.Spec.Groups) {
			out = append(out, groupExprs(g, source)...)
		}
	}
	return out, nil
}

func groupExprs(g ruleGroupYAML, source string) []Expr {
	out := make([]Expr, 0, len(g.Rules))
	for i, r := range g.Rules {
		name := r.Alert
		if name == "" {
			name = r.Record
		}
		e := Expr{Source: fmt.Sprintf("%s: group %q rule %d (%s)", source, g.Name, i, name), Query: string(r.Expr), Record: r.Record}
		if r.Alert != "" {
			texts := make([]string, 0, len(r.Labels)+len(r.Annotations))
			for _, m := range []map[string]string{r.Labels, r.Annotations} {
				for _, v := range m {
					texts = append(texts, v)
				}
			}
			e.TemplateLabels = TemplateLabels(texts...)
			for k := range r.Labels {
				e.StaticLabels = append(e.StaticLabels, k)
			}
		}
		out = append(out, e)
	}
	return out
}

// splitYAMLDocuments splits on "---" separator lines.
func splitYAMLDocuments(raw []byte) ([][]byte, error) {
	lines := bytes.Split(raw, []byte("\n"))
	docs := make([][]byte, 0, 1)
	var cur [][]byte
	for _, line := range lines {
		if strings.TrimRight(string(line), " \r") == "---" {
			docs = appendDoc(docs, cur)
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	docs = appendDoc(docs, cur)
	if len(docs) > maxYAMLDocuments {
		return nil, fmt.Errorf("%d YAML documents exceed the limit of %d", len(docs), maxYAMLDocuments)
	}
	return docs, nil
}

func appendDoc(docs [][]byte, lines [][]byte) [][]byte {
	doc := bytes.Join(lines, []byte("\n"))
	if len(bytes.TrimSpace(doc)) == 0 {
		return docs
	}
	return append(docs, doc)
}
