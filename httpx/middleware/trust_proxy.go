// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"context"
	"net"
	"net/http"
	"strings"
)

const maxForwardedHops = 64

type forwardedProtoKey struct{}

// TrustProxyOptions controls which proxy sources are trusted to supply
// X-Forwarded-For / X-Forwarded-Proto headers.
type TrustProxyOptions struct {
	// TrustedCIDRs is a list of CIDR ranges whose requests are trusted to
	// carry forwarded headers (e.g. "10.0.0.0/8" for in-cluster, "127.0.0.1/32"
	// for localhost). An empty slice means trust nothing.
	TrustedCIDRs []string
}

// TrustProxy rewrites r.RemoteAddr by walking X-Forwarded-For from the trusted
// direct peer toward the client. It selects the first untrusted hop from the
// right, so caller-supplied entries on the left cannot spoof the client IP.
//
// Without a proxy-trust policy, apps accept spoofed X-Forwarded-For from any
// client — which trivially bypasses rate limiters and geofencing.
func TrustProxy(opts TrustProxyOptions) Middleware {
	nets := parseCIDRs(opts.TrustedCIDRs)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(nets) == 0 || !peerInNets(r.RemoteAddr, nets) {
				stripForwardedHeaders(r.Header)
				next.ServeHTTP(w, r)
				return
			}
			if proto := validatedForwardedProto(r.Header); proto != "" {
				ctx := context.WithValue(r.Context(), forwardedProtoKey{}, proto)
				r = r.WithContext(ctx)
			}
			rewriteRemoteAddrFromXFF(r, nets)
			next.ServeHTTP(w, r)
		})
	}
}

// ForwardedProtoFromContext returns the external request scheme authenticated
// by [TrustProxy], or "" when no valid trusted value was supplied.
func ForwardedProtoFromContext(ctx context.Context) string {
	proto, _ := ctx.Value(forwardedProtoKey{}).(string)
	return proto
}

func validatedForwardedProto(header http.Header) string {
	values := header.Values("X-Forwarded-Proto")
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return ""
	}
	proto := strings.ToLower(strings.TrimSpace(values[0]))
	if proto != "http" && proto != "https" {
		return ""
	}
	return proto
}

func stripForwardedHeaders(header http.Header) {
	header.Del("Forwarded")
	header.Del("X-Forwarded-For")
	header.Del("X-Forwarded-Host")
	header.Del("X-Forwarded-Proto")
	header.Del("X-Real-IP")
}

// rewriteRemoteAddrFromXFF rewrites r.RemoteAddr from a validated
// X-Forwarded-For chain, but only when the direct peer is in nets.
// It is a no-op when nets is empty, the peer is untrusted, the request
// carries no X-Forwarded-For header, or the derived entry is empty.
func rewriteRemoteAddrFromXFF(r *http.Request, nets []*net.IPNet) {
	if len(nets) == 0 || !peerInNets(r.RemoteAddr, nets) {
		return
	}
	xffValues := r.Header.Values("X-Forwarded-For")
	if len(xffValues) == 0 {
		return
	}
	xff := strings.Join(xffValues, ",")
	if clientIP, ok := forwardedClientIP(xff, nets); ok {
		r.RemoteAddr = clientIP
	}
}

func forwardedClientIP(xff string, trusted []*net.IPNet) (string, bool) {
	end := len(xff)
	for range maxForwardedHops {
		comma := strings.LastIndexByte(xff[:end], ',')
		trimmedHop := strings.TrimSpace(xff[comma+1 : end])
		ip := net.ParseIP(strings.Trim(trimmedHop, "[]"))
		if ip == nil {
			return "", false
		}
		if !ipInNets(ip, trusted) {
			return ip.String(), true
		}
		if comma < 0 {
			return ip.String(), true
		}
		end = comma
	}
	return "", false
}

func parseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func peerInNets(remoteAddr string, nets []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ipInNets(ip, nets)
}

func ipInNets(ip net.IP, nets []*net.IPNet) bool {
	for _, network := range nets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
