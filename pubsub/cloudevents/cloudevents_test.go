// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cloudevents_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

func validEvent() cloudevents.Event {
	return cloudevents.Event{
		ID:              "6e8bc430-9c3a-11d9-9669-0800200c9a66",
		Source:          "/vmafx/controller",
		Type:            "job.completed",
		Subject:         "job/42",
		Time:            time.Date(2026, 10, 7, 12, 30, 0, 123000000, time.UTC),
		DataContentType: "application/json",
		DataSchema:      "https://schemas.example.com/job.json",
		Data:            []byte(`{"job":42}`),
		Extensions: map[string]string{
			cloudevents.ExtensionTraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"tenant":                         "acme",
		},
	}
}

// requireAttributeError asserts err names attribute name and wraps sentinel.
func requireAttributeError(t *testing.T, err error, name string, sentinel error) {
	t.Helper()
	require.Error(t, err)
	var attrErr *cloudevents.AttributeError
	require.ErrorAs(t, err, &attrErr)
	require.Equal(t, name, attrErr.Name)
	require.ErrorIs(t, err, sentinel)
	require.Contains(t, err.Error(), `"`+name+`"`)
}

func TestEventValidateAccepts(t *testing.T) {
	t.Parallel()
	minimal := cloudevents.Event{ID: "1", Source: "s", Type: "t"}
	oneCharExtension := minimal
	oneCharExtension.Extensions = map[string]string{"x": "", "123": "v"}
	nbsp := minimal
	nbsp.Subject = "a b" // U+00A0 is the first printable rune after the C1 control range.
	maxYear := minimal
	maxYear.Time = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	for name, ev := range map[string]cloudevents.Event{
		"full":               validEvent(),
		"minimal":            minimal,
		"one char extension": oneCharExtension,
		"first printable C1": nbsp,
		"max RFC 3339 year":  maxYear,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.NoError(t, ev.Validate())
		})
	}
}

func TestEventValidateRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*cloudevents.Event)
		attr     string
		sentinel error
	}{
		{"missing id", func(e *cloudevents.Event) { e.ID = "" }, "id", cloudevents.ErrMissingAttribute},
		{"missing source", func(e *cloudevents.Event) { e.Source = "" }, "source", cloudevents.ErrMissingAttribute},
		{"missing type", func(e *cloudevents.Event) { e.Type = "" }, "type", cloudevents.ErrMissingAttribute},
		{"source not URI", func(e *cloudevents.Event) { e.Source = "%zz" }, "source", cloudevents.ErrInvalidAttribute},
		{"control in subject", func(e *cloudevents.Event) { e.Subject = "a\nb" }, "subject", cloudevents.ErrInvalidAttribute},
		{"C1 control in id", func(e *cloudevents.Event) { e.ID = "a\u009fb" }, "id", cloudevents.ErrInvalidAttribute},
		{"noncharacter in type", func(e *cloudevents.Event) { e.Type = "a￾b" }, "type", cloudevents.ErrInvalidAttribute},
		{"invalid UTF-8", func(e *cloudevents.Event) { e.Type = "a\xffb" }, "type", cloudevents.ErrInvalidAttribute},
		{"bad media type", func(e *cloudevents.Event) { e.DataContentType = "text/" }, "datacontenttype", cloudevents.ErrInvalidAttribute},
		{"relative dataschema", func(e *cloudevents.Event) { e.DataSchema = "/schema.json" }, "dataschema", cloudevents.ErrInvalidAttribute},
		{"year past 9999", func(e *cloudevents.Event) { e.Time = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, "time", cloudevents.ErrInvalidAttribute},
		{"upper-case extension", func(e *cloudevents.Event) { e.Extensions = map[string]string{"Tenant": "a"} }, "Tenant", cloudevents.ErrInvalidAttribute},
		{"underscore extension", func(e *cloudevents.Event) { e.Extensions = map[string]string{"a_b": "a"} }, "a_b", cloudevents.ErrInvalidAttribute},
		{"empty extension name", func(e *cloudevents.Event) { e.Extensions = map[string]string{"": "a"} }, "", cloudevents.ErrInvalidAttribute},
		{"reserved extension", func(e *cloudevents.Event) { e.Extensions = map[string]string{"subject": "a"} }, "subject", cloudevents.ErrInvalidAttribute},
		{"data extension", func(e *cloudevents.Event) { e.Extensions = map[string]string{"data": "a"} }, "data", cloudevents.ErrInvalidAttribute},
		{"control in extension", func(e *cloudevents.Event) { e.Extensions = map[string]string{"tenant": "\x00"} }, "tenant", cloudevents.ErrInvalidAttribute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ev := validEvent()
			tt.mutate(&ev)
			requireAttributeError(t, ev.Validate(), tt.attr, tt.sentinel)
		})
	}
}

func TestAttributesRoundTrip(t *testing.T) {
	t.Parallel()
	ev := validEvent()
	attrs, err := ev.Attributes()
	require.NoError(t, err)
	require.Equal(t, cloudevents.SpecVersion, attrs[cloudevents.AttrSpecVersion])
	require.Equal(t, "2026-10-07T12:30:00.123Z", attrs[cloudevents.AttrTime])
	require.Equal(t, "acme", attrs["tenant"])

	back, err := cloudevents.FromAttributes(attrs, ev.Data)
	require.NoError(t, err)
	requireSameEvent(t, ev, back)
}

func TestAttributesOmitsEmptyOptionalAttributes(t *testing.T) {
	t.Parallel()
	attrs, err := cloudevents.Event{ID: "1", Source: "s", Type: "t"}.Attributes()
	require.NoError(t, err)
	require.Len(t, attrs, 4)
}

func TestAttributesRejectsInvalidEvent(t *testing.T) {
	t.Parallel()
	_, err := cloudevents.Event{ID: "1", Source: "s"}.Attributes()
	requireAttributeError(t, err, "type", cloudevents.ErrMissingAttribute)
}

func TestFromAttributesRejects(t *testing.T) {
	t.Parallel()
	base := func() map[string]string {
		return map[string]string{"specversion": "1.0", "id": "1", "source": "s", "type": "t"}
	}
	tests := []struct {
		name     string
		mutate   func(map[string]string)
		attr     string
		sentinel error
	}{
		{"missing specversion", func(m map[string]string) { delete(m, "specversion") }, "specversion", cloudevents.ErrMissingAttribute},
		{"old specversion", func(m map[string]string) { m["specversion"] = "0.3" }, "specversion", cloudevents.ErrInvalidAttribute},
		{"missing type", func(m map[string]string) { delete(m, "type") }, "type", cloudevents.ErrMissingAttribute},
		{"missing source", func(m map[string]string) { delete(m, "source") }, "source", cloudevents.ErrMissingAttribute},
		{"empty id", func(m map[string]string) { m["id"] = "" }, "id", cloudevents.ErrMissingAttribute},
		{"present empty subject", func(m map[string]string) { m["subject"] = "" }, "subject", cloudevents.ErrInvalidAttribute},
		{"bad time", func(m map[string]string) { m["time"] = "yesterday" }, "time", cloudevents.ErrInvalidAttribute},
		{"bad extension name", func(m map[string]string) { m["Bad"] = "x" }, "Bad", cloudevents.ErrInvalidAttribute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attrs := base()
			tt.mutate(attrs)
			_, err := cloudevents.FromAttributes(attrs, nil)
			requireAttributeError(t, err, tt.attr, tt.sentinel)
		})
	}
}

func TestFromAttributesAcceptsOffsetTime(t *testing.T) {
	t.Parallel()
	ev, err := cloudevents.FromAttributes(map[string]string{
		"specversion": "1.0", "id": "1", "source": "s", "type": "t",
		"time": "2018-04-05T03:56:24+02:00",
	}, nil)
	require.NoError(t, err)
	require.True(t, ev.Time.Equal(time.Date(2018, 4, 5, 1, 56, 24, 0, time.UTC)))
	require.Nil(t, ev.Extensions)
}

func TestModeValidate(t *testing.T) {
	t.Parallel()
	require.NoError(t, cloudevents.ModeBinary.Validate())
	require.NoError(t, cloudevents.ModeStructured.Validate())
	require.ErrorIs(t, cloudevents.Mode(2).Validate(), cloudevents.ErrInvalidMode)
	require.ErrorIs(t, cloudevents.Mode(-1).Validate(), cloudevents.ErrInvalidMode)
	require.Equal(t, "binary", cloudevents.ModeBinary.String())
	require.Equal(t, "structured", cloudevents.ModeStructured.String())
	require.Equal(t, "Mode(7)", cloudevents.Mode(7).String())
}

func TestAttributeErrorWithoutDetail(t *testing.T) {
	t.Parallel()
	err := &cloudevents.AttributeError{Name: "type", Err: cloudevents.ErrMissingAttribute}
	require.Equal(t, `cloudevents: required attribute missing: "type"`, err.Error())
	require.True(t, errors.Is(err, cloudevents.ErrMissingAttribute))
	require.False(t, strings.HasSuffix(err.Error(), ": "))
}

// requireSameEvent compares events with time equality instead of struct identity.
func requireSameEvent(t *testing.T, want, got cloudevents.Event) {
	t.Helper()
	require.True(t, want.Time.Equal(got.Time), "time: want %v, got %v", want.Time, got.Time)
	want.Time, got.Time = time.Time{}, time.Time{}
	require.Equal(t, want, got)
}
