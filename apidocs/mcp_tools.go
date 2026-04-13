package apidocs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// openAPIToTools converts an OpenAPI 3.x spec into the list of MCP tools.
// The tool's input schema is a JSON Schema object with properties for each
// parameter (path/query/header/cookie) plus, if the operation has a JSON
// request body, nested under "body".
func openAPIToTools(spec []byte) ([]Tool, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(spec)
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("validate spec: %w", err)
	}

	var tools []Tool
	for path, pathItem := range doc.Paths.Map() {
		for method, op := range pathItem.Operations() {
			tool, buildErr := operationToTool(method, path, op)
			if buildErr != nil {
				return nil, fmt.Errorf("operation %s %s: %w", method, path, buildErr)
			}
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

func operationToTool(method, path string, op *openapi3.Operation) (Tool, error) {
	props := map[string]any{}
	required := addParamProps(op.Parameters, props)
	if bodyReq, ok := addBodyProp(op.RequestBody, props); ok && bodyReq {
		required = append(required, "body")
	}

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

	return Tool{
		Name:        toolName(method, path, op),
		Description: toolDescription(op),
		InputSchema: rawSchema,
		method:      method,
		path:        path,
	}, nil
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

func addParamProps(params openapi3.Parameters, props map[string]any) []string {
	var required []string
	for _, paramRef := range params {
		if paramRef == nil || paramRef.Value == nil {
			continue
		}
		p := paramRef.Value
		schema := schemaRefToAny(p.Schema)
		if schema == nil {
			schema = map[string]any{"type": "string"}
		}
		if p.Description != "" {
			if m, ok := schema.(map[string]any); ok && m["description"] == nil {
				m["description"] = p.Description
			}
		}
		props[p.Name] = schema
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return required
}

// addBodyProp adds the JSON request-body schema to props as "body". Returns
// (bodyRequired, hasBody).
func addBodyProp(body *openapi3.RequestBodyRef, props map[string]any) (bool, bool) {
	if body == nil || body.Value == nil {
		return false, false
	}
	for ct, media := range body.Value.Content {
		if !strings.Contains(ct, "json") || media == nil || media.Schema == nil {
			continue
		}
		props["body"] = schemaRefToAny(media.Schema)
		return body.Value.Required, true
	}
	return false, false
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

// buildCall assembles the path (with path params + query string) and the
// body reader for a tools/call invocation. Pure function — does not mutate
// tool.
func buildCall(tool *Tool, args json.RawMessage) (path string, body io.Reader, contentType string, err error) {
	path = tool.path
	if len(args) == 0 {
		return path, nil, "", nil
	}
	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return "", nil, "", fmt.Errorf("unmarshal arguments: %w", err)
	}

	query := url.Values{}
	for k, v := range argMap {
		if k == "body" {
			continue
		}
		strVal := argAsString(v)
		placeholder := "{" + k + "}"
		if strings.Contains(path, placeholder) {
			path = strings.ReplaceAll(path, placeholder, url.PathEscape(strVal))
			continue
		}
		query.Set(k, strVal)
	}
	if qs := query.Encode(); qs != "" {
		path += "?" + qs
	}

	bodyRaw, hasBody := argMap["body"]
	if !hasBody {
		return path, nil, "", nil
	}
	return path, bytes.NewReader(bodyRaw), "application/json", nil
}

// argAsString renders a JSON-encoded value as a plain string suitable for
// URL path/query substitution. Strings unquote; numbers/bools stringify.
func argAsString(v json.RawMessage) string {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return strings.Trim(string(v), `"`)
}
