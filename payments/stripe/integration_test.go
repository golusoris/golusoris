// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build integration

package stripe

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdk "github.com/stripe/stripe-go/v87"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golusoris/golusoris/internal/testimages"
)

// startStripeMock boots a real stripe-mock container and returns its host:port address.
// Skips cleanly when Docker is unavailable.
func startStripeMock(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        testimages.StripeMock,
			ReaperImage:  testimages.Ryuk,
			ExposedPorts: []string{"12111/tcp"},
			WaitingFor:   wait.ForListeningPort("12111/tcp"),
		},
		Started: true,
	}
	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("payments/stripe: start stripe-mock container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		if termErr := container.Terminate(stopCtx); termErr != nil {
			t.Logf("payments/stripe: terminate stripe-mock container: %v", termErr)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("payments/stripe: container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "12111/tcp")
	if err != nil {
		t.Fatalf("payments/stripe: mapped port: %v", err)
	}
	return net.JoinHostPort(host, port.Port())
}

func newRealMockClient(t *testing.T, addr string) *Client {
	t.Helper()
	backends := sdk.NewBackendsWithConfig(&sdk.BackendConfig{
		URL:               sdk.String("http://" + addr),
		HTTPClient:        &http.Client{Timeout: 10 * time.Second},
		LeveledLogger:     &sdk.LeveledLogger{Level: sdk.LevelNull},
		MaxNetworkRetries: sdk.Int64(0),
	})
	sc := sdk.NewClient("sk_test_123", sdk.WithBackends(backends))
	return &Client{sc: sc, logger: slog.New(slog.DiscardHandler)}
}

func TestIntegration_Positive(t *testing.T) {
	t.Parallel()
	addr := startStripeMock(t)
	c := newRealMockClient(t, addr)

	t.Run("checkout session", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		url, err := c.NewCheckoutSession(ctx, CheckoutParams{
			Mode:       "payment",
			LineItems:  map[string]int64{"price_123": 1},
			SuccessURL: "https://example.com/success",
			CancelURL:  "https://example.com/cancel",
		})
		require.NoError(t, err)
		require.NotEmpty(t, url)
	})

	t.Run("portal session", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		url, err := c.NewPortalSession(ctx, PortalParams{
			CustomerID: "cus_123",
			ReturnURL:  "https://example.com/billing",
		})
		require.NoError(t, err)
		require.NotEmpty(t, url)
	})

	t.Run("create payment intent", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pi, err := c.CreatePaymentIntent(ctx, 2000, "eur", "")
		require.NoError(t, err)
		require.NotNil(t, pi)
		require.NotEmpty(t, pi.ID)
	})
}

func TestIntegration_Negative(t *testing.T) {
	t.Parallel()
	addr := startStripeMock(t)
	c := newRealMockClient(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// An invalid mode should be rejected by stripe-mock with a 400 Bad Request.
	url, err := c.NewCheckoutSession(ctx, CheckoutParams{
		Mode:       "invalid_mode",
		SuccessURL: "https://example.com/success",
		CancelURL:  "https://example.com/cancel",
	})
	require.Error(t, err)
	require.Empty(t, url)

	var serr *sdk.Error
	require.True(t, errors.As(err, &serr), "expected error to unwrap to *sdk.Error")
	require.Equal(t, http.StatusBadRequest, serr.HTTPStatusCode)
}

func TestIntegration_Boundary(t *testing.T) {
	t.Parallel()
	addr := startStripeMock(t)
	c := newRealMockClient(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pi, err := c.CreatePaymentIntent(ctx, 1, "eur", "")
	require.NoError(t, err)
	require.NotNil(t, pi)
	require.NotEmpty(t, pi.ID)
}
