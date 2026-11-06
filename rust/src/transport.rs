// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! One HTTP attempt and the mapping of its outcome (design §3.3 to §3.5).

use std::time::Duration;

use reqwest::header::HeaderMap;
use serde_json::Value;

use crate::{Backend, Decision, Error, ErrorKind, Reason};

/// How much of a response body the crate reads.
pub(crate) const MAX_RESPONSE_BODY: usize = 1 << 20;

/// The error codes of the Speedle Cloud contract.
const CLOUD_CODES: &[&str] = &[
    "invalid_request",
    "unsupported_token_assertion",
    "unauthorized",
    "service_mismatch",
    "request_too_large",
    "rate_limited",
    "daily_quota_exceeded",
    "internal_error",
    "quota_unavailable",
    "snapshot_unavailable",
];

/// The outcome of one attempt.
pub(crate) struct AttemptResult {
    pub(crate) outcome: Result<Decision, Error>,
    pub(crate) status: Option<u16>,
    /// The error may be retried (design §6.3).
    pub(crate) retryable: bool,
    /// A usable `Retry-After`.
    pub(crate) retry_after: Option<Duration>,
}

impl AttemptResult {
    pub(crate) fn failed(err: Error, retryable: bool) -> Self {
        AttemptResult { outcome: Err(err), status: None, retryable, retry_after: None }
    }
}

/// Classifies an error with no complete response.
pub(crate) fn transport_failure(err: reqwest::Error) -> AttemptResult {
    if is_tls_failure(&err) {
        let e = Error::new(
            ErrorKind::Network,
            format!("TLS handshake or certificate verification failed: {}", chain(&err)),
        )
        .with_source(err);
        return AttemptResult::failed(e, false);
    }
    let e = Error::new(ErrorKind::Network, chain(&err)).with_source(err);
    AttemptResult::failed(e, true)
}

/// The error and its sources.
fn chain(err: &reqwest::Error) -> String {
    let mut s = err.to_string();
    let mut cur = std::error::Error::source(err);
    while let Some(e) = cur {
        s.push_str(": ");
        s.push_str(&e.to_string());
        cur = e.source();
    }
    s
}

fn is_tls_failure(err: &(dyn std::error::Error + 'static)) -> bool {
    let mut cur = Some(err);
    while let Some(e) = cur {
        if e.downcast_ref::<rustls::Error>().is_some() {
            return true;
        }
        if let Some(io) = e.downcast_ref::<std::io::Error>() {
            if let Some(inner) = io.get_ref() {
                if is_tls_failure(inner) {
                    return true;
                }
            }
        }
        cur = e.source();
    }
    false
}

/// Maps a complete response.
pub(crate) fn interpret(
    backend: Backend,
    status: u16,
    headers: &HeaderMap,
    body: &[u8],
    truncated: bool,
) -> AttemptResult {
    let quota = header(headers, "x-quota-daily-remaining").and_then(parse_quota);
    let retry_after = header(headers, "retry-after").and_then(parse_retry_after);
    let mut res = AttemptResult {
        outcome: Err(Error::new(ErrorKind::Http, "")),
        status: Some(status),
        retryable: false,
        retry_after: None,
    };

    if status == 200 {
        res.outcome = if truncated {
            Err(Error::new(ErrorKind::UnexpectedResponse, "the response body is too large"))
        } else {
            parse_decision(body)
        }
        .map(|mut d| {
            d.quota_daily_remaining = quota;
            d
        })
        .map_err(|e| e.with_status(status).with_quota(quota));
        return res;
    }

    let (code, message) = error_body(backend, body);
    let kind = match status {
        400 => ErrorKind::InvalidRequest,
        401 => ErrorKind::Unauthorized,
        403 => ErrorKind::Forbidden,
        413 => ErrorKind::RequestTooLarge,
        429 => match (backend, code.as_deref()) {
            (Backend::SpeedlePlus, _) | (_, Some("rate_limited")) => {
                // A burst 429 is retried only with a usable Retry-After.
                res.retryable = retry_after.is_some();
                res.retry_after = retry_after;
                ErrorKind::RateLimited
            }
            (_, Some("daily_quota_exceeded")) => ErrorKind::QuotaExceeded,
            _ => ErrorKind::TooManyRequests,
        },
        503 => {
            res.retryable = true;
            res.retry_after = retry_after;
            ErrorKind::ServiceUnavailable
        }
        502 | 504 => {
            res.retryable = true;
            res.retry_after = retry_after;
            ErrorKind::Server
        }
        500..=599 => ErrorKind::Server,
        _ => ErrorKind::Http,
    };
    let mut e = Error::new(kind, message).with_status(status).with_code(code).with_quota(quota);
    if status == 429 {
        e = e.with_retry_after(retry_after);
    }
    res.outcome = Err(e);
    res
}

fn header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers.get(name).and_then(|v| v.to_str().ok())
}

/// A non-negative integer, or `None`.
pub(crate) fn parse_quota(v: &str) -> Option<u64> {
    if v.is_empty() || !v.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    v.parse().ok()
}

/// A positive integer number of seconds, or `None`.
pub(crate) fn parse_retry_after(v: &str) -> Option<Duration> {
    parse_quota(v).filter(|n| *n > 0).map(Duration::from_secs)
}

/// Reads `{"error": "..."}`. For Speedle Cloud a known contract code becomes
/// the code and anything else the message; for self-hosted Speedle it is the
/// message.
fn error_body(backend: Backend, body: &[u8]) -> (Option<String>, String) {
    let text = match serde_json::from_slice::<Value>(body) {
        Ok(Value::Object(map)) => match map.get("error") {
            Some(Value::String(s)) => sanitize(s),
            _ => return (None, String::new()),
        },
        _ => return (None, String::new()),
    };
    if backend == Backend::SpeedleCloud && CLOUD_CODES.contains(&text.as_str()) {
        (Some(text), String::new())
    } else {
        (None, text)
    }
}

/// Bounds a server message and replaces control characters.
fn sanitize(s: &str) -> String {
    const MAX: usize = 512;
    let mut out: String = s.chars().map(|c| if c.is_control() { ' ' } else { c }).collect();
    if out.len() > MAX {
        let mut cut = MAX;
        while !out.is_char_boundary(cut) {
            cut -= 1;
        }
        out.truncate(cut);
        out.push('…');
    }
    out
}

fn parse_decision(body: &[u8]) -> Result<Decision, Error> {
    let unexpected = |m: &str| Error::new(ErrorKind::UnexpectedResponse, m);
    let map = match serde_json::from_slice::<Value>(body) {
        Ok(Value::Object(map)) => map,
        _ => return Err(unexpected("the response body is not a JSON object")),
    };
    let allowed = match map.get("allowed") {
        Some(Value::Bool(b)) => *b,
        _ => return Err(unexpected("the response has no boolean allowed field")),
    };
    let reason = match map.get("reason") {
        Some(Value::Number(n)) => n.as_i64().ok_or_else(|| unexpected("the response reason is not an integer"))?,
        _ => return Err(unexpected("the response has no integer reason field")),
    };
    let error_message = match map.get("errorMessage") {
        None | Some(Value::Null) => None,
        Some(Value::String(s)) => Some(s.clone()),
        Some(_) => return Err(unexpected("the response errorMessage is not a string")),
    };
    Ok(Decision { allowed, reason: Reason::from_code(reason), error_message, quota_daily_remaining: None })
}

#[cfg(test)]
mod tests {
    use super::*;
    use reqwest::header::HeaderValue;

    fn headers(pairs: &[(&'static str, &'static str)]) -> HeaderMap {
        let mut h = HeaderMap::new();
        for (k, v) in pairs {
            h.insert(*k, HeaderValue::from_static(v));
        }
        h
    }

    struct Case {
        backend: Backend,
        status: u16,
        headers: &'static [(&'static str, &'static str)],
        body: &'static str,
        kind: ErrorKind,
        code: Option<&'static str>,
        message: &'static str,
        retryable: bool,
    }

    #[test]
    fn error_mapping() {
        use Backend::*;
        use ErrorKind::*;
        let cases = [
            Case {
                backend: SpeedleCloud,
                status: 400,
                headers: &[],
                body: r#"{"error":"invalid_request"}"#,
                kind: InvalidRequest,
                code: Some("invalid_request"),
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 400,
                headers: &[],
                body: r#"{"error":"SPDL-0004 bad"}"#,
                kind: InvalidRequest,
                code: None,
                message: "SPDL-0004 bad",
                retryable: false,
            },
            Case {
                backend: SpeedlePlus,
                status: 400,
                headers: &[],
                body: r#"{"error":"invalid_request"}"#,
                kind: InvalidRequest,
                code: None,
                message: "invalid_request",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 401,
                headers: &[],
                body: r#"{"error":"unauthorized"}"#,
                kind: Unauthorized,
                code: Some("unauthorized"),
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 403,
                headers: &[],
                body: r#"{"error":"service_mismatch"}"#,
                kind: Forbidden,
                code: Some("service_mismatch"),
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 404,
                headers: &[],
                body: "404 page not found",
                kind: Http,
                code: None,
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 413,
                headers: &[],
                body: "",
                kind: RequestTooLarge,
                code: None,
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 415,
                headers: &[],
                body: "",
                kind: Http,
                code: None,
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 429,
                headers: &[("retry-after", "1")],
                body: r#"{"error":"rate_limited"}"#,
                kind: RateLimited,
                code: Some("rate_limited"),
                message: "",
                retryable: true,
            },
            Case {
                backend: SpeedleCloud,
                status: 429,
                headers: &[],
                body: r#"{"error":"rate_limited"}"#,
                kind: RateLimited,
                code: Some("rate_limited"),
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 429,
                headers: &[("retry-after", "3600")],
                body: r#"{"error":"daily_quota_exceeded"}"#,
                kind: QuotaExceeded,
                code: Some("daily_quota_exceeded"),
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 429,
                headers: &[("retry-after", "1")],
                body: "<html>",
                kind: TooManyRequests,
                code: None,
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedlePlus,
                status: 429,
                headers: &[("retry-after", "2")],
                body: r#"{"error":"slow down"}"#,
                kind: RateLimited,
                code: None,
                message: "slow down",
                retryable: true,
            },
            Case {
                backend: SpeedleCloud,
                status: 500,
                headers: &[],
                body: r#"{"error":"internal_error"}"#,
                kind: Server,
                code: Some("internal_error"),
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 501,
                headers: &[],
                body: "",
                kind: Server,
                code: None,
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 502,
                headers: &[],
                body: "<html>",
                kind: Server,
                code: None,
                message: "",
                retryable: true,
            },
            Case {
                backend: SpeedleCloud,
                status: 503,
                headers: &[],
                body: r#"{"error":"snapshot_unavailable"}"#,
                kind: ServiceUnavailable,
                code: Some("snapshot_unavailable"),
                message: "",
                retryable: true,
            },
            Case {
                backend: SpeedleCloud,
                status: 504,
                headers: &[],
                body: "",
                kind: Server,
                code: None,
                message: "",
                retryable: true,
            },
            Case {
                backend: SpeedleCloud,
                status: 302,
                headers: &[("location", "https://evil.example/")],
                body: "",
                kind: Http,
                code: None,
                message: "",
                retryable: false,
            },
            Case {
                backend: SpeedleCloud,
                status: 204,
                headers: &[],
                body: "",
                kind: Http,
                code: None,
                message: "",
                retryable: false,
            },
        ];
        for c in cases {
            let r = interpret(c.backend, c.status, &headers(c.headers), c.body.as_bytes(), false);
            let e = r.outcome.expect_err("an error");
            assert_eq!(
                (e.kind(), e.status(), e.code(), e.message(), r.retryable),
                (c.kind, Some(c.status), c.code, c.message, c.retryable),
                "{} {}",
                c.status,
                c.body
            );
        }
    }

    #[test]
    fn retry_after_and_quota_on_429() {
        let r = interpret(
            Backend::SpeedleCloud,
            429,
            &headers(&[("retry-after", "60"), ("x-quota-daily-remaining", "12")]),
            br#"{"error":"rate_limited"}"#,
            false,
        );
        let e = r.outcome.err().unwrap();
        assert_eq!(
            (e.retry_after(), e.quota_daily_remaining(), r.retry_after),
            (Some(Duration::from_secs(60)), Some(12), Some(Duration::from_secs(60)))
        );
        let r = interpret(Backend::SpeedleCloud, 503, &headers(&[("retry-after", "2")]), b"", false);
        assert_eq!((r.outcome.err().unwrap().retry_after(), r.retry_after), (None, Some(Duration::from_secs(2))));
    }

    #[test]
    fn header_parsing() {
        assert_eq!(parse_quota("9999"), Some(9999));
        for bad in ["", "-1", "1.5", " 1", "abc", "99999999999999999999999"] {
            assert_eq!(parse_quota(bad), None, "{bad}");
        }
        assert_eq!(parse_retry_after("0"), None);
        assert_eq!(parse_retry_after("Wed, 21 Oct 2026 07:28:00 GMT"), None);
        assert_eq!(parse_retry_after("1"), Some(Duration::from_secs(1)));
    }

    #[test]
    fn decisions() {
        let r = interpret(
            Backend::SpeedlePlus,
            200,
            &headers(&[("x-quota-daily-remaining", "5")]),
            br#"{"allowed":false,"reason":4,"errorMessage":"boom","x":1}"#,
            false,
        );
        assert_eq!(
            r.outcome.unwrap(),
            Decision {
                allowed: false,
                reason: Reason::ErrorInEvaluation,
                error_message: Some("boom".into()),
                quota_daily_remaining: Some(5)
            }
        );
        let r = interpret(
            Backend::SpeedleCloud,
            200,
            &HeaderMap::new(),
            br#"{"allowed":true,"reason":99,"errorMessage":null}"#,
            false,
        );
        assert_eq!(r.outcome.unwrap().reason, Reason::Unknown(99));
        for body in [
            "not json",
            "[]",
            "null",
            r#"{"allowed":"true","reason":0}"#,
            r#"{"reason":0}"#,
            r#"{"allowed":true}"#,
            r#"{"allowed":true,"reason":"0"}"#,
            r#"{"allowed":true,"reason":1.5}"#,
            r#"{"allowed":true,"reason":0,"errorMessage":3}"#,
        ] {
            let r = interpret(Backend::SpeedleCloud, 200, &HeaderMap::new(), body.as_bytes(), false);
            assert_eq!(r.outcome.err().map(|e| e.kind()), Some(ErrorKind::UnexpectedResponse), "{body}");
        }
        let r = interpret(Backend::SpeedleCloud, 200, &HeaderMap::new(), b"{}", true);
        assert_eq!(r.outcome.err().map(|e| e.kind()), Some(ErrorKind::UnexpectedResponse));
    }

    #[test]
    fn sanitize_bounds_messages() {
        assert_eq!(sanitize("a\nb"), "a b");
        let long = "é".repeat(400);
        let s = sanitize(&long);
        assert!(s.len() <= 512 + '…'.len_utf8() && s.ends_with('…'));
    }
}
