// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// maxOutputDepth bounds the walk to an expression's result node.
const maxOutputDepth = 64

// walk collects findings and the label sets each aggregation and the whole
// expression can see.
type walk struct {
	c        *checker
	expr     Expr
	findings []Finding
	all      *labelSet
	scopes   map[*parser.AggregateExpr]*labelSet
	selected map[*parser.VectorSelector]*labelSet
}

func (c *checker) check(e Expr) []Finding {
	ast, err := c.parser.ParseExpr(expandGrafanaVars(e.Query))
	if err != nil {
		return []Finding{{Source: e.Source, Query: e.Query, Problem: "does not parse: " + err.Error()}}
	}
	w := &walk{c: c, expr: e, all: newLabelSet(), scopes: map[*parser.AggregateExpr]*labelSet{}, selected: map[*parser.VectorSelector]*labelSet{}}
	parser.Inspect(ast, func(node parser.Node, path []parser.Node) error {
		w.visit(node, path)
		return nil
	})
	w.checkGroupings()
	w.checkTemplates(ast)
	return w.findings
}

func (w *walk) report(format string, args ...any) {
	w.findings = append(w.findings, Finding{Source: w.expr.Source, Query: w.expr.Query, Problem: fmt.Sprintf(format, args...)})
}

func (w *walk) visit(node parser.Node, path []parser.Node) {
	switch n := node.(type) {
	case *parser.VectorSelector:
		labels := w.selector(n)
		w.selected[n] = labels
		w.addToScopes(path, labels)
	case *parser.Call:
		if dst := createdLabel(n); dst != "" {
			created := newLabelSet()
			created.names[dst] = struct{}{}
			w.addToScopes(path, created)
		}
	}
}

// addToScopes makes labels visible to every enclosing aggregation and to the
// expression as a whole.
func (w *walk) addToScopes(path []parser.Node, labels *labelSet) {
	w.all.merge(labels)
	for _, anc := range path {
		agg, ok := anc.(*parser.AggregateExpr)
		if !ok {
			continue
		}
		scope, ok := w.scopes[agg]
		if !ok {
			scope = newLabelSet()
			w.scopes[agg] = scope
		}
		scope.merge(labels)
	}
}

// createdLabel returns the destination label of label_replace/label_join.
func createdLabel(call *parser.Call) string {
	if call.Func == nil || (call.Func.Name != "label_replace" && call.Func.Name != "label_join") || len(call.Args) < 2 {
		return ""
	}
	if lit, ok := call.Args[1].(*parser.StringLiteral); ok {
		return lit.Val
	}
	return ""
}

// selector resolves one vector selector and checks its label matchers.
func (w *walk) selector(vs *parser.VectorSelector) *labelSet {
	name, nameMatcher := metricName(vs)
	allowed := w.resolve(name, nameMatcher)
	for _, m := range vs.LabelMatchers {
		if m.Name == model.MetricNameLabel || allowed.has(m.Name) {
			continue
		}
		w.report("label %q is not on %s (known: %s)", m.Name, describe(name, nameMatcher), allowed.sorted())
	}
	return allowed
}

func metricName(vs *parser.VectorSelector) (string, *labels.Matcher) {
	if vs.Name != "" {
		return vs.Name, nil
	}
	for _, m := range vs.LabelMatchers {
		if m.Name != model.MetricNameLabel {
			continue
		}
		if m.Type == labels.MatchEqual {
			return m.Value, nil
		}
		return "", m
	}
	return "", nil
}

func describe(name string, m *labels.Matcher) string {
	if m != nil {
		return "series matching " + m.String()
	}
	return name
}

// resolve returns the labels a series carries (plus target labels), or an
// open set for recorded, synthetic and regex-named series.
func (w *walk) resolve(name string, nameMatcher *labels.Matcher) *labelSet {
	set := newLabelSet()
	for l := range w.c.cfg.targetLabels {
		set.names[l] = struct{}{}
	}
	switch {
	case nameMatcher != nil:
		set.open = true
		if !w.anySeriesMatches(nameMatcher) {
			w.report("no known series matches %s", nameMatcher)
		}
	case name == "":
		set.open = true
		w.report("selector has no metric name")
	default:
		w.resolveNamed(name, set)
	}
	return set
}

func (w *walk) resolveNamed(name string, set *labelSet) {
	if _, ok := w.c.cfg.open[name]; ok {
		set.open = true
		return
	}
	def, ok := w.c.cat.LookupSeries(name)
	if !ok {
		set.open = true
		w.report("metric %q is not emitted (not in the catalog)", name)
		return
	}
	for _, l := range def.SeriesLabels(name) {
		set.names[l] = struct{}{}
	}
}

func (w *walk) anySeriesMatches(m *labels.Matcher) bool {
	for _, s := range w.c.cat.Series() {
		if m.Matches(s) {
			return true
		}
	}
	for s := range w.c.cfg.open {
		if m.Matches(s) {
			return true
		}
	}
	return false
}

// checkGroupings reports by() labels no selector under the aggregation has.
func (w *walk) checkGroupings() {
	for agg, scope := range w.scopes {
		if agg.Without {
			continue
		}
		for _, l := range agg.Grouping {
			if !scope.has(l) {
				w.report("%s by (%s): label %q is on none of the aggregated series (known: %s)", agg.Op, strings.Join(agg.Grouping, ", "), l, scope.sorted())
			}
		}
	}
}

var templateLabelRE = regexp.MustCompile(`\$labels\.([a-zA-Z_][a-zA-Z0-9_]*)`)

// TemplateLabels extracts the {{ $labels.x }} names from rule templates.
func TemplateLabels(templates ...string) []string {
	var out []string
	for _, t := range templates {
		for _, m := range templateLabelRE.FindAllStringSubmatch(t, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// checkTemplates reports {{ $labels.x }} that the alert's result series
// cannot carry.
func (w *walk) checkTemplates(ast parser.Expr) {
	if len(w.expr.TemplateLabels) == 0 {
		return
	}
	result := w.outputLabels(ast)
	for _, l := range w.expr.StaticLabels {
		result.names[l] = struct{}{}
	}
	for _, l := range w.expr.TemplateLabels {
		if !result.has(l) {
			w.report("template reads $labels.%s, but result series carry only: %s", l, result.sorted())
		}
	}
}

// outputLabels approximates the labels of the expression's result: it
// descends through parentheses and comparisons to the result-defining node;
// a by() aggregation yields exactly its grouping, a selector its own labels,
// anything else the union of every selector's labels.
func (w *walk) outputLabels(ast parser.Expr) *labelSet {
	node := ast
	for range maxOutputDepth {
		next, done := w.step(node)
		if done != nil {
			return done
		}
		if next == nil {
			break
		}
		node = next
	}
	return w.union()
}

// step moves one level towards the result node, or returns its labels.
func (w *walk) step(node parser.Expr) (parser.Expr, *labelSet) {
	switch n := node.(type) {
	case *parser.ParenExpr:
		return n.Expr, nil
	case *parser.BinaryExpr:
		if _, scalar := n.LHS.(*parser.NumberLiteral); scalar {
			return n.RHS, nil
		}
		return n.LHS, nil
	case *parser.AggregateExpr:
		if n.Without {
			return nil, nil
		}
		set := newLabelSet()
		for _, l := range n.Grouping {
			set.names[l] = struct{}{}
		}
		return nil, set
	case *parser.VectorSelector:
		out := newLabelSet()
		out.merge(w.selected[n])
		return nil, out
	}
	return nil, nil
}

func (w *walk) union() *labelSet {
	out := newLabelSet()
	out.merge(w.all)
	return out
}

// grafanaVarRE matches $var, ${var}, ${var:format} and [[var]].
var grafanaVarRE = regexp.MustCompile(`\$\{([a-zA-Z0-9_]+)(?::[^}]*)?\}|\$([a-zA-Z0-9_]+)|\[\[([a-zA-Z0-9_]+)\]\]`)

// expandGrafanaVars replaces Grafana variables outside string literals with
// parseable stand-ins: durations after "[" or ":" (range/step), numbers
// elsewhere. Variables inside quotes (matcher values) stay literal.
func expandGrafanaVars(q string) string {
	var b strings.Builder
	b.Grow(len(q))
	quote := byte(0)
	for i := 0; i < len(q); i++ {
		ch := q[i]
		switch {
		case quote != 0:
			b.WriteByte(ch)
			quote = closeQuote(q, i, quote)
		case ch == '"' || ch == '\'' || ch == '`':
			b.WriteByte(ch)
			quote = ch
		default:
			i += writeVar(&b, q, i)
		}
	}
	return b.String()
}

// closeQuote returns the open quote after q[i], or 0 when q[i] closes it.
func closeQuote(q string, i int, quote byte) byte {
	if q[i] == quote && (quote == '`' || !escaped(q, i)) {
		return 0
	}
	return quote
}

// escaped reports whether q[i] follows an odd run of backslashes.
func escaped(q string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && q[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// writeVar writes q[i] or the stand-in for a variable starting at i and
// returns how many extra bytes it consumed.
func writeVar(b *strings.Builder, q string, i int) int {
	loc := grafanaVarRE.FindStringSubmatchIndex(q[i:])
	if loc == nil || loc[0] != 0 {
		b.WriteByte(q[i])
		return 0
	}
	m := grafanaVarRE.FindStringSubmatch(q[i:])
	name := m[1] + m[2] + m[3]
	b.WriteString(standIn(name, previousNonSpace(q, i)))
	return loc[1] - 1
}

func previousNonSpace(q string, i int) byte {
	for j := i - 1; j >= 0; j-- {
		if q[j] != ' ' && q[j] != '\t' && q[j] != '\n' {
			return q[j]
		}
	}
	return 0
}

func standIn(name string, prev byte) string {
	switch name {
	case "__range_s", "__range_ms", "__interval_ms":
		return "300"
	case "__rate_interval", "__interval", "__range":
		return "5m"
	}
	if prev == '[' || prev == ':' {
		return "5m"
	}
	return "1"
}
