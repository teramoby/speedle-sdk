// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

use std::fmt;
use std::time::Duration;

use crate::{Error, ErrorKind};

/// Selects the auth rules and the error body interpretation. The request
/// format is the same for both.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Backend {
    /// Speedle Cloud (`https://cloud.speedle.io`).
    SpeedleCloud,
    /// A self-hosted Speedle ADS.
    Speedle,
}

/// One subject principal.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Principal {
    /// Serialized as `"type"`; an empty value is sent as `"user"`.
    /// Speedle knows `user`, `group`, `role` and `entity`.
    pub kind: String,
    pub name: String,
    /// Sent only when set.
    pub idd: Option<String>,
}

impl Principal {
    pub const USER: &'static str = "user";
    pub const GROUP: &'static str = "group";
    pub const ROLE: &'static str = "role";
    pub const ENTITY: &'static str = "entity";

    /// A principal of the given type and name.
    pub fn new(kind: impl Into<String>, name: impl Into<String>) -> Self {
        Principal { kind: kind.into(), name: name.into(), idd: None }
    }
}

/// The principals of a call. It must not be empty.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Subject(Vec<Principal>);

impl Subject {
    /// One user principal.
    pub fn user(name: impl Into<String>) -> Self {
        Subject(vec![Principal::new(Principal::USER, name)])
    }

    pub fn of(principals: impl IntoIterator<Item = Principal>) -> Self {
        Subject(principals.into_iter().collect())
    }

    pub fn principals(&self) -> &[Principal] {
        &self.0
    }
}

impl From<&str> for Subject {
    fn from(name: &str) -> Self {
        Subject::user(name)
    }
}

/// Per-call options.
#[derive(Clone, Debug, Default)]
pub struct CallOptions {
    /// Overrides the client's service name for this call. An empty value
    /// fails the call with `InvalidRequest`.
    pub service_name: Option<String>,
}

/// How the client authenticates.
pub struct Auth(pub(crate) AuthKind);

pub(crate) enum AuthKind {
    None,
    Bearer(String),
    MutualTls(reqwest::Identity),
}

impl Auth {
    /// Sends no credential: self-hosted Speedle on a trusted network or
    /// loopback.
    pub fn none() -> Self {
        Auth(AuthKind::None)
    }

    /// Sends `Authorization: Bearer <token>`: Speedle Cloud, or
    /// self-hosted Speedle behind an authenticating gateway. Requires `https`.
    /// `build` rejects an empty token and one with whitespace or control
    /// characters. The token is never printed by `Debug`, hooks or errors.
    pub fn bearer(token: impl Into<String>) -> Self {
        Auth(AuthKind::Bearer(token.into()))
    }

    /// Presents a client certificate (PEM certificate chain and PEM private
    /// key) on the TLS handshake, for self-hosted Speedle with `--client-cert`
    /// and `--force-client-cert=true`. Server verification stays on. Requires
    /// `https`.
    pub fn mutual_tls(cert_pem: &[u8], key_pem: &[u8]) -> Result<Self, Error> {
        let mut pem = cert_pem.to_vec();
        if !pem.ends_with(b"\n") {
            pem.push(b'\n');
        }
        pem.extend_from_slice(key_pem);
        reqwest::Identity::from_pem(&pem)
            .map(|id| Auth(AuthKind::MutualTls(id)))
            .map_err(|_| Error::configuration("the mutual TLS certificate or key is not valid PEM"))
    }
}

impl fmt::Debug for Auth {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self.0 {
            AuthKind::None => f.write_str("Auth::none()"),
            AuthKind::Bearer(_) => f.write_str("Auth::bearer(<redacted>)"),
            AuthKind::MutualTls(_) => f.write_str("Auth::mutual_tls(<certificate>)"),
        }
    }
}

/// Bounds the retries of one call.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RetryPolicy {
    /// Attempts per call, counting the first; 1 disables retries. Default 3.
    pub max_attempts: u32,
    /// Backoff base. Default 100 ms.
    pub base_delay: Duration,
    /// Backoff cap. Default 2 s.
    pub max_delay: Duration,
    /// One attempt: connect, TLS, request and response. Default 2 s.
    pub attempt_timeout: Duration,
    /// The whole call, attempts and delays included. Default 5 s.
    pub deadline: Duration,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        RetryPolicy {
            max_attempts: 3,
            base_delay: Duration::from_millis(100),
            max_delay: Duration::from_secs(2),
            attempt_timeout: Duration::from_secs(2),
            deadline: Duration::from_secs(5),
        }
    }
}

impl RetryPolicy {
    /// The defaults with `max_attempts` 1.
    pub fn disabled() -> Self {
        RetryPolicy { max_attempts: 1, ..RetryPolicy::default() }
    }
}

/// What `is_allowed` does when a call gives up.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum FailureMode {
    /// Return the error (default).
    #[default]
    Error,
    /// `is_allowed` returns `Ok(false)` instead of any crate error, including
    /// circuit-open and validation errors; `on_give_up` still fires. `check`
    /// always returns the error. There is no fail-open mode.
    Closed,
}

/// The optional per-client circuit breaker.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CircuitBreaker {
    /// Consecutive failed calls that open the breaker. Default 5.
    pub failure_threshold: u32,
    /// How long the breaker stays open. Default 30 s.
    pub cool_down: Duration,
}

impl Default for CircuitBreaker {
    fn default() -> Self {
        CircuitBreaker { failure_threshold: 5, cool_down: Duration::from_secs(30) }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum BreakerState {
    Closed,
    Open,
    HalfOpen,
}

impl BreakerState {
    #[cfg(test)]
    pub(crate) fn name(self) -> &'static str {
        match self {
            BreakerState::Closed => "closed",
            BreakerState::Open => "open",
            BreakerState::HalfOpen => "half_open",
        }
    }
}

/// Why a call ended in an error.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum GiveUpReason {
    NonRetryable,
    AttemptsExhausted,
    Deadline,
    RetryAfterExceedsDeadline,
    CircuitOpen,
}

impl GiveUpReason {
    #[cfg(test)]
    pub(crate) fn name(self) -> &'static str {
        match self {
            GiveUpReason::NonRetryable => "non_retryable",
            GiveUpReason::AttemptsExhausted => "attempts_exhausted",
            GiveUpReason::Deadline => "deadline",
            GiveUpReason::RetryAfterExceedsDeadline => "retry_after_exceeds_deadline",
            GiveUpReason::CircuitOpen => "circuit_open",
        }
    }
}

/// Passed to [`Hooks::on_attempt`] after every attempt.
#[derive(Clone, Debug)]
pub struct AttemptEvent {
    pub attempt: u32,
    pub duration: Duration,
    pub status: Option<u16>,
    pub error_kind: Option<ErrorKind>,
}

/// Passed to [`Hooks::on_retry`] before every retry delay.
#[derive(Clone, Debug)]
pub struct RetryEvent {
    /// The next attempt.
    pub attempt: u32,
    pub delay: Duration,
    pub error_kind: ErrorKind,
}

/// Passed to [`Hooks::on_give_up`] when a call ends in an error.
#[derive(Clone, Copy, Debug)]
pub struct GiveUpEvent<'a> {
    pub attempts: u32,
    pub reason: GiveUpReason,
    pub error: &'a Error,
}

/// Passed to [`Hooks::on_breaker_state_change`].
#[derive(Clone, Copy, Debug)]
pub struct BreakerStateChangeEvent {
    pub from: BreakerState,
    pub to: BreakerState,
}

/// Optional callbacks. They run synchronously on the calling task and should
/// be cheap. A panic in a hook is caught and ignored (unless the program is
/// built with `panic = "abort"`). Payloads never contain the token, headers,
/// subject, action, resource, attributes or request body.
pub trait Hooks: Send + Sync + 'static {
    fn on_attempt(&self, _event: &AttemptEvent) {}
    fn on_retry(&self, _event: &RetryEvent) {}
    fn on_give_up(&self, _event: &GiveUpEvent<'_>) {}
    fn on_breaker_state_change(&self, _event: &BreakerStateChangeEvent) {}
}

/// The decision reason, the same for both backends. Unknown codes are kept.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Reason {
    GrantPolicyFound,
    DenyPolicyFound,
    ServiceNotFound,
    NoApplicablePolicies,
    ErrorInEvaluation,
    /// Comes with `allowed: true` but is not a production grant.
    DiscoverMode,
    ReasonNotAvailable,
    Unknown(i64),
}

impl Reason {
    pub(crate) fn from_code(code: i64) -> Self {
        match code {
            0 => Reason::GrantPolicyFound,
            1 => Reason::DenyPolicyFound,
            2 => Reason::ServiceNotFound,
            3 => Reason::NoApplicablePolicies,
            4 => Reason::ErrorInEvaluation,
            5 => Reason::DiscoverMode,
            6 => Reason::ReasonNotAvailable,
            other => Reason::Unknown(other),
        }
    }

    /// The integer code.
    pub fn code(self) -> i64 {
        match self {
            Reason::GrantPolicyFound => 0,
            Reason::DenyPolicyFound => 1,
            Reason::ServiceNotFound => 2,
            Reason::NoApplicablePolicies => 3,
            Reason::ErrorInEvaluation => 4,
            Reason::DiscoverMode => 5,
            Reason::ReasonNotAvailable => 6,
            Reason::Unknown(n) => n,
        }
    }
}

/// The result of `check`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Decision {
    pub allowed: bool,
    pub reason: Reason,
    /// Self-hosted Speedle only: set when evaluation failed.
    pub error_message: Option<String>,
    /// Speedle Cloud only.
    pub quota_daily_remaining: Option<u64>,
}
