// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

// Command e2e is the Speedle SDK end-to-end runner for Go. It runs every
// vector in e2e/vectors.json with Client.Check and fails on any mismatch. The
// contract is in e2e/README.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	speedle "github.com/teramoby/speedle-sdk/go"
)

type vector struct {
	Name    string `json:"name"`
	Request struct {
		Subject struct {
			Principals []struct {
				Type string `json:"type"`
				Name string `json:"name"`
				IDD  string `json:"idd"`
			} `json:"principals"`
		} `json:"subject"`
		Resource   string `json:"resource"`
		Action     string `json:"action"`
		Attributes []struct {
			Name  string          `json:"name"`
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"attributes"`
	} `json:"request"`
	Expect *struct {
		Allowed      *bool   `json:"allowed"`
		Reason       *int    `json:"reason"`
		ErrorMessage *string `json:"errorMessage"`
	} `json:"expect"`
}

func env(name string, required bool) string {
	value := os.Getenv(name)
	if value == "" && required {
		usage(name + " is required")
	}
	return value
}

func usage(message string) {
	fmt.Fprintln(os.Stderr, "go e2e:", message)
	os.Exit(2)
}

func main() {
	endpoint := env("SPEEDLE_E2E_ENDPOINT", true)
	service := env("SPEEDLE_E2E_SERVICE", true)
	backend := env("SPEEDLE_E2E_BACKEND", false)
	token := env("SPEEDLE_E2E_TOKEN", false)
	if backend == "" {
		backend = string(speedle.BackendSpeedleCloud)
	}
	if backend != string(speedle.BackendSpeedleCloud) && backend != string(speedle.BackendSpeedle) {
		usage("SPEEDLE_E2E_BACKEND must be speedle-cloud or speedle")
	}
	if backend == string(speedle.BackendSpeedleCloud) && token == "" {
		usage("SPEEDLE_E2E_TOKEN is required for speedle-cloud")
	}
	if len(os.Args) != 2 {
		usage("usage: e2e VECTORS_JSON")
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		usage(err.Error())
	}
	var file struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		usage(err.Error())
	}
	if len(file.Vectors) == 0 {
		usage(os.Args[1] + " has no vectors")
	}

	auth := speedle.NoAuth()
	if token != "" {
		auth = speedle.BearerToken(token)
	}
	client, err := speedle.NewClient(speedle.Config{
		Backend:         speedle.Backend(backend),
		Endpoint:        endpoint,
		Auth:            auth,
		ServiceName:     service,
		UserAgentSuffix: "speedle-sdk-e2e",
	})
	if err != nil {
		usage(err.Error())
	}
	defer client.Close()

	failed := 0
	for _, v := range file.Vectors {
		want := "no expectation"
		switch e := v.Expect; {
		case e != nil && (e.Allowed == nil || e.Reason == nil):
			want = "invalid expectation"
		case e != nil:
			want = format(*e.Allowed, *e.Reason, e.ErrorMessage)
		}
		got, err := run(client, &v)
		if err != nil {
			got = err.Error()
		}
		if want == got {
			fmt.Println("PASS", v.Name)
		} else {
			failed++
			fmt.Printf("FAIL %s: want %s, got %s\n", v.Name, want, got)
		}
	}
	fmt.Printf("go: %d passed, %d failed against %s\n", len(file.Vectors)-failed, failed, endpoint)
	if failed > 0 {
		os.Exit(1)
	}
}

func run(client *speedle.Client, v *vector) (string, error) {
	var subject speedle.Subject
	for _, p := range v.Request.Subject.Principals {
		subject = append(subject, speedle.Principal{Type: p.Type, Name: p.Name, IDD: p.IDD})
	}
	var attrs speedle.Attributes
	for _, a := range v.Request.Attributes {
		attr, err := attribute(a.Name, a.Type, a.Value)
		if err != nil {
			return "", err
		}
		attrs = append(attrs, attr)
	}
	d, err := client.Check(context.Background(), subject, v.Request.Action, v.Request.Resource, attrs)
	if err != nil {
		return "", err
	}
	var message *string
	if d.ErrorMessage != "" {
		message = &d.ErrorMessage
	}
	return format(d.Allowed, int(d.Reason), message), nil
}

// attribute builds the typed attribute; a JSON array selects the array form.
func attribute(name, kind string, raw json.RawMessage) (speedle.Attribute, error) {
	array := len(raw) > 0 && raw[0] == '['
	switch kind {
	case "string":
		var v []string
		if err := decode(raw, array, &v); err != nil {
			return speedle.Attribute{}, err
		}
		if array {
			return speedle.Strings(name, v...), nil
		}
		return speedle.String(name, v[0]), nil
	case "numeric":
		var v []float64
		if err := decode(raw, array, &v); err != nil {
			return speedle.Attribute{}, err
		}
		if array {
			return speedle.Numerics(name, v...), nil
		}
		return speedle.Numeric(name, v[0]), nil
	case "bool":
		var v []bool
		if err := decode(raw, array, &v); err != nil {
			return speedle.Attribute{}, err
		}
		if array {
			return speedle.Bools(name, v...), nil
		}
		return speedle.Bool(name, v[0]), nil
	case "datetime":
		var s []string
		if err := decode(raw, array, &s); err != nil {
			return speedle.Attribute{}, err
		}
		v := make([]time.Time, len(s))
		for i := range s {
			t, err := time.Parse(time.RFC3339Nano, s[i])
			if err != nil {
				return speedle.Attribute{}, err
			}
			v[i] = t
		}
		if array {
			return speedle.Datetimes(name, v...), nil
		}
		return speedle.Datetime(name, v[0]), nil
	}
	return speedle.Attribute{}, errors.New("unknown attribute type " + kind)
}

// decode reads a JSON value or array into a slice.
func decode[T any](raw json.RawMessage, array bool, out *[]T) error {
	if array {
		return json.Unmarshal(raw, out)
	}
	var one T
	if err := json.Unmarshal(raw, &one); err != nil {
		return err
	}
	*out = []T{one}
	return nil
}

func format(allowed bool, reason int, errorMessage *string) string {
	s := "{allowed: " + strconv.FormatBool(allowed) + ", reason: " + strconv.Itoa(reason)
	if errorMessage == nil {
		return s + "}"
	}
	m, _ := json.Marshal(*errorMessage)
	return s + ", errorMessage: " + string(m) + "}"
}
