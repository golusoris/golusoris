package invoice_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/money"
	"github.com/golusoris/golusoris/payments/invoice"
)

func sampleInvoice(t *testing.T) invoice.Invoice {
	t.Helper()
	eur := money.New(2900, "EUR")
	return invoice.Invoice{
		ID:        "inv-1",
		Number:    "INV-000001",
		TenantID:  "tenant1",
		Status:    invoice.StatusIssued,
		IssueDate: time.Date(2026, 4, 14, 0, 0, 0, 0, time.UTC),
		DueDate:   time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC),
		LineItems: []invoice.LineItem{
			{Description: "Pro plan, monthly", Quantity: 1, UnitPrice: eur, Amount: eur},
		},
		Subtotal: eur,
		Total:    eur,
		Currency: "EUR",
		BillFrom: invoice.Party{Name: "Acme Inc."},
		BillTo:   invoice.Party{Name: "Customer Ltd."},
	}
}

func TestValidate_OK(t *testing.T) {
	t.Parallel()
	require.NoError(t, invoice.Validate(sampleInvoice(t)))
}

func TestValidate_Fails(t *testing.T) {
	t.Parallel()
	bad := sampleInvoice(t)
	bad.Number = ""
	require.Error(t, invoice.Validate(bad))

	bad = sampleInvoice(t)
	bad.LineItems = nil
	require.Error(t, invoice.Validate(bad))

	bad = sampleInvoice(t)
	bad.Currency = ""
	require.Error(t, invoice.Validate(bad))
}

func TestHTMLRenderer_Render(t *testing.T) {
	t.Parallel()
	r := invoice.NewHTMLRenderer(nil)
	out, err := r.Render(context.Background(), sampleInvoice(t))
	require.NoError(t, err)
	html := string(out)
	require.True(t, strings.Contains(html, "INV-000001"))
	require.True(t, strings.Contains(html, "Pro plan, monthly"))
	require.True(t, strings.Contains(html, "Acme Inc."))
	require.True(t, strings.Contains(html, "Customer Ltd."))
	require.True(t, strings.HasPrefix(html, "<!doctype html>"))
	require.Equal(t, "text/html; charset=utf-8", r.ContentType())
}

func TestMemoryNumberer_Sequential(t *testing.T) {
	t.Parallel()
	n := invoice.NewMemoryNumberer("INV", 6)
	first, err := n.Next(context.Background(), "t1")
	require.NoError(t, err)
	require.Equal(t, "INV-000001", first)

	second, _ := n.Next(context.Background(), "t1")
	require.Equal(t, "INV-000002", second)

	// Per-tenant counter.
	other, _ := n.Next(context.Background(), "t2")
	require.Equal(t, "INV-000001", other)
}

func TestMemoryNumberer_NoPrefix(t *testing.T) {
	t.Parallel()
	n := invoice.NewMemoryNumberer("", 4)
	v, _ := n.Next(context.Background(), "t1")
	require.Equal(t, "0001", v)
}

func TestMemoryNumberer_CustomSeparator(t *testing.T) {
	t.Parallel()
	n := invoice.NewMemoryNumberer("INV", 3)
	n.SetSeparator("/")
	v, _ := n.Next(context.Background(), "t1")
	require.Equal(t, "INV/001", v)
}
