// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package out

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// closeErrBody is a response body whose Close always fails.
type closeErrBody struct {
	io.Reader
	err error
}

func (b closeErrBody) Close() error { return b.err }

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newPostDispatcher(rt http.RoundTripper) *Dispatcher {
	opts := Options{}
	opts.defaults()
	return &Dispatcher{
		opts:   opts,
		client: &http.Client{Transport: rt},
		logger: slog.New(slog.DiscardHandler),
	}
}

// Positive: a body-close failure after a 2xx is logged, not reported, so the
// acknowledged delivery is never retried.
func TestPost_closeErrorIsNotDeliveryFailure(t *testing.T) {
	t.Parallel()
	var defaults Options
	defaults.defaults()
	d := newPostDispatcher(rtFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "sha256=sig", r.Header.Get(defaults.SignHeader))
		require.Equal(t, "evt", r.Header.Get("X-Webhook-Event"))
		require.Equal(t, "del-1", r.Header.Get("X-Webhook-Delivery"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       closeErrBody{Reader: strings.NewReader("ok"), err: errors.New("close boom")},
			Request:    r,
		}, nil
	}))
	code, err := d.post(context.Background(), "http://endpoint.test/hook", "del-1", "evt", "sig", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)
}

// Negative: a transport error is returned with a zero status code.
func TestPost_transportError(t *testing.T) {
	t.Parallel()
	want := errors.New("dial boom")
	d := newPostDispatcher(rtFunc(func(*http.Request) (*http.Response, error) { return nil, want }))
	code, err := d.post(context.Background(), "http://endpoint.test/hook", "del-2", "evt", "sig", nil)
	require.ErrorIs(t, err, want)
	require.Equal(t, 0, code)
}

// Boundary: non-2xx status codes pass through unchanged with a nil error;
// deliver decides what to do with them.
func TestPost_statusPassthrough(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		d := newPostDispatcher(rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}))
		code, err := d.post(context.Background(), "http://endpoint.test/hook", "del-3", "evt", "sig", nil)
		require.NoError(t, err)
		require.Equal(t, status, code)
	}
}
