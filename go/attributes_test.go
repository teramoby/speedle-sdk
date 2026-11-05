// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func wireJSON(t *testing.T, attrs Attributes) string {
	t.Helper()
	w, werr := attrs.toWire()
	if werr != nil {
		t.Fatal(werr)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAttributeWireForm(t *testing.T) {
	at := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)
	tokyo := time.FixedZone("JST", 9*3600)
	got := wireJSON(t, Attributes{
		String("s", "x"), Strings("ss", "a", "b"), Strings("empty"),
		Numeric("n", 4), Numerics("ns", 1.5, 2),
		Bool("b", false), Bools("bs", true, false),
		Datetime("d", at.In(tokyo)), Datetimes("ds", at, at.Add(1500*time.Millisecond)),
	})
	want := `[{"name":"s","type":"string","value":"x"},{"name":"ss","type":"string","value":["a","b"]},` +
		`{"name":"empty","type":"string","value":[]},{"name":"n","type":"numeric","value":4},` +
		`{"name":"ns","type":"numeric","value":[1.5,2]},{"name":"b","type":"bool","value":false},` +
		`{"name":"bs","type":"bool","value":[true,false]},{"name":"d","type":"datetime","value":"2026-11-05T03:00:00Z"},` +
		`{"name":"ds","type":"datetime","value":["2026-11-05T03:00:00Z","2026-11-05T03:00:01.5Z"]}]`
	if got != want {
		t.Errorf("wire form\n got %s\nwant %s", got, want)
	}
}

func TestAttributeAccessors(t *testing.T) {
	a := Strings("tags", "a", "b")
	if a.Name() != "tags" || a.Type() != TypeString || !a.IsArray() {
		t.Errorf("accessors: %q %q %v", a.Name(), a.Type(), a.IsArray())
	}
	v := a.Value().([]string)
	v[0] = "changed"
	if a.Value().([]string)[0] != "a" {
		t.Error("Value returned the internal slice")
	}
	src := []float64{1, 2}
	n := Numerics("n", src...)
	src[0] = 9
	if n.Value().([]float64)[0] != 1 {
		t.Error("constructor kept the caller's slice")
	}
	if b := Bool("b", true); b.IsArray() || b.Value() != true || b.Type() != TypeBool {
		t.Errorf("Bool: %+v", b)
	}
}

func TestAttributeChecks(t *testing.T) {
	cases := map[string]Attributes{
		"empty name":      {String("", "x")},
		"duplicate":       {String("a", "x"), Numeric("a", 1)},
		"NaN":             {Numeric("n", math.NaN())},
		"infinity":        {Numeric("n", math.Inf(1))},
		"NaN in array":    {Numerics("n", 1, math.Inf(-1))},
		"zero attribute":  {Attribute{}},
		"hand-built type": {Attribute{name: "x", typ: TypeString, value: 3}},
	}
	for name, attrs := range cases {
		if _, err := attrs.toWire(); err == nil || !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: got %v, want ErrInvalidRequest", name, err)
		}
	}
}

func TestInfer(t *testing.T) {
	at := time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)
	got, err := Infer(map[string]any{
		"vip":     true,
		"team":    "eng",
		"looks":   "2026-11-05T03:00:00Z",
		"count":   int64(3),
		"small":   uint8(7),
		"ratio":   float32(0.5),
		"num":     json.Number("12"),
		"when":    at,
		"tags":    []string{"a", "b"},
		"anyList": []any{1.0, 2},
		"times":   []time.Time{at},
		"flags":   [2]bool{true, false},
		"é":       "accent",
		"Z":       "upper",
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	types := map[string]AttributeType{}
	for _, a := range got {
		names = append(names, a.Name())
		types[a.Name()] = a.Type()
	}
	wantNames := []string{"Z", "anyList", "count", "flags", "looks", "num", "ratio", "small", "tags", "team", "times", "vip", "when", "é"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("order %v, want %v", names, wantNames)
	}
	wantTypes := map[string]AttributeType{
		"Z": TypeString, "anyList": TypeNumeric, "count": TypeNumeric, "flags": TypeBool, "looks": TypeString,
		"num": TypeNumeric, "ratio": TypeNumeric, "small": TypeNumeric, "tags": TypeString, "team": TypeString,
		"times": TypeDatetime, "vip": TypeBool, "when": TypeDatetime, "é": TypeString,
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("types %v, want %v", types, wantTypes)
	}
	if w := wireJSON(t, Attributes{got[1]}); w != `[{"name":"anyList","type":"numeric","value":[1,2]}]` {
		t.Errorf("anyList wire %s", w)
	}
}

func TestInferRejects(t *testing.T) {
	var nilTime *time.Time
	for name, v := range map[string]any{
		"null":       nil,
		"empty list": []string{},
		"mixed":      []any{"a", 1},
		"null item":  []any{"a", nil},
		"map":        map[string]any{"a": 1},
		"struct":     struct{}{},
		"nested":     []any{[]any{1}},
		"nil time":   nilTime,
	} {
		if _, err := Infer(map[string]any{"x": v}); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: got %v, want ErrInvalidRequest", name, err)
		}
	}
	if _, err := Infer(map[string]any{"": "x"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty name: got %v", err)
	}
	if got, err := Infer(nil); err != nil || len(got) != 0 {
		t.Errorf("nil map: %v %v", got, err)
	}
}
