// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// headerMatcher is one entry of exchange.request.headers: an exact value,
// {"absent": true} or {"pattern": "<regex>"}.
type headerMatcher struct {
	exact   *string
	absent  bool
	pattern *regexp.Regexp
	source  string
}

func (m *headerMatcher) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		m.exact = &s
		return nil
	}
	var obj struct {
		Absent  *bool   `json:"absent"`
		Pattern *string `json:"pattern"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return fmt.Errorf("header matcher: %w", err)
	}
	switch {
	case obj.Absent != nil && obj.Pattern == nil:
		if !*obj.Absent {
			return fmt.Errorf("header matcher: absent must be true")
		}
		m.absent = true
	case obj.Pattern != nil && obj.Absent == nil:
		// The schema says ECMAScript; the vectors use the common subset that
		// RE2 shares. The pattern must match the whole value.
		re, err := regexp.Compile(`^(?:` + *obj.Pattern + `)$`)
		if err != nil {
			return fmt.Errorf("header matcher: pattern %q: %w", *obj.Pattern, err)
		}
		m.pattern = re
		m.source = *obj.Pattern
	default:
		return fmt.Errorf("header matcher: need exactly one of absent or pattern")
	}
	return nil
}

// matchHeaders checks the listed headers. Names are case-insensitive; a
// header sent several times is compared as its values joined with ", ".
func matchHeaders(want map[string]headerMatcher, got http.Header) []string {
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	var problems []string
	for _, name := range names {
		m := want[name]
		values, present := got[http.CanonicalHeaderKey(name)]
		value := strings.Join(values, ", ")
		switch {
		case m.absent:
			if present {
				problems = append(problems, fmt.Sprintf("header %s: want absent, got %q", name, redactHeader(name, value)))
			}
		case !present:
			problems = append(problems, fmt.Sprintf("header %s: missing", name))
		case m.exact != nil:
			if value != *m.exact {
				problems = append(problems, fmt.Sprintf("header %s: want %q, got %q", name, redactHeader(name, *m.exact), redactHeader(name, value)))
			}
		case m.pattern != nil:
			if !m.pattern.MatchString(value) {
				problems = append(problems, fmt.Sprintf("header %s: %q does not match %q", name, redactHeader(name, value), m.source))
			}
		}
	}
	return problems
}

// redactHeader keeps credentials out of mismatch reports. The vectors only
// carry example tokens, but SDK bugs could send something else.
func redactHeader(name, value string) string {
	if !strings.EqualFold(name, "Authorization") {
		return value
	}
	scheme, cred, ok := strings.Cut(value, " ")
	if !ok {
		return fmt.Sprintf("<%d bytes>", len(value))
	}
	return fmt.Sprintf("%s <%d bytes>", scheme, len(cred))
}

// decodeJSON parses b keeping numbers as json.Number.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

// compareJSON compares parsed JSON values. Object key order is ignored,
// numbers are compared by value (4 equals 4.0), and array order matters
// unless the array's JSON Pointer is in unordered.
func compareJSON(want, got any, pointer string, unordered map[string]bool) []string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: want object, got %s", ptr(pointer), describe(got))}
		}
		keys := make([]string, 0, len(w)+len(g))
		seen := map[string]bool{}
		for k := range w {
			keys, seen[k] = append(keys, k), true
		}
		for k := range g {
			if !seen[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var problems []string
		for _, k := range keys {
			child := pointer + "/" + escapePointer(k)
			wv, inW := w[k]
			gv, inG := g[k]
			switch {
			case !inG:
				problems = append(problems, fmt.Sprintf("%s: missing", child))
			case !inW:
				problems = append(problems, fmt.Sprintf("%s: unexpected member", child))
			default:
				problems = append(problems, compareJSON(wv, gv, child, unordered)...)
			}
		}
		return problems
	case []any:
		g, ok := got.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: want array, got %s", ptr(pointer), describe(got))}
		}
		if len(w) != len(g) {
			return []string{fmt.Sprintf("%s: want %d items, got %d", ptr(pointer), len(w), len(g))}
		}
		if unordered[pointer] {
			return compareMultiset(w, g, pointer, unordered)
		}
		var problems []string
		for i := range w {
			problems = append(problems, compareJSON(w[i], g[i], pointer+"/"+strconv.Itoa(i), unordered)...)
		}
		return problems
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return []string{fmt.Sprintf("%s: want number %s, got %s", ptr(pointer), w, describe(got))}
		}
		wf, err1 := w.Float64()
		gf, err2 := g.Float64()
		if err1 != nil || err2 != nil || wf != gf {
			return []string{fmt.Sprintf("%s: want %s, got %s", ptr(pointer), w, g)}
		}
		return nil
	default: // string, bool, nil
		if want != got {
			return []string{fmt.Sprintf("%s: want %s, got %s", ptr(pointer), describe(want), describe(got))}
		}
		return nil
	}
}

// compareMultiset matches each wanted item with a distinct equal item.
func compareMultiset(want, got []any, pointer string, unordered map[string]bool) []string {
	used := make([]bool, len(got))
	for i, w := range want {
		found := false
		for j, g := range got {
			if !used[j] && len(compareJSON(w, g, pointer+"/"+strconv.Itoa(i), unordered)) == 0 {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return []string{fmt.Sprintf("%s: item %d has no equal item in the request (unordered)", ptr(pointer), i)}
		}
	}
	return nil
}

func ptr(p string) string {
	if p == "" {
		return "body"
	}
	return p
}

func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func describe(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(x)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return "number " + x.String()
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}
