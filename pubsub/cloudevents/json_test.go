// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cloudevents_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

func TestMarshalStructuredEmbedsJSONData(t *testing.T) {
	t.Parallel()
	ev := validEvent()
	body, err := cloudevents.MarshalStructured(ev)
	require.NoError(t, err)

	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &obj))
	require.JSONEq(t, `{"job":42}`, string(obj["data"]))
	require.NotContains(t, obj, "data_base64")
	require.JSONEq(t, `"1.0"`, string(obj["specversion"]))
	require.JSONEq(t, `"acme"`, string(obj["tenant"]))

	back, err := cloudevents.UnmarshalStructured(body)
	require.NoError(t, err)
	requireSameEvent(t, ev, back)
}

func TestMarshalStructuredBase64ForNonJSONData(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"application/octet-stream", "text/plain; charset=utf-8"} {
		t.Run(contentType, func(t *testing.T) {
			t.Parallel()
			ev := cloudevents.Event{
				ID: "1", Source: "s", Type: "t",
				DataContentType: contentType, Data: []byte{0x00, 0xff, 'x'},
			}
			body, err := cloudevents.MarshalStructured(ev)
			require.NoError(t, err)
			var obj map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &obj))
			require.JSONEq(t, `"AP94"`, string(obj["data_base64"]))
			require.NotContains(t, obj, "data")

			back, err := cloudevents.UnmarshalStructured(body)
			require.NoError(t, err)
			requireSameEvent(t, ev, back)
		})
	}
}

func TestMarshalStructuredJSONSuffixAndNoData(t *testing.T) {
	t.Parallel()
	withSuffix := cloudevents.Event{
		ID: "1", Source: "s", Type: "t",
		DataContentType: "application/vnd.vmafx.job+json", Data: []byte(`[1,2]`),
	}
	body, err := cloudevents.MarshalStructured(withSuffix)
	require.NoError(t, err)
	require.Contains(t, string(body), `"data":[1,2]`)

	noData, err := cloudevents.MarshalStructured(cloudevents.Event{ID: "1", Source: "s", Type: "t"})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"1","source":"s","type":"t","specversion":"1.0"}`, string(noData))
}

func TestMarshalStructuredRejects(t *testing.T) {
	t.Parallel()
	_, err := cloudevents.MarshalStructured(cloudevents.Event{ID: "1", Type: "t"})
	requireAttributeError(t, err, "source", cloudevents.ErrMissingAttribute)

	_, err = cloudevents.MarshalStructured(cloudevents.Event{
		ID: "1", Source: "s", Type: "t", DataContentType: "application/json", Data: []byte("not json"),
	})
	require.ErrorIs(t, err, cloudevents.ErrInvalidData)
}

func TestMarshalStructuredWithoutContentType(t *testing.T) {
	t.Parallel()
	jsonBody, err := cloudevents.MarshalStructured(cloudevents.Event{
		ID: "1", Source: "s", Type: "t", Data: []byte(`{"a":1}`),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"1","source":"s","type":"t","specversion":"1.0",
		"datacontenttype":"application/json","data":{"a":1}}`, string(jsonBody))

	binaryBody, err := cloudevents.MarshalStructured(cloudevents.Event{
		ID: "1", Source: "s", Type: "t", Data: []byte("not json"),
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"1","source":"s","type":"t","specversion":"1.0",
		"data_base64":"bm90IGpzb24="}`, string(binaryBody))
}

func TestUnmarshalStructuredDefaultsJSONContentType(t *testing.T) {
	t.Parallel()
	ev, err := cloudevents.UnmarshalStructured([]byte(
		`{"specversion":"1.0","id":"1","source":"s","type":"t","data":"text"}`))
	require.NoError(t, err)
	require.Equal(t, cloudevents.ContentTypeJSON, ev.DataContentType)
	require.Equal(t, `"text"`, string(ev.Data))
}

func TestUnmarshalStructuredExtensionTypes(t *testing.T) {
	t.Parallel()
	ev, err := cloudevents.UnmarshalStructured([]byte(`{
		"specversion":"1.0","id":"1","source":"s","type":"t",
		"flag":true,"max":2147483647,"min":-2147483648,"subject":null,"dataschema":null
	}`))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"flag": "true", "max": "2147483647", "min": "-2147483648"}, ev.Extensions)
	require.Empty(t, ev.Subject)
}

func TestUnmarshalStructuredNonJSONStringData(t *testing.T) {
	t.Parallel()
	ev, err := cloudevents.UnmarshalStructured([]byte(
		`{"specversion":"1.0","id":"1","source":"s","type":"t","datacontenttype":"application/xml","data":"<a/>"}`))
	require.NoError(t, err)
	require.Equal(t, "<a/>", string(ev.Data))
}

func TestUnmarshalStructuredRejectsMissingAttributesByName(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		body string
		attr string
	}{
		"missing type":   {`{"specversion":"1.0","id":"1","source":"s"}`, "type"},
		"missing source": {`{"specversion":"1.0","id":"1","type":"t"}`, "source"},
		"null type":      {`{"specversion":"1.0","id":"1","source":"s","type":null}`, "type"},
		"missing id":     {`{"specversion":"1.0","source":"s","type":"t"}`, "id"},
		"no specversion": {`{"id":"1","source":"s","type":"t"}`, "specversion"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := cloudevents.UnmarshalStructured([]byte(tt.body))
			requireAttributeError(t, err, tt.attr, cloudevents.ErrMissingAttribute)
		})
	}
}

func TestUnmarshalStructuredRejectsMalformed(t *testing.T) {
	t.Parallel()
	const head = `{"specversion":"1.0","id":"1","source":"s","type":"t"`
	tests := map[string]struct {
		body     string
		sentinel error
	}{
		"not JSON":             {`{`, cloudevents.ErrMalformedEvent},
		"array":                {`[]`, cloudevents.ErrMalformedEvent},
		"null document":        {`null`, cloudevents.ErrMalformedEvent},
		"data and data_base64": {head + `,"data":1,"data_base64":"AA=="}`, cloudevents.ErrMalformedEvent},
		"numeric core":         {`{"specversion":"1.0","id":1,"source":"s","type":"t"}`, cloudevents.ErrInvalidAttribute},
		"object extension":     {head + `,"ext":{}}`, cloudevents.ErrInvalidAttribute},
		"int32 overflow":       {head + `,"ext":2147483648}`, cloudevents.ErrInvalidAttribute},
		"fractional extension": {head + `,"ext":1.5}`, cloudevents.ErrInvalidAttribute},
		"xml data not string":  {head + `,"datacontenttype":"application/xml","data":{}}`, cloudevents.ErrInvalidData},
		"bad base64":           {head + `,"data_base64":"!!"}`, cloudevents.ErrInvalidData},
		"numeric base64":       {head + `,"data_base64":1}`, cloudevents.ErrInvalidData},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := cloudevents.UnmarshalStructured([]byte(tt.body))
			require.ErrorIs(t, err, tt.sentinel)
		})
	}
}

func TestDecodeStructuredChecksFormat(t *testing.T) {
	t.Parallel()
	body, err := cloudevents.MarshalStructured(validEvent())
	require.NoError(t, err)

	_, err = cloudevents.DecodeStructured("application/cloudevents+json; charset=UTF-8", body)
	require.NoError(t, err)
	_, err = cloudevents.DecodeStructured("Application/CloudEvents+JSON", body)
	require.NoError(t, err)
	_, err = cloudevents.DecodeStructured("application/cloudevents+avro", body)
	require.ErrorIs(t, err, cloudevents.ErrUnsupportedFormat)
	_, err = cloudevents.DecodeStructured("", body)
	require.ErrorIs(t, err, cloudevents.ErrUnsupportedFormat)
}

func TestIsStructuredContentType(t *testing.T) {
	t.Parallel()
	for contentType, want := range map[string]bool{
		"application/cloudevents+json; charset=UTF-8": true,
		"APPLICATION/CLOUDEVENTS+json":                true,
		"application/cloudevents":                     true,
		"application/cloudevent":                      false,
		"application/json":                            false,
		"":                                            false,
	} {
		require.Equal(t, want, cloudevents.IsStructuredContentType(contentType), contentType)
	}
}
