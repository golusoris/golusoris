// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// RequestIDHeader is the header read + written by [RequestID].
const RequestIDHeader = "X-Request-ID"

const maxRequestIDBytes = 128

const requestIDCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:"

// RequestIDOptions controls when an inbound request ID may be retained.
type RequestIDOptions struct {
	// TrustedCIDRs contains direct peers allowed to supply X-Request-ID.
	TrustedCIDRs []string
}

type requestIDKey struct{}

// RequestID always replaces an inbound X-Request-ID with a fresh UUIDv7.
func RequestID(next http.Handler) http.Handler {
	return requestID(nil)(next)
}

// RequestIDFromTrustedPeers retains a bounded, valid inbound ID only when the
// direct peer belongs to opts.TrustedCIDRs. Run it before TrustProxy so it sees
// the actual proxy peer.
func RequestIDFromTrustedPeers(opts RequestIDOptions) Middleware {
	return requestID(parseCIDRs(opts.TrustedCIDRs))
}

func requestID(trustedPeers []*net.IPNet) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := ""
			inbound := r.Header.Get(RequestIDHeader)
			if peerInNets(r.RemoteAddr, trustedPeers) && validRequestID(inbound) {
				id = inbound
			} else {
				v, err := uuid.NewV7()
				if err == nil {
					id = v.String()
				}
			}
			if id != "" {
				r.Header.Set(RequestIDHeader, id)
				ctx := context.WithValue(r.Context(), requestIDKey{}, id)
				w.Header().Set(RequestIDHeader, id)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			r.Header.Del(RequestIDHeader)
			next.ServeHTTP(w, r)
		})
	}
}

func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDBytes {
		return false
	}
	for index := range len(id) {
		if strings.IndexByte(requestIDCharacters, id[index]) < 0 {
			return false
		}
	}
	return true
}

// RequestIDFromContext returns the request ID set by [RequestID] or "".
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}
