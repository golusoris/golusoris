<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — pubsub/cloudevents/

CloudEvents 1.0 envelope. Stdlib only. Stateless: no fx module.

## API

```go
ev := cloudevents.Event{ID: id, Source: "/vmafx/controller", Type: "job.completed",
    Subject: "job/42", Data: body, Extensions: map[string]string{"tenant": "acme"}}
err := ev.Validate()
attrs, err := ev.Attributes()                    // canonical strings, specversion included
ev, err = cloudevents.FromAttributes(attrs, data) // binary-mode decode
body, err := cloudevents.MarshalStructured(ev)    // JSON event format
ev, err = cloudevents.UnmarshalStructured(body)
ev, err = cloudevents.DecodeStructured(contentType, body) // non-JSON format -> ErrUnsupportedFormat
v := cloudevents.EncodeHeaderValue(s)             // NATS/HTTP percent-encoding
s, err = cloudevents.DecodeHeaderValue(v)
```

- Protocol mapping lives in bindings: `pubsub/nats` (`ce-` headers,
 percent-encoded), `pubsub/kafka` (`ce_` headers, `content-type`).
- Validation failures name attribute: `errors.As(err, &*AttributeError)`;
 `Err` is `ErrMissingAttribute` or `ErrInvalidAttribute`.
- Extensions: names `[a-z0-9]+`, core names reserved. Values canonical
 strings. JSON decode accepts string, boolean, int32; null means unset.
- JSON data: `*/json`, `*/*+json`, or no content type plus valid JSON -> `data`
 member. Other bytes -> `data_base64`. Absent content type plus `data` decodes
 as `application/json`.
- `Time` zero means omitted. Encoded RFC 3339 with nanoseconds.

## Decision

Own implementation over `cloudevents/sdk-go/v2` v2.16.2: SDK core pulls zap,
json-iterator, modern-go/reflect2, bytebufferpool; ships no franz-go binding.
Spec surface needed here is small. Sources: spec v1.0.2, JSON format v1.0.2,
Kafka binding v1.0.2, NATS binding 1.0.3-wip (binary mode via `ce-` headers).

## Don't

- Don't hand-build `ce-`/`ce_` headers. Use binding helpers.
- Don't put non-JSON bytes in `data` with JSON content type. Encoder rejects.
- Don't treat `id` alone as global key. Spec uniqueness is `source` + `id`.
