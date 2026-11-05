// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! Client for the Speedle authorization decision service:
//! `POST /authz-check/v1/is-allowed` on Speedle Cloud or on a self-hosted
//! Speedle+ ADS.
//!
//! ```no_run
//! use std::time::{Duration, UNIX_EPOCH};
//! use speedle::{Attribute, Auth, Backend, Client};
//!
//! # async fn run() -> Result<(), Box<dyn std::error::Error>> {
//! let speedle = Client::builder(Backend::SpeedleCloud, "https://cloud.speedle.io",
//!                               Auth::bearer(std::env::var("SPEEDLE_TOKEN")?), "books")
//!     .build()?;
//! let attrs = [
//!     Attribute::string("team", "eng"),
//!     Attribute::datetime("issuedAt", UNIX_EPOCH + Duration::from_secs(1_793_847_600)), // 2026-11-05T03:00:00Z
//! ];
//! if speedle.is_allowed(&"alice".into(), "get", "/books/123", &attrs).await? {
//!     // ...
//! }
//! # Ok(())
//! # }
//! ```
//!
//! Build one [`Client`] per endpoint and share it (clones share the pool and
//! the breaker). Every call is retried with exponential backoff and full
//! jitter within a deadline ([`RetryPolicy`]), fails with a typed [`Error`]
//! (or, with [`FailureMode::Closed`], makes `is_allowed` return `false`), and
//! never returns `true` without a 200 response whose `allowed` is `true`.
//!
//! The Speedle Cloud token is a server-side secret: never ship it to
//! browsers, mobile apps or desktop clients. Subjects and attributes must
//! come from the application's own authenticated context, not from untrusted
//! client input. The crate sends nothing but the is-allowed request: no
//! telemetry and no logging; [`Hooks`] are the only observability interface.
//!
//! The async client needs a Tokio runtime. The `blocking` feature adds
//! [`blocking::Client`] for programs without one.

mod attributes;
#[cfg(feature = "blocking")]
pub mod blocking;
mod breaker;
mod builder;
mod client;
mod clock;
#[cfg(test)]
mod conformance;
mod error;
#[cfg(test)]
mod tests;
mod transport;
mod types;

pub use attributes::{Attribute, AttributeType, AttributeValue};
pub use builder::{ClientBuilder, IS_ALLOWED_PATH};
pub use client::Client;
pub use error::{Error, ErrorKind};
pub use types::{
    AttemptEvent, Auth, Backend, BreakerState, BreakerStateChangeEvent, CallOptions, CircuitBreaker, Decision,
    FailureMode, GiveUpEvent, GiveUpReason, Hooks, Principal, Reason, RetryEvent, RetryPolicy, Subject,
};
