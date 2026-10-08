// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package cloudevents implements the CloudEvents 1.0 envelope: context
// attribute validation, the JSON event format used by structured content
// mode, and the attribute and header-value codecs that the binary content
// mode bindings in pubsub/nats and pubsub/kafka build on.
//
//	ev := cloudevents.Event{ID: id, Source: "/vmafx/controller", Type: "job.completed", Data: body}
//	payload, err := cloudevents.MarshalStructured(ev)
//	back, err := cloudevents.UnmarshalStructured(payload)
//
// Decoders reject events that miss a required attribute and name it through
// [AttributeError]. The package depends on the standard library only.
package cloudevents

import (
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"
)

// SpecVersion is the only CloudEvents specification version this package
// produces and accepts.
const SpecVersion = "1.0"

// Media types used by the JSON event format.
const (
	// ContentTypeJSON is implied for data when datacontenttype is absent.
	ContentTypeJSON = "application/json"
	// ContentTypeStructuredJSON is the media type of a structured JSON event.
	ContentTypeStructuredJSON = "application/cloudevents+json"
	// StructuredMediaTypePrefix marks a structured-mode content type.
	StructuredMediaTypePrefix = "application/cloudevents"
)

// Context attribute names defined by the CloudEvents 1.0 specification.
const (
	AttrID              = "id"
	AttrSource          = "source"
	AttrSpecVersion     = "specversion"
	AttrType            = "type"
	AttrDataContentType = "datacontenttype"
	AttrDataSchema      = "dataschema"
	AttrSubject         = "subject"
	AttrTime            = "time"
)

// Extension attribute names from the CloudEvents distributed-tracing extension.
const (
	ExtensionTraceParent = "traceparent"
	ExtensionTraceState  = "tracestate"
)

// Mode selects a CloudEvents content mode for a protocol binding.
type Mode int

const (
	// ModeBinary carries attributes in protocol headers and data as the body.
	ModeBinary Mode = iota
	// ModeStructured carries the whole event as a JSON document in the body.
	ModeStructured
)

// String returns the content mode name.
func (m Mode) String() string {
	switch m {
	case ModeBinary:
		return "binary"
	case ModeStructured:
		return "structured"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// Validate rejects content modes other than [ModeBinary] and [ModeStructured].
func (m Mode) Validate() error {
	if m != ModeBinary && m != ModeStructured {
		return fmt.Errorf("%w: %s", ErrInvalidMode, m)
	}
	return nil
}

var (
	// ErrMissingAttribute reports an absent required context attribute.
	ErrMissingAttribute = errors.New("cloudevents: required attribute missing")
	// ErrInvalidAttribute reports a context attribute that violates the specification.
	ErrInvalidAttribute = errors.New("cloudevents: invalid attribute")
	// ErrInvalidData reports event data that its datacontenttype cannot carry.
	ErrInvalidData = errors.New("cloudevents: invalid data")
	// ErrMalformedEvent reports a message that is not a well-formed event encoding.
	ErrMalformedEvent = errors.New("cloudevents: malformed event")
	// ErrUnsupportedFormat reports a structured event in a format other than JSON.
	ErrUnsupportedFormat = errors.New("cloudevents: unsupported event format")
	// ErrInvalidMode reports an unknown content mode.
	ErrInvalidMode = errors.New("cloudevents: invalid content mode")
	// ErrInvalidHeaderValue reports a protocol header value that cannot be decoded.
	ErrInvalidHeaderValue = errors.New("cloudevents: invalid header value")
)

// AttributeError names the context attribute that failed validation. Err is
// [ErrMissingAttribute] or [ErrInvalidAttribute].
type AttributeError struct {
	Name   string
	Err    error
	Detail string
}

// Error implements error.
func (e *AttributeError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%v: %q", e.Err, e.Name)
	}
	return fmt.Sprintf("%v: %q: %s", e.Err, e.Name, e.Detail)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *AttributeError) Unwrap() error { return e.Err }

func missing(name string) error {
	return &AttributeError{Name: name, Err: ErrMissingAttribute}
}

func invalid(name, detail string) error {
	return &AttributeError{Name: name, Err: ErrInvalidAttribute, Detail: detail}
}

// Event is one CloudEvents 1.0 event. Empty optional attributes and a zero
// Time are omitted on the wire. Extensions hold extension attributes in their
// canonical string encoding.
type Event struct {
	ID              string
	Source          string
	Type            string
	Subject         string
	Time            time.Time
	DataContentType string
	DataSchema      string
	Data            []byte
	Extensions      map[string]string
}

// Validate checks every context attribute against the CloudEvents 1.0 rules.
func (e Event) Validate() error {
	checks := [...]func() error{
		func() error { return requiredString(AttrID, e.ID) },
		func() error { return validateSource(e.Source) },
		func() error { return requiredString(AttrType, e.Type) },
		func() error { return optionalString(AttrSubject, e.Subject) },
		func() error { return validateContentType(e.DataContentType) },
		func() error { return validateDataSchema(e.DataSchema) },
		func() error { return validateTime(e.Time) },
		func() error { return validateExtensions(e.Extensions) },
	}
	for _, check := range checks {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// Attributes validates e and returns every context attribute, specversion
// included, in its canonical string encoding. Binary-mode bindings map each
// entry to one protocol header.
func (e Event) Attributes() (map[string]string, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	attrs := make(map[string]string, 8+len(e.Extensions))
	maps.Copy(attrs, e.Extensions)
	attrs[AttrSpecVersion] = SpecVersion
	attrs[AttrID] = e.ID
	attrs[AttrSource] = e.Source
	attrs[AttrType] = e.Type
	setIfNotEmpty(attrs, AttrSubject, e.Subject)
	setIfNotEmpty(attrs, AttrDataContentType, e.DataContentType)
	setIfNotEmpty(attrs, AttrDataSchema, e.DataSchema)
	if !e.Time.IsZero() {
		attrs[AttrTime] = e.Time.Format(time.RFC3339Nano)
	}
	return attrs, nil
}

func setIfNotEmpty(attrs map[string]string, name, value string) {
	if value != "" {
		attrs[name] = value
	}
}

// FromAttributes builds an event from canonical attribute strings, as read
// from binary-mode protocol headers, plus its data, and validates it.
// Attributes other than the core ones become extensions; empty data becomes nil.
func FromAttributes(attrs map[string]string, data []byte) (Event, error) {
	if len(data) == 0 {
		data = nil
	}
	version, ok := attrs[AttrSpecVersion]
	if !ok {
		return Event{}, missing(AttrSpecVersion)
	}
	if version != SpecVersion {
		return Event{}, invalid(AttrSpecVersion, fmt.Sprintf("got %q, want %q", version, SpecVersion))
	}
	for _, name := range [...]string{AttrSubject, AttrDataContentType, AttrDataSchema, AttrTime} {
		if value, present := attrs[name]; present && value == "" {
			return Event{}, invalid(name, "present but empty")
		}
	}
	ev := Event{
		ID: attrs[AttrID], Source: attrs[AttrSource], Type: attrs[AttrType],
		Subject: attrs[AttrSubject], DataContentType: attrs[AttrDataContentType],
		DataSchema: attrs[AttrDataSchema], Data: data,
		Extensions: extensionsOf(attrs),
	}
	if raw, present := attrs[AttrTime]; present {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return Event{}, invalid(AttrTime, "not an RFC 3339 timestamp")
		}
		ev.Time = parsed
	}
	if err := ev.Validate(); err != nil {
		return Event{}, err
	}
	return ev, nil
}

func extensionsOf(attrs map[string]string) map[string]string {
	var ext map[string]string
	for name, value := range attrs {
		if isCoreAttribute(name) {
			continue
		}
		if ext == nil {
			ext = make(map[string]string, len(attrs))
		}
		ext[name] = value
	}
	return ext
}

func isCoreAttribute(name string) bool {
	switch name {
	case AttrID, AttrSource, AttrSpecVersion, AttrType,
		AttrDataContentType, AttrDataSchema, AttrSubject, AttrTime:
		return true
	default:
		return false
	}
}

func requiredString(name, value string) error {
	if value == "" {
		return missing(name)
	}
	return optionalString(name, value)
}

func optionalString(name, value string) error {
	if !validString(value) {
		return invalid(name, "contains characters CloudEvents strings disallow")
	}
	return nil
}

func validateSource(source string) error {
	if err := requiredString(AttrSource, source); err != nil {
		return err
	}
	if _, err := url.Parse(source); err != nil {
		return invalid(AttrSource, "not a URI-reference")
	}
	return nil
}

func validateContentType(contentType string) error {
	if contentType == "" {
		return nil
	}
	if _, _, err := mime.ParseMediaType(contentType); err != nil {
		return invalid(AttrDataContentType, "not an RFC 2046 media type")
	}
	return nil
}

func validateDataSchema(schema string) error {
	if schema == "" {
		return nil
	}
	parsed, err := url.Parse(schema)
	if err != nil || !parsed.IsAbs() {
		return invalid(AttrDataSchema, "not an absolute URI")
	}
	return optionalString(AttrDataSchema, schema)
}

func validateTime(t time.Time) error {
	if !t.IsZero() && (t.Year() < 0 || t.Year() > 9999) {
		return invalid(AttrTime, "year outside RFC 3339 range")
	}
	return nil
}

func validateExtensions(ext map[string]string) error {
	for _, name := range slices.Sorted(maps.Keys(ext)) {
		if !validName(name) {
			return invalid(name, "extension names use only a-z and 0-9")
		}
		if isCoreAttribute(name) || name == "data" {
			return invalid(name, "extension name is reserved")
		}
		if err := optionalString(name, ext[name]); err != nil {
			return err
		}
	}
	return nil
}

func validName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// validString applies the CloudEvents String rules: valid UTF-8 without
// control characters or Unicode noncharacters.
func validString(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r <= 0x1F || (r >= 0x7F && r <= 0x9F) || isNonCharacter(r) {
			return false
		}
	}
	return true
}

func isNonCharacter(r rune) bool {
	return (r >= 0xFDD0 && r <= 0xFDEF) || r&0xFFFE == 0xFFFE
}
