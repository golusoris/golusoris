// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats_test

import (
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/nats"
)

func sampleEvent(id string) cloudevents.Event {
	return cloudevents.Event{
		ID:              id,
		Source:          "/vmafx/controller",
		Type:            "job.completed",
		Subject:         "job 42 ✓",
		Time:            time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		DataContentType: "application/json",
		Data:            []byte(`{"job":42}`),
		Extensions:      map[string]string{"tenant": "acme"},
	}
}

func requireSameEvent(t *testing.T, want, got cloudevents.Event) {
	t.Helper()
	require.True(t, want.Time.Equal(got.Time))
	want.Time, got.Time = time.Time{}, time.Time{}
	require.Equal(t, want, got)
}

func TestNewCloudEventMsgBinary(t *testing.T) {
	t.Parallel()
	ev := sampleEvent("id-1")
	msg, err := nats.NewCloudEventMsg("events.jobs", ev, cloudevents.ModeBinary)
	require.NoError(t, err)
	require.Equal(t, "events.jobs", msg.Subject)
	require.Equal(t, "1.0", msg.Header.Get("ce-specversion"))
	require.Equal(t, "id-1", msg.Header.Get("ce-id"))
	require.Equal(t, "job%2042%20%E2%9C%93", msg.Header.Get("ce-subject"))
	require.Equal(t, "application/json", msg.Header.Get("ce-datacontenttype"))
	require.Equal(t, "acme", msg.Header.Get("ce-tenant"))
	require.Empty(t, msg.Header.Get("Content-Type"))
	require.Equal(t, ev.Data, msg.Data)

	back, err := nats.DecodeCloudEvent(msg.Header, msg.Data)
	require.NoError(t, err)
	requireSameEvent(t, ev, back)
}

func TestNewCloudEventMsgStructured(t *testing.T) {
	t.Parallel()
	ev := sampleEvent("id-2")
	msg, err := nats.NewCloudEventMsg("events.jobs", ev, cloudevents.ModeStructured)
	require.NoError(t, err)
	require.Equal(t, "application/cloudevents+json; charset=UTF-8", msg.Header.Get("Content-Type"))
	require.Empty(t, msg.Header.Get("ce-id"))

	back, err := nats.DecodeCloudEvent(msg.Header, msg.Data)
	require.NoError(t, err)
	requireSameEvent(t, ev, back)
}

func TestNewCloudEventMsgRejects(t *testing.T) {
	t.Parallel()
	_, err := nats.NewCloudEventMsg("s", cloudevents.Event{ID: "1", Type: "t"}, cloudevents.ModeBinary)
	require.ErrorIs(t, err, cloudevents.ErrMissingAttribute)
	_, err = nats.NewCloudEventMsg("s", cloudevents.Event{ID: "1", Type: "t"}, cloudevents.ModeStructured)
	require.ErrorIs(t, err, cloudevents.ErrMissingAttribute)
	_, err = nats.NewCloudEventMsg("s", sampleEvent("1"), cloudevents.Mode(5))
	require.ErrorIs(t, err, cloudevents.ErrInvalidMode)
}

func TestDecodeCloudEventLegacyStructuredWithoutHeaders(t *testing.T) {
	t.Parallel()
	body, err := cloudevents.MarshalStructured(sampleEvent("id-3"))
	require.NoError(t, err)
	back, err := nats.DecodeCloudEvent(nil, body)
	require.NoError(t, err)
	require.Equal(t, "id-3", back.ID)
}

func TestDecodeCloudEventHeaderNamesIgnoreCase(t *testing.T) {
	t.Parallel()
	header := natsgo.Header{
		"Ce-Specversion": {"1.0"}, "CE-ID": {"x"}, "ce-source": {"%2Fsrc"}, "Ce-Type": {"t"},
	}
	ev, err := nats.DecodeCloudEvent(header, nil)
	require.NoError(t, err)
	require.Equal(t, "/src", ev.Source)
	require.Nil(t, ev.Data)

	header = natsgo.Header{"content-type": {"Application/CloudEvents+json"}}
	_, err = nats.DecodeCloudEvent(header, []byte(`{"specversion":"1.0","id":"1","source":"s","type":"t"}`))
	require.NoError(t, err)
}

func TestDecodeCloudEventRejects(t *testing.T) {
	t.Parallel()
	base := func() natsgo.Header {
		return natsgo.Header{"ce-specversion": {"1.0"}, "ce-id": {"1"}, "ce-source": {"s"}, "ce-type": {"t"}}
	}
	missingType := base()
	missingType.Del("ce-type")
	_, err := nats.DecodeCloudEvent(missingType, nil)
	var attrErr *cloudevents.AttributeError
	require.ErrorAs(t, err, &attrErr)
	require.Equal(t, "type", attrErr.Name)

	missingSource := base()
	missingSource.Del("ce-source")
	_, err = nats.DecodeCloudEvent(missingSource, nil)
	require.ErrorAs(t, err, &attrErr)
	require.Equal(t, "source", attrErr.Name)

	duplicate := base()
	duplicate["Ce-Id"] = []string{"2"}
	_, err = nats.DecodeCloudEvent(duplicate, nil)
	require.ErrorIs(t, err, cloudevents.ErrMalformedEvent)

	repeated := base()
	repeated.Add("ce-id", "2")
	_, err = nats.DecodeCloudEvent(repeated, nil)
	require.ErrorIs(t, err, cloudevents.ErrMalformedEvent)

	badEscape := base()
	badEscape.Set("ce-subject", "%C0%A0")
	_, err = nats.DecodeCloudEvent(badEscape, nil)
	require.ErrorIs(t, err, cloudevents.ErrInvalidHeaderValue)

	_, err = nats.DecodeCloudEvent(natsgo.Header{"Content-Type": {"application/cloudevents+avro"}}, []byte("x"))
	require.ErrorIs(t, err, cloudevents.ErrUnsupportedFormat)

	_, err = nats.DecodeCloudEvent(nil, []byte("not an event"))
	require.ErrorIs(t, err, cloudevents.ErrMalformedEvent)
}
