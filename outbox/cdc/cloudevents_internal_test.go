// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"

	dbcdc "github.com/golusoris/golusoris/db/cdc"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/kafka"
)

const testEventID = "6e8bc430-9c3a-11d9-9669-0800200c9a66"

func envelopeEvent() outbox.Event {
	return outbox.Event{
		ID: 42, Kind: "job.completed", Payload: json.RawMessage(`{"job":42}`),
		CreatedAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		EventID:   testEventID, Subject: "job/42", Tenant: "acme",
	}
}

type recordingCloudEventPublisher struct {
	deadline bool
	subject  string
	event    cloudevents.Event
	mode     cloudevents.Mode
	err      error
}

func (p *recordingCloudEventPublisher) PublishCloudEvent(
	ctx context.Context, subject string, ev cloudevents.Event, mode cloudevents.Mode,
) (*jetstream.PubAck, error) {
	_, p.deadline = ctx.Deadline()
	p.subject, p.event, p.mode = subject, ev, mode
	return &jetstream.PubAck{}, p.err
}

type recordingProducer struct {
	deadline bool
	records  []*kafka.Record
	err      error
}

func (p *recordingProducer) Produce(ctx context.Context, records ...*kafka.Record) error {
	_, p.deadline = ctx.Deadline()
	p.records = append(p.records, records...)
	return p.err
}

func TestRowValuesDecodeEnvelopeColumns(t *testing.T) {
	t.Parallel()
	row := withRowValue(validRow(), "event_id", testEventID)
	row["source"], row["subject"], row["data_schema"] = "/src", "job/42", "https://s.example/j.json"
	row["tenant"], row["traceparent"], row["tracestate"] = "acme", "00-tp", "a=b"
	ev, err := rowToEvent(row)
	require.NoError(t, err)
	require.Equal(t, testEventID, ev.EventID)
	require.Equal(t, "/src", ev.Source)
	require.Equal(t, "job/42", ev.Subject)
	require.Equal(t, "https://s.example/j.json", ev.DataSchema)
	require.Equal(t, "acme", ev.Tenant)
	require.Equal(t, "00-tp", ev.TraceParent)
	require.Equal(t, "a=b", ev.TraceState)

	legacy, err := rowToEvent(validRow())
	require.NoError(t, err, "rows from before the CloudEvents migration still decode")
	require.Empty(t, legacy.EventID)

	values := map[string]dbcdc.ColumnValue{}
	for name, value := range validRow() {
		values[name] = dbcdc.ColumnValue{Kind: dbcdc.ColumnValueText, Data: []byte(value)}
	}
	values["tenant"] = dbcdc.ColumnValue{Kind: dbcdc.ColumnValueNull}
	nullTenant, err := rowValuesToEvent(values)
	require.NoError(t, err)
	require.Empty(t, nullTenant.Tenant)

	values["event_id"] = dbcdc.ColumnValue{Kind: dbcdc.ColumnValueBinary, Data: []byte{1}}
	_, err = rowValuesToEvent(values)
	require.ErrorContains(t, err, "event_id: expected text, got binary")
}

func TestNATSCloudEventSinkSend(t *testing.T) {
	t.Parallel()
	publisher := &recordingCloudEventPublisher{}
	sink := &NATSCloudEventSink{client: publisher, subject: "events.jobs", opts: CloudEventOptions{
		Source: "/vmafx/controller", Mode: cloudevents.ModeStructured,
	}}
	require.NoError(t, sink.Send(context.Background(), envelopeEvent()))
	require.True(t, publisher.deadline, "Send bounds the publish")
	require.Equal(t, "events.jobs", publisher.subject)
	require.Equal(t, cloudevents.ModeStructured, publisher.mode)
	require.Equal(t, testEventID, publisher.event.ID)
	require.Equal(t, "/vmafx/controller", publisher.event.Source)
	require.Equal(t, "acme", publisher.event.Extensions[outbox.TenantExtension])

	publisher.err = errors.New("no responders")
	require.ErrorIs(t, sink.Send(context.Background(), envelopeEvent()), publisher.err)
}

func TestNATSCloudEventSinkRejects(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, NewNATSCloudEventSink(nil, "s", CloudEventOptions{}).Send(t.Context(), envelopeEvent()),
		"client is required")

	publisher := &recordingCloudEventPublisher{}
	sink := &NATSCloudEventSink{client: publisher, subject: "s", opts: CloudEventOptions{Source: "/src"}}
	legacy := envelopeEvent()
	legacy.EventID = ""
	var attrErr *cloudevents.AttributeError
	require.ErrorAs(t, sink.Send(t.Context(), legacy), &attrErr)
	require.Equal(t, "id", attrErr.Name)

	noSource := &NATSCloudEventSink{client: publisher, subject: "s"}
	require.ErrorAs(t, noSource.Send(t.Context(), envelopeEvent()), &attrErr)
	require.Equal(t, "source", attrErr.Name)
	require.Empty(t, publisher.subject, "invalid events never reach the publisher")
}

func TestKafkaCloudEventSinkSend(t *testing.T) {
	t.Parallel()
	producer := &recordingProducer{}
	sink := &KafkaCloudEventSink{client: producer, topic: "events", opts: CloudEventOptions{Source: "/src"}}
	withSubject := envelopeEvent()
	withoutSubject := envelopeEvent()
	withoutSubject.Subject = ""
	require.NoError(t, sink.Send(context.Background(), withSubject))
	require.NoError(t, sink.Send(context.Background(), withoutSubject))
	require.True(t, producer.deadline)
	require.Len(t, producer.records, 2)
	require.Equal(t, []byte("job/42"), producer.records[0].Key, "subject keys the record")
	require.Equal(t, []byte("job.completed"), producer.records[1].Key, "kind keys a record without subject")

	decoded, err := kafka.DecodeCloudEventRecord(producer.records[0])
	require.NoError(t, err)
	require.Equal(t, testEventID, decoded.ID)
	require.Equal(t, "job.completed", decoded.Type)
}

func TestKafkaCloudEventSinkRejects(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, NewKafkaCloudEventSink(nil, "t", CloudEventOptions{}).Send(t.Context(), envelopeEvent()),
		"client is required")

	producer := &recordingProducer{err: errors.New("broker down")}
	sink := &KafkaCloudEventSink{client: producer, topic: "t", opts: CloudEventOptions{Source: "/src"}}
	require.ErrorIs(t, sink.Send(t.Context(), envelopeEvent()), producer.err)

	badMode := &KafkaCloudEventSink{client: producer, topic: "t", opts: CloudEventOptions{Source: "/src", Mode: 9}}
	require.ErrorIs(t, badMode.Send(t.Context(), envelopeEvent()), cloudevents.ErrInvalidMode)
}

func TestCloudEventOptionsTimeout(t *testing.T) {
	t.Parallel()
	require.Equal(t, DefaultCloudEventSinkTimeout, CloudEventOptions{}.timeout())
	require.Equal(t, DefaultCloudEventSinkTimeout, CloudEventOptions{Timeout: -time.Second}.timeout())
	require.Equal(t, time.Second, CloudEventOptions{Timeout: time.Second}.timeout())
}
