// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/httpx/internal/secretparam"
)

// SecretParams holds immutable extra names; its comparable zero value adds none.
type SecretParams struct {
	names *map[string]struct{}
}

// NewSecretParams canonicalizes case-insensitive names added to the shared defaults.
func NewSecretParams(names ...string) SecretParams {
	if len(names) == 0 {
		return SecretParams{}
	}
	set := secretparam.Set(nil, names)
	return SecretParams{names: &set}
}

func (s SecretParams) withDefaults() map[string]struct{} {
	set := secretparam.Set(secretparam.Defaults(), nil)
	if s.names != nil {
		maps.Copy(set, *s.names)
	}
	return set
}

type redactingTransport struct {
	next    http.RoundTripper
	secrets map[string]struct{}
}

func (t *redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if span := trace.SpanFromContext(req.Context()); span.IsRecording() && req.URL != nil {
		if target, changed := redactedURL(req.URL, t.secrets); changed {
			span.SetAttributes(attribute.String("url.full", target))
		}
	}
	return t.next.RoundTrip(req) //nolint:wrapcheck // WHY: retryablehttp type-asserts the bare transport error; a wrap makes TLS verification failures retry.
}

func redactedURL(target *url.URL, secrets map[string]struct{}) (string, bool) {
	clone := *target
	query, queryChanged := redactedQuery(target.RawQuery, secrets)
	opaque, opaqueChanged := redactedOpaque(target.Opaque, secrets)
	if !queryChanged && !opaqueChanged && target.Fragment == "" && target.RawFragment == "" {
		return "", false
	}
	if queryChanged {
		clone.RawQuery = query
	}
	if opaqueChanged {
		clone.Opaque = opaque
	}
	clone.User = nil
	clone.Fragment = ""
	clone.RawFragment = ""
	return clone.String(), true
}

func redactedOpaque(opaque string, secrets map[string]struct{}) (string, bool) {
	base, _, fragment := strings.Cut(opaque, "#")
	path, query, found := strings.Cut(base, "?")
	if found {
		if replacement, changed := redactedQuery(query, secrets); changed {
			return path + "?" + replacement, true
		}
	}
	return base, fragment
}

func redactedQuery(query string, secrets map[string]struct{}) (string, bool) {
	var replacement strings.Builder
	copied := 0
	for start := 0; start < len(query); {
		end := len(query)
		if offset := strings.IndexAny(query[start:], "&;"); offset >= 0 {
			end = start + offset
		}
		name, _, _ := strings.Cut(query[start:end], "=")
		if secretQueryName(name, secrets) {
			replacement.WriteString(query[copied:start])
			replacement.WriteString(name)
			replacement.WriteString("=REDACTED")
			copied = end
		}
		start = end + 1
	}
	if copied == 0 {
		return "", false
	}
	replacement.WriteString(query[copied:])
	return replacement.String(), true
}

func secretQueryName(name string, secrets map[string]struct{}) bool {
	for canonical := range secrets {
		if queryNameMatches(name, canonical) {
			return true
		}
	}
	return false
}

func queryNameMatches(name, canonical string) bool {
	matched := 0
	for offset := 0; offset < len(name); {
		value, next, valid := queryNameRune(name, offset)
		if !valid {
			return false
		}
		offset = next
		if value == '_' || value == '-' {
			continue
		}
		want, width := utf8.DecodeRuneInString(canonical[matched:])
		if matched == len(canonical) || unicode.ToLower(value) != want {
			return false
		}
		matched += width
	}
	return matched == len(canonical)
}

func queryNameRune(name string, offset int) (rune, int, bool) {
	var buffer [utf8.UTFMax]byte
	var positions [utf8.UTFMax]int
	count := 0
	for count < len(buffer) && offset < len(name) {
		value, next, valid := queryNameByte(name, offset)
		if !valid {
			return 0, offset, false
		}
		buffer[count] = value
		positions[count] = next
		count++
		offset = next
		if utf8.FullRune(buffer[:count]) {
			break
		}
	}
	value, width := utf8.DecodeRune(buffer[:count])
	return value, positions[width-1], true
}

func queryNameByte(name string, offset int) (byte, int, bool) {
	value := name[offset]
	switch value {
	case '+':
		return ' ', offset + 1, true
	case '%':
		if len(name)-offset < 3 {
			return 0, offset, false
		}
		high := strings.IndexByte("0123456789abcdef", name[offset+1]|0x20)
		low := strings.IndexByte("0123456789abcdef", name[offset+2]|0x20)
		if high < 0 || low < 0 {
			return 0, offset, false
		}
		return byte((high<<4 | low) & 0xff), offset + 3, true
	default:
		return value, offset + 1, true
	}
}
