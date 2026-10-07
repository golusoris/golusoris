// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jsonschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	invopop "github.com/invopop/jsonschema"
)

// DurationPattern matches the strings core/config decodes into a
// time.Duration via time.ParseDuration ("5s", "1h30m", "-1.5h", "0").
const DurationPattern = `^[-+]?(0|(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h))+)$`

// maxConfigWalk bounds the type and value walks; config trees are small.
const maxConfigWalk = 1 << 16

var durationType = reflect.TypeFor[time.Duration]()

// ConfigOptions tunes [GenerateConfig].
type ConfigOptions struct {
	// EnvPrefix is the core/config env prefix ("APP_"); empty omits env names.
	EnvPrefix string
	// Delimiter is the koanf path separator; empty means ".".
	Delimiter string
	// Title is the schema title.
	Title string
	// ID is the schema $id; empty omits it.
	ID string
}

// GenerateConfig emits a JSON Schema (draft 2020-12) for a koanf-tagged
// config struct, usable as a Helm values.schema.json. Property names come
// from `koanf` tags, defaults from the values in v (pass DefaultOptions()),
// time.Duration fields are strings matching [DurationPattern], and every
// leaf description names its environment variable. Nothing is required
// and unknown keys are rejected. Recursive types return
// [*ErrUnsupportedType].
func GenerateConfig(v any, opts ConfigOptions) ([]byte, error) {
	root := reflect.ValueOf(v)
	if root.Kind() == reflect.Pointer {
		if root.IsNil() {
			return nil, errors.New("jsonschema: generate config: nil pointer")
		}
		root = root.Elem()
	}
	if root.Kind() != reflect.Struct {
		return nil, fmt.Errorf("jsonschema: generate config: %s is not a struct", typeNameOf(v))
	}
	if err := checkAcyclic(root.Type()); err != nil {
		return nil, err
	}
	if opts.Delimiter == "" {
		opts.Delimiter = "."
	}
	schema, err := reflectConfig(root)
	if err != nil {
		return nil, err
	}
	schema.Title = opts.Title
	schema.ID = invopop.ID(opts.ID)
	if err = annotate(schema, root, opts); err != nil {
		return nil, err
	}
	doc, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("jsonschema: marshal config schema: %w", err)
	}
	return doc, nil
}

func reflectConfig(root reflect.Value) (schema *invopop.Schema, err error) {
	defer func() {
		if recover() != nil {
			schema, err = nil, &ErrUnsupportedType{Type: root.Type().String()}
		}
	}()
	reflector := &invopop.Reflector{
		FieldNameTag:               "koanf",
		DoNotReference:             true, // per-path defaults and env names need unshared schemas
		ExpandedStruct:             true,
		RequiredFromJSONSchemaTags: true,
		Anonymous:                  true,
		Mapper: func(t reflect.Type) *invopop.Schema {
			if t == durationType {
				return &invopop.Schema{Type: "string", Pattern: DurationPattern}
			}
			return nil
		},
	}
	return reflector.ReflectFromType(root.Type()), nil
}

// configField mirrors the reflector's field naming for the koanf tag:
// name "" with inline=true means an embedded struct whose fields are hoisted.
func configField(f reflect.StructField) (name string, inline bool) {
	tag := strings.Split(f.Tag.Get("koanf"), ",")[0]
	schemaTag := strings.Split(f.Tag.Get("jsonschema"), ",")[0]
	if tag == "-" || schemaTag == "-" {
		return "", false
	}
	if f.Anonymous && tag == "" && structOf(f.Type) != nil {
		return "", true
	}
	if !f.IsExported() {
		return "", false
	}
	if tag == "" {
		return f.Name, false
	}
	return tag, false
}

// structOf unwraps pointers, slices, arrays, and maps to a struct type, or nil.
func structOf(t reflect.Type) reflect.Type {
	for range maxConfigWalk {
		kind := t.Kind()
		if kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Array || kind == reflect.Map {
			t = t.Elem()
			continue
		}
		if kind != reflect.Struct || t == reflect.TypeFor[time.Time]() {
			return nil
		}
		return t
	}
	return nil
}

// checkAcyclic rejects recursive struct types before the reflector, which
// recurses without bound when references are disabled.
func checkAcyclic(root reflect.Type) error {
	type frame struct {
		t    reflect.Type
		next int
	}
	const onStack, done = 1, 2
	state := map[reflect.Type]int{root: onStack}
	stack := []frame{{t: root}}
	for steps := 0; steps < maxConfigWalk && len(stack) > 0; steps++ {
		top := &stack[len(stack)-1]
		if top.next >= top.t.NumField() {
			state[top.t] = done
			stack = stack[:len(stack)-1]
			continue
		}
		field := top.t.Field(top.next)
		top.next++
		if name, inline := configField(field); name == "" && !inline {
			continue
		}
		child := structOf(field.Type)
		if child == nil || state[child] == done {
			continue
		}
		if state[child] == onStack {
			return &ErrUnsupportedType{Type: child.String()}
		}
		state[child] = onStack
		stack = append(stack, frame{t: child})
	}
	if len(stack) > 0 {
		return fmt.Errorf("jsonschema: generate config: type graph exceeds %d steps", maxConfigWalk)
	}
	return nil
}

// configFrame is one struct level of the annotate walk.
type configFrame struct {
	schema   *invopop.Schema
	value    reflect.Value
	path     []string
	defaults bool
}

// annotate walks schema and value together, adding defaults to leaves and
// env names to leaf descriptions.
func annotate(root *invopop.Schema, value reflect.Value, opts ConfigOptions) error {
	stack := []configFrame{{schema: root, value: value, defaults: true}}
	for steps := 0; steps < maxConfigWalk && len(stack) > 0; steps++ {
		top := stack[len(stack)-1]
		stack = append(stack[:len(stack)-1], annotateStruct(top, opts)...)
	}
	if len(stack) > 0 {
		return fmt.Errorf("jsonschema: generate config: value tree exceeds %d steps", maxConfigWalk)
	}
	return nil
}

// annotateStruct annotates the leaves of one struct and returns the nested
// structs still to visit.
func annotateStruct(top configFrame, opts ConfigOptions) []configFrame {
	var children []configFrame
	for i := range top.value.NumField() {
		name, inline := configField(top.value.Type().Field(i))
		field := top.value.Field(i)
		if inline {
			child, ok := structValue(field)
			children = append(children, configFrame{top.schema, child, top.path, top.defaults && ok})
			continue
		}
		prop, found := propertyOf(top.schema, name)
		if !found {
			continue
		}
		path := append(append(make([]string, 0, len(top.path)+1), top.path...), name)
		if child, ok := structValue(field); child.IsValid() {
			children = append(children, configFrame{prop, child, path, top.defaults && ok})
			continue
		}
		annotateLeaf(prop, field, path, top.defaults, opts)
	}
	return children
}

func propertyOf(schema *invopop.Schema, name string) (*invopop.Schema, bool) {
	if name == "" || schema == nil || schema.Properties == nil {
		return nil, false
	}
	return schema.Properties.Get(name)
}

// structValue returns the struct behind v (a struct or pointer to one); ok
// is false for a nil pointer, whose zero value still carries env names.
func structValue(v reflect.Value) (reflect.Value, bool) {
	if v.Kind() == reflect.Pointer && v.Type().Elem().Kind() == reflect.Struct {
		if v.IsNil() {
			return reflect.Zero(v.Type().Elem()), false
		}
		return v.Elem(), true
	}
	if v.Kind() == reflect.Struct && v.Type() != reflect.TypeFor[time.Time]() {
		return v, true
	}
	return reflect.Value{}, false
}

func annotateLeaf(prop *invopop.Schema, field reflect.Value, path []string, defaults bool, opts ConfigOptions) {
	if defaults {
		if def, ok := defaultOf(field); ok {
			prop.Default = def
		}
	}
	if opts.EnvPrefix == "" {
		return
	}
	key := strings.Join(path, opts.Delimiter)
	env := opts.EnvPrefix + strings.ToUpper(strings.Join(path, "_"))
	note := "Env: " + env + "."
	if strings.Contains(key, "_") {
		note = "Env: " + env + " (declare config.Options.CompoundKeys entry \"" + key + "\")."
	}
	if kind := field.Kind(); kind == reflect.Slice || kind == reflect.Array {
		note += " Comma-separated."
	} else if kind == reflect.Map {
		note = "Env: " + env + "_* (one variable per map key)."
	}
	prop.Description = strings.TrimSpace(prop.Description + " " + note)
}

// defaultOf renders a leaf default as the YAML/JSON value core/config
// decodes; empty strings, nil or empty collections, and maps are omitted.
// Kind getters, not Interface: fields hoisted from an unexported embedded
// struct are read-only values that Interface refuses.
func defaultOf(v reflect.Value) (any, bool) {
	kind := v.Kind()
	switch {
	case v.Type() == durationType:
		return time.Duration(v.Int()).String(), true
	case kind == reflect.Bool:
		return v.Bool(), true
	case v.CanInt():
		return v.Int(), true
	case v.CanUint():
		return v.Uint(), true
	case v.CanFloat():
		return v.Float(), true
	case kind == reflect.String:
		return v.String(), v.Len() > 0
	case kind == reflect.Slice || kind == reflect.Array:
		return sliceDefault(v)
	default:
		return nil, false
	}
}

func sliceDefault(v reflect.Value) (any, bool) {
	elem := v.Type().Elem()
	if v.Len() == 0 || !v.CanInterface() || elem == durationType || structOf(elem) != nil {
		return nil, false
	}
	return v.Interface(), true
}
