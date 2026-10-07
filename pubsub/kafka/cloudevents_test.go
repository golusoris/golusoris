// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package kafka_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/kafka"
)

func sampleEvent(id string) cloudevents.Event {
	return cloudevents.Event{
		ID:              id,
		Source:          "/vmafx/controller",
		Type:            "job.completed",
		Subject:         "job/42",
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

func headerMap(rec *kafka.Record) map[string]string {
	out := make(map[string]string, len(rec.Headers))
	for _, h := range rec.Headers {
		out[h.Key] = string(h.Value)
	}
	return out
}

func TestNewCloudEventRecordBinary(t *testing.T) {
	t.Parallel()
	ev := sampleEvent("id-1")
	rec, err := kafka.NewCloudEventRecord("events", []byte("job/42"), ev, cloudevents.ModeBinary)
	require.NoError(t, err)
	require.Equal(t, "events", rec.Topic)
	require.Equal(t, []byte("job/42"), rec.Key)
	require.Equal(t, map[string]string{
		"ce_specversion": "1.0", "ce_id": "id-1", "ce_source": "/vmafx/controller",
		"ce_type": "job.completed", "ce_subject": "job/42", "ce_time": "2026-10-07T12:00:00Z",
		"ce_tenant": "acme", "content-type": "application/json",
	}, headerMap(rec))
	require.Equal(t, ev.Data, rec.Value)
	require.True(t, rec.Timestamp.IsZero())

	back, err := kafka.DecodeCloudEventRecord(rec)
	require.NoError(t, err)
	requireSameEvent(t, ev, back)
}

func TestNewCloudEventRecordStructured(t *testing.T) {
	t.Parallel()
	ev := sampleEvent("id-2")
	rec, err := kafka.NewCloudEventRecord("events", nil, ev, cloudevents.ModeStructured)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"content-type": "application/cloudevents+json; charset=UTF-8"}, headerMap(rec))

	back, err := kafka.DecodeCloudEventRecord(rec)
	require.NoError(t, err)
	requireSameEvent(t, ev, back)
}

func TestNewCloudEventRecordRejects(t *testing.T) {
	t.Parallel()
	_, err := kafka.NewCloudEventRecord("events", nil, cloudevents.Event{ID: "1", Source: "s"}, cloudevents.ModeBinary)
	require.ErrorIs(t, err, cloudevents.ErrMissingAttribute)
	_, err = kafka.NewCloudEventRecord("events", nil, cloudevents.Event{ID: "1", Type: "t"}, cloudevents.ModeStructured)
	require.ErrorIs(t, err, cloudevents.ErrMissingAttribute)
	_, err = kafka.NewCloudEventRecord("events", nil, sampleEvent("1"), cloudevents.Mode(3))
	require.ErrorIs(t, err, cloudevents.ErrInvalidMode)
}

func TestDecodeCloudEventRecordRejects(t *testing.T) {
	t.Parallel()
	base := func() *kafka.Record {
		return &kafka.Record{Headers: []kgo.RecordHeader{
			{Key: "ce_specversion", Value: []byte("1.0")},
			{Key: "ce_id", Value: []byte("1")},
			{Key: "ce_source", Value: []byte("s")},
			{Key: "ce_type", Value: []byte("t")},
		}}
	}
	_, err := kafka.DecodeCloudEventRecord(nil)
	require.ErrorIs(t, err, kafka.ErrNilRecord)

	for _, drop := range []string{"type", "source"} {
		rec := base()
		kept := rec.Headers[:0]
		for _, h := range rec.Headers {
			if h.Key != "ce_"+drop {
				kept = append(kept, h)
			}
		}
		rec.Headers = kept
		_, err = kafka.DecodeCloudEventRecord(rec)
		var attrErr *cloudevents.AttributeError
		require.ErrorAs(t, err, &attrErr, drop)
		require.Equal(t, drop, attrErr.Name)
	}

	repeated := base()
	repeated.Headers = append(repeated.Headers, kgo.RecordHeader{Key: "CE_ID", Value: []byte("2")})
	_, err = kafka.DecodeCloudEventRecord(repeated)
	require.ErrorIs(t, err, cloudevents.ErrMalformedEvent)

	avro := &kafka.Record{
		Headers: []kgo.RecordHeader{{Key: "content-type", Value: []byte("application/cloudevents+avro")}},
		Value:   []byte("x"),
	}
	_, err = kafka.DecodeCloudEventRecord(avro)
	require.ErrorIs(t, err, cloudevents.ErrUnsupportedFormat)

	_, err = kafka.DecodeCloudEventRecord(&kafka.Record{Value: []byte("plain payload")})
	require.ErrorIs(t, err, cloudevents.ErrMissingAttribute, "a record without ce_ headers is not a CloudEvent")
}

func TestDecodeCloudEventRecordHeaderCase(t *testing.T) {
	t.Parallel()
	rec := &kafka.Record{Headers: []kgo.RecordHeader{
		{Key: "CE_SPECVERSION", Value: []byte("1.0")},
		{Key: "Ce_Id", Value: []byte("1")},
		{Key: "ce_source", Value: []byte("s")},
		{Key: "ce_type", Value: []byte("t")},
		{Key: "Content-Type", Value: []byte("text/plain")},
		{Key: "traceparent", Value: []byte("ignored")},
	}, Value: []byte("hello")}
	ev, err := kafka.DecodeCloudEventRecord(rec)
	require.NoError(t, err)
	require.Equal(t, "text/plain", ev.DataContentType)
	require.Equal(t, []byte("hello"), ev.Data)
	require.Nil(t, ev.Extensions, "non-ce_ headers are not attributes")
}
