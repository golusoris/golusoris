// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fleet

import (
	"encoding"
	jsonv1 "encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"reflect"
	"unicode/utf8"
)

const (
	maxInputJSONDepth  = 512
	maxInputJSONValues = 100_000
)

type jsonValueFrame struct {
	value reflect.Value
	depth int
}

var (
	jsonMarshalerType   = reflect.TypeFor[jsonv1.Marshaler]()
	jsonMarshalerToType = reflect.TypeFor[jsonv2.MarshalerTo]()
	textAppenderType    = reflect.TypeFor[encoding.TextAppender]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	rawJSONMessageType  = reflect.TypeFor[jsonv1.RawMessage]()
)

func validateJSONInputMemory(input any, maxBytes int) error {
	stack := []jsonValueFrame{{value: reflect.ValueOf(input)}}
	values := 0
	for len(stack) > 0 {
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		value := unwrapJSONInterface(frame.value)
		if !value.IsValid() {
			continue
		}
		values++
		if values > maxInputJSONValues {
			return fmt.Errorf("ai/tiny/serve/fleet: input exceeds %d JSON values", maxInputJSONValues)
		}
		if frame.depth > maxInputJSONDepth {
			return fmt.Errorf("ai/tiny/serve/fleet: input exceeds JSON depth %d", maxInputJSONDepth)
		}
		if err := rejectCustomJSONMarshaler(value); err != nil {
			return err
		}
		if err := validateJSONScalarMemory(value, int64(maxBytes)); err != nil {
			return err
		}
		var err error
		stack, err = appendJSONChildren(stack, value, frame.depth+1, maxInputJSONValues-values)
		if err != nil {
			return err
		}
	}
	return nil
}

func rejectCustomJSONMarshaler(value reflect.Value) error {
	valueType := value.Type()
	if valueType == rawJSONMessageType ||
		(valueType.Kind() == reflect.Pointer && valueType.Elem() == rawJSONMessageType) {
		return nil
	}
	if implementsCustomJSONMarshaler(valueType) {
		return fmt.Errorf(
			"ai/tiny/serve/fleet: input type %s uses a custom JSON marshaler that cannot be memory-bounded",
			valueType,
		)
	}
	if valueType.Kind() != reflect.Pointer && implementsCustomJSONMarshaler(reflect.PointerTo(valueType)) {
		return fmt.Errorf(
			"ai/tiny/serve/fleet: input type %s uses a custom JSON marshaler that cannot be memory-bounded",
			valueType,
		)
	}
	return nil
}

func implementsCustomJSONMarshaler(valueType reflect.Type) bool {
	return valueType.Implements(jsonMarshalerType) ||
		valueType.Implements(jsonMarshalerToType) ||
		valueType.Implements(textAppenderType) ||
		valueType.Implements(textMarshalerType)
}

func unwrapJSONInterface(value reflect.Value) reflect.Value {
	for value.IsValid() && value.Kind() == reflect.Interface {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}

func validateJSONScalarMemory(value reflect.Value, maxBytes int64) error {
	if value.Type() == reflect.TypeFor[jsonv1.RawMessage]() {
		if int64(value.Len()) > maxBytes {
			return inputScalarLimitError(maxBytes)
		}
		return nil
	}
	switch value.Kind() {
	case reflect.String:
		if jsonStringExceeds(value.String(), maxBytes) {
			return inputScalarLimitError(maxBytes)
		}
	case reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 && base64JSONBytes(value.Len()) > maxBytes {
			return inputScalarLimitError(maxBytes)
		}
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Array,
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Struct,
		reflect.UnsafePointer:
	}
	return nil
}

func appendJSONChildren(
	stack []jsonValueFrame,
	value reflect.Value,
	depth int,
	remaining int,
) ([]jsonValueFrame, error) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			stack = append(stack, jsonValueFrame{value: value.Elem(), depth: depth})
		}
	case reflect.Array, reflect.Slice:
		return appendJSONArrayChildren(stack, value, depth, remaining)
	case reflect.Map:
		return appendJSONMapChildren(stack, value, depth, remaining)
	case reflect.Struct:
		return appendJSONStructChildren(stack, value, depth, remaining)
	case reflect.Invalid,
		reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.String,
		reflect.UnsafePointer:
	}
	return stack, nil
}

func appendJSONArrayChildren(
	stack []jsonValueFrame,
	value reflect.Value,
	depth int,
	remaining int,
) ([]jsonValueFrame, error) {
	if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
		return stack, nil
	}
	if value.Len() > remaining {
		return nil, tooManyJSONValuesError()
	}
	for i := range value.Len() {
		stack = append(stack, jsonValueFrame{value: value.Index(i), depth: depth})
	}
	return stack, nil
}

func appendJSONMapChildren(
	stack []jsonValueFrame,
	value reflect.Value,
	depth int,
	remaining int,
) ([]jsonValueFrame, error) {
	if value.Len() > remaining/2 {
		return nil, tooManyJSONValuesError()
	}
	iterator := value.MapRange()
	for iterator.Next() {
		stack = append(stack,
			jsonValueFrame{value: iterator.Key(), depth: depth},
			jsonValueFrame{value: iterator.Value(), depth: depth},
		)
	}
	return stack, nil
}

func appendJSONStructChildren(
	stack []jsonValueFrame,
	value reflect.Value,
	depth int,
	remaining int,
) ([]jsonValueFrame, error) {
	if value.NumField() > remaining {
		return nil, tooManyJSONValuesError()
	}
	for _, field := range value.Fields() {
		stack = append(stack, jsonValueFrame{value: field, depth: depth})
	}
	return stack, nil
}

func jsonStringExceeds(value string, limit int64) bool {
	encodedBytes := int64(2)
	for i := 0; i < len(value); {
		char := value[i]
		if char < utf8.RuneSelf {
			i++
			encodedBytes += asciiJSONWidth(char)
		} else {
			runeValue, size := utf8.DecodeRuneInString(value[i:])
			i += size
			encodedBytes += runeJSONWidth(runeValue, size)
		}
		if encodedBytes > limit {
			return true
		}
	}
	return encodedBytes > limit
}

func asciiJSONWidth(char byte) int64 {
	switch char {
	case '\\', '"', '\b', '\f', '\n', '\r', '\t':
		return 2
	case '<', '>', '&':
		return 6
	default:
		if char < 0x20 {
			return 6
		}
		return 1
	}
}

func runeJSONWidth(runeValue rune, size int) int64 {
	switch {
	case runeValue == utf8.RuneError && size == 1:
		return 3
	case runeValue == '\u2028' || runeValue == '\u2029':
		return 6
	default:
		return int64(size)
	}
}

func base64JSONBytes(rawBytes int) int64 {
	return 2 + 4*((int64(rawBytes)+2)/3)
}

func inputScalarLimitError(maxBytes int64) error {
	return fmt.Errorf("ai/tiny/serve/fleet: input scalar exceeds cap %d bytes", maxBytes)
}

func tooManyJSONValuesError() error {
	return fmt.Errorf("ai/tiny/serve/fleet: input exceeds %d JSON values", maxInputJSONValues)
}
