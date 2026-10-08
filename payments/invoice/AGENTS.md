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

`Numberer.Next` MUST be gap-free under concurrent calls. `MemoryNumberer` uses per-tenant counter behind mutex. Postgres-backed Numberer should use either `SERIAL` per tenant or
`next_invoice_number` row updated inside `SELECT … FOR UPDATE`.
Never use UUIDs or random IDs as invoice numbers — auditors and tax
authorities expect strict sequential numbering per tenant.

`Reset(tenantID)` exists for tests only — calling it in production
breaks audit trails.

## PDF composition

`payments/invoice` stays renderer-neutral. Framework's shipped `pdf/` module
uses chromedp for HTML→PDF in its own Go submodule. Options:

1. Apps run stand-alone HTML→PDF microservice (Gotenberg, weasyprint
 container) and feed it bytes from `HTMLRenderer.Render`.
2. Apps store HTML directly — most jurisdictions accept HTML/PDF
 equivalents for digital invoices, and modern email clients render
 inline HTML well.
3. Wire app-level `PDFRenderer` that wraps `HTMLRenderer` and sends output
 through `pdf/`.

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
