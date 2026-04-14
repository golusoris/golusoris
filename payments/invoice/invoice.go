// Package invoice provides invoice modelling, sequential numbering,
// and HTML rendering for SaaS billing. PDF rendering is intentionally
// not bundled — when the framework's pdf/ module lands (CGO chromedp
// sub-module), apps can pipe the HTML through it; for now most apps
// either store HTML directly or use a stand-alone HTML→PDF microservice.
//
// Numbering is sequential per tenant and gap-free under concurrency
// (the contract on [Numberer.Next] requires this; the in-memory impl
// uses a per-tenant counter, the Postgres impl uses a SERIAL or a
// row-locked next-number table).
//
// Usage:
//
//	r := invoice.NewHTMLRenderer(nil) // default template
//	num := invoice.NewMemoryNumberer("INV", 6) // INV-000001 …
//	id := id.New().NewUUID().String()
//	number, _ := num.Next(ctx, "tenant_42")
//	html, _ := r.Render(ctx, invoice.Invoice{
//	    ID: id, Number: number,
//	    TenantID: "tenant_42", CustomerID: "cust_99",
//	    IssueDate: time.Now(), DueDate: time.Now().AddDate(0, 0, 14),
//	    LineItems: []invoice.LineItem{
//	        {Description: "Pro plan, monthly", Quantity: 1,
//	         UnitPrice: money.New(2900, "EUR"), Amount: money.New(2900, "EUR")},
//	    },
//	    Subtotal: money.New(2900, "EUR"),
//	    Total:    money.New(2900, "EUR"),
//	    Currency: "EUR",
//	})
package invoice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golusoris/golusoris/money"
)

// Status is the invoice lifecycle state.
type Status string

// Lifecycle states.
const (
	StatusDraft  Status = "draft"
	StatusIssued Status = "issued"
	StatusPaid   Status = "paid"
	StatusVoid   Status = "void"
)

// LineItem is one row on an invoice.
type LineItem struct {
	Description string
	Quantity    float64
	UnitPrice   money.Money
	Amount      money.Money // Quantity * UnitPrice (caller computes; we don't multiply float×Money)
	Metadata    map[string]string
}

// Invoice is the canonical model.
type Invoice struct {
	ID         string
	Number     string
	TenantID   string
	CustomerID string
	Status     Status
	IssueDate  time.Time
	DueDate    time.Time
	LineItems  []LineItem
	Subtotal   money.Money
	Tax        money.Money
	Total      money.Money
	Currency   string
	Notes      string
	BillTo     Party
	BillFrom   Party
	Metadata   map[string]string
}

// Party is a billing party (issuer or recipient).
type Party struct {
	Name    string
	Email   string
	Address []string // multi-line address
	TaxID   string   // VAT / EIN / etc.
}

// Numberer assigns sequential numbers per tenant.
type Numberer interface {
	// Next returns the next number for the tenant. Implementations must
	// be gap-free under concurrent calls (use a transaction or atomic
	// counter — never UUIDs or random IDs).
	Next(ctx context.Context, tenantID string) (string, error)
}

// Renderer turns an Invoice into bytes (HTML, PDF, …).
type Renderer interface {
	Render(ctx context.Context, inv Invoice) ([]byte, error)
	// ContentType describes the output (e.g. "text/html").
	ContentType() string
}

// Validate reports the first structural issue with the invoice.
func Validate(inv Invoice) error {
	if inv.ID == "" {
		return errors.New("invoice: ID required")
	}
	if inv.Number == "" {
		return errors.New("invoice: Number required")
	}
	if inv.TenantID == "" {
		return errors.New("invoice: TenantID required")
	}
	if len(inv.LineItems) == 0 {
		return errors.New("invoice: at least one LineItem required")
	}
	if inv.Currency == "" {
		return errors.New("invoice: Currency required")
	}
	if inv.IssueDate.IsZero() {
		return errors.New("invoice: IssueDate required")
	}
	for i, li := range inv.LineItems {
		if li.Description == "" {
			return fmt.Errorf("invoice: LineItem[%d].Description required", i)
		}
	}
	return nil
}
