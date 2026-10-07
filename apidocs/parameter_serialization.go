// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const maxSerializedParameterItems = 256

type parameterValueKind uint8

const (
	parameterScalar parameterValueKind = iota
	parameterArray
	parameterObject
)

type parameterField struct {
	name  string
	value string
}

type parameterValue struct {
	kind   parameterValueKind
	scalar string
	array  []string
	object []parameterField
}

func decodeParameterValue(raw json.RawMessage) (parameterValue, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return parameterValue{}, fmt.Errorf("decode JSON: %w", err)
	}
	switch typed := value.(type) {
	case []any:
		return parameterArrayValue(typed)
	case map[string]any:
		return parameterObjectValue(typed)
	default:
		scalar, err := parameterAtom(typed)
		return parameterValue{kind: parameterScalar, scalar: scalar}, err
	}
}

func parameterArrayValue(values []any) (parameterValue, error) {
	if len(values) > maxSerializedParameterItems {
		return parameterValue{}, errors.New("array exceeds 256 items")
	}
	result := parameterValue{kind: parameterArray, array: make([]string, 0, len(values))}
	for i := range values {
		value, err := parameterAtom(values[i])
		if err != nil {
			return parameterValue{}, fmt.Errorf("array item %d: %w", i, err)
		}
		result.array = append(result.array, value)
	}
	return result, nil
}

func parameterObjectValue(values map[string]any) (parameterValue, error) {
	if len(values) > maxSerializedParameterItems {
		return parameterValue{}, errors.New("object exceeds 256 properties")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := parameterValue{kind: parameterObject, object: make([]parameterField, 0, len(keys))}
	for i := range keys {
		value, err := parameterAtom(values[keys[i]])
		if err != nil {
			return parameterValue{}, fmt.Errorf("object property %q: %w", keys[i], err)
		}
		result.object = append(result.object, parameterField{name: keys[i], value: value})
	}
	return result, nil
}

func parameterAtom(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case json.Number:
		return typed.String(), nil
	case bool:
		if typed {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("nested value of type %T is not serializable", value)
	}
}

func rejectUndeclaredCallParameters(
	handled map[string]struct{},
	args map[string]json.RawMessage,
	bodyDeclared bool,
) error {
	unknown := make([]string, 0)
	for name := range args {
		_, declared := handled[name]
		if declared || (name == "body" && bodyDeclared) {
			continue
		}
		unknown = append(unknown, name)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("apidocs: undeclared tool argument %q", unknown[0])
}

func addPathParameter(parts *callParts, param toolParameter, value parameterValue) error {
	serialized, err := serializePathParameter(param, value)
	if err != nil {
		return err
	}
	if serialized == "." || serialized == ".." {
		return fmt.Errorf("apidocs: path parameter %q is a dot segment", param.name)
	}
	placeholder := "{" + param.name + "}"
	parts.path = strings.ReplaceAll(parts.path, placeholder, serialized)
	return nil
}

func serializePathParameter(param toolParameter, value parameterValue) (string, error) {
	switch param.style {
	case "simple":
		return serializeSimple(value, param.explode, url.PathEscape)
	case "label":
		return serializeLabel(value, param.explode)
	case "matrix":
		return serializeMatrix(param.name, value, param.explode)
	default:
		return "", fmt.Errorf("apidocs: unsupported path style %q", param.style)
	}
}

func serializeLabel(value parameterValue, explode bool) (string, error) {
	if value.kind == parameterArray && explode {
		return "." + joinEscaped(value.array, ".", url.PathEscape), nil
	}
	if value.kind == parameterObject && explode {
		return "." + joinObject(value.object, ".", "=", url.PathEscape), nil
	}
	serialized, err := serializeSimple(value, false, url.PathEscape)
	return "." + serialized, err
}

func serializeMatrix(name string, value parameterValue, explode bool) (string, error) {
	escapedName := url.PathEscape(name)
	switch {
	case value.kind == parameterArray && explode:
		return matrixArray(escapedName, value.array), nil
	case value.kind == parameterObject && explode:
		return matrixObject(value.object), nil
	default:
		serialized, err := serializeSimple(value, false, url.PathEscape)
		return ";" + escapedName + "=" + serialized, err
	}
}

func matrixArray(name string, values []string) string {
	var result strings.Builder
	for i := range values {
		result.WriteByte(';')
		result.WriteString(name)
		result.WriteByte('=')
		result.WriteString(url.PathEscape(values[i]))
	}
	return result.String()
}

func matrixObject(fields []parameterField) string {
	var result strings.Builder
	for i := range fields {
		result.WriteByte(';')
		result.WriteString(url.PathEscape(fields[i].name))
		result.WriteByte('=')
		result.WriteString(url.PathEscape(fields[i].value))
	}
	return result.String()
}

func addQueryParameter(query url.Values, param toolParameter, value parameterValue) error {
	switch param.style {
	case "form":
		return addFormQuery(query, param, value)
	case "spaceDelimited":
		return addDelimitedQuery(query, param, value, " ")
	case "pipeDelimited":
		return addDelimitedQuery(query, param, value, "|")
	case "deepObject":
		return addDeepObjectQuery(query, param, value)
	default:
		return fmt.Errorf("apidocs: unsupported query style %q", param.style)
	}
}

func addFormQuery(query url.Values, param toolParameter, value parameterValue) error {
	switch {
	case value.kind == parameterArray && param.explode:
		addQueryValues(query, param.name, value.array)
	case value.kind == parameterObject && param.explode:
		for i := range value.object {
			query.Add(value.object[i].name, value.object[i].value)
		}
	default:
		serialized, err := serializeSimple(value, false, identity)
		if err != nil {
			return err
		}
		query.Add(param.name, serialized)
	}
	return nil
}

func addDelimitedQuery(
	query url.Values,
	param toolParameter,
	value parameterValue,
	delimiter string,
) error {
	if value.kind == parameterObject {
		return fmt.Errorf("apidocs: query style %q does not serialize objects", param.style)
	}
	if value.kind == parameterArray && param.explode {
		addQueryValues(query, param.name, value.array)
		return nil
	}
	if value.kind == parameterArray {
		query.Add(param.name, strings.Join(value.array, delimiter))
		return nil
	}
	query.Add(param.name, value.scalar)
	return nil
}

func addDeepObjectQuery(query url.Values, param toolParameter, value parameterValue) error {
	if value.kind != parameterObject || !param.explode {
		return errors.New("apidocs: deepObject requires an exploded object")
	}
	for i := range value.object {
		query.Add(param.name+"["+value.object[i].name+"]", value.object[i].value)
	}
	return nil
}

func addQueryValues(query url.Values, name string, values []string) {
	for i := range values {
		query.Add(name, values[i])
	}
}

func addHeaderParameter(parts *callParts, param toolParameter, value parameterValue) error {
	if param.style != "simple" {
		return fmt.Errorf("apidocs: unsupported header style %q", param.style)
	}
	serialized, err := serializeSimple(value, param.explode, identity)
	if err != nil {
		return err
	}
	parts.headers.Set(param.name, serialized)
	return nil
}

func addCookieParameter(parts *callParts, param toolParameter, value parameterValue) error {
	if param.style != "form" {
		return fmt.Errorf("apidocs: unsupported cookie style %q", param.style)
	}
	if value.kind == parameterObject && param.explode {
		for i := range value.object {
			if err := appendRequestCookie(parts, value.object[i].name, value.object[i].value); err != nil {
				return err
			}
		}
		return nil
	}
	serialized, err := serializeCookieValue(value, param.explode)
	if err != nil {
		return err
	}
	return appendRequestCookie(parts, param.name, serialized)
}

func serializeCookieValue(value parameterValue, explode bool) (string, error) {
	if value.kind == parameterArray && explode {
		return strings.Join(value.array, "&"), nil
	}
	return serializeSimple(value, false, identity)
}

func appendRequestCookie(parts *callParts, name, value string) error {
	//nolint:gosec // G124 response-cookie attributes do not apply to outbound Cookie headers.
	// #nosec G124 -- This validates and forwards a request Cookie, not a Set-Cookie response.
	cookie := http.Cookie{Name: name, Value: value}
	if err := cookie.Valid(); err != nil {
		return fmt.Errorf("apidocs: invalid request cookie %q: %w", name, err)
	}
	parts.cookies = append(parts.cookies, cookie)
	return nil
}

func serializeSimple(
	value parameterValue,
	explodeObject bool,
	escape func(string) string,
) (string, error) {
	switch value.kind {
	case parameterScalar:
		return escape(value.scalar), nil
	case parameterArray:
		return joinEscaped(value.array, ",", escape), nil
	case parameterObject:
		separator := ","
		if explodeObject {
			return joinObject(value.object, separator, "=", escape), nil
		}
		return joinObject(value.object, separator, separator, escape), nil
	default:
		return "", errors.New("apidocs: unsupported parameter value")
	}
}

func joinEscaped(values []string, separator string, escape func(string) string) string {
	escaped := make([]string, len(values))
	for i := range values {
		escaped[i] = escape(values[i])
	}
	return strings.Join(escaped, separator)
}

func joinObject(
	fields []parameterField,
	itemSeparator string,
	keyValueSeparator string,
	escape func(string) string,
) string {
	items := make([]string, len(fields))
	for i := range fields {
		items[i] = escape(fields[i].name) + keyValueSeparator + escape(fields[i].value)
	}
	return strings.Join(items, itemSeparator)
}

func identity(value string) string {
	return value
}
