// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"unicode/utf8"
)

// exportFile is the Speedle Cloud lossless JSON export (ServiceExportSchema in
// teramoby/cloud.speedle.io packages/shared/src/service.ts), which is also the
// console's JSON import file (cloud.speedle.io#191). Field order follows the
// export so that rewriting the file keeps the diff small.
type exportFile struct {
	Service  exportService  `json:"service"`
	Policies []exportPolicy `json:"policies"`
}

type exportService struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type exportPolicy struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	SPDL      string          `json:"spdl"`
	Parsed    json.RawMessage `json:"parsed"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

// The parsed forms mirror Speedle+ api/pms Policy and RolePolicy (field order
// and JSON names), restricted to the fields that Cloud's ParsedAuthzPolicy and
// ParsedRolePolicy schemas allow. Required fields have no omitempty.
type parsedPermission struct {
	Resource           string   `json:"resource,omitempty"`
	ResourceExpression string   `json:"resourceExpression,omitempty"`
	Actions            []string `json:"actions"`
}

type parsedAuthz struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Effect      string             `json:"effect"`
	Permissions []parsedPermission `json:"permissions"`
	Principals  [][]string         `json:"principals"`
	Condition   string             `json:"condition,omitempty"`
}

type parsedRole struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Effect              string   `json:"effect"`
	Roles               []string `json:"roles"`
	Principals          []string `json:"principals"`
	Resources           []string `json:"resources,omitempty"`
	ResourceExpressions []string `json:"resourceExpressions,omitempty"`
	Condition           string   `json:"condition,omitempty"`
}

var (
	uuidV7      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	serviceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
)

// Cloud limits (packages/shared/src/limits.ts).
const (
	maxPolicies       = 200
	maxSPDLBytes      = 4 * 1024
	maxPolicyNameRune = 128
)

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

func readPolicies(path string) (*exportFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f exportFile
	if err := decodeStrict(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// validate checks the export schema rules that matter for an import. The
// console runs the full schema; this catches mistakes before a gamma run.
func (f *exportFile) validate() error {
	if !uuidV7.MatchString(f.Service.ID) || !serviceName.MatchString(f.Service.Name) {
		return errors.New("service: invalid id or name")
	}
	if f.Service.Revision < 0 || f.Service.CreatedAt < 0 || f.Service.UpdatedAt < 0 {
		return errors.New("service: negative revision or time")
	}
	if len(f.Policies) == 0 || len(f.Policies) > maxPolicies {
		return fmt.Errorf("policies: need 1 to %d, have %d", maxPolicies, len(f.Policies))
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for i, p := range f.Policies {
		where := fmt.Sprintf("policies[%d] %q", i, p.Name)
		switch {
		case !uuidV7.MatchString(p.ID) || ids[p.ID]:
			return fmt.Errorf("%s: invalid or duplicate id", where)
		case p.Kind != "authz" && p.Kind != "role":
			return fmt.Errorf("%s: kind must be authz or role", where)
		case p.Name == "" || utf8.RuneCountInString(p.Name) > maxPolicyNameRune:
			return fmt.Errorf("%s: name must have 1 to %d code points", where, maxPolicyNameRune)
		case names[p.Kind+"/"+p.Name]:
			return fmt.Errorf("%s: duplicate %s policy name", where, p.Kind)
		case p.SPDL == "" || len(p.SPDL) > maxSPDLBytes:
			return fmt.Errorf("%s: SPDL must have 1 to %d bytes", where, maxSPDLBytes)
		case p.CreatedAt < 0 || p.UpdatedAt < 0:
			return fmt.Errorf("%s: negative time", where)
		}
		ids[p.ID] = true
		names[p.Kind+"/"+p.Name] = true
		if _, err := p.decodeParsed(); err != nil {
			return fmt.Errorf("%s: parsed: %w", where, err)
		}
	}
	return nil
}

// decodeParsed returns the parsed form as *parsedAuthz or *parsedRole.
func (p *exportPolicy) decodeParsed() (any, error) {
	var v any = &parsedAuthz{}
	if p.Kind == "role" {
		v = &parsedRole{}
	}
	if err := decodeStrict(p.Parsed, v); err != nil {
		return nil, err
	}
	return v, nil
}

// marshalPlain is json.Marshal without HTML escaping, so that conditions such
// as "a <= 3 && b" stay readable in the files.
func marshalPlain(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
