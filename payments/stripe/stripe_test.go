package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdk "github.com/stripe/stripe-go/v82"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
)

// capturedRequest records what the stub Stripe server received.
type capturedRequest struct {
	method string
	path   string
	form   url.Values
	auth   string
}

// stubServer spins up an httptest.Server that records the inbound request and
// replies with status + body. The recorded request is written to *got.
func stubServer(t *testing.T, status int, body string, got *capturedRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("stub: read body: %v", err)
		}
		form, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Errorf("stub: parse form: %v", err)
		}
		*got = capturedRequest{
			method: r.Method,
			path:   r.URL.Path,
			form:   form,
			auth:   r.Header.Get("Authorization"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newStubClient builds a [Client] whose Stripe backend is pointed at srv, so
// every API call is hermetic. Network retries are disabled and the SDK logger
// is silenced.
func newStubClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	backends := sdk.NewBackendsWithConfig(&sdk.BackendConfig{
		URL:               sdk.String(srv.URL),
		HTTPClient:        srv.Client(),
		LeveledLogger:     &sdk.LeveledLogger{Level: sdk.LevelNull},
		MaxNetworkRetries: sdk.Int64(0),
	})
	sc := sdk.NewClient("sk_test_123", sdk.WithBackends(backends))
	return &Client{sc: sc, logger: slog.New(slog.DiscardHandler)}
}

// errorBody is a canned Stripe API error response body.
func errorBody(typ, msg, code string) string {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]string{"type": typ, "message": msg, "code": code},
	})
	return string(b)
}

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{name: "valid key", opts: Options{SecretKey: "sk_test_abc"}, wantErr: false},
		{name: "empty key", opts: Options{SecretKey: ""}, wantErr: true},
		{
			name: "key with webhook secret",
			opts: Options{SecretKey: "sk_live_xyz", WebhookSecret: "whsec_123"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := New(tc.opts, slog.New(slog.DiscardHandler))
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, c)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, c)
			require.NotNil(t, c.sc)
		})
	}
}

func TestNew_NilLoggerAccepted(t *testing.T) {
	t.Parallel()
	// New does not dereference the logger at construction; a nil logger must
	// not panic here (the wrapper only logs on call paths).
	c, err := New(Options{SecretKey: "sk_test_abc"}, nil)
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestNewCheckoutSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		params     CheckoutParams
		status     int
		body       string
		wantURL    string
		wantErr    bool
		wantMode   string // expected mode form field
		assertForm func(t *testing.T, f url.Values)
	}{
		{
			name: "success defaults mode to payment",
			params: CheckoutParams{
				LineItems:  map[string]int64{"price_abc": 2},
				SuccessURL: "https://example.com/ok",
				CancelURL:  "https://example.com/no",
			},
			status:   http.StatusOK,
			body:     `{"id":"cs_test_1","url":"https://checkout.stripe.com/c/cs_test_1"}`,
			wantURL:  "https://checkout.stripe.com/c/cs_test_1",
			wantMode: "payment",
			assertForm: func(t *testing.T, f url.Values) {
				t.Helper()
				require.Equal(t, "https://example.com/ok", f.Get("success_url"))
				require.Equal(t, "https://example.com/no", f.Get("cancel_url"))
				require.Equal(t, "price_abc", f.Get("line_items[0][price]"))
				require.Equal(t, "2", f.Get("line_items[0][quantity]"))
			},
		},
		{
			name: "subscription with customer + ref + metadata",
			params: CheckoutParams{
				CustomerID:        "cus_42",
				LineItems:         map[string]int64{"price_sub": 1},
				SuccessURL:        "https://example.com/ok",
				CancelURL:         "https://example.com/no",
				Mode:              "subscription",
				ClientReferenceID: "order_99",
				Metadata:          map[string]string{"plan": "pro"},
			},
			status:   http.StatusOK,
			body:     `{"id":"cs_test_2","url":"https://checkout.stripe.com/c/cs_test_2"}`,
			wantURL:  "https://checkout.stripe.com/c/cs_test_2",
			wantMode: "subscription",
			assertForm: func(t *testing.T, f url.Values) {
				t.Helper()
				require.Equal(t, "cus_42", f.Get("customer"))
				require.Equal(t, "order_99", f.Get("client_reference_id"))
				require.Equal(t, "pro", f.Get("metadata[plan]"))
			},
		},
		{
			name: "stripe error propagates",
			params: CheckoutParams{
				LineItems:  map[string]int64{"price_abc": 1},
				SuccessURL: "https://example.com/ok",
				CancelURL:  "https://example.com/no",
			},
			status:  http.StatusBadRequest,
			body:    errorBody("invalid_request_error", "No such price", "resource_missing"),
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got capturedRequest
			srv := stubServer(t, tc.status, tc.body, &got)
			c := newStubClient(t, srv)

			gotURL, err := c.NewCheckoutSession(context.Background(), tc.params)
			if tc.wantErr {
				require.Error(t, err)
				require.Empty(t, gotURL)
				var serr *sdk.Error
				require.ErrorAs(t, err, &serr, "wrapped error must unwrap to *sdk.Error")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantURL, gotURL)

			require.Equal(t, http.MethodPost, got.method)
			require.Equal(t, "/v1/checkout/sessions", got.path)
			require.Equal(t, "Bearer sk_test_123", got.auth)
			require.Equal(t, tc.wantMode, got.form.Get("mode"))
			if tc.assertForm != nil {
				tc.assertForm(t, got.form)
			}
		})
	}
}

func TestNewPortalSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		params  PortalParams
		status  int
		body    string
		wantURL string
		wantErr bool
	}{
		{
			name:    "success",
			params:  PortalParams{CustomerID: "cus_7", ReturnURL: "https://example.com/billing"},
			status:  http.StatusOK,
			body:    `{"id":"bps_1","url":"https://billing.stripe.com/p/session/bps_1"}`,
			wantURL: "https://billing.stripe.com/p/session/bps_1",
		},
		{
			name:    "stripe error propagates",
			params:  PortalParams{CustomerID: "cus_missing", ReturnURL: "https://example.com/billing"},
			status:  http.StatusBadRequest,
			body:    errorBody("invalid_request_error", "No such customer", "resource_missing"),
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got capturedRequest
			srv := stubServer(t, tc.status, tc.body, &got)
			c := newStubClient(t, srv)

			gotURL, err := c.NewPortalSession(context.Background(), tc.params)
			if tc.wantErr {
				require.Error(t, err)
				require.Empty(t, gotURL)
				var serr *sdk.Error
				require.ErrorAs(t, err, &serr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantURL, gotURL)

			require.Equal(t, http.MethodPost, got.method)
			require.Equal(t, "/v1/billing_portal/sessions", got.path)
			require.Equal(t, tc.params.CustomerID, got.form.Get("customer"))
			require.Equal(t, tc.params.ReturnURL, got.form.Get("return_url"))
		})
	}
}

func TestCreatePaymentIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		amount     int64
		currency   string
		customerID string
		status     int
		body       string
		wantID     string
		wantErr    bool
		wantCust   bool
	}{
		{
			name:     "success without customer",
			amount:   1500,
			currency: "usd",
			status:   http.StatusOK,
			body:     `{"id":"pi_1","amount":1500,"currency":"usd","status":"requires_payment_method"}`,
			wantID:   "pi_1",
		},
		{
			name:       "success with customer",
			amount:     999,
			currency:   "eur",
			customerID: "cus_55",
			status:     http.StatusOK,
			body:       `{"id":"pi_2","amount":999,"currency":"eur"}`,
			wantID:     "pi_2",
			wantCust:   true,
		},
		{
			name:     "stripe api error propagates",
			amount:   100,
			currency: "usd",
			status:   http.StatusInternalServerError,
			body:     errorBody("api_error", "internal error", ""),
			wantErr:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got capturedRequest
			srv := stubServer(t, tc.status, tc.body, &got)
			c := newStubClient(t, srv)

			pi, err := c.CreatePaymentIntent(context.Background(), tc.amount, tc.currency, tc.customerID)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, pi)
				var serr *sdk.Error
				require.ErrorAs(t, err, &serr)
				require.Equal(t, sdk.ErrorTypeAPI, serr.Type)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, pi)
			require.Equal(t, tc.wantID, pi.ID)

			require.Equal(t, http.MethodPost, got.method)
			require.Equal(t, "/v1/payment_intents", got.path)
			require.Equal(t, tc.currency, got.form.Get("currency"))
			if tc.wantCust {
				require.Equal(t, tc.customerID, got.form.Get("customer"))
			} else {
				require.Empty(t, got.form.Get("customer"))
			}
		})
	}
}

func TestErrorWrapping_PreservesMessage(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	srv := stubServer(t, http.StatusBadRequest,
		errorBody("invalid_request_error", "No such price: 'price_x'", "resource_missing"), &got)
	c := newStubClient(t, srv)

	_, err := c.NewCheckoutSession(context.Background(), CheckoutParams{
		LineItems:  map[string]int64{"price_x": 1},
		SuccessURL: "https://e/ok",
		CancelURL:  "https://e/no",
	})
	require.Error(t, err)
	// Wrapper prefix is present.
	require.Contains(t, err.Error(), "stripe: checkout session:")
	// And the underlying typed error survives the %w wrap.
	var serr *sdk.Error
	require.True(t, errors.As(err, &serr))
	require.Equal(t, sdk.ErrorTypeInvalidRequest, serr.Type)
	require.Equal(t, http.StatusBadRequest, serr.HTTPStatusCode)
}

// --- fx / config wiring ---

// writeConfig writes a temporary JSON config file and returns a *config.Config
// loaded from it (file watching disabled for hermetic tests).
func writeConfig(t *testing.T, payload map[string]any) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	cfg, err := config.New(config.Options{Files: []string{path}})
	require.NoError(t, err)
	return cfg
}

func TestNewFromConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload map[string]any
		wantErr bool
	}{
		{
			name: "valid secret key",
			payload: map[string]any{
				"payments": map[string]any{
					"stripe": map[string]any{
						"secret_key":     "sk_test_fromcfg",
						"webhook_secret": "whsec_cfg",
					},
				},
			},
		},
		{
			name: "missing secret key fails",
			payload: map[string]any{
				"payments": map[string]any{"stripe": map[string]any{"webhook_secret": "whsec_only"}},
			},
			wantErr: true,
		},
		{
			name:    "absent section fails",
			payload: map[string]any{"other": "value"},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := writeConfig(t, tc.payload)
			c, err := newFromConfig(cfg, slog.New(slog.DiscardHandler))
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, c)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, c)
			require.NotNil(t, c.sc)
		})
	}
}

// TestModule_ProvidesClient boots the Module via fxtest to cover the fx
// wiring: newFromConfig resolving Options from config and providing *Client.
func TestModule_ProvidesClient(t *testing.T) {
	t.Parallel()
	cfg := writeConfig(t, map[string]any{
		"payments": map[string]any{
			"stripe": map[string]any{"secret_key": "sk_test_module"},
		},
	})

	var resolved *Client
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		Module,
		fx.Populate(&resolved),
	)
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	require.NoError(t, app.Start(startCtx))

	require.NotNil(t, resolved)
	require.NotNil(t, resolved.sc)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	require.NoError(t, app.Stop(stopCtx))
}
