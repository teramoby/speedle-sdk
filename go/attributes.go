// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"time"
)

// AttributeType is one of the four Speedle attribute types.
type AttributeType string

const (
	TypeString   AttributeType = "string"
	TypeNumeric  AttributeType = "numeric"
	TypeBool     AttributeType = "bool"
	TypeDatetime AttributeType = "datetime"
)

// Attribute is one Speedle JsonAttribute {name, type, value}: a name, a type
// and a single value or an array of values of that type. Build it with
// [String], [Strings], [Numeric], [Numerics], [Bool], [Bools], [Datetime] or
// [Datetimes]. The zero Attribute is invalid and fails the call.
type Attribute struct {
	name  string
	typ   AttributeType
	array bool
	value any // string, float64, bool, time.Time or a slice of one
}

// String is a single string attribute.
func String(name, value string) Attribute {
	return Attribute{name: name, typ: TypeString, value: value}
}

// Strings is a string array attribute; no values gives an empty array.
func Strings(name string, values ...string) Attribute {
	return Attribute{name: name, typ: TypeString, array: true, value: append([]string{}, values...)}
}

// Numeric is a single numeric attribute. NaN and infinities fail the call.
// Self-hosted Speedle decodes numbers as float64, so integers above 2^53 lose
// precision.
func Numeric(name string, value float64) Attribute {
	return Attribute{name: name, typ: TypeNumeric, value: value}
}

// Numerics is a numeric array attribute.
func Numerics(name string, values ...float64) Attribute {
	return Attribute{name: name, typ: TypeNumeric, array: true, value: append([]float64{}, values...)}
}

// Bool is a single boolean attribute.
func Bool(name string, value bool) Attribute {
	return Attribute{name: name, typ: TypeBool, value: value}
}

// Bools is a boolean array attribute.
func Bools(name string, values ...bool) Attribute {
	return Attribute{name: name, typ: TypeBool, array: true, value: append([]bool{}, values...)}
}

// Datetime is a single datetime attribute, sent as RFC 3339 in UTC with
// fractional seconds only when they are not zero. Self-hosted Speedle stores it
// as Unix seconds and drops the fraction.
func Datetime(name string, value time.Time) Attribute {
	return Attribute{name: name, typ: TypeDatetime, value: value}
}

// Datetimes is a datetime array attribute.
func Datetimes(name string, values ...time.Time) Attribute {
	return Attribute{name: name, typ: TypeDatetime, array: true, value: append([]time.Time{}, values...)}
}

// Name returns the attribute name.
func (a Attribute) Name() string { return a.name }

// Type returns the attribute type.
func (a Attribute) Type() AttributeType { return a.typ }

// IsArray reports whether the attribute is in array form.
func (a Attribute) IsArray() bool { return a.array }

// Value returns a string, float64, bool or time.Time, or a copy of the slice
// of one of these for the array form.
func (a Attribute) Value() any {
	switch v := a.value.(type) {
	case []string:
		return append([]string{}, v...)
	case []float64:
		return append([]float64{}, v...)
	case []bool:
		return append([]bool{}, v...)
	case []time.Time:
		return append([]time.Time{}, v...)
	}
	return a.value
}

// Attributes is the typed attribute list of a call, sent in this order.
type Attributes []Attribute

// wireAttribute is the JsonAttribute on the wire.
type wireAttribute struct {
	Name  string        `json:"name"`
	Type  AttributeType `json:"type"`
	Value any           `json:"value"`
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// toWire runs the §3.2.1 checks and builds the wire form.
func (attrs Attributes) toWire() ([]wireAttribute, *Error) {
	if len(attrs) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(attrs))
	out := make([]wireAttribute, 0, len(attrs))
	for i, a := range attrs {
		if a.name == "" {
			return nil, invalidRequest("attribute %d has an empty name", i)
		}
		if seen[a.name] {
			return nil, invalidRequest("attribute %q is given more than once", a.name)
		}
		seen[a.name] = true
		w := wireAttribute{Name: a.name, Type: a.typ}
		switch v := a.value.(type) {
		case string, bool:
			w.Value = v
		case []string, []bool:
			w.Value = v
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, invalidRequest("attribute %q is not a finite number", a.name)
			}
			w.Value = v
		case []float64:
			for _, f := range v {
				if math.IsNaN(f) || math.IsInf(f, 0) {
					return nil, invalidRequest("attribute %q has a value that is not a finite number", a.name)
				}
			}
			w.Value = v
		case time.Time:
			w.Value = formatTime(v)
		case []time.Time:
			s := make([]string, len(v))
			for j, t := range v {
				s[j] = formatTime(t)
			}
			w.Value = s
		default:
			return nil, invalidRequest("attribute %q was not built with an attribute constructor", a.name)
		}
		out = append(out, w)
	}
	return out, nil
}

// Infer builds the inferred form (design §3.2.1) from native values: a string
// becomes string, any Go integer or float (and json.Number) numeric, a bool
// bool, a time.Time datetime, and a non-empty slice or array whose items all
// have one of these kinds the array form of that type. A string is never
// taken for a datetime. Anything else, including nil, an empty slice, a mixed
// slice and a map, returns an error that wraps [ErrInvalidRequest]; use the
// typed constructors instead. The result is sorted by name in Unicode code
// point order.
func Infer(m map[string]any) (Attributes, error) {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names) // byte order of UTF-8 is code point order
	out := make(Attributes, 0, len(m))
	for _, name := range names {
		a, err := inferOne(name, m[name])
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func inferOne(name string, v any) (Attribute, *Error) {
	if name == "" {
		return Attribute{}, invalidRequest("attribute with an empty name")
	}
	if typ, single, ok := inferScalar(v); ok {
		return Attribute{name: name, typ: typ, value: single}, nil
	}
	rv := reflect.ValueOf(v)
	if v == nil || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return Attribute{}, invalidRequest("attribute %q: cannot infer a type from %s; use a typed attribute", name, describeKind(v))
	}
	if rv.Len() == 0 {
		return Attribute{}, invalidRequest("attribute %q: cannot infer a type from an empty list; use a typed attribute", name)
	}
	var typ AttributeType
	items := make([]any, rv.Len())
	for i := range rv.Len() {
		t, item, ok := inferScalar(rv.Index(i).Interface())
		if !ok {
			return Attribute{}, invalidRequest("attribute %q: item %d is %s; use a typed attribute", name, i, describeKind(rv.Index(i).Interface()))
		}
		if i > 0 && t != typ {
			return Attribute{}, invalidRequest("attribute %q mixes %s and %s items; use a typed attribute", name, typ, t)
		}
		typ, items[i] = t, item
	}
	a := Attribute{name: name, typ: typ, array: true}
	switch typ {
	case TypeString:
		s := make([]string, len(items))
		for i, x := range items {
			s[i] = x.(string)
		}
		a.value = s
	case TypeNumeric:
		s := make([]float64, len(items))
		for i, x := range items {
			s[i] = x.(float64)
		}
		a.value = s
	case TypeBool:
		s := make([]bool, len(items))
		for i, x := range items {
			s[i] = x.(bool)
		}
		a.value = s
	case TypeDatetime:
		s := make([]time.Time, len(items))
		for i, x := range items {
			s[i] = x.(time.Time)
		}
		a.value = s
	}
	return a, nil
}

// inferScalar maps one native value to its type and stored value.
func inferScalar(v any) (AttributeType, any, bool) {
	switch x := v.(type) {
	case nil:
		return "", nil, false
	case string:
		return TypeString, x, true
	case bool:
		return TypeBool, x, true
	case time.Time:
		return TypeDatetime, x, true
	case *time.Time:
		if x == nil {
			return "", nil, false
		}
		return TypeDatetime, *x, true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return "", nil, false
		}
		return TypeNumeric, f, true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return TypeNumeric, float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return TypeNumeric, float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return TypeNumeric, rv.Float(), true
	case reflect.String:
		return TypeString, rv.String(), true
	case reflect.Bool:
		return TypeBool, rv.Bool(), true
	}
	return "", nil, false
}

func describeKind(v any) string {
	if v == nil {
		return "null"
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Map:
		return "a map"
	case reflect.Struct:
		return "a struct"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Pointer:
		return "a pointer"
	}
	return "an unsupported " + reflect.TypeOf(v).String()
}
