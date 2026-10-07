// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

const (
	// cloudEventHeaderPrefix is the NATS binding prefix for attribute headers.
	cloudEventHeaderPrefix = "ce-"
	contentTypeHeader      = "Content-Type"
	structuredContentType  = cloudevents.ContentTypeStructuredJSON + "; charset=UTF-8"
)

// NewCloudEventMsg builds a NATS message carrying ev. Binary mode maps every
// attribute to a percent-encoded ce- header and sends data as the body;
// structured mode sends the JSON event format with a Content-Type header.
func NewCloudEventMsg(subject string, ev cloudevents.Event, mode cloudevents.Mode) (*nats.Msg, error) {
	if err := mode.Validate(); err != nil {
		return nil, fmt.Errorf("nats: cloudevent: %w", err)
	}
	msg := nats.NewMsg(subject)
	if mode == cloudevents.ModeStructured {
		body, err := cloudevents.MarshalStructured(ev)
		if err != nil {
			return nil, fmt.Errorf("nats: cloudevent: %w", err)
		}
		msg.Header.Set(contentTypeHeader, structuredContentType)
		msg.Data = body
		return msg, nil
	}
	attrs, err := ev.Attributes()
	if err != nil {
		return nil, fmt.Errorf("nats: cloudevent: %w", err)
	}
	for name, value := range attrs {
		msg.Header.Set(cloudEventHeaderPrefix+name, cloudevents.EncodeHeaderValue(value))
	}
	msg.Data = ev.Data
	return msg, nil
}

// DecodeCloudEvent decodes a CloudEvent from a NATS message's headers and
// data, for *nats.Msg (msg.Header, msg.Data) and jetstream.Msg
// (msg.Headers(), msg.Data()) alike. A Content-Type of
// application/cloudevents selects structured mode, ce- headers select binary
// mode, and a message with neither is read as a structured event, which is
// how NATS binding 1.0.2 producers send it. Header names match
// case-insensitively.
func DecodeCloudEvent(header nats.Header, data []byte) (cloudevents.Event, error) {
	contentType := firstHeaderFold(header, contentTypeHeader)
	if cloudevents.IsStructuredContentType(contentType) {
		ev, err := cloudevents.DecodeStructured(contentType, data)
		if err != nil {
			return cloudevents.Event{}, fmt.Errorf("nats: decode cloudevent: %w", err)
		}
		return ev, nil
	}
	attrs, err := binaryAttributes(header)
	if err != nil {
		return cloudevents.Event{}, err
	}
	var ev cloudevents.Event
	if len(attrs) == 0 {
		ev, err = cloudevents.UnmarshalStructured(data)
	} else {
		ev, err = cloudevents.FromAttributes(attrs, data)
	}
	if err != nil {
		return cloudevents.Event{}, fmt.Errorf("nats: decode cloudevent: %w", err)
	}
	return ev, nil
}

func firstHeaderFold(header nats.Header, name string) string {
	for key, values := range header {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func binaryAttributes(header nats.Header) (map[string]string, error) {
	attrs := make(map[string]string, len(header))
	for key, values := range header {
		name, ok := strings.CutPrefix(strings.ToLower(key), cloudEventHeaderPrefix)
		if !ok {
			continue
		}
		if _, seen := attrs[name]; seen || len(values) != 1 {
			return nil, fmt.Errorf("nats: decode cloudevent: %w: header %q repeats", cloudevents.ErrMalformedEvent, key)
		}
		value, err := cloudevents.DecodeHeaderValue(values[0])
		if err != nil {
			return nil, fmt.Errorf("nats: decode cloudevent header %q: %w", key, err)
		}
		attrs[name] = value
	}
	return attrs, nil
}

// PublishCloudEvent publishes ev through JetStream to a stream bound to
// subject and waits for its PubAck. The Nats-Msg-Id header carries ev.ID, so
// the stream stores a redelivered event once within its duplicate window;
// the returned PubAck reports such a drop as Duplicate. The ack wait ends at
// Config.AckTimeout ([DefaultAckTimeout] when zero) or at ctx's deadline,
// whichever comes first.
func (c *Client) PublishCloudEvent(
	ctx context.Context,
	subject string,
	ev cloudevents.Event,
	mode cloudevents.Mode,
) (*jetstream.PubAck, error) {
	msg, err := NewCloudEventMsg(subject, ev, mode)
	if err != nil {
		return nil, err
	}
	if c.js == nil {
		return nil, ErrNoJetStream
	}
	pubCtx, cancel := context.WithTimeout(ctx, c.publishAckTimeout())
	defer cancel()
	ack, err := c.js.PublishMsg(pubCtx, msg, jetstream.WithMsgID(cloudevents.EncodeHeaderValue(ev.ID)))
	if err != nil {
		return nil, fmt.Errorf("nats: publish cloudevent %q to %s: %w", ev.ID, subject, err)
	}
	return ack, nil
}

func (c *Client) publishAckTimeout() time.Duration {
	if c.ackTimeout <= 0 {
		return DefaultAckTimeout
	}
	return c.ackTimeout
}
