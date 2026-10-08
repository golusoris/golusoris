// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cloudevents_test

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

// FuzzUnmarshalStructured checks that any accepted event re-encodes to a
// stable document: decode, encode, decode, encode yields identical bytes.
func FuzzUnmarshalStructured(f *testing.F) {
	f.Add([]byte(`{"specversion":"1.0","id":"1","source":"s","type":"t","data":{"a":1}}`))
	f.Add([]byte(`{"specversion":"1.0","id":"1","source":"s","type":"t","datacontenttype":"text/plain","data":"x"}`))
	f.Add([]byte(`{"specversion":"1.0","id":"1","source":"s","type":"t","data_base64":"AP8=","ext":5}`))
	f.Add([]byte(`{"specversion":"1.0","id":"1","source":"s","type":"t","time":"2018-04-05T03:56:24+02:00"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		ev, err := cloudevents.UnmarshalStructured(body)
		if err != nil {
			return
		}
		first, err := cloudevents.MarshalStructured(ev)
		if err != nil {
			t.Fatalf("accepted event does not re-encode: %v", err)
		}
		again, err := cloudevents.UnmarshalStructured(first)
		if err != nil {
			t.Fatalf("re-encoded event does not decode: %v", err)
		}
		second, err := cloudevents.MarshalStructured(again)
		if err != nil {
			t.Fatalf("second encode: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("unstable encoding:\n%s\n%s", first, second)
		}
	})
}

// FuzzHeaderValueRoundTrip checks that percent-encoding is lossless for UTF-8.
func FuzzHeaderValueRoundTrip(f *testing.F) {
	f.Add("Euro € 😀")
	f.Add(`"quoted" 100%`)
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		if !utf8.ValidString(value) {
			return
		}
		decoded, err := cloudevents.DecodeHeaderValue(cloudevents.EncodeHeaderValue(value))
		if err != nil {
			t.Fatalf("decode %q: %v", value, err)
		}
		if decoded != value {
			t.Fatalf("round trip: got %q, want %q", decoded, value)
		}
	})
}
