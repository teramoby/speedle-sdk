// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::Arc;
use std::time::Duration;

use reqwest::header::{HeaderValue, ACCEPT, AUTHORIZATION, CONTENT_TYPE};
use serde::Serialize;

use crate::attributes::{to_wire, WireAttribute};
use crate::breaker::{Breaker, Changes, Outcome};
use crate::clock::{Clock, Random};
use crate::transport::{interpret, transport_failure, AttemptResult, MAX_RESPONSE_BODY};
use crate::{
    AttemptEvent, Attribute, Auth, Backend, CallOptions, ClientBuilder, Decision, Error, ErrorKind, FailureMode,
    GiveUpEvent, GiveUpReason, Hooks, Principal, RetryEvent, RetryPolicy, Subject,
};

/// Calls is-allowed on one endpoint. Cheap to clone: clones share the
/// connection pool and the breaker. Create it once and share it.
#[derive(Clone)]
pub struct Client(pub(crate) Arc<Inner>);

pub(crate) struct Inner {
    pub(crate) backend: Backend,
    pub(crate) url: String,
    pub(crate) service_name: String,
    pub(crate) authorization: Option<HeaderValue>,
    pub(crate) retry: RetryPolicy,
    pub(crate) failure_mode: FailureMode,
    pub(crate) breaker: Option<Breaker>,
    pub(crate) hooks: Option<Arc<dyn Hooks>>,
    pub(crate) http: reqwest::Client,
    pub(crate) clock: Arc<dyn Clock>,
    pub(crate) random: Arc<dyn Random>,
}

impl std::fmt::Debug for Client {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Client")
            .field("backend", &self.0.backend)
            .field("url", &self.0.url)
            .field("service_name", &self.0.service_name)
            .finish_non_exhaustive()
    }
}

#[derive(Serialize)]
struct WirePrincipal<'a> {
    #[serde(rename = "type")]
    kind: &'a str,
    name: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    idd: Option<&'a str>,
}

#[derive(Serialize)]
struct WireSubject<'a> {
    principals: Vec<WirePrincipal<'a>>,
}

/// The Speedle JsonContext, fields in the design's order.
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct WireRequest<'a> {
    subject: WireSubject<'a>,
    service_name: &'a str,
    resource: &'a str,
    action: &'a str,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    attributes: Vec<WireAttribute<'a>>,
}

impl Client {
    /// Starts building a client. `endpoint` is the base URL, for example
    /// `https://cloud.speedle.io`. `service_name` is sent on every call.
    /// Current Speedle Cloud returns 404 `service_not_found` for an unknown
    /// service. Older Cloud returns 403 `service_mismatch`.
    pub fn builder(backend: Backend, endpoint: &str, auth: Auth, service_name: impl Into<String>) -> ClientBuilder {
        ClientBuilder::new(backend, endpoint, auth, service_name.into())
    }

    /// Returns the `allowed` value of a 200 response. Every other outcome is
    /// an error, or `Ok(false)` with [`FailureMode::Closed`]. Dropping the
    /// future cancels the call, including during a retry delay.
    pub async fn is_allowed(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
    ) -> Result<bool, Error> {
        self.is_allowed_with(subject, action, resource, attributes, &CallOptions::default()).await
    }

    /// Returns the full decision. Always returns the error when the call
    /// gives up, whatever the failure mode.
    pub async fn check(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
    ) -> Result<Decision, Error> {
        self.check_with(subject, action, resource, attributes, &CallOptions::default()).await
    }

    /// [`Client::is_allowed`] with per-call options.
    pub async fn is_allowed_with(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
        options: &CallOptions,
    ) -> Result<bool, Error> {
        match self.0.call(subject, action, resource, attributes, options).await {
            Ok(d) => Ok(d.allowed),
            Err(_) if self.0.failure_mode == FailureMode::Closed => Ok(false),
            Err(e) => Err(e),
        }
    }

    /// [`Client::check`] with per-call options.
    pub async fn check_with(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
        options: &CallOptions,
    ) -> Result<Decision, Error> {
        self.0.call(subject, action, resource, attributes, options).await
    }
}

/// Releases a half-open trial when the call's future is dropped.
struct TrialGuard<'a> {
    inner: &'a Inner,
    trial: bool,
    done: bool,
}

impl TrialGuard<'_> {
    fn finish(mut self, outcome: Outcome) {
        self.done = true;
        self.inner.record(self.trial, outcome);
    }
}

impl Drop for TrialGuard<'_> {
    fn drop(&mut self) {
        if !self.done {
            self.inner.record(self.trial, Outcome::Neutral);
        }
    }
}

struct GiveUp {
    error: Error,
    reason: GiveUpReason,
    attempts: u32,
}

impl Inner {
    fn build_body(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
        options: &CallOptions,
    ) -> Result<Vec<u8>, Error> {
        let service_name = match &options.service_name {
            Some(s) if s.is_empty() => return Err(Error::invalid_request("the service_name override is empty")),
            Some(s) => s.as_str(),
            None => self.service_name.as_str(),
        };
        let principals = subject.principals();
        if principals.is_empty() {
            return Err(Error::invalid_request("the subject has no principals"));
        }
        let principals = principals
            .iter()
            .enumerate()
            .map(|(i, p): (usize, &Principal)| {
                if p.name.is_empty() {
                    return Err(Error::invalid_request(format!("principal {i} has an empty name")));
                }
                let kind = if p.kind.is_empty() { Principal::USER } else { p.kind.as_str() };
                Ok(WirePrincipal { kind, name: &p.name, idd: p.idd.as_deref() })
            })
            .collect::<Result<_, _>>()?;
        let request = WireRequest {
            subject: WireSubject { principals },
            service_name,
            resource,
            action,
            attributes: to_wire(attributes)?,
        };
        serde_json::to_vec(&request).map_err(|_| Error::invalid_request("the request cannot be encoded as JSON"))
    }

    async fn call(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
        options: &CallOptions,
    ) -> Result<Decision, Error> {
        let body = match self.build_body(subject, action, resource, attributes, options) {
            Ok(b) => b,
            Err(e) => {
                self.on_give_up(0, GiveUpReason::NonRetryable, &e);
                return Err(e);
            }
        };
        let mut guard = TrialGuard { inner: self, trial: false, done: self.breaker.is_none() };
        if let Some(b) = &self.breaker {
            let (ok, trial, changes) = b.allow(self.clock.now());
            self.on_breaker(changes);
            if !ok {
                guard.done = true;
                let e = Error::new(ErrorKind::CircuitOpen, "the circuit breaker is open");
                self.on_give_up(0, GiveUpReason::CircuitOpen, &e);
                return Err(e);
            }
            guard.trial = trial;
        }
        match self.retry_loop(&body).await {
            Ok(d) => {
                guard.finish(Outcome::Success);
                Ok(d)
            }
            Err(g) => {
                self.on_give_up(g.attempts, g.reason, &g.error);
                guard.finish(classify(&g.error));
                Err(g.error)
            }
        }
    }

    fn record(&self, trial: bool, outcome: Outcome) {
        if let Some(b) = &self.breaker {
            self.on_breaker(b.record(trial, outcome, self.clock.now()));
        }
    }

    /// The algorithm of design §6.3.
    async fn retry_loop(&self, body: &[u8]) -> Result<Decision, GiveUp> {
        let p = &self.retry;
        let deadline_at = self.clock.now() + p.deadline;
        let mut attempt = 0u32;
        loop {
            attempt += 1;
            let remaining = deadline_at.saturating_duration_since(self.clock.now());
            if remaining.is_zero() {
                let error =
                    Error::new(ErrorKind::DeadlineExceeded, "the call deadline left no time for another attempt");
                return Err(GiveUp { error, reason: GiveUpReason::Deadline, attempts: attempt - 1 });
            }
            let cut_by_deadline = remaining <= p.attempt_timeout;
            let timeout = remaining.min(p.attempt_timeout);
            let began = self.clock.now();
            let mut res = self.send(body, timeout).await;
            if let Err(e) = &mut res.outcome {
                if e.kind() == ErrorKind::Timeout && cut_by_deadline {
                    e.set_kind(ErrorKind::DeadlineExceeded, "the call deadline ended the attempt");
                    res.retryable = false;
                }
            }
            self.on_attempt(AttemptEvent {
                attempt,
                duration: self.clock.now().saturating_duration_since(began),
                status: res.status,
                error_kind: res.outcome.as_ref().err().map(Error::kind),
            });
            let error = match res.outcome {
                Ok(d) => return Ok(d),
                Err(e) => e,
            };
            let stop = if error.kind() == ErrorKind::DeadlineExceeded {
                Some(GiveUpReason::Deadline)
            } else if !res.retryable {
                Some(GiveUpReason::NonRetryable)
            } else if attempt >= p.max_attempts {
                Some(GiveUpReason::AttemptsExhausted)
            } else {
                None
            };
            if let Some(reason) = stop {
                return Err(GiveUp { error, reason, attempts: attempt });
            }
            let mut delay = self.backoff(attempt);
            if let Some(ra) = res.retry_after {
                delay = delay.max(ra);
            }
            if delay >= deadline_at.saturating_duration_since(self.clock.now()) {
                let reason = if res.retry_after.is_some() {
                    GiveUpReason::RetryAfterExceedsDeadline
                } else {
                    GiveUpReason::Deadline
                };
                return Err(GiveUp { error, reason, attempts: attempt });
            }
            let kind = error.kind();
            self.on_retry(RetryEvent { attempt: attempt + 1, delay, error_kind: kind });
            self.clock.sleep(delay).await;
        }
    }

    /// Full jitter: random(0, min(max_delay, base_delay × 2^(n-1))).
    fn backoff(&self, attempt: u32) -> Duration {
        let p = &self.retry;
        let ceiling = 2u32
            .checked_pow(attempt - 1)
            .and_then(|f| p.base_delay.checked_mul(f))
            .map_or(p.max_delay, |d| d.min(p.max_delay));
        let nanos = (ceiling.as_nanos() as f64 * self.random.next().clamp(0.0, 1.0)).round();
        Duration::from_nanos(nanos.min(u64::MAX as f64) as u64)
    }

    async fn send(&self, body: &[u8], timeout: Duration) -> AttemptResult {
        let mut request = self
            .http
            .post(&self.url)
            .header(CONTENT_TYPE, "application/json")
            .header(ACCEPT, "application/json")
            .body(body.to_vec());
        if let Some(auth) = &self.authorization {
            request = request.header(AUTHORIZATION, auth.clone());
        }
        let exchange = async {
            let mut resp = request.send().await?;
            let status = resp.status().as_u16();
            let headers = resp.headers().clone();
            let mut data = Vec::new();
            let mut truncated = false;
            while let Some(chunk) = resp.chunk().await? {
                if data.len() + chunk.len() > MAX_RESPONSE_BODY {
                    truncated = true;
                    break;
                }
                data.extend_from_slice(&chunk);
            }
            Ok::<_, reqwest::Error>((status, headers, data, truncated))
        };
        match tokio::time::timeout(timeout, exchange).await {
            Err(_) => AttemptResult::failed(Error::new(ErrorKind::Timeout, "the attempt timed out"), true),
            Ok(Err(e)) => transport_failure(e),
            Ok(Ok((status, headers, data, truncated))) => interpret(self.backend, status, &headers, &data, truncated),
        }
    }

    fn hook(&self, f: impl FnOnce(&dyn Hooks)) {
        if let Some(h) = &self.hooks {
            let _ = catch_unwind(AssertUnwindSafe(|| f(h.as_ref())));
        }
    }

    fn on_attempt(&self, ev: AttemptEvent) {
        self.hook(|h| h.on_attempt(&ev));
    }

    fn on_retry(&self, ev: RetryEvent) {
        self.hook(|h| h.on_retry(&ev));
    }

    fn on_give_up(&self, attempts: u32, reason: GiveUpReason, error: &Error) {
        self.hook(|h| h.on_give_up(&GiveUpEvent { attempts, reason, error }));
    }

    fn on_breaker(&self, changes: Changes) {
        for ev in changes {
            self.hook(|h| h.on_breaker_state_change(&ev));
        }
    }
}

/// Maps a final error to its breaker outcome (design §6.6).
fn classify(e: &Error) -> Outcome {
    match e.kind() {
        ErrorKind::Network | ErrorKind::Timeout | ErrorKind::DeadlineExceeded => return Outcome::Failure,
        _ => {}
    }
    match e.status() {
        Some(500..=599) => Outcome::Failure,
        Some(200..=299) | Some(400..=499) => Outcome::Success,
        _ => Outcome::Neutral,
    }
}
