// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cloudevents_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

func TestEncodeHeaderValue(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Euro € 😀":      "Euro%20%E2%82%AC%20%F0%9F%98%80", // NATS binding 3.1.3.2 example
		`say "hi" 100%`: "say%20%22hi%22%20100%25",
		"/a/b?c=d":      "/a/b?c=d",
		"\x7e\x7f\x21":  "~%7F!", // boundaries of printable ASCII
		"":              "",
	} {
		require.Equal(t, want, cloudevents.EncodeHeaderValue(in), in)
	}
}

func TestDecodeHeaderValue(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Euro%20%E2%82%AC%20%F0%9F%98%80": "Euro € 😀",
		"%e2%82%ac":                       "€", // lower-case hex accepted
		"%41BC":                           "ABC",
		`"a b"`:                           "a b",
		`"say \"hi\""`:                    `say "hi"`,
		"  padded\t":                      "padded",
		"a+b":                             "a+b",
		"":                                "",
	} {
		got, err := cloudevents.DecodeHeaderValue(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
}

func TestDecodeHeaderValueRejects(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"%C0%A0", // overlong U+0020, rejected by the binding
		"%G1",
		"%4",
		`"unterminated`,
		`"`,
		`"a"b"`,
		`"a\"`,
		"%FF",
	} {
		_, err := cloudevents.DecodeHeaderValue(in)
		require.ErrorIs(t, err, cloudevents.ErrInvalidHeaderValue, in)
	}
}
