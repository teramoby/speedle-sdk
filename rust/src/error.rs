// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

use std::fmt;
use std::time::Duration;

/// The kind of an [`Error`] (design §7).
///
/// `RateLimited` and `QuotaExceeded` are the two kinds of `TooManyRequests`;
/// `DeadlineExceeded` is a kind of `Timeout`, which is a kind of `Network`.
#[non_exhaustive]
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum ErrorKind {
    /// The client options are invalid.
    Configuration,
    /// HTTP 400, or the attribute and subject checks before sending.
    InvalidRequest,
    /// HTTP 401.
    Unauthorized,
    /// HTTP 403, including Speedle Cloud `service_mismatch`.
    Forbidden,
    /// HTTP 413.
    RequestTooLarge,
    /// HTTP 429 with an unknown Speedle Cloud code.
    TooManyRequests,
    /// A burst 429 (Speedle Cloud `rate_limited`, or any 429 for self-hosted Speedle).
    RateLimited,
    /// Speedle Cloud `daily_quota_exceeded`.
    QuotaExceeded,
    /// HTTP 503.
    ServiceUnavailable,
    /// HTTP 500, 502, 504 and other 5xx.
    Server,
    /// Any other status, including 3xx and 404.
    Http,
    /// A 200 whose body is not the expected JSON.
    UnexpectedResponse,
    /// A transport failure, including TLS.
    Network,
    /// The last attempt hit the attempt timeout.
    Timeout,
    /// The call's deadline ended an attempt or left no time to start one.
    DeadlineExceeded,
    /// The circuit breaker rejected the call.
    CircuitOpen,
}

impl ErrorKind {
    /// The error name shared by every Speedle SDK, for example
    /// `"RateLimitedError"`.
    pub(crate) fn name(self) -> &'static str {
        match self {
            ErrorKind::Configuration => "ConfigurationError",
            ErrorKind::InvalidRequest => "InvalidRequestError",
            ErrorKind::Unauthorized => "UnauthorizedError",
            ErrorKind::Forbidden => "ForbiddenError",
            ErrorKind::RequestTooLarge => "RequestTooLargeError",
            ErrorKind::TooManyRequests => "TooManyRequestsError",
            ErrorKind::RateLimited => "RateLimitedError",
            ErrorKind::QuotaExceeded => "QuotaExceededError",
            ErrorKind::ServiceUnavailable => "ServiceUnavailableError",
            ErrorKind::Server => "ServerError",
            ErrorKind::Http => "HttpError",
            ErrorKind::UnexpectedResponse => "UnexpectedResponseError",
            ErrorKind::Network => "NetworkError",
            ErrorKind::Timeout => "TimeoutError",
            ErrorKind::DeadlineExceeded => "DeadlineExceededError",
            ErrorKind::CircuitOpen => "CircuitOpenError",
        }
    }
}

impl fmt::Display for ErrorKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.name())
    }
}

/// The one error type of the crate. Messages never contain the token,
/// attributes or the request body.
pub struct Error(Box<Inner>);

struct Inner {
    kind: ErrorKind,
    status: Option<u16>,
    code: Option<String>,
    message: String,
    retry_after: Option<Duration>,
    quota_daily_remaining: Option<u64>,
    source: Option<Box<dyn std::error::Error + Send + Sync>>,
}

impl Error {
    pub(crate) fn new(kind: ErrorKind, message: impl Into<String>) -> Self {
        Error(Box::new(Inner {
            kind,
            status: None,
            code: None,
            message: message.into(),
            retry_after: None,
            quota_daily_remaining: None,
            source: None,
        }))
    }

    pub(crate) fn configuration(message: impl Into<String>) -> Self {
        Error::new(ErrorKind::Configuration, message)
    }

    pub(crate) fn invalid_request(message: impl Into<String>) -> Self {
        Error::new(ErrorKind::InvalidRequest, message)
    }

    pub(crate) fn with_status(mut self, status: u16) -> Self {
        self.0.status = Some(status);
        self
    }

    pub(crate) fn with_code(mut self, code: Option<String>) -> Self {
        self.0.code = code;
        self
    }

    pub(crate) fn with_retry_after(mut self, retry_after: Option<Duration>) -> Self {
        self.0.retry_after = retry_after;
        self
    }

    pub(crate) fn with_quota(mut self, quota: Option<u64>) -> Self {
        self.0.quota_daily_remaining = quota;
        self
    }

    pub(crate) fn with_source(mut self, source: impl std::error::Error + Send + Sync + 'static) -> Self {
        self.0.source = Some(Box::new(source));
        self
    }

    pub(crate) fn set_kind(&mut self, kind: ErrorKind, message: impl Into<String>) {
        self.0.kind = kind;
        self.0.message = message.into();
    }

    /// The error kind.
    pub fn kind(&self) -> ErrorKind {
        self.0.kind
    }

    /// The HTTP status, or `None` when there was no response.
    pub fn status(&self) -> Option<u16> {
        self.0.status
    }

    /// The Speedle Cloud error code, such as `service_mismatch`. Always
    /// `None` for self-hosted Speedle.
    pub fn code(&self) -> Option<&str> {
        self.0.code.as_deref()
    }

    /// The `Retry-After` of a 429, when present and a positive integer.
    pub fn retry_after(&self) -> Option<Duration> {
        self.0.retry_after
    }

    /// The Speedle Cloud `X-Quota-Daily-Remaining` header, when present and
    /// valid.
    pub fn quota_daily_remaining(&self) -> Option<u64> {
        self.0.quota_daily_remaining
    }

    /// The server's error text or the crate's description.
    pub fn message(&self) -> &str {
        &self.0.message
    }
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "speedle: {}", self.0.kind)?;
        if let Some(status) = self.0.status {
            write!(f, " (HTTP {status})")?;
        }
        if let Some(code) = &self.0.code {
            write!(f, " {code}")?;
        }
        if !self.0.message.is_empty() {
            write!(f, ": {}", self.0.message)?;
        }
        Ok(())
    }
}

impl fmt::Debug for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Error")
            .field("kind", &self.0.kind)
            .field("status", &self.0.status)
            .field("code", &self.0.code)
            .field("message", &self.0.message)
            .field("retry_after", &self.0.retry_after)
            .field("quota_daily_remaining", &self.0.quota_daily_remaining)
            .finish()
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        self.0.source.as_ref().map(|s| s.as_ref() as &(dyn std::error::Error + 'static))
    }
}
