// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
)

// runLoad creates the service and every policy in a self-hosted Speedle PMS with spctl,
// from the SPDL in policies.json, the same source the Cloud import reparses.
func runLoad(args []string) error {
	fs := newFlags("load")
	spctl := fs.String("spctl", "", "path to the spctl binary (required)")
	pms := fs.String("pms", "", "PMS base URL, for example http://127.0.0.1:16733/policy-mgmt/v1/ (required)")
	service := fs.String("service", defaultService, "service name to create")
	policies := fs.String("policies", "e2e/policies.json", "policy file")
	_ = fs.Parse(args)
	if *spctl == "" || *pms == "" {
		return errors.New("load: -spctl and -pms are required")
	}
	f, err := readPolicies(*policies)
	if err != nil {
		return err
	}
	run := func(what string, args ...string) error {
		cmd := exec.Command(*spctl, append([]string{"--pms-endpoint", *pms}, args...)...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("spctl %s: %w\n%s", what, err, out.String())
		}
		return nil
	}
	if err := run("create service", "create", "service", *service); err != nil {
		return err
	}
	for _, p := range f.Policies {
		sub := "policy"
		if p.Kind == "role" {
			sub = "rolepolicy"
		}
		if err := run("create "+sub+" "+p.Name, "create", sub, p.Name, "--pdl-command", p.SPDL, "--service-name", *service); err != nil {
			return err
		}
	}
	fmt.Printf("loaded %d policies into self-hosted Speedle service %q\n", len(f.Policies), *service)
	return nil
}

// storeFile is the part of the Speedle file store (api/pms PolicyStore) that
// holds the parsed policies.
type storeFile struct {
	Services []struct {
		Name         string            `json:"name"`
		Policies     []json.RawMessage `json:"policies"`
		RolePolicies []json.RawMessage `json:"rolePolicies"`
	} `json:"services"`
}

// runParsed compares the parsed form in policies.json with the policies that
// self-hosted Speedle PMS stored after load, or rewrites the file with them (-write).
func runParsed(args []string) error {
	fs := newFlags("parsed")
	storePath := fs.String("store", "", "self-hosted Speedle file store written by PMS (required)")
	service := fs.String("service", defaultService, "service name in the store")
	policies := fs.String("policies", "e2e/policies.json", "policy file")
	write := fs.Bool("write", false, "rewrite the parsed forms in the policy file")
	_ = fs.Parse(args)
	if *storePath == "" {
		return errors.New("parsed: -store is required")
	}
	f, err := readPolicies(*policies)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*storePath)
	if err != nil {
		return err
	}
	var store storeFile
	if err := json.Unmarshal(data, &store); err != nil {
		return fmt.Errorf("%s: %w", *storePath, err)
	}
	stored := map[string]any{}
	found := false
	for _, s := range store.Services {
		if s.Name != *service {
			continue
		}
		found = true
		for kind, items := range map[string][]json.RawMessage{"authz": s.Policies, "role": s.RolePolicies} {
			for _, raw := range items {
				var v any = &parsedAuthz{}
				if kind == "role" {
					v = &parsedRole{}
				}
				// Not strict: the store also has metadata, which Cloud does not export.
				if err := json.Unmarshal(raw, v); err != nil {
					return fmt.Errorf("%s: %w", *storePath, err)
				}
				name := reflect.ValueOf(v).Elem().FieldByName("Name").String()
				reflect.ValueOf(v).Elem().FieldByName("ID").SetString("")
				stored[kind+"/"+name] = v
			}
		}
	}
	if !found {
		return fmt.Errorf("%s: no service %q", *storePath, *service)
	}
	if len(stored) != len(f.Policies) {
		return fmt.Errorf("store has %d policies, %s has %d", len(stored), *policies, len(f.Policies))
	}
	var diffs []string
	for i := range f.Policies {
		p := &f.Policies[i]
		want, ok := stored[p.Kind+"/"+p.Name]
		if !ok {
			return fmt.Errorf("%s policy %q is not in the store", p.Kind, p.Name)
		}
		got, err := p.decodeParsed()
		if err != nil {
			return err
		}
		if reflect.DeepEqual(got, want) {
			continue
		}
		if !*write {
			w, _ := json.Marshal(want)
			g, _ := json.Marshal(got)
			diffs = append(diffs, fmt.Sprintf("%s %q:\n  file:      %s\n  Speedle:   %s", p.Kind, p.Name, g, w))
			continue
		}
		if p.Parsed, err = marshalPlain(want); err != nil {
			return err
		}
	}
	if *write {
		if err := writeJSON(*policies, f); err != nil {
			return err
		}
		fmt.Printf("wrote the self-hosted Speedle parsed form of %d policies to %s\n", len(f.Policies), *policies)
		return nil
	}
	if len(diffs) > 0 {
		return fmt.Errorf("parsed forms differ from self-hosted Speedle (rerun with -write to update):\n%s", strings.Join(diffs, "\n"))
	}
	fmt.Printf("parsed forms of all %d policies match self-hosted Speedle\n", len(f.Policies))
	return nil
}
