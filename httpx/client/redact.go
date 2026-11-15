// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client

import (
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/httpx/internal/secretparam"
)

type redactingTransport struct {
	next    http.RoundTripper
	secrets map[string]struct{}
}

func (t *redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if span := trace.SpanFromContext(req.Context()); span.IsRecording() && req.URL != nil && req.URL.RawQuery != "" {
		if target, changed := redactedURL(req.URL, t.secrets); changed {
			span.SetAttributes(attribute.String("url.full", target))
		}
	}
	return t.next.RoundTrip(req) //nolint:wrapcheck // WHY: retryablehttp type-asserts the bare transport error; a wrap makes TLS verification failures retry.
}

func redactedURL(target *url.URL, secrets map[string]struct{}) (string, bool) {
	parts := strings.Split(target.RawQuery, "&")
	changed := false
	for index, part := range parts {
		name, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			continue
		}
		if _, secret := secrets[secretparam.CanonicalName(decoded)]; secret {
			parts[index] = name + "=REDACTED"
			changed = true
		}
	}
	if !changed {
		return "", false
	}
	clone := *target
	clone.User = nil
	clone.RawQuery = strings.Join(parts, "&")
	return clone.String(), true
}
