// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/knadh/koanf/v2"
)

const (
	maxEnvStructDepth  = 32
	maxEnvStructFields = 4096
)

type envStruct struct {
	typ   reflect.Type
	path  string
	depth int
}

func (c *Config) unmarshalEnv(path string, into any, conf koanf.UnmarshalConf) error {
	if len(c.envValues) == 0 {
		return nil
	}
	typ := reflect.TypeOf(into)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct {
		return nil
	}
	paths, err := envStructPaths(envStruct{typ: typ.Elem(), path: path, depth: 1}, c.opts.Delimiter)
	if err != nil {
		return err
	}
	overrides := koanf.New(c.opts.Delimiter)
	if err := c.setEnvOverrides(overrides, paths); err != nil {
		return err
	}
	if len(overrides.Keys()) == 0 {
		return nil
	}
	if err := overrides.UnmarshalWithConf(path, into, conf); err != nil {
		return fmt.Errorf("config: unmarshal env %q: %w", path, err)
	}
	return nil
}

func (c *Config) setEnvOverrides(overrides *koanf.Koanf, paths []string) error {
	lookup := compoundLookup(c.opts)
	for _, key := range paths {
		name := strings.ToUpper(strings.ReplaceAll(key, c.opts.Delimiter, "_"))
		if explicit, ok := lookup[name]; ok && explicit != key {
			continue
		}
		if value, ok := c.envValues[name]; ok {
			if err := overrides.Set(key, value); err != nil {
				return fmt.Errorf("config: set env override %q: %w", key, err)
			}
		}
	}
	return nil
}

func envStructPaths(root envStruct, delim string) ([]string, error) {
	queue := []envStruct{root}
	paths := []string{}
	fields := 0
	for n := 0; n < len(queue) && n <= maxEnvStructFields; n++ {
		node := queue[n]
		if node.depth > maxEnvStructDepth {
			return nil, errors.New("config: env struct exceeds 32 levels")
		}
		if node.typ.NumField() > maxEnvStructFields-fields {
			return nil, errors.New("config: env struct exceeds 4096 fields")
		}
		fields += node.typ.NumField()
		queue, paths = appendEnvStructFields(node, delim, queue, paths)
	}
	return paths, nil
}

func appendEnvStructFields(node envStruct, delim string, queue []envStruct, paths []string) ([]envStruct, []string) {
	for i := range node.typ.NumField() {
		next := envStructField(node, node.typ.Field(i), delim)
		if next.typ == nil {
			continue
		}
		switch {
		case next.typ.Kind() == reflect.Struct:
			queue = append(queue, next)
		case envScalarLeaf(next.typ) && strings.Contains(next.path, "_"):
			paths = append(paths, next.path)
		}
	}
	return queue, paths
}

func envStructField(parent envStruct, field reflect.StructField, delim string) envStruct {
	name, keep := envStructTag(field)
	if !keep {
		return envStruct{}
	}
	typ := field.Type
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	// An unexported embedded struct can still expose settable squashed fields.
	if field.PkgPath != "" && (name != "" || typ.Kind() != reflect.Struct) {
		return envStruct{}
	}
	path := parent.path
	if name != "" {
		if path != "" {
			path += delim
		}
		path += name
	}
	return envStruct{typ: typ, path: path, depth: parent.depth + 1}
}

func envStructTag(field reflect.StructField) (string, bool) {
	name, options, _ := strings.Cut(field.Tag.Get("koanf"), ",")
	if name == "-" {
		return "", false
	}
	if name == "" {
		name = field.Name
	}
	for _, option := range strings.Split(options, ",") {
		switch option {
		case "squash":
			return "", true
		case "remain":
			return "", false
		}
	}
	return name, true
}

// envScalarLeaf limits overrides to kinds a single env string decodes into;
// maps and interfaces keep their file values.
func envScalarLeaf(typ reflect.Type) bool {
	kind := typ.Kind()
	if kind == reflect.Slice || kind == reflect.Array {
		kind = typ.Elem().Kind()
	}
	return kind != reflect.Map && kind != reflect.Interface && kind != reflect.Func &&
		kind != reflect.Chan && kind != reflect.UnsafePointer && kind != reflect.Struct
}
