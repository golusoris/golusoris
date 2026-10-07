// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package snapshot makes bounded, type-preserving copies of mutable value
// graphs. It rejects values that cannot be copied safely.
package snapshot

import (
	"fmt"
	"reflect"
	"time"
)

const maxCloneNodes = 65_536

type reference struct {
	kind    reflect.Kind
	typeOf  reflect.Type
	pointer uintptr
	length  int
}

type target struct {
	root      *reflect.Value
	container reflect.Value
	index     int
	mapKey    reflect.Value
}

type job struct {
	source reflect.Value
	target target
}

// Clone returns an independent copy of value while preserving concrete types
// and graph cycles. Work is capped at 65,536 nodes.
func Clone[T any](value T) (T, error) {
	var zero T
	if any(value) == nil {
		return zero, nil
	}
	cloned, err := cloneValue(any(value))
	if err != nil {
		return zero, err
	}
	out, ok := cloned.(T)
	if !ok {
		return zero, fmt.Errorf("snapshot: cloned %T cannot become requested type", cloned)
	}
	return out, nil
}

func cloneValue(value any) (any, error) {
	var root reflect.Value
	jobs := []job{{source: reflect.ValueOf(value), target: target{root: &root}}}
	memo := make(map[reference]reflect.Value)
	for steps := 0; steps < maxCloneNodes && len(jobs) > 0; steps++ {
		current := jobs[len(jobs)-1]
		jobs = jobs[:len(jobs)-1]
		children, err := cloneOne(current, memo)
		if err != nil {
			return nil, err
		}
		if len(jobs)+len(children) > maxCloneNodes {
			return nil, fmt.Errorf("snapshot: value exceeds %d clone nodes", maxCloneNodes)
		}
		jobs = append(jobs, children...)
	}
	if len(jobs) != 0 {
		return nil, fmt.Errorf("snapshot: value exceeds %d clone nodes", maxCloneNodes)
	}
	return root.Interface(), nil
}

func cloneOne(current job, memo map[reference]reflect.Value) ([]job, error) {
	source := current.source
	if !source.IsValid() {
		return nil, current.target.assign(reflect.Value{})
	}
	if scalar(source.Kind()) {
		return nil, current.target.assign(source)
	}
	return cloneContainer(current, memo)
}

func scalar(kind reflect.Kind) bool {
	switch kind {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
		return true
	case reflect.Invalid, reflect.Array, reflect.Chan, reflect.Func, reflect.Interface,
		reflect.Map, reflect.Pointer, reflect.Slice, reflect.Struct, reflect.UnsafePointer:
		return false
	}
	return false
}

func cloneContainer(current job, memo map[reference]reflect.Value) ([]job, error) {
	source := current.source
	//nolint:exhaustive // default rejects future mutable kinds.
	switch source.Kind() {
	case reflect.Interface:
		if source.IsNil() {
			return nil, current.target.assign(reflect.Zero(source.Type()))
		}
		return []job{{source: source.Elem(), target: current.target}}, nil
	case reflect.Map:
		return cloneMap(current, memo)
	case reflect.Slice:
		return cloneSlice(current, memo)
	case reflect.Pointer:
		return clonePointer(current, memo)
	case reflect.Array:
		return cloneArray(current)
	case reflect.Struct:
		if source.Type() == reflect.TypeFor[time.Time]() {
			return nil, current.target.assign(source)
		}
		return cloneStruct(current)
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return nil, fmt.Errorf("snapshot: unsupported mutable value type %s", source.Type())
	default:
		return nil, fmt.Errorf("snapshot: unsupported value kind %s", source.Kind())
	}
}

func cloneMap(current job, memo map[reference]reflect.Value) ([]job, error) {
	source := current.source
	if source.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("snapshot: unsupported map key type %s", source.Type().Key())
	}
	if source.IsNil() {
		return nil, current.target.assign(reflect.Zero(source.Type()))
	}
	if err := validateWidth("map", source.Len()); err != nil {
		return nil, err
	}
	ref := cloneRef(source)
	if cloned, ok := memo[ref]; ok {
		return nil, current.target.assign(cloned)
	}
	cloned := reflect.MakeMapWithSize(source.Type(), source.Len())
	memo[ref] = cloned
	if err := current.target.assign(cloned); err != nil {
		return nil, err
	}
	children := make([]job, 0, source.Len())
	iter := source.MapRange()
	for iter.Next() {
		children = append(children, job{
			source: iter.Value(),
			target: target{container: cloned, mapKey: iter.Key()},
		})
	}
	return children, nil
}

func cloneSlice(current job, memo map[reference]reflect.Value) ([]job, error) {
	source := current.source
	if source.IsNil() {
		return nil, current.target.assign(reflect.Zero(source.Type()))
	}
	if err := validateWidth("slice", source.Len()); err != nil {
		return nil, err
	}
	ref := cloneRef(source)
	if cloned, ok := memo[ref]; ok {
		return nil, current.target.assign(cloned)
	}
	cloned := reflect.MakeSlice(source.Type(), source.Len(), source.Len())
	memo[ref] = cloned
	if err := current.target.assign(cloned); err != nil {
		return nil, err
	}
	return indexedJobs(source, cloned), nil
}

func clonePointer(current job, memo map[reference]reflect.Value) ([]job, error) {
	source := current.source
	if source.IsNil() {
		return nil, current.target.assign(reflect.Zero(source.Type()))
	}
	ref := cloneRef(source)
	if cloned, ok := memo[ref]; ok {
		return nil, current.target.assign(cloned)
	}
	cloned := reflect.New(source.Type().Elem())
	memo[ref] = cloned
	if err := current.target.assign(cloned); err != nil {
		return nil, err
	}
	return []job{{source: source.Elem(), target: target{container: cloned.Elem(), index: -1}}}, nil
}

func cloneArray(current job) ([]job, error) {
	if err := validateWidth("array", current.source.Len()); err != nil {
		return nil, err
	}
	cloned := reflect.New(current.source.Type()).Elem()
	if err := current.target.assign(cloned); err != nil {
		return nil, err
	}
	return indexedJobs(current.source, cloned), nil
}

func cloneStruct(current job) ([]job, error) {
	source := current.source
	if err := validateWidth("struct", source.NumField()); err != nil {
		return nil, err
	}
	cloned := reflect.New(source.Type()).Elem()
	cloned.Set(source)
	if err := current.target.assign(cloned); err != nil {
		return nil, err
	}
	children := make([]job, 0, source.NumField())
	for i := range source.NumField() {
		field := source.Type().Field(i)
		if !field.IsExported() {
			if !deepImmutable(field.Type) {
				return nil, fmt.Errorf(
					"snapshot: unsupported mutable unexported field %s.%s",
					source.Type(),
					field.Name,
				)
			}
			continue
		}
		children = append(children, job{
			source: source.Field(i),
			target: target{container: cloned, index: i},
		})
	}
	return children, nil
}

func validateWidth(kind string, width int) error {
	if width > maxCloneNodes {
		return fmt.Errorf("snapshot: %s width %d exceeds %d clone nodes", kind, width, maxCloneNodes)
	}
	return nil
}

func indexedJobs(source, cloned reflect.Value) []job {
	children := make([]job, 0, source.Len())
	for i := range source.Len() {
		children = append(children, job{
			source: source.Index(i),
			target: target{container: cloned, index: i},
		})
	}
	return children
}

func cloneRef(value reflect.Value) reference {
	ref := reference{kind: value.Kind(), typeOf: value.Type(), pointer: value.Pointer()}
	if value.Kind() == reflect.Slice {
		ref.length = value.Len()
	}
	return ref
}

func (destination target) assign(value reflect.Value) error {
	if destination.root != nil {
		*destination.root = value
		return nil
	}
	var targetType reflect.Type
	switch {
	case destination.mapKey.IsValid():
		targetType = destination.container.Type().Elem()
	case destination.index >= 0:
		targetType = indexedValue(destination.container, destination.index).Type()
	default:
		targetType = destination.container.Type()
	}
	assigned, err := assignable(value, targetType)
	if err != nil {
		return err
	}
	switch {
	case destination.mapKey.IsValid():
		destination.container.SetMapIndex(destination.mapKey, assigned)
	case destination.index < 0:
		destination.container.Set(assigned)
	default:
		indexedValue(destination.container, destination.index).Set(assigned)
	}
	return nil
}

func indexedValue(container reflect.Value, index int) reflect.Value {
	if container.Kind() == reflect.Struct {
		return container.Field(index)
	}
	return container.Index(index)
}

func assignable(value reflect.Value, targetType reflect.Type) (reflect.Value, error) {
	if !value.IsValid() {
		return reflect.Zero(targetType), nil
	}
	if !value.Type().AssignableTo(targetType) {
		return reflect.Value{}, fmt.Errorf("snapshot: cannot assign cloned %s to %s", value.Type(), targetType)
	}
	return value, nil
}

func deepImmutable(root reflect.Type) bool {
	seen := make(map[reflect.Type]bool)
	stack := []reflect.Type{root}
	for steps := 0; steps < 256 && len(stack) > 0; steps++ {
		typeOf := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[typeOf] {
			continue
		}
		seen[typeOf] = true
		switch typeOf.Kind() {
		case reflect.Invalid:
			return false
		case reflect.Array:
			stack = append(stack, typeOf.Elem())
		case reflect.Struct:
			for field := range typeOf.Fields() {
				stack = append(stack, field.Type)
			}
		case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface,
			reflect.Chan, reflect.Func, reflect.UnsafePointer:
			return false
		case reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
			reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
			continue
		default:
			return false
		}
	}
	return len(stack) == 0
}
