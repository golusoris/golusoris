// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package outbox

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/pubsub/cloudevents"
)

// TenantExtension is the CloudEvents extension attribute that carries
// [Envelope.Tenant] and [Event.Tenant].
const TenantExtension = "tenant"

const (
	traceParentKey = "traceparent"
	traceStateKey  = "tracestate"
)

var (
	// ErrKindRequired reports an event without a kind.
	ErrKindRequired = errors.New("outbox: kind required")
	// ErrInvalidEnvelope reports envelope attributes that cannot form a CloudEvent.
	ErrInvalidEnvelope = errors.New("outbox: invalid envelope")
)

// Envelope holds optional CloudEvents context attributes stored with an
// outbox event. The event kind becomes the CloudEvents type and the payload
// its application/json data.
type Envelope struct {
	// EventID is the CloudEvents id, a UUID. Empty lets PostgreSQL assign a
	// random UUID. Set it to make an insert idempotent: a second row with
	// the same id violates the unique index.
	EventID string
	// Source is the CloudEvents source (URI-reference). Empty defers to the
	// default source of the sink that publishes the event.
	Source string
	// Subject is the CloudEvents subject, for example "job/42".
	Subject string
	// DataSchema is an absolute URI of the payload schema.
	DataSchema string
	// Tenant travels as the "tenant" extension attribute.
	Tenant string
	// TraceParent and TraceState are W3C trace context headers. Empty
	// TraceParent captures the span context of ctx, if it has a valid one.
	TraceParent string
	TraceState  string
}

// AddEvent writes an event with CloudEvents attributes to the outbox within
// an existing transaction. The payload rules match [Add]. Envelope values are
// validated before the insert; failures wrap [ErrInvalidEnvelope], and an
// invalid attribute is named by a *cloudevents.AttributeError in the chain.
func AddEvent(ctx context.Context, tx pgx.Tx, kind string, payload any, env Envelope) error {
	if kind == "" {
		return ErrKindRequired
	}
	env, err := env.withTraceContext(ctx).normalize(ctx, kind)
	if err != nil {
		return err
	}
	raw, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO golusoris_outbox
		     (kind, payload, event_id, source, subject, data_schema, tenant, traceparent, tracestate)
		 VALUES ($1, $2, COALESCE(NULLIF($3::text, '')::uuid, gen_random_uuid()), $4, $5, $6, $7, $8, $9)`,
		kind, raw, env.EventID, env.Source, env.Subject, env.DataSchema,
		env.Tenant, env.TraceParent, env.TraceState,
	)
	if err != nil {
		return fmt.Errorf("outbox: insert: %w", err)
	}
	return nil
}

// withTraceContext fills TraceParent and TraceState from ctx's span context
// unless the caller set TraceParent.
func (e Envelope) withTraceContext(ctx context.Context) Envelope {
	if e.TraceParent != "" {
		return e
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	e.TraceParent = carrier.Get(traceParentKey)
	if e.TraceParent != "" {
		e.TraceState = carrier.Get(traceStateKey)
	}
	return e
}

// normalize validates e for an event of kind and returns it with EventID in
// canonical UUID form.
func (e Envelope) normalize(ctx context.Context, kind string) (Envelope, error) {
	if e.EventID != "" {
		id, err := uuid.Parse(e.EventID)
		if err != nil {
			return Envelope{}, fmt.Errorf("%w: event id %q is not a UUID: %w", ErrInvalidEnvelope, e.EventID, err)
		}
		e.EventID = id.String()
	}
	if e.TraceParent == "" && e.TraceState != "" {
		return Envelope{}, fmt.Errorf("%w: tracestate requires traceparent", ErrInvalidEnvelope)
	}
	if e.TraceParent != "" && !validTraceParent(ctx, e.TraceParent, e.TraceState) {
		return Envelope{}, fmt.Errorf("%w: traceparent %q is not a valid W3C trace context",
			ErrInvalidEnvelope, e.TraceParent)
	}
	probe := cloudevents.Event{
		ID: "probe", Source: cmp.Or(e.Source, "/probe"), Type: kind, Subject: e.Subject,
		DataSchema: e.DataSchema, Extensions: extensions(e.Tenant, e.TraceParent, e.TraceState),
	}
	if err := probe.Validate(); err != nil {
		return Envelope{}, fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
	}
	return e, nil
}

func validTraceParent(ctx context.Context, traceParent, traceState string) bool {
	// Clearing the caller's span keeps an unparsable carrier from validating as ctx's own span.
	clean := trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	carrier := propagation.MapCarrier{traceParentKey: traceParent, traceStateKey: traceState}
	extracted := propagation.TraceContext{}.Extract(clean, carrier)
	return trace.SpanContextFromContext(extracted).IsValid()
}

func extensions(tenant, traceParent, traceState string) map[string]string {
	ext := make(map[string]string, 3)
	for name, value := range map[string]string{
		TenantExtension:                  tenant,
		cloudevents.ExtensionTraceParent: traceParent,
		cloudevents.ExtensionTraceState:  traceState,
	} {
		if value != "" {
			ext[name] = value
		}
	}
	if len(ext) == 0 {
		return nil
	}
	return ext
}

// CloudEvent maps ev to a CloudEvents 1.0 event: EventID becomes id, Kind
// type, CreatedAt time, Payload application/json data, and Tenant,
// TraceParent, and TraceState extension attributes. defaultSource applies
// when the row has no source. The id is stable across retries and replays.
func (ev Event) CloudEvent(defaultSource string) (cloudevents.Event, error) {
	ce := cloudevents.Event{
		ID:              ev.EventID,
		Source:          cmp.Or(ev.Source, defaultSource),
		Type:            ev.Kind,
		Subject:         ev.Subject,
		Time:            ev.CreatedAt.UTC(),
		DataContentType: cloudevents.ContentTypeJSON,
		DataSchema:      ev.DataSchema,
		Data:            ev.Payload,
		Extensions:      extensions(ev.Tenant, ev.TraceParent, ev.TraceState),
	}
	if err := ce.Validate(); err != nil {
		return cloudevents.Event{}, fmt.Errorf("outbox: event %d: cloudevent: %w", ev.ID, err)
	}
	return ce, nil
}
