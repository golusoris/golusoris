// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/kafka"
	"github.com/golusoris/golusoris/pubsub/nats"
)

// DefaultCloudEventSinkTimeout bounds one CloudEvents sink Send when
// CloudEventOptions.Timeout is zero.
const DefaultCloudEventSinkTimeout = 10 * time.Second

// CloudEventOptions configures [NATSCloudEventSink] and [KafkaCloudEventSink].
type CloudEventOptions struct {
	// Source is the CloudEvents source for rows stored without one.
	Source string
	// Mode selects the content mode; the zero value is binary.
	Mode cloudevents.Mode
	// Timeout bounds one Send; zero uses DefaultCloudEventSinkTimeout.
	Timeout time.Duration
}

func (o CloudEventOptions) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultCloudEventSinkTimeout
	}
	return o.Timeout
}

type natsCloudEventPublisher interface {
	PublishCloudEvent(context.Context, string, cloudevents.Event, cloudevents.Mode) (*jetstream.PubAck, error)
}

// NATSCloudEventSink publishes outbox events as CloudEvents through
// JetStream. The Nats-Msg-Id header carries the stable event id, so a replay
// inside the stream's duplicate window is stored once. The subject must be
// bound to a stream.
type NATSCloudEventSink struct {
	client  natsCloudEventPublisher
	subject string
	opts    CloudEventOptions
}

// NewNATSCloudEventSink returns a Sink that publishes CloudEvents to subject.
func NewNATSCloudEventSink(client *nats.Client, subject string, opts CloudEventOptions) *NATSCloudEventSink {
	var publisher natsCloudEventPublisher
	if client != nil {
		publisher = client
	}
	return &NATSCloudEventSink{client: publisher, subject: subject, opts: opts}
}

// Send implements [Sink].
func (s *NATSCloudEventSink) Send(ctx context.Context, ev outbox.Event) error {
	if validate.IsNil(s.client) {
		return errors.New("nats cloudevent sink: client is required")
	}
	ce, err := ev.CloudEvent(s.opts.Source)
	if err != nil {
		return fmt.Errorf("nats cloudevent sink: %w", err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, s.opts.timeout())
	defer cancel()
	if _, err = s.client.PublishCloudEvent(sendCtx, s.subject, ce, s.opts.Mode); err != nil {
		return fmt.Errorf("nats cloudevent sink: %w", err)
	}
	return nil
}

type kafkaProducer interface {
	Produce(context.Context, ...*kafka.Record) error
}

// KafkaCloudEventSink produces outbox events as CloudEvents to a Kafka topic.
// The record key is the event subject, or its kind when the subject is
// empty, so events about one subject keep their order. Kafka does not drop
// replays; consumers deduplicate on source plus id.
type KafkaCloudEventSink struct {
	client kafkaProducer
	topic  string
	opts   CloudEventOptions
}

// NewKafkaCloudEventSink returns a Sink that produces CloudEvents to topic.
func NewKafkaCloudEventSink(client *kafka.Client, topic string, opts CloudEventOptions) *KafkaCloudEventSink {
	var producer kafkaProducer
	if client != nil {
		producer = client
	}
	return &KafkaCloudEventSink{client: producer, topic: topic, opts: opts}
}

// Send implements [Sink].
func (s *KafkaCloudEventSink) Send(ctx context.Context, ev outbox.Event) error {
	if validate.IsNil(s.client) {
		return errors.New("kafka cloudevent sink: client is required")
	}
	ce, err := ev.CloudEvent(s.opts.Source)
	if err != nil {
		return fmt.Errorf("kafka cloudevent sink: %w", err)
	}
	rec, err := kafka.NewCloudEventRecord(s.topic, []byte(cmp.Or(ev.Subject, ev.Kind)), ce, s.opts.Mode)
	if err != nil {
		return fmt.Errorf("kafka cloudevent sink: %w", err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, s.opts.timeout())
	defer cancel()
	if err = s.client.Produce(sendCtx, rec); err != nil {
		return fmt.Errorf("kafka cloudevent sink: %w", err)
	}
	return nil
}
