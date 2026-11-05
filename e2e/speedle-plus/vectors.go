// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// vectorFile is e2e/vectors.json.
type vectorFile struct {
	Description string   `json:"description"`
	SpeedlePlus string   `json:"speedlePlus"`
	Vectors     []vector `json:"vectors"`
}

type vector struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Request     json.RawMessage `json:"request"`
	Expect      *decision       `json:"expect,omitempty"`
}

// decision is the Speedle+ IsAllowedResponse body.
type decision struct {
	Allowed      bool   `json:"allowed"`
	Reason       int    `json:"reason"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// UnmarshalJSON requires allowed and reason, so that an expectation with a
// missing field cannot match a deny by its zero value.
func (d *decision) UnmarshalJSON(data []byte) error {
	var raw struct {
		Allowed      *bool   `json:"allowed"`
		Reason       *int    `json:"reason"`
		ErrorMessage *string `json:"errorMessage"`
	}
	if err := decodeStrict(data, &raw); err != nil {
		return err
	}
	if raw.Allowed == nil || raw.Reason == nil {
		return errors.New("expect needs allowed and reason")
	}
	*d = decision{Allowed: *raw.Allowed, Reason: *raw.Reason}
	if raw.ErrorMessage != nil {
		d.ErrorMessage = *raw.ErrorMessage
	}
	return nil
}

// vectorRequest is a Speedle+ JsonContext without serviceName, which the
// runners take from SPEEDLE_E2E_SERVICE.
type vectorRequest struct {
	Subject struct {
		Principals []principal `json:"principals"`
	} `json:"subject"`
	Resource   string      `json:"resource"`
	Action     string      `json:"action"`
	Attributes []attribute `json:"attributes,omitempty"`
}

type principal struct {
	Type string `json:"type"`
	Name string `json:"name"`
	IDD  string `json:"idd,omitempty"`
}

type attribute struct {
	Name  string          `json:"name"`
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

// wireRequest is the body sent to ADS, in the SDK field order (design §3.2).
type wireRequest struct {
	Subject struct {
		Principals []principal `json:"principals"`
	} `json:"subject"`
	ServiceName string      `json:"serviceName"`
	Resource    string      `json:"resource"`
	Action      string      `json:"action"`
	Attributes  []attribute `json:"attributes,omitempty"`
}

func readVectors(path string) (*vectorFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f vectorFile
	if err := decodeStrict(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Vectors) == 0 {
		return nil, fmt.Errorf("%s: no vectors", path)
	}
	names := map[string]bool{}
	for i, v := range f.Vectors {
		if v.Name == "" || names[v.Name] {
			return nil, fmt.Errorf("%s: vectors[%d]: empty or duplicate name %q", path, i, v.Name)
		}
		names[v.Name] = true
		if _, err := v.decodeRequest(); err != nil {
			return nil, fmt.Errorf("%s: vector %q: %w", path, v.Name, err)
		}
	}
	return &f, nil
}

// decodeRequest checks the request against the rules every SDK enforces
// (design §3.2.1), so that each SDK sends exactly this body.
func (v *vector) decodeRequest() (*vectorRequest, error) {
	var r vectorRequest
	if err := decodeStrict(v.Request, &r); err != nil {
		return nil, err
	}
	if len(r.Subject.Principals) == 0 || r.Resource == "" || r.Action == "" {
		return nil, errors.New("request needs principals, resource and action")
	}
	for _, p := range r.Subject.Principals {
		if p.Type == "" || p.Name == "" {
			return nil, errors.New("every principal needs type and name")
		}
	}
	seen := map[string]bool{}
	for _, a := range r.Attributes {
		if a.Name == "" || seen[a.Name] {
			return nil, fmt.Errorf("empty or duplicate attribute name %q", a.Name)
		}
		seen[a.Name] = true
		if err := checkValue(a); err != nil {
			return nil, fmt.Errorf("attribute %q: %w", a.Name, err)
		}
	}
	return &r, nil
}

func checkValue(a attribute) error {
	var items []json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(a.Value), []byte("[")) {
		if err := json.Unmarshal(a.Value, &items); err != nil {
			return err
		}
	} else {
		items = []json.RawMessage{a.Value}
	}
	for _, item := range items {
		var v any
		if err := json.Unmarshal(item, &v); err != nil {
			return err
		}
		ok := false
		switch a.Type {
		case "string":
			_, ok = v.(string)
		case "numeric":
			_, ok = v.(float64)
		case "bool":
			_, ok = v.(bool)
		case "datetime":
			// The SDKs take a time value and write RFC 3339 in UTC (design
			// §3.2.1). Vectors use that form, with at most millisecond
			// precision because a JavaScript Date has no finer resolution.
			s, isString := v.(string)
			t, err := time.Parse(time.RFC3339Nano, s)
			ok = isString && err == nil && t.UTC().Format(time.RFC3339Nano) == s &&
				t.Nanosecond()%int(time.Millisecond) == 0
		default:
			return fmt.Errorf("type %q is not string, numeric, bool or datetime", a.Type)
		}
		if !ok {
			return fmt.Errorf("value %s is not a canonical %s", item, a.Type)
		}
	}
	return nil
}

// runVectors sends every vector to a Speedle+ REST ADS and checks the decision,
// or records it as the expectation (-write).
func runVectors(args []string) error {
	fs := newFlags("vectors")
	endpoint := fs.String("endpoint", "", "ADS base URL, for example http://127.0.0.1:16734 (required)")
	service := fs.String("service", defaultService, "serviceName to send")
	path := fs.String("vectors", "e2e/vectors.json", "vector file")
	write := fs.Bool("write", false, "record the decisions as the expectations")
	commit := fs.String("commit", "", "Speedle+ commit the ADS was built from, recorded with -write")
	_ = fs.Parse(args)
	if *endpoint == "" {
		return errors.New("vectors: -endpoint is required")
	}
	f, err := readVectors(*path)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	url := strings.TrimRight(*endpoint, "/") + "/authz-check/v1/is-allowed"
	failed := 0
	for i := range f.Vectors {
		v := &f.Vectors[i]
		r, _ := v.decodeRequest()
		var body wireRequest
		body.Subject = r.Subject
		body.ServiceName = *service
		body.Resource, body.Action, body.Attributes = r.Resource, r.Action, r.Attributes
		got, err := post(client, url, body)
		switch {
		case err != nil:
			failed++
			fmt.Printf("FAIL %s: %v\n", v.Name, err)
		case *write:
			v.Expect = got
			fmt.Printf("RECORD %s: %s\n", v.Name, mustJSON(got))
		case v.Expect == nil:
			failed++
			fmt.Printf("FAIL %s: no expectation recorded\n", v.Name)
		case *v.Expect != *got:
			failed++
			fmt.Printf("FAIL %s: want %s, got %s\n", v.Name, mustJSON(v.Expect), mustJSON(got))
		default:
			fmt.Printf("PASS %s\n", v.Name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d vectors failed against Speedle+", failed, len(f.Vectors))
	}
	if *write {
		if *commit == "" {
			return errors.New("vectors: -write needs -commit")
		}
		f.SpeedlePlus = *commit
		if err := writeJSON(*path, f); err != nil {
			return err
		}
		fmt.Printf("recorded %d decisions in %s\n", len(f.Vectors), *path)
		return nil
	}
	fmt.Printf("all %d vectors match Speedle+\n", len(f.Vectors))
	return nil
}

func post(client *http.Client, url string, body wireRequest) (*decision, error) {
	data, err := marshalPlain(body)
	if err != nil {
		return nil, err
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var d struct {
		Allowed      *bool   `json:"allowed"`
		Reason       *int    `json:"reason"`
		ErrorMessage *string `json:"errorMessage"`
	}
	if err := json.Unmarshal(raw, &d); err != nil || d.Allowed == nil || d.Reason == nil {
		return nil, fmt.Errorf("unexpected body: %s", bytes.TrimSpace(raw))
	}
	got := &decision{Allowed: *d.Allowed, Reason: *d.Reason}
	if d.ErrorMessage != nil {
		got.ErrorMessage = *d.ErrorMessage
	}
	return got, nil
}

func mustJSON(v any) string {
	b, _ := marshalPlain(v)
	return string(b)
}
