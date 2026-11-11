// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"crypto/tls"
	"unicode"
)

// transportConfig is what an Auth contributes to the client.
type transportConfig struct {
	authorization string // full Authorization header value, or ""
	certificates  []tls.Certificate
	kind          authKind
}

type authKind int

const (
	authNone authKind = iota
	authBearer
	authMutualTLS
)

// Auth is how the client authenticates. Use [NoAuth], [BearerToken] or
// [MutualTLS]. The interface has an unexported method so that more
// implementations can be added later without breaking callers.
type Auth interface{ apply(*transportConfig) error }

// NoAuth sends no credential: self-hosted Speedle on a trusted network or
// loopback.
func NoAuth() Auth { return noAuth{} }

type noAuth struct{}

func (noAuth) apply(c *transportConfig) error { c.kind = authNone; return nil }
func (noAuth) String() string                 { return "NoAuth()" }

// BearerToken sends "Authorization: Bearer <token>": Speedle Cloud, or
// self-hosted Speedle behind an authenticating gateway. On Speedle Cloud,
// token is the account-level API key; the parameter name is unchanged. It
// requires an https endpoint. NewClient rejects an empty value and one with
// whitespace or control characters. The credential is never printed by fmt,
// hooks or errors.
func BearerToken(token string) Auth { return bearerAuth{token: token} }

type bearerAuth struct{ token string }

func (b bearerAuth) apply(c *transportConfig) error {
	if b.token == "" {
		return configError("bearer token is empty")
	}
	for _, r := range b.token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return configError("bearer token contains whitespace or control characters")
		}
	}
	c.kind = authBearer
	c.authorization = "Bearer " + b.token
	return nil
}

func (bearerAuth) String() string   { return "BearerToken(<redacted>)" }
func (bearerAuth) GoString() string { return "speedle.BearerToken(<redacted>)" }

// MutualTLS presents certificate on the TLS handshake, for self-hosted Speedle
// with --client-cert and --force-client-cert=true. Server verification stays
// on. It requires an https endpoint.
func MutualTLS(certificate tls.Certificate) Auth { return mtlsAuth{cert: certificate} }

type mtlsAuth struct{ cert tls.Certificate }

func (m mtlsAuth) apply(c *transportConfig) error {
	if len(m.cert.Certificate) == 0 || m.cert.PrivateKey == nil {
		return configError("mutual TLS certificate has no certificate chain or private key")
	}
	c.kind = authMutualTLS
	c.certificates = []tls.Certificate{m.cert}
	return nil
}

func (mtlsAuth) String() string   { return "MutualTLS(<certificate>)" }
func (mtlsAuth) GoString() string { return "speedle.MutualTLS(<certificate>)" }
