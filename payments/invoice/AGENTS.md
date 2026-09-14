<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# payments/invoice

Invoice modelling, sequential numbering, and HTML rendering for SaaS
billing. PDF rendering is intentionally not bundled — see Notes below.

## Surface

- `invoice.Invoice` + `invoice.LineItem` + `invoice.Party` types.
- `invoice.Status` lifecycle: draft, issued, paid, void.
- `invoice.Numberer` interface + `NewMemoryNumberer(prefix, width)`.
- `invoice.Renderer` interface + `NewHTMLRenderer(*template.Template)`.
- `invoice.Validate(Invoice) error` — structural check before render.
- `invoice.DefaultHTMLTemplate` — minimal stylable HTML; override via
  `HTMLRenderer.SetTemplate`.

## Numbering rules

`Numberer.Next` MUST be gap-free under concurrent calls. The
`MemoryNumberer` uses a per-tenant counter behind a mutex. A
Postgres-backed Numberer should use either a `SERIAL` per tenant or
a `next_invoice_number` row updated inside `SELECT … FOR UPDATE`.
Never use UUIDs or random IDs as invoice numbers — auditors and tax
authorities expect strict sequential numbering per tenant.

`Reset(tenantID)` exists for tests only — calling it in production
breaks audit trails.

## PDF — not here, by design

The framework's `pdf/` module (chromedp/HTML→PDF) is in the CGO-
deferred sub-module list (PLAN §3.16b). Until it lands, options:

1. Apps run a stand-alone HTML→PDF microservice (Gotenberg, weasyprint
   container) and feed it the bytes from `HTMLRenderer.Render`.
2. Apps store the HTML directly — most jurisdictions accept HTML/PDF
   equivalents for digital invoices, and modern email clients render
   inline HTML well.
3. When `pdf/` lands, wire a `PDFRenderer` that wraps `HTMLRenderer`
   and pipes through chromedp.

## Composition

Typical SaaS billing flow:

```go
sub, _ := subsService.Get(ctx, subID)
inv := invoice.Invoice{
    ID:        must(id.New().NewUUID()).String(),
    Number:    must(numberer.Next(ctx, sub.CustomerID)),
    TenantID:  sub.CustomerID,
    Status:    invoice.StatusIssued,
    IssueDate: clk.Now(),
    LineItems: []invoice.LineItem{...},
    // ...
}
html, _ := htmlRenderer.Render(ctx, inv)
_ = bucket.Put(ctx, "invoices/"+inv.ID+".html", bytes.NewReader(html))
_ = notify.Send(ctx, notify.Message{
    To: []string{customer.Email}, Subject: "Invoice " + inv.Number,
    HTML: string(html),
})
```

(All composed pieces live in this framework.)
