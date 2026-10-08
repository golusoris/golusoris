// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// openAPIToTools converts an OpenAPI 3.x spec into the list of MCP tools.
// The tool's input schema is a JSON Schema object with properties for each
// parameter (path/query/header/cookie) plus, if the operation has a JSON
// request body, nested under "body".
func openAPIToTools(spec []byte) ([]Tool, error) {
	doc, err := loadOpenAPISpec(spec)
	if err != nil {
		return nil, err
	}

	var tools []Tool
	type operationRef struct {
		method string
		path   string
	}
	operationsByName := make(map[string]operationRef)
	paths := sortedKeys(doc.Paths.Map())
	jsonSchema2020 := strings.HasPrefix(doc.OpenAPI, "3.1")
	for pathIndex := range paths {
		path := paths[pathIndex]
		pathItem := doc.Paths.Value(path)
		operations := pathItem.Operations()
		methods := sortedKeys(operations)
		for methodIndex := range methods {
			method := methods[methodIndex]
			tool, buildErr := operationToTool(
				method, path, pathItem.Parameters, operations[method], jsonSchema2020,
			)
			if buildErr != nil {
				return nil, fmt.Errorf("operation %s %s: %w", method, path, buildErr)
			}
			if previous, ok := operationsByName[tool.Name]; ok {
				return nil, fmt.Errorf(
					"duplicate MCP tool name %q for %s %s and %s %s",
					tool.Name,
					strings.ToUpper(previous.method),
					previous.path,
					strings.ToUpper(method),
					path,
				)
			}
			operationsByName[tool.Name] = operationRef{method: method, path: path}
			tools = append(tools, tool)
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Name != tools[j].Name {
			return tools[i].Name < tools[j].Name
		}
		if tools[i].method != tools[j].method {
			return tools[i].method < tools[j].method
		}
		return tools[i].path < tools[j].path
	})
	return tools, nil
}

func loadOpenAPISpec(spec []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(spec)
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("validate spec: %w", err)
	}
	return doc, nil
}

func validateOpenAPISpec(spec []byte) error {
	_, err := loadOpenAPISpec(spec)
	return err
}

func operationToTool(
	method string,
	path string,
	pathParams openapi3.Parameters,
	op *openapi3.Operation,
	jsonSchema2020 bool,
) (Tool, error) {
	params := mergeParameters(pathParams, op.Parameters)
	props := map[string]any{}
	required, toolParams, err := addParamProps(params, props)
	if err != nil {
		return Tool{}, err
	}
	bodyRequired, bodyContentType, hasBody := addBodyProp(op.RequestBody, props)
	if hasBody && bodyRequired {
		required = append(required, "body")
	}
	sort.Strings(required)

	inputSchema := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		inputSchema["required"] = required
	}
	rawSchema, err := json.Marshal(inputSchema)
	if err != nil {
		return Tool{}, fmt.Errorf("marshal input schema: %w", err)
	}
	var inputValidator openapi3.Schema
	if err := json.Unmarshal(rawSchema, &inputValidator); err != nil {
		return Tool{}, fmt.Errorf("compile input schema: %w", err)
	}

	return Tool{
		Name:            toolName(method, path, op),
		Description:     toolDescription(op),
		InputSchema:     rawSchema,
		method:          method,
		path:            path,
		params:          toolParams,
		bodyContentType: bodyContentType,
		inputValidator:  &inputValidator,
		jsonSchema2020:  jsonSchema2020,
	}, nil
}

func sortedKeys[Value any](values map[string]Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeParameters(pathParams, operationParams openapi3.Parameters) openapi3.Parameters {
	merged := make(openapi3.Parameters, 0, len(pathParams)+len(operationParams))
	positions := make(map[string]int, len(pathParams)+len(operationParams))
	for i := range pathParams {
		merged, positions = mergeParameter(merged, positions, pathParams[i])
	}
	for i := range operationParams {
		merged, positions = mergeParameter(merged, positions, operationParams[i])
	}
	sort.Slice(merged, func(i, j int) bool {
		left, right := merged[i].Value, merged[j].Value
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.In < right.In
	})
	return merged
}

func mergeParameter(
	merged openapi3.Parameters,
	positions map[string]int,
	ref *openapi3.ParameterRef,
) (openapi3.Parameters, map[string]int) {
	if ref == nil || ref.Value == nil {
		return merged, positions
	}
	key := ref.Value.In + "\x00" + ref.Value.Name
	if index, ok := positions[key]; ok {
		merged[index] = ref
		return merged, positions
	}
	positions[key] = len(merged)
	return append(merged, ref), positions
}

func toolName(method, path string, op *openapi3.Operation) string {
	if op.OperationID != "" {
		return op.OperationID
	}
	// Fallback: METHOD_path with slashes -> underscores, params dropped.
	return strings.ToLower(method) + sanitizePath(path)
}

func toolDescription(op *openapi3.Operation) string {
	if op.Summary != "" {
		return op.Summary
	}
	return op.Description
}

type toolParameter struct {
	name     string
	location string
	style    string
	explode  bool
}

func addParamProps(
	params openapi3.Parameters,
	props map[string]any,
) ([]string, []toolParameter, error) {
	var required []string
	toolParams := make([]toolParameter, 0, len(params))
	locations := make(map[string]string, len(params))
	for i := range params {
		paramRef := params[i]
		if paramRef == nil || paramRef.Value == nil {
			continue
		}
		p := paramRef.Value
		if err := setParameterProp(p, props, locations); err != nil {
			return nil, nil, err
		}
		if p.Required {
			required = append(required, p.Name)
		}
		method, methodErr := p.SerializationMethod()
		if methodErr != nil {
			return nil, nil, fmt.Errorf("parameter %q serialization: %w", p.Name, methodErr)
		}
		toolParams = append(toolParams, toolParameter{
			name:     p.Name,
			location: p.In,
			style:    method.Style,
			explode:  method.Explode,
		})
	}
	return required, toolParams, nil
}

func setParameterProp(
	param *openapi3.Parameter,
	props map[string]any,
	locations map[string]string,
) error {
	if param.Name == "body" {
		return errors.New(`parameter "body" is reserved for JSON request bodies`)
	}
	if location, ok := locations[param.Name]; ok && location != param.In {
		return fmt.Errorf("parameter %q appears in both %s and %s", param.Name, location, param.In)
	}
	locations[param.Name] = param.In
	props[param.Name] = parameterSchema(param)
	return nil
}

func parameterSchema(param *openapi3.Parameter) any {
	schema := schemaRefToAny(param.Schema)
	if schema == nil {
		schema = map[string]any{"type": "string"}
	}
	object, ok := schema.(map[string]any)
	if ok && param.Description != "" && object["description"] == nil {
		object["description"] = param.Description
	}
	return schema
}

// addBodyProp adds the selected JSON request-body schema to props as "body".
// It returns the required flag, exact media type, and whether a body was found.
func addBodyProp(body *openapi3.RequestBodyRef, props map[string]any) (bool, string, bool) {
	if body == nil || body.Value == nil {
		return false, "", false
	}
	contentTypes := sortedKeys(body.Value.Content)
	for i := range contentTypes {
		ct := contentTypes[i]
		media := body.Value.Content[ct]
		if !strings.Contains(ct, "json") || media == nil || media.Schema == nil {
			continue
		}
		props["body"] = schemaRefToAny(media.Schema)
		return body.Value.Required, ct, true
	}
	return false, "", false
}

// schemaRefToAny inlines a schema ref into a plain JSON-Schema-ish map.
// kin-openapi resolves refs when loading, so we just JSON-round-trip.
func schemaRefToAny(ref *openapi3.SchemaRef) any {
	if ref == nil || ref.Value == nil {
		return nil
	}
	b, err := ref.Value.MarshalJSON()
	if err != nil {
		return nil
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return nil
	}
	return v
}

// sanitizePath turns "/users/{id}/posts" into "_users_id_posts" for use as
// a fallback tool name when operationId is missing.
func sanitizePath(p string) string {
	s := strings.ReplaceAll(p, "/", "_")
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	return s
}

type callParts struct {
	path        string
	body        io.Reader
	contentType string
	headers     http.Header
	cookies     []http.Cookie
}

// buildCall assembles a tools/call invocation without mutating the tool.
func buildCall(tool *Tool, args json.RawMessage) (callParts, error) {
	parts := callParts{path: tool.path, headers: make(http.Header)}
	if len(args) == 0 {
		return parts, nil
	}
	argMap, err := decodeCallArguments(args)
	if err != nil {
		return callParts{}, fmt.Errorf("unmarshal arguments: %w", err)
	}

	query := url.Values{}
	handled, err := addDeclaredCallParameters(&parts, query, tool.params, argMap)
	if err != nil {
		return callParts{}, err
	}
	if err := rejectUndeclaredCallParameters(handled, argMap, tool.bodyContentType != ""); err != nil {
		return callParts{}, err
	}
	appendCallQuery(&parts, query)
	addCallBody(&parts, argMap, tool.bodyContentType)
	return parts, nil
}

func decodeCallArguments(args json.RawMessage) (map[string]json.RawMessage, error) {
	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return argMap, nil
}

func addDeclaredCallParameters(
	parts *callParts,
	query url.Values,
	params []toolParameter,
	argMap map[string]json.RawMessage,
) (map[string]struct{}, error) {
	handled := make(map[string]struct{}, len(params))
	for i := range params {
		param := params[i]
		value, ok := argMap[param.name]
		if !ok {
			continue
		}
		handled[param.name] = struct{}{}
		if err := addCallParameter(parts, query, param, value); err != nil {
			return nil, err
		}
	}
	return handled, nil
}

func appendCallQuery(parts *callParts, query url.Values) {
	if qs := query.Encode(); qs != "" {
		parts.path += "?" + qs
	}
}

func addCallBody(parts *callParts, argMap map[string]json.RawMessage, contentType string) {
	bodyRaw, ok := argMap["body"]
	if !ok {
		return
	}
	if contentType == "" {
		contentType = "application/json"
	}
	parts.body = bytes.NewReader(bodyRaw)
	parts.contentType = contentType
}

func addCallParameter(
	parts *callParts,
	query url.Values,
	param toolParameter,
	raw json.RawMessage,
) error {
	value, err := decodeParameterValue(raw)
	if err != nil {
		return fmt.Errorf("apidocs: parameter %q: %w", param.name, err)
	}
	switch param.location {
	case openapi3.ParameterInPath:
		return addPathParameter(parts, param, value)
	case openapi3.ParameterInHeader:
		return addHeaderParameter(parts, param, value)
	case openapi3.ParameterInCookie:
		return addCookieParameter(parts, param, value)
	default:
		return addQueryParameter(query, param, value)
	}
}
