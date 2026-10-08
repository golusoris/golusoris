// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import "net/http"

type rewrite struct{ host string }

// RoundTrip redirects every request to the test server, but sends it on the
// shared default transport.
func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Host = r.host
	return http.DefaultTransport.RoundTrip(clone)
}
