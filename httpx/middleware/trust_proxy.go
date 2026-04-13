package middleware

import (
	"net"
	"net/http"
	"strings"
)

// TrustProxyOptions controls which proxy sources are trusted to supply
// X-Forwarded-For / X-Forwarded-Proto headers.
type TrustProxyOptions struct {
	// TrustedCIDRs is a list of CIDR ranges whose requests are trusted to
	// carry forwarded headers (e.g. "10.0.0.0/8" for in-cluster, "127.0.0.1/32"
	// for localhost). An empty slice means trust nothing.
	TrustedCIDRs []string
}

// TrustProxy rewrites r.RemoteAddr from the first X-Forwarded-For entry when
// the direct peer is in TrustedCIDRs. Untrusted proxies' headers are
// ignored, preserving the real RemoteAddr.
//
// Without a proxy-trust policy, apps accept spoofed X-Forwarded-For from any
// client — which trivially bypasses rate limiters and geofencing.
func TrustProxy(opts TrustProxyOptions) Middleware {
	nets := parseCIDRs(opts.TrustedCIDRs)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(nets) > 0 && peerInNets(r.RemoteAddr, nets) {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					// First entry is the original client.
					if idx := strings.IndexByte(xff, ','); idx > 0 {
						xff = strings.TrimSpace(xff[:idx])
					}
					if xff != "" {
						r.RemoteAddr = xff
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
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
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
