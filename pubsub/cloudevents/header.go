// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cloudevents

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const upperHex = "0123456789ABCDEF"

// EncodeHeaderValue percent-encodes an attribute value for a protocol header,
// as the NATS and HTTP bindings require: space, double quote, percent, and
// every byte outside printable ASCII become %XY with upper-case hex.
func EncodeHeaderValue(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	for i := range len(value) {
		c := value[i]
		if c > ' ' && c < 0x7F && c != '"' && c != '%' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upperHex[c>>4])
		b.WriteByte(upperHex[c&0x0F])
	}
	return b.String()
}

// DecodeHeaderValue reverses [EncodeHeaderValue]. It first unescapes an
// RFC 7230 quoted string, which older producers emit, then performs one round
// of percent-decoding. Results that are not valid UTF-8, such as overlong
// encodings, fail with [ErrInvalidHeaderValue].
func DecodeHeaderValue(value string) (string, error) {
	unquoted, err := unquoteHeaderValue(strings.Trim(value, " \t"))
	if err != nil {
		return "", err
	}
	decoded, err := url.PathUnescape(unquoted)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidHeaderValue, err)
	}
	if !utf8.ValidString(decoded) {
		return "", fmt.Errorf("%w: percent-decoded value is not UTF-8", ErrInvalidHeaderValue)
	}
	return decoded, nil
}

func unquoteHeaderValue(value string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return value, nil
	}
	if len(value) < 2 || !strings.HasSuffix(value, `"`) {
		return "", fmt.Errorf("%w: unterminated quoted string", ErrInvalidHeaderValue)
	}
	inner := value[1 : len(value)-1]
	var b strings.Builder
	b.Grow(len(inner))
	escaped := false
	for i := range len(inner) {
		c := inner[i]
		switch {
		case escaped:
			b.WriteByte(c)
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			return "", fmt.Errorf("%w: unescaped quote inside quoted string", ErrInvalidHeaderValue)
		default:
			b.WriteByte(c)
		}
	}
	if escaped {
		return "", fmt.Errorf("%w: dangling escape in quoted string", ErrInvalidHeaderValue)
	}
	return b.String(), nil
}
