// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheLeaderStreamsUntilClose(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.MaxInFlight = 1
	c, _ := testCache(t, opts)
	body := &countedBody{Reader: strings.NewReader("stream")}
	transport := c.Wrap(transportFunc(func(*http.Request) (*http.Response, error) {
		resp := response(200, "stream", "Cache-Control", "no-store")
		resp.Body = body
		return resp, nil
	}))
	resp, err := transport.RoundTrip(request(t, "http://provider.test/data"))
	require.NoError(t, err)
	data := make([]byte, len("stream"))
	_, err = io.ReadFull(resp.Body, data)
	require.NoError(t, err)
	require.Equal(t, "stream", string(data))
	require.Zero(t, body.closes.Load())
	require.Len(t, c.slots, 1)
	require.NoError(t, resp.Body.Close())
	require.NoError(t, resp.Body.Close())
	require.EqualValues(t, 1, body.closes.Load())
	require.Empty(t, c.slots)
}

func TestStreamingHandoffAbandonOrders(t *testing.T) {
	t.Parallel()
	for _, publishFirst := range []bool{true, false} {
		t.Run(strconv.FormatBool(publishFirst), func(t *testing.T) {
			t.Parallel()
			body := &countedBody{Reader: strings.NewReader("stream")}
			result := &flightResult{streaming: true, response: &http.Response{Body: body}}
			var handoff streamingHandoff
			if publishFirst {
				require.NoError(t, handoff.publish(result))
				require.Zero(t, body.closes.Load())
				require.NoError(t, handoff.abandon())
			} else {
				require.NoError(t, handoff.abandon())
				require.ErrorIs(t, handoff.publish(result), context.Canceled)
			}
			require.NoError(t, handoff.abandon())
			require.EqualValues(t, 1, body.closes.Load())
		})
	}
}

func TestStreamingHandoffTakenBodyStaysOpen(t *testing.T) {
	t.Parallel()
	body := &countedBody{Reader: strings.NewReader("stream")}
	result := &flightResult{streaming: true, response: &http.Response{Body: body}}
	var handoff streamingHandoff
	require.NoError(t, handoff.publish(result))
	handoff.take()
	require.NoError(t, handoff.abandon())
	require.Zero(t, body.closes.Load())
	require.NoError(t, body.Close())
	require.EqualValues(t, 1, body.closes.Load())
}

func TestStreamingHandoffCloseErrors(t *testing.T) {
	t.Parallel()
	for _, publishFirst := range []bool{true, false} {
		t.Run(strconv.FormatBool(publishFirst), func(t *testing.T) {
			t.Parallel()
			cause := errors.New("synthetic close failure")
			body := &countedBody{Reader: strings.NewReader("stream"), err: cause}
			result := &flightResult{streaming: true, response: &http.Response{Body: body}}
			var handoff streamingHandoff
			if publishFirst {
				require.NoError(t, handoff.publish(result))
				require.ErrorIs(t, handoff.abandon(), cause)
			} else {
				require.NoError(t, handoff.abandon())
				err := handoff.publish(result)
				require.ErrorIs(t, err, cause)
				require.ErrorIs(t, err, context.Canceled)
			}
			require.NoError(t, handoff.abandon())
			require.EqualValues(t, 1, body.closes.Load())
		})
	}
}
