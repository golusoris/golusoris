// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cloudevents

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"strconv"
	"strings"
)

const (
	jsonData       = "data"
	jsonDataBase64 = "data_base64"
)

// MarshalStructured renders e in the CloudEvents JSON event format. Data
// declared as */json or */*+json is embedded as a JSON value; data without a
// datacontenttype is embedded the same way, with application/json stated, when
// it is valid JSON. Any other data is carried base64-encoded in data_base64.
func MarshalStructured(e Event) ([]byte, error) {
	attrs, err := e.Attributes()
	if err != nil {
		return nil, err
	}
	obj := make(map[string]json.RawMessage, len(attrs)+1)
	for name, value := range attrs {
		encoded, encErr := json.Marshal(value)
		if encErr != nil {
			return nil, fmt.Errorf("cloudevents: marshal attribute %q: %w", name, encErr)
		}
		obj[name] = encoded
	}
	if len(e.Data) > 0 {
		member, value, dataErr := encodeData(e)
		if dataErr != nil {
			return nil, dataErr
		}
		obj[member] = value
		if member == jsonData && e.DataContentType == "" {
			// The JSON format asks re-encoders to state the implied content type.
			obj[AttrDataContentType] = json.RawMessage(`"` + ContentTypeJSON + `"`)
		}
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("cloudevents: marshal event: %w", err)
	}
	return out, nil
}

func encodeData(e Event) (string, json.RawMessage, error) {
	declaredJSON := e.DataContentType != "" && isJSONContentType(e.DataContentType)
	if declaredJSON || (e.DataContentType == "" && json.Valid(e.Data)) {
		if !json.Valid(e.Data) {
			return "", nil, fmt.Errorf("%w: datacontenttype %q declares JSON but data is not JSON",
				ErrInvalidData, e.DataContentType)
		}
		return jsonData, json.RawMessage(e.Data), nil
	}
	encoded, err := json.Marshal(base64.StdEncoding.EncodeToString(e.Data))
	if err != nil {
		return "", nil, fmt.Errorf("cloudevents: marshal data_base64: %w", err)
	}
	return jsonDataBase64, encoded, nil
}

// isJSONContentType reports whether data with contentType is JSON-formatted;
// an absent content type implies application/json.
func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	_, subtype, ok := strings.Cut(mediaType, "/")
	return ok && (subtype == "json" || strings.HasSuffix(subtype, "+json"))
}

// UnmarshalStructured decodes and validates a CloudEvents JSON event. A
// missing datacontenttype next to a data member is set to application/json,
// as the JSON format requires before re-encoding in another binding.
func UnmarshalStructured(body []byte) (Event, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return Event{}, fmt.Errorf("%w: %w", ErrMalformedEvent, err)
	}
	if obj == nil {
		return Event{}, fmt.Errorf("%w: not a JSON object", ErrMalformedEvent)
	}
	attrs, err := jsonAttributes(obj)
	if err != nil {
		return Event{}, err
	}
	data, contentType, err := decodeData(obj, attrs[AttrDataContentType])
	if err != nil {
		return Event{}, err
	}
	if contentType != "" {
		attrs[AttrDataContentType] = contentType
	}
	return FromAttributes(attrs, data)
}

// DecodeStructured decodes body whose protocol content type is contentType.
// Event formats other than JSON fail with [ErrUnsupportedFormat].
func DecodeStructured(contentType string, body []byte) (Event, error) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != ContentTypeStructuredJSON {
		return Event{}, fmt.Errorf("%w: %q", ErrUnsupportedFormat, contentType)
	}
	return UnmarshalStructured(body)
}

// IsStructuredContentType reports whether a protocol content type marks
// structured mode: it starts with application/cloudevents, ignoring case.
func IsStructuredContentType(contentType string) bool {
	prefix := len(StructuredMediaTypePrefix)
	return len(contentType) >= prefix && strings.EqualFold(contentType[:prefix], StructuredMediaTypePrefix)
}

func jsonAttributes(obj map[string]json.RawMessage) (map[string]string, error) {
	attrs := make(map[string]string, len(obj))
	for name, raw := range obj {
		if name == jsonData || name == jsonDataBase64 || isJSONNull(raw) {
			continue
		}
		value, err := attributeValue(name, raw)
		if err != nil {
			return nil, err
		}
		attrs[name] = value
	}
	return attrs, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// attributeValue maps a JSON member to its canonical string. Core attributes
// are JSON strings; extensions may also be booleans or 32-bit integers.
func attributeValue(name string, raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	if isCoreAttribute(name) {
		return "", invalid(name, "must be a JSON string")
	}
	trimmed := string(bytes.TrimSpace(raw))
	if trimmed == "true" || trimmed == "false" {
		return trimmed, nil
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || n < math.MinInt32 || n > math.MaxInt32 {
		return "", invalid(name, "extension values are strings, booleans, or 32-bit integers")
	}
	return strconv.FormatInt(n, 10), nil
}

func decodeData(obj map[string]json.RawMessage, contentType string) ([]byte, string, error) {
	dataRaw, hasData := obj[jsonData]
	b64Raw, hasB64 := obj[jsonDataBase64]
	hasB64 = hasB64 && !isJSONNull(b64Raw)
	switch {
	case hasData && hasB64:
		return nil, "", fmt.Errorf("%w: data and data_base64 are mutually exclusive", ErrMalformedEvent)
	case hasB64:
		data, err := decodeBase64(b64Raw)
		return data, contentType, err
	case !hasData:
		return nil, contentType, nil
	case isJSONContentType(contentType):
		if contentType == "" {
			contentType = ContentTypeJSON
		}
		return []byte(dataRaw), contentType, nil
	default:
		var text string
		if err := json.Unmarshal(dataRaw, &text); err != nil {
			return nil, "", fmt.Errorf("%w: non-JSON datacontenttype %q requires string data", ErrInvalidData, contentType)
		}
		return []byte(text), contentType, nil
	}
}

func decodeBase64(raw json.RawMessage) ([]byte, error) {
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("%w: data_base64 must be a JSON string", ErrInvalidData)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: data_base64: %w", ErrInvalidData, err)
	}
	return data, nil
}
