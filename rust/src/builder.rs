// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

use std::net::IpAddr;
use std::sync::Arc;
use std::time::Duration;

use reqwest::header::HeaderValue;

use crate::breaker::Breaker;
use crate::client::{Client, Inner};
use crate::clock::{Clock, DefaultRandom, Random, SystemClock};
use crate::types::AuthKind;
use crate::{Auth, Backend, CircuitBreaker, Error, FailureMode, Hooks, RetryPolicy};

/// The path appended to the endpoint's base URL.
pub const IS_ALLOWED_PATH: &str = "/authz-check/v1/is-allowed";

/// Builds a [`Client`]. Created by [`Client::builder`].
pub struct ClientBuilder {
    pub(crate) backend: Backend,
    pub(crate) endpoint: String,
    pub(crate) auth: Auth,
    pub(crate) service_name: String,
    pub(crate) retry: RetryPolicy,
    pub(crate) failure_mode: FailureMode,
    pub(crate) breaker: Option<CircuitBreaker>,
    pub(crate) hooks: Option<Arc<dyn Hooks>>,
    pub(crate) roots: Vec<Vec<u8>>,
    pub(crate) allow_insecure_http: bool,
    pub(crate) user_agent_suffix: Option<String>,
}

impl ClientBuilder {
    pub(crate) fn new(backend: Backend, endpoint: &str, auth: Auth, service_name: String) -> Self {
        ClientBuilder {
            backend,
            endpoint: endpoint.to_owned(),
            auth,
            service_name,
            retry: RetryPolicy::default(),
            failure_mode: FailureMode::Error,
            breaker: None,
            hooks: None,
            roots: Vec::new(),
            allow_insecure_http: false,
            user_agent_suffix: None,
        }
    }

    /// The retry policy; default [`RetryPolicy::default`].
    pub fn retry(mut self, policy: RetryPolicy) -> Self {
        self.retry = policy;
        self
    }

    pub fn failure_mode(mut self, mode: FailureMode) -> Self {
        self.failure_mode = mode;
        self
    }

    /// Turns the circuit breaker on.
    pub fn circuit_breaker(mut self, breaker: CircuitBreaker) -> Self {
        self.breaker = Some(breaker);
        self
    }

    pub fn hooks(mut self, hooks: impl Hooks) -> Self {
        self.hooks = Some(Arc::new(hooks));
        self
    }

    /// Trusts one or more extra CA certificates (PEM) in addition to the
    /// platform's root store. Certificate verification cannot be disabled.
    pub fn add_root_certificate_pem(mut self, pem: &[u8]) -> Self {
        self.roots.push(pem.to_vec());
        self
    }

    /// Accepts an `http://` endpoint on a non-loopback host for
    /// [`Backend::SpeedlePlus`] with [`Auth::none`]. Default false.
    pub fn allow_insecure_http(mut self, allow: bool) -> Self {
        self.allow_insecure_http = allow;
        self
    }

    /// Appended to the `User-Agent` after a space.
    pub fn user_agent_suffix(mut self, suffix: impl Into<String>) -> Self {
        self.user_agent_suffix = Some(suffix.into());
        self
    }

    /// Validates the options and builds the client. Errors have kind
    /// [`ErrorKind::Configuration`](crate::ErrorKind::Configuration).
    pub fn build(self) -> Result<Client, Error> {
        self.build_with(Arc::new(SystemClock), Arc::new(DefaultRandom::new()))
    }

    pub(crate) fn build_with(self, clock: Arc<dyn Clock>, random: Arc<dyn Random>) -> Result<Client, Error> {
        let (authorization, identity, auth_none) = match self.auth.0 {
            AuthKind::None => (None, None, true),
            AuthKind::Bearer(token) => {
                if token.is_empty() {
                    return Err(Error::configuration("the bearer token is empty"));
                }
                if token.chars().any(|c| c.is_whitespace() || c.is_control()) {
                    return Err(Error::configuration("the bearer token contains whitespace or control characters"));
                }
                let mut value = HeaderValue::from_str(&format!("Bearer {token}"))
                    .map_err(|_| Error::configuration("the bearer token is not a valid header value"))?;
                value.set_sensitive(true);
                (Some(value), None, false)
            }
            AuthKind::MutualTls(identity) => (None, Some(identity), false),
        };
        let (url, https) = check_endpoint(&self.endpoint, self.backend, auth_none, self.allow_insecure_http)?;
        if self.service_name.is_empty() {
            return Err(Error::configuration("the service name is required"));
        }
        let r = &self.retry;
        if r.max_attempts == 0 {
            return Err(Error::configuration("max_attempts must be at least 1"));
        }
        if r.attempt_timeout.is_zero() || r.deadline.is_zero() {
            return Err(Error::configuration("attempt_timeout and deadline must be positive"));
        }
        if let Some(b) = &self.breaker {
            if b.failure_threshold == 0 {
                return Err(Error::configuration("failure_threshold must be at least 1"));
            }
        }
        let mut user_agent = format!(
            "speedle-sdk-rust/{} (rust/{}; {})",
            env!("CARGO_PKG_VERSION"),
            env!("SPEEDLE_RUSTC_VERSION"),
            std::env::consts::OS
        );
        if let Some(suffix) = &self.user_agent_suffix {
            if suffix.chars().any(char::is_control) {
                return Err(Error::configuration("the User-Agent suffix contains control characters"));
            }
            user_agent.push(' ');
            user_agent.push_str(suffix);
        }

        let mut http = reqwest::Client::builder()
            .use_rustls_tls()
            .tls_built_in_native_certs(true)
            .min_tls_version(reqwest::tls::Version::TLS_1_2)
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(user_agent.clone())
            .pool_idle_timeout(Duration::from_secs(90))
            .https_only(https);
        for pem in &self.roots {
            let certs = reqwest::Certificate::from_pem_bundle(pem)
                .map_err(|_| Error::configuration("a root certificate is not valid PEM"))?;
            if certs.is_empty() {
                return Err(Error::configuration("a root certificate PEM holds no certificate"));
            }
            for cert in certs {
                http = http.add_root_certificate(cert);
            }
        }
        if let Some(identity) = identity {
            http = http.identity(identity);
        }
        let http = http.build().map_err(|e| Error::configuration(format!("cannot build the HTTP client: {e}")))?;

        Ok(Client(Arc::new(Inner {
            backend: self.backend,
            url,
            service_name: self.service_name,
            authorization,
            retry: self.retry,
            failure_mode: self.failure_mode,
            breaker: self.breaker.as_ref().map(Breaker::new),
            hooks: self.hooks,
            http,
            clock,
            random,
        })))
    }
}

/// Validates the endpoint (design §3.1, §6.1) and returns the request URL
/// and whether it is https.
fn check_endpoint(
    endpoint: &str,
    backend: Backend,
    auth_none: bool,
    allow_insecure: bool,
) -> Result<(String, bool), Error> {
    let mut url = reqwest::Url::parse(endpoint).map_err(|_| Error::configuration("the endpoint is not a valid URL"))?;
    if url.query().is_some() || url.fragment().is_some() {
        return Err(Error::configuration("the endpoint must not have a query string or fragment"));
    }
    if !url.username().is_empty() || url.password().is_some() {
        return Err(Error::configuration("the endpoint must not contain user info"));
    }
    let host = url.host_str().unwrap_or_default().to_owned();
    if host.is_empty() {
        return Err(Error::configuration("the endpoint must have a host"));
    }
    let https = match url.scheme() {
        "https" => true,
        "http" => {
            if backend != Backend::SpeedlePlus {
                return Err(Error::configuration("the endpoint must use https for Speedle Cloud"));
            }
            if !auth_none {
                return Err(Error::configuration("the endpoint must use https with bearer or mutual TLS auth"));
            }
            if !is_loopback(&host) && !allow_insecure {
                return Err(Error::configuration(
                    "http is accepted only for loopback hosts unless allow_insecure_http is set",
                ));
            }
            false
        }
        _ => return Err(Error::configuration("the endpoint scheme must be https")),
    };
    let path = format!("{}{IS_ALLOWED_PATH}", url.path().trim_end_matches('/'));
    url.set_path(&path);
    Ok((url.to_string(), https))
}

fn is_loopback(host: &str) -> bool {
    if host.eq_ignore_ascii_case("localhost") {
        return true;
    }
    host.trim_start_matches('[').trim_end_matches(']').parse::<IpAddr>().is_ok_and(|ip| ip.is_loopback())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ErrorKind;

    fn build(backend: Backend, endpoint: &str, auth: Auth) -> Result<Client, Error> {
        Client::builder(backend, endpoint, auth, "books").build()
    }

    #[test]
    fn rejects_bad_configuration() {
        use Backend::*;
        let cases: Vec<(&str, Result<Client, Error>)> = vec![
            ("empty token", build(SpeedleCloud, "https://h", Auth::bearer(""))),
            ("token with space", build(SpeedleCloud, "https://h", Auth::bearer("a b"))),
            ("token with newline", build(SpeedleCloud, "https://h", Auth::bearer("a\n"))),
            ("not a URL", build(SpeedleCloud, "cloud.speedle.io", Auth::bearer("t"))),
            ("query", build(SpeedleCloud, "https://h/?a=1", Auth::bearer("t"))),
            ("empty query", build(SpeedleCloud, "https://h/?", Auth::bearer("t"))),
            ("fragment", build(SpeedleCloud, "https://h/#x", Auth::bearer("t"))),
            ("user info", build(SpeedleCloud, "https://u:p@h/", Auth::bearer("t"))),
            ("ftp", build(SpeedleCloud, "ftp://h/", Auth::bearer("t"))),
            ("http cloud", build(SpeedleCloud, "http://127.0.0.1:1", Auth::none())),
            ("http bearer", build(SpeedlePlus, "http://127.0.0.1:1", Auth::bearer("t"))),
            ("http remote", build(SpeedlePlus, "http://ads.internal:6734", Auth::none())),
            ("no service", Client::builder(SpeedleCloud, "https://h", Auth::bearer("t"), "").build()),
            (
                "zero attempts",
                Client::builder(SpeedleCloud, "https://h", Auth::bearer("t"), "s")
                    .retry(RetryPolicy { max_attempts: 0, ..Default::default() })
                    .build(),
            ),
            (
                "zero deadline",
                Client::builder(SpeedleCloud, "https://h", Auth::bearer("t"), "s")
                    .retry(RetryPolicy { deadline: Duration::ZERO, ..Default::default() })
                    .build(),
            ),
            (
                "zero threshold",
                Client::builder(SpeedleCloud, "https://h", Auth::bearer("t"), "s")
                    .circuit_breaker(CircuitBreaker { failure_threshold: 0, ..Default::default() })
                    .build(),
            ),
            (
                "bad root",
                Client::builder(SpeedleCloud, "https://h", Auth::bearer("t"), "s")
                    .add_root_certificate_pem(b"nope")
                    .build(),
            ),
            (
                "control in suffix",
                Client::builder(SpeedleCloud, "https://h", Auth::bearer("t"), "s")
                    .user_agent_suffix("a\r\nX: 1")
                    .build(),
            ),
            (
                "bearer over http with allow_insecure_http",
                Client::builder(SpeedlePlus, "http://h", Auth::bearer("t"), "s").allow_insecure_http(true).build(),
            ),
        ];
        for (name, result) in cases {
            let err = result.err().unwrap_or_else(|| panic!("{name}: accepted"));
            assert_eq!(err.kind(), ErrorKind::Configuration, "{name}");
            assert!(!err.to_string().contains("a b"), "{name}: token in message");
        }
        assert_eq!(Auth::mutual_tls(b"x", b"y").err().map(|e| e.kind()), Some(ErrorKind::Configuration));
    }

    #[test]
    fn accepts_endpoints() {
        use Backend::*;
        let cases = [
            (
                SpeedleCloud,
                "https://cloud.speedle.io",
                Auth::bearer("t"),
                false,
                "https://cloud.speedle.io/authz-check/v1/is-allowed",
            ),
            (
                SpeedleCloud,
                "https://gw.example/prefix/",
                Auth::bearer("t"),
                false,
                "https://gw.example/prefix/authz-check/v1/is-allowed",
            ),
            (
                SpeedlePlus,
                "http://127.0.0.1:6734",
                Auth::none(),
                false,
                "http://127.0.0.1:6734/authz-check/v1/is-allowed",
            ),
            (
                SpeedlePlus,
                "http://localhost:6734",
                Auth::none(),
                false,
                "http://localhost:6734/authz-check/v1/is-allowed",
            ),
            (SpeedlePlus, "http://[::1]:6734", Auth::none(), false, "http://[::1]:6734/authz-check/v1/is-allowed"),
            (
                SpeedlePlus,
                "http://ads.internal:6734",
                Auth::none(),
                true,
                "http://ads.internal:6734/authz-check/v1/is-allowed",
            ),
            (
                SpeedlePlus,
                "https://gw.internal",
                Auth::bearer("t"),
                false,
                "https://gw.internal/authz-check/v1/is-allowed",
            ),
        ];
        for (backend, endpoint, auth, insecure, want) in cases {
            let c = Client::builder(backend, endpoint, auth, "s").allow_insecure_http(insecure).build().unwrap();
            assert_eq!(c.0.url, want, "{endpoint}");
        }
    }

    #[test]
    fn auth_debug_is_redacted() {
        let s = format!("{:?}", Auth::bearer("spdl_secret"));
        assert!(!s.contains("spdl_secret"), "{s}");
    }
}
