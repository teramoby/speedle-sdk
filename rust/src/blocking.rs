// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! A blocking client for programs without an async runtime (feature
//! `blocking`).
//!
//! It runs the async client on its own small Tokio runtime, so the retry
//! policy, breaker and hooks behave the same. As with `reqwest::blocking`, it
//! must not be called from inside an async runtime; that panics. A call is
//! bounded by the retry deadline only.

use std::sync::Arc;

use crate::{
    Attribute, Auth, Backend, CallOptions, CircuitBreaker, Decision, Error, FailureMode, Hooks, RetryPolicy, Subject,
};

/// The blocking counterpart of [`crate::Client`]. Cheap to clone.
#[derive(Clone, Debug)]
pub struct Client {
    inner: crate::Client,
    runtime: Arc<tokio::runtime::Runtime>,
}

/// Builds a [`Client`]; the same options as [`crate::ClientBuilder`].
pub struct ClientBuilder(crate::ClientBuilder);

impl Client {
    pub fn builder(backend: Backend, endpoint: &str, auth: Auth, service_name: impl Into<String>) -> ClientBuilder {
        ClientBuilder(crate::Client::builder(backend, endpoint, auth, service_name))
    }

    pub fn is_allowed(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
    ) -> Result<bool, Error> {
        self.runtime.block_on(self.inner.is_allowed(subject, action, resource, attributes))
    }

    pub fn check(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
    ) -> Result<Decision, Error> {
        self.runtime.block_on(self.inner.check(subject, action, resource, attributes))
    }

    pub fn is_allowed_with(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
        options: &CallOptions,
    ) -> Result<bool, Error> {
        self.runtime.block_on(self.inner.is_allowed_with(subject, action, resource, attributes, options))
    }

    pub fn check_with(
        &self,
        subject: &Subject,
        action: &str,
        resource: &str,
        attributes: &[Attribute],
        options: &CallOptions,
    ) -> Result<Decision, Error> {
        self.runtime.block_on(self.inner.check_with(subject, action, resource, attributes, options))
    }
}

impl ClientBuilder {
    pub fn retry(self, policy: RetryPolicy) -> Self {
        ClientBuilder(self.0.retry(policy))
    }

    pub fn failure_mode(self, mode: FailureMode) -> Self {
        ClientBuilder(self.0.failure_mode(mode))
    }

    pub fn circuit_breaker(self, breaker: CircuitBreaker) -> Self {
        ClientBuilder(self.0.circuit_breaker(breaker))
    }

    pub fn hooks(self, hooks: impl Hooks) -> Self {
        ClientBuilder(self.0.hooks(hooks))
    }

    pub fn add_root_certificate_pem(self, pem: &[u8]) -> Self {
        ClientBuilder(self.0.add_root_certificate_pem(pem))
    }

    pub fn allow_insecure_http(self, allow: bool) -> Self {
        ClientBuilder(self.0.allow_insecure_http(allow))
    }

    pub fn user_agent_suffix(self, suffix: impl Into<String>) -> Self {
        ClientBuilder(self.0.user_agent_suffix(suffix))
    }

    pub fn build(self) -> Result<Client, Error> {
        let runtime = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(1)
            .thread_name("speedle-blocking")
            .enable_all()
            .build()
            .map_err(|e| Error::configuration(format!("cannot start the runtime: {e}")))?;
        // reqwest's connector needs the runtime context when it is built.
        let inner = {
            let _guard = runtime.enter();
            self.0.build()?
        };
        Ok(Client { inner, runtime: Arc::new(runtime) })
    }
}
