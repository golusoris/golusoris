// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package mcp

import (
	"context"
	"net/http"
	"time"
)

// HTTPShutdownGrace bounds a streamable-HTTP server's graceful shutdown. It
// exceeds the 5s that [http.Server.Shutdown] waits on a connection that has
// not sent a request yet (golang/go#22682), so one unused client connection
// cannot exhaust the grace.
const HTTPShutdownGrace = 10 * time.Second

// EndStreamsOnShutdown wraps the streamable-HTTP handler h so that its GET
// requests, the long-lived server-to-client event streams, end as soon as srv
// begins shutting down or ctx ends. [http.Server.Shutdown] does not cancel
// active requests, so without this every connected client holds Shutdown
// until its deadline. Other requests, such as in-flight tool calls, keep
// draining.
func EndStreamsOnShutdown(ctx context.Context, srv *http.Server, h http.Handler) http.Handler {
	streams, endStreams := context.WithCancel(ctx)
	srv.RegisterOnShutdown(endStreams)
	// A func rather than the context: contextcheck treats a captured context as the handler's own.
	onEnd := func(f func()) (stop func() bool) { return context.AfterFunc(streams, f) }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			h.ServeHTTP(w, r)
			return
		}
		reqCtx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer onEnd(cancel)()
		h.ServeHTTP(w, r.WithContext(reqCtx))
	})
}
