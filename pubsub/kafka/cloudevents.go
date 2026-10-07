// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package kafka

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

const (
	// cloudEventHeaderPrefix is the Kafka binding prefix for attribute headers.
	cloudEventHeaderPrefix = "ce_"
	contentTypeHeader      = "content-type"
	structuredContentType  = cloudevents.ContentTypeStructuredJSON + "; charset=UTF-8"
)

// ErrNilRecord reports a nil record passed to [DecodeCloudEventRecord].
var ErrNilRecord = errors.New("kafka: record is nil")

// NewCloudEventRecord builds a Kafka record carrying ev under key. Binary mode
// maps datacontenttype to the content-type header, every other attribute to a
// ce_ header, and data to the record value; structured mode sends the JSON
// event format with content-type application/cloudevents+json. The broker
// assigns the timestamp.
func NewCloudEventRecord(topic string, key []byte, ev cloudevents.Event, mode cloudevents.Mode) (*Record, error) {
	if err := mode.Validate(); err != nil {
		return nil, fmt.Errorf("kafka: cloudevent: %w", err)
	}
	rec := &Record{Topic: topic, Key: key}
	if mode == cloudevents.ModeStructured {
		body, err := cloudevents.MarshalStructured(ev)
		if err != nil {
			return nil, fmt.Errorf("kafka: cloudevent: %w", err)
		}
		rec.Headers = []kgo.RecordHeader{{Key: contentTypeHeader, Value: []byte(structuredContentType)}}
		rec.Value = body
		return rec, nil
	}
	attrs, err := ev.Attributes()
	if err != nil {
		return nil, fmt.Errorf("kafka: cloudevent: %w", err)
	}
	rec.Headers = make([]kgo.RecordHeader, 0, len(attrs))
	for _, name := range slices.Sorted(maps.Keys(attrs)) {
		headerKey := cloudEventHeaderPrefix + name
		if name == cloudevents.AttrDataContentType {
			headerKey = contentTypeHeader
		}
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: headerKey, Value: []byte(attrs[name])})
	}
	rec.Value = ev.Data
	return rec, nil
}

// DecodeCloudEventRecord decodes a CloudEvent from rec. A content-type header
// starting with application/cloudevents selects structured mode; any other
// record is read in binary mode, as the Kafka binding prescribes. Header
// names match case-insensitively.
func DecodeCloudEventRecord(rec *Record) (cloudevents.Event, error) {
	if rec == nil {
		return cloudevents.Event{}, ErrNilRecord
	}
	attrs, contentType, err := recordAttributes(rec.Headers)
	if err != nil {
		return cloudevents.Event{}, err
	}
	var ev cloudevents.Event
	if cloudevents.IsStructuredContentType(contentType) {
		ev, err = cloudevents.DecodeStructured(contentType, rec.Value)
	} else {
		ev, err = cloudevents.FromAttributes(attrs, rec.Value)
	}
	if err != nil {
		return cloudevents.Event{}, fmt.Errorf("kafka: decode cloudevent: %w", err)
	}
	return ev, nil
}

// recordAttributes collects ce_ headers plus content-type, which binary mode
// maps to datacontenttype. A repeated attribute header is malformed.
func recordAttributes(headers []kgo.RecordHeader) (map[string]string, string, error) {
	attrs := make(map[string]string, len(headers))
	for _, header := range headers {
		lower := strings.ToLower(header.Key)
		name, ok := strings.CutPrefix(lower, cloudEventHeaderPrefix)
		if lower == contentTypeHeader {
			name, ok = cloudevents.AttrDataContentType, true
		}
		if !ok {
			continue
		}
		if _, seen := attrs[name]; seen {
			return nil, "", fmt.Errorf("kafka: decode cloudevent: %w: header %q repeats",
				cloudevents.ErrMalformedEvent, header.Key)
		}
		attrs[name] = string(header.Value)
	}
	return attrs, attrs[cloudevents.AttrDataContentType], nil
}
