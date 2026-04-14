package invoice

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
)

// DefaultHTMLTemplate is a minimal but production-usable invoice
// template. Apps can replace it via [HTMLRenderer.SetTemplate].
const DefaultHTMLTemplate = `<!doctype html>
<html lang="en"><head>
  <meta charset="utf-8">
  <title>Invoice {{.Number}}</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; max-width: 720px; margin: 2em auto; color: #222; }
    h1 { margin: 0 0 .25em; }
    .meta { color: #666; font-size: .9em; }
    table { width: 100%; border-collapse: collapse; margin: 2em 0; }
    th, td { padding: .5em .75em; text-align: left; border-bottom: 1px solid #eee; }
    th { background: #fafafa; font-weight: 600; }
    .num { text-align: right; font-variant-numeric: tabular-nums; }
    .totals { margin-top: 1em; }
    .totals tr td { border: 0; padding: .25em .75em; }
    .totals .total { font-size: 1.1em; font-weight: 700; border-top: 2px solid #222; }
    .parties { display: flex; gap: 3em; }
    .parties section { flex: 1; }
    .parties h3 { margin: 0 0 .5em; font-size: .95em; color: #666; text-transform: uppercase; letter-spacing: .05em; }
    .notes { color: #555; font-size: .9em; margin-top: 2em; padding-top: 1em; border-top: 1px solid #eee; }
  </style>
</head><body>
<h1>Invoice {{.Number}}</h1>
<p class="meta">
  Issued {{.IssueDate.Format "2006-01-02"}}{{if not .DueDate.IsZero}} · Due {{.DueDate.Format "2006-01-02"}}{{end}} · Status: {{.Status}}
</p>

<div class="parties">
  <section><h3>From</h3>
    <div>{{.BillFrom.Name}}</div>
    {{range .BillFrom.Address}}<div>{{.}}</div>{{end}}
    {{if .BillFrom.TaxID}}<div>Tax ID: {{.BillFrom.TaxID}}</div>{{end}}
    {{if .BillFrom.Email}}<div>{{.BillFrom.Email}}</div>{{end}}
  </section>
  <section><h3>To</h3>
    <div>{{.BillTo.Name}}</div>
    {{range .BillTo.Address}}<div>{{.}}</div>{{end}}
    {{if .BillTo.TaxID}}<div>Tax ID: {{.BillTo.TaxID}}</div>{{end}}
    {{if .BillTo.Email}}<div>{{.BillTo.Email}}</div>{{end}}
  </section>
</div>

<table>
  <thead><tr>
    <th>Description</th><th class="num">Qty</th><th class="num">Unit price</th><th class="num">Amount</th>
  </tr></thead>
  <tbody>
  {{range .LineItems}}<tr>
    <td>{{.Description}}</td>
    <td class="num">{{printf "%g" .Quantity}}</td>
    <td class="num">{{.UnitPrice.String}}</td>
    <td class="num">{{.Amount.String}}</td>
  </tr>{{end}}
  </tbody>
</table>

<table class="totals">
  <tr><td class="num">Subtotal</td><td class="num">{{.Subtotal.String}}</td></tr>
  {{if not .Tax.IsZero}}<tr><td class="num">Tax</td><td class="num">{{.Tax.String}}</td></tr>{{end}}
  <tr class="total"><td class="num">Total</td><td class="num">{{.Total.String}}</td></tr>
</table>

{{if .Notes}}<div class="notes">{{.Notes}}</div>{{end}}
</body></html>
`

// HTMLRenderer renders an Invoice to HTML via html/template.
type HTMLRenderer struct {
	tmpl *template.Template
}

// NewHTMLRenderer returns a renderer using either the supplied
// template or [DefaultHTMLTemplate] when nil.
func NewHTMLRenderer(t *template.Template) *HTMLRenderer {
	if t == nil {
		t = template.Must(template.New("invoice").Parse(DefaultHTMLTemplate))
	}
	return &HTMLRenderer{tmpl: t}
}

// SetTemplate replaces the renderer's template (e.g. branded version).
func (r *HTMLRenderer) SetTemplate(t *template.Template) { r.tmpl = t }

// Render implements [Renderer].
func (r *HTMLRenderer) Render(_ context.Context, inv Invoice) ([]byte, error) {
	if err := Validate(inv); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := r.tmpl.Execute(&buf, inv); err != nil {
		return nil, fmt.Errorf("invoice: render: %w", err)
	}
	return buf.Bytes(), nil
}

// ContentType implements [Renderer].
func (r *HTMLRenderer) ContentType() string { return "text/html; charset=utf-8" }

// Compile-time check.
var _ Renderer = (*HTMLRenderer)(nil)
