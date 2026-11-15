// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"strconv"

	"go.yaml.in/yaml/v4"
)

type pending struct {
	node    *yaml.Node
	path    []string
	keyLine int
	key     bool
	alias   bool
}

func extend(path []string, key string) []string {
	next := make([]string, len(path)+1)
	copy(next, path)
	next[len(path)] = key
	return next
}

func mappingChildren(item pending) ([]pending, error) {
	content := item.node.Content
	if len(content)%2 != 0 || len(content) > maxNodes {
		return nil, errors.New("workflow parser: invalid or oversized mapping")
	}
	seen := make(map[string]bool, len(content)/2)
	children := make([]pending, 0, len(content))
	for i := 0; i < len(content) && i < maxNodes; i += 2 {
		key, val := content[i], content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" {
			return nil, fmt.Errorf("workflow parser: non-string or merge key at line %d", key.Line)
		}
		if seen[key.Value] {
			return nil, fmt.Errorf("workflow parser: duplicate key %q at line %d", key.Value, key.Line)
		}
		seen[key.Value] = true
		path := extend(item.path, key.Value)
		children = append(children,
			pending{key, path, key.Line, true, item.alias},
			pending{val, path, key.Line, false, item.alias})
	}
	return children, nil
}

func children(item pending) ([]pending, error) {
	if item.node.Kind == yaml.MappingNode {
		return mappingChildren(item)
	}
	content := item.node.Content
	if len(content) > maxNodes {
		return nil, errors.New("workflow parser: sequence exceeds node bound")
	}
	items := make([]pending, 0, len(content))
	for i, child := range content[:min(len(content), maxNodes)] {
		path := item.path
		if item.node.Kind == yaml.SequenceNode {
			path = extend(path, "#"+strconv.Itoa(i))
		}
		items = append(items, pending{child, path, item.keyLine, false, item.alias})
	}
	return items, nil
}

func newScalar(item pending) scalar {
	node := item.node
	s := scalar{
		Path: item.path, Text: node.Value, Line: node.Line,
		KeyLine: item.keyLine, Key: item.key, Alias: item.alias, Style: node.Style,
	}
	if !item.key {
		s.Refs, s.Calls, s.Error = scalarExpressions(s)
	}
	return s
}

func flatten(root *yaml.Node) ([]scalar, error) {
	stack := []pending{{node: root}}
	result := make([]scalar, 0, 256)
	for visited := 0; len(stack) > 0 && visited < maxNodes; visited++ {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(item.path) > 128 {
			return nil, errors.New("workflow parser: YAML depth bound exceeded")
		}
		if item.node.Kind == yaml.AliasNode {
			stack = append(stack, pending{item.node.Alias, item.path, item.keyLine, item.key, true})
			continue
		}
		if item.node.Kind == yaml.ScalarNode {
			result = append(result, newScalar(item))
			continue
		}
		next, err := children(item)
		if err != nil {
			return nil, fmt.Errorf("workflow parser: walk YAML: %w", err)
		}
		for i := len(next) - 1; i >= 0 && i < maxNodes; i-- {
			stack = append(stack, next[i])
		}
	}
	if len(stack) != 0 {
		return nil, errors.New("workflow parser: YAML alias or node bound exceeded")
	}
	return result, nil
}
