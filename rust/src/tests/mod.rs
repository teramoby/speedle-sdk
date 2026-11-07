// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! Client unit tests against the scripted server in `server`.

mod server;

use std::sync::{Arc, Mutex};
use std::time::{Duration, UNIX_EPOCH};

use server::{hang, reply, start, Mode, Reply, TestServer};

use crate::clock::{FixedRandom, VirtualClock};
use crate::*;

#[derive(Default)]
pub(crate) struct Recorder {
    pub(crate) attempts: Mutex<Vec<AttemptEvent>>,
    pub(crate) retries: Mutex<Vec<RetryEvent>>,
    pub(crate) give_ups: Mutex<Vec<(u32, GiveUpReason, ErrorKind)>>,
    pub(crate) breaker: Mutex<Vec<(BreakerState, BreakerState)>>,
}

#[derive(Clone, Default)]
pub(crate) struct Rec(pub(crate) Arc<Recorder>);

impl Hooks for Rec {
    fn on_attempt(&self, e: &AttemptEvent) {
        self.0.attempts.lock().unwrap().push(e.clone());
    }
    fn on_retry(&self, e: &RetryEvent) {
        self.0.retries.lock().unwrap().push(e.clone());
    }
    fn on_give_up(&self, e: &GiveUpEvent<'_>) {
        self.0.give_ups.lock().unwrap().push((e.attempts, e.reason, e.error.kind()));
    }
    fn on_breaker_state_change(&self, e: &BreakerStateChangeEvent) {
        self.0.breaker.lock().unwrap().push((e.from, e.to));
    }
}

const OK: &str = r#"{"allowed":true,"reason":0}"#;

/// A self-hosted Speedle client over plain HTTP on loopback with a virtual clock.
fn self_hosted(srv: &TestServer, b: impl FnOnce(ClientBuilder) -> ClientBuilder) -> (Client, Arc<VirtualClock>) {
    let clock = Arc::new(VirtualClock::default());
    let c = b(Client::builder(Backend::Speedle, &srv.url("http"), Auth::none(), "books"))
        .build_with(clock.clone(), Arc::new(FixedRandom(1.0)))
        .unwrap();
    (c, clock)
}

async fn plain(replies: Vec<Reply>) -> TestServer {
    start(Mode::Plain, replies).await
}

fn alice() -> Subject {
    Subject::user("alice")
}

#[tokio::test]
async fn request_format() {
    let srv = plain(vec![reply(200, OK), reply(200, OK)]).await;
    let (c, _) = self_hosted(&srv, |b| b);
    let subject = Subject::of([
        Principal::new("", "bob"),
        Principal { kind: Principal::GROUP.into(), name: "readers".into(), idd: Some("corp".into()) },
    ]);
    let attrs =
        [Attribute::string("b", "x"), Attribute::datetime("a", UNIX_EPOCH + Duration::from_secs(1_793_847_600))];
    assert!(c.is_allowed(&subject, "get", "/books/<1>&", &attrs).await.unwrap());
    let r = srv.request(0);
    assert_eq!(r.request_line, "POST /authz-check/v1/is-allowed HTTP/1.1");
    assert_eq!(
        r.body,
        r#"{"subject":{"principals":[{"type":"user","name":"bob"},{"type":"group","name":"readers","idd":"corp"}]},"serviceName":"books","resource":"/books/<1>&","action":"get","attributes":[{"name":"b","type":"string","value":"x"},{"name":"a","type":"datetime","value":"2026-11-05T03:00:00Z"}]}"#
    );
    assert_eq!(r.header("content-type"), Some("application/json"));
    assert_eq!(r.header("authorization"), None);
    let ua = r.header("user-agent").unwrap();
    assert!(
        ua.starts_with(&format!("speedle-sdk-rust/{} (rust/1.", env!("CARGO_PKG_VERSION")))
            && ua.ends_with(&format!("; {})", std::env::consts::OS)),
        "{ua}"
    );

    let opts = CallOptions { service_name: Some("orders".into()) };
    c.is_allowed_with(&alice(), "get", "/r", &[], &opts).await.unwrap();
    assert_eq!(
        srv.request(1).body,
        r#"{"subject":{"principals":[{"type":"user","name":"alice"}]},"serviceName":"orders","resource":"/r","action":"get"}"#
    );
}

#[tokio::test]
async fn validation_sends_nothing() {
    let srv = plain(vec![]).await;
    let rec = Rec::default();
    let (c, _) = self_hosted(&srv, |b| b.hooks(rec.clone()));
    let empty = CallOptions { service_name: Some(String::new()) };
    let errs = [
        c.check(&Subject::of([]), "get", "/r", &[]).await,
        c.check(&Subject::of([Principal::new("user", "")]), "get", "/r", &[]).await,
        c.check_with(&alice(), "get", "/r", &[], &empty).await,
        c.check(&alice(), "get", "/r", &[Attribute::bool("x", true), Attribute::bool("x", false)]).await,
        c.check(&alice(), "get", "/r", &[Attribute::numeric("n", f64::NAN)]).await,
    ];
    for e in errs {
        assert_eq!(e.err().map(|e| e.kind()), Some(ErrorKind::InvalidRequest));
    }
    assert_eq!(srv.count(), 0);
    let gu = rec.0.give_ups.lock().unwrap();
    assert_eq!(gu.len(), 5);
    assert_eq!(gu[0], (0, GiveUpReason::NonRetryable, ErrorKind::InvalidRequest));
}

#[tokio::test]
async fn retries_503_with_backoff() {
    let srv = plain(vec![reply(503, ""), reply(503, ""), reply(200, OK)]).await;
    let rec = Rec::default();
    let (c, clock) = self_hosted(&srv, |b| b.hooks(rec.clone()));
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap());
    assert_eq!(clock.take_sleeps(), [Duration::from_millis(100), Duration::from_millis(200)]);
    let attempts = rec.0.attempts.lock().unwrap();
    assert_eq!(attempts.iter().map(|a| a.status).collect::<Vec<_>>(), [Some(503), Some(503), Some(200)]);
    assert_eq!(attempts[0].error_kind, Some(ErrorKind::ServiceUnavailable));
    assert_eq!(rec.0.retries.lock().unwrap().iter().map(|r| r.attempt).collect::<Vec<_>>(), [2, 3]);
}

#[tokio::test]
async fn retry_after_raises_the_delay() {
    let srv =
        plain(vec![Reply { status: 503, headers: vec![("Retry-After", "2")], ..Default::default() }, reply(200, OK)])
            .await;
    let (c, clock) = self_hosted(&srv, |b| b);
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap());
    assert_eq!(clock.take_sleeps(), [Duration::from_secs(2)]);
}

#[tokio::test]
async fn non_retryable_and_exhausted() {
    let rec = Rec::default();
    let srv = plain(vec![reply(500, ""), reply(429, ""), reply(502, ""), reply(502, ""), reply(502, "")]).await;
    let (c, _) = self_hosted(&srv, |b| b.hooks(rec.clone()));
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::Server);
    // A self-hosted Speedle 429 without Retry-After is a burst 429 that is not retried.
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::RateLimited);
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().status(), Some(502));
    assert_eq!(srv.count(), 5);
    let reasons: Vec<GiveUpReason> = rec.0.give_ups.lock().unwrap().iter().map(|g| g.1).collect();
    assert_eq!(reasons, [GiveUpReason::NonRetryable, GiveUpReason::NonRetryable, GiveUpReason::AttemptsExhausted]);
}

#[tokio::test]
async fn backoff_that_does_not_fit_gives_up_with_deadline() {
    let rec = Rec::default();
    let srv = plain(vec![reply(503, "")]).await;
    let policy =
        RetryPolicy { base_delay: Duration::from_secs(1), deadline: Duration::from_secs(1), ..Default::default() };
    let (c, _) = self_hosted(&srv, |b| b.retry(policy).hooks(rec.clone()));
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::ServiceUnavailable);
    assert_eq!(rec.0.give_ups.lock().unwrap()[0].1, GiveUpReason::Deadline);
}

#[tokio::test]
async fn timeouts_and_deadline() {
    let rec = Rec::default();
    let srv = plain(vec![hang(), reply(200, OK)]).await;
    let policy = RetryPolicy { attempt_timeout: Duration::from_millis(100), ..Default::default() };
    let (c, _) = self_hosted(&srv, |b| b.retry(policy).hooks(rec.clone()));
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap());
    let a = rec.0.attempts.lock().unwrap()[0].clone();
    assert_eq!((a.status, a.error_kind), (None, Some(ErrorKind::Timeout)));
    assert!(a.duration >= Duration::from_millis(100));

    let srv = plain(vec![hang()]).await;
    let policy = RetryPolicy { max_attempts: 1, attempt_timeout: Duration::from_millis(50), ..Default::default() };
    let (c, _) = self_hosted(&srv, |b| b.retry(policy));
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::Timeout);

    let srv = plain(vec![hang()]).await;
    let policy = RetryPolicy { deadline: Duration::from_millis(80), ..Default::default() };
    let (c, _) = self_hosted(&srv, |b| b.retry(policy));
    let e = c.check(&alice(), "get", "/r", &[]).await.err().unwrap();
    assert_eq!((e.kind(), e.status()), (ErrorKind::DeadlineExceeded, None));
}

#[tokio::test]
async fn fail_closed() {
    let rec = Rec::default();
    let srv = plain(vec![reply(401, ""), reply(401, "")]).await;
    let (c, _) = self_hosted(&srv, |b| b.failure_mode(FailureMode::Closed).hooks(rec.clone()));
    assert!(!c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap());
    assert!(!c.is_allowed(&Subject::of([]), "get", "/r", &[]).await.unwrap());
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::Unauthorized);
    assert_eq!(rec.0.give_ups.lock().unwrap().len(), 3);
}

#[tokio::test]
async fn breaker_opens_recovers_and_reopens() {
    let rec = Rec::default();
    let srv =
        plain(vec![reply(502, ""), reply(404, ""), reply(502, ""), reply(504, ""), reply(500, ""), reply(200, OK)])
            .await;
    let cb = CircuitBreaker { failure_threshold: 2, cool_down: Duration::from_secs(10) };
    let (c, clock) = self_hosted(&srv, |b| b.retry(RetryPolicy::disabled()).circuit_breaker(cb).hooks(rec.clone()));
    for _ in 0..4 {
        let _ = c.is_allowed(&alice(), "get", "/r", &[]).await;
    }
    assert_eq!(c.0.breaker.as_ref().unwrap().state(), BreakerState::Open);
    let _ = c.is_allowed(&Subject::of([]), "get", "/r", &[]).await;
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::CircuitOpen);
    assert_eq!(srv.count(), 4);
    clock.advance(Duration::from_secs(10));
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::Server);
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::CircuitOpen);
    clock.advance(Duration::from_secs(10));
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap());
    use BreakerState::*;
    assert_eq!(
        *rec.0.breaker.lock().unwrap(),
        [(Closed, Open), (Open, HalfOpen), (HalfOpen, Open), (Open, HalfOpen), (HalfOpen, Closed)]
    );
}

#[tokio::test]
async fn dropped_trial_frees_the_half_open_slot() {
    let srv = plain(vec![reply(503, ""), hang(), reply(200, OK)]).await;
    let cb = CircuitBreaker { failure_threshold: 1, cool_down: Duration::from_secs(1) };
    let (c, clock) = self_hosted(&srv, |b| b.retry(RetryPolicy::disabled()).circuit_breaker(cb));
    let _ = c.check(&alice(), "get", "/r", &[]).await;
    clock.advance(Duration::from_secs(1));
    let trial = tokio::time::timeout(Duration::from_millis(100), c.check(&alice(), "get", "/r", &[])).await;
    assert!(trial.is_err(), "the trial was cancelled by the caller");
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap(), "a new trial is allowed");
}

#[tokio::test]
async fn cancellation_during_a_real_delay() {
    let rec = Rec::default();
    let srv = plain(vec![reply(503, "")]).await;
    let c = Client::builder(Backend::Speedle, &srv.url("http"), Auth::none(), "books")
        .retry(RetryPolicy {
            base_delay: Duration::from_secs(1),
            deadline: Duration::from_secs(60),
            ..Default::default()
        })
        .failure_mode(FailureMode::Closed)
        .hooks(rec.clone())
        // Real clock, fixed jitter: the delay is the full 1 s.
        .build_with(Arc::new(crate::clock::SystemClock), Arc::new(FixedRandom(1.0)))
        .unwrap();
    let started = std::time::Instant::now();
    let r = tokio::time::timeout(Duration::from_millis(200), c.is_allowed(&alice(), "get", "/r", &[])).await;
    assert!(r.is_err() && started.elapsed() < Duration::from_millis(900));
    assert!(rec.0.give_ups.lock().unwrap().is_empty(), "no give-up on cancellation");
    assert_eq!(rec.0.retries.lock().unwrap().len(), 1);
}

struct Panicky;

impl Hooks for Panicky {
    fn on_attempt(&self, _: &AttemptEvent) {
        panic!("hook")
    }
    fn on_retry(&self, _: &RetryEvent) {
        panic!("hook")
    }
    fn on_give_up(&self, _: &GiveUpEvent<'_>) {
        panic!("hook")
    }
    fn on_breaker_state_change(&self, _: &BreakerStateChangeEvent) {
        panic!("hook")
    }
}

#[tokio::test]
async fn hook_panics_are_ignored() {
    let srv = plain(vec![reply(503, ""), reply(200, r#"{"allowed":false,"reason":1}"#)]).await;
    let (c, _) = self_hosted(&srv, |b| {
        b.hooks(Panicky).circuit_breaker(CircuitBreaker { failure_threshold: 1, ..Default::default() })
    });
    let prev = std::panic::take_hook();
    std::panic::set_hook(Box::new(|_| {}));
    let r = c.is_allowed(&alice(), "get", "/r", &[]).await;
    std::panic::set_hook(prev);
    assert!(!r.unwrap());
}

#[tokio::test]
async fn connection_refused_is_retried() {
    let addr = {
        let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        l.local_addr().unwrap()
    };
    let rec = Rec::default();
    let clock = Arc::new(VirtualClock::default());
    let c = Client::builder(Backend::Speedle, &format!("http://{addr}"), Auth::none(), "s")
        .hooks(rec.clone())
        .build_with(clock, Arc::new(FixedRandom(1.0)))
        .unwrap();
    assert_eq!(c.check(&alice(), "get", "/r", &[]).await.err().unwrap().kind(), ErrorKind::Network);
    assert_eq!(rec.0.attempts.lock().unwrap().len(), 3);
}

#[tokio::test]
async fn bearer_over_tls() {
    let srv = start(
        Mode::Tls,
        vec![Reply { status: 200, headers: vec![("X-Quota-Daily-Remaining", "41")], body: OK, hang: false }],
    )
    .await;
    let c = Client::builder(Backend::SpeedleCloud, &srv.url("https"), Auth::bearer("spdl_test"), "books")
        .add_root_certificate_pem(srv.ca_pem.as_bytes())
        .build()
        .unwrap();
    let d = c.check(&alice(), "get", "/r", &[]).await.unwrap();
    assert_eq!((d.allowed, d.quota_daily_remaining), (true, Some(41)));
    assert_eq!(srv.request(0).header("authorization"), Some("Bearer spdl_test"));
}

#[tokio::test]
async fn untrusted_server_is_not_retried() {
    let srv = start(Mode::Tls, vec![]).await;
    let rec = Rec::default();
    let c = Client::builder(Backend::SpeedleCloud, &srv.url("https"), Auth::bearer("t"), "books")
        .hooks(rec.clone())
        .build()
        .unwrap();
    let e = c.check(&alice(), "get", "/r", &[]).await.err().unwrap();
    assert_eq!(e.kind(), ErrorKind::Network, "{e}");
    assert_eq!(rec.0.attempts.lock().unwrap().len(), 1);
    assert_eq!(rec.0.give_ups.lock().unwrap()[0].1, GiveUpReason::NonRetryable);
}

#[tokio::test]
async fn mutual_tls() {
    let srv = start(Mode::MutualTls, vec![reply(200, OK)]).await;
    let auth = Auth::mutual_tls(srv.pki.client_cert_pem.as_bytes(), srv.pki.client_key_pem.as_bytes()).unwrap();
    let c = Client::builder(Backend::Speedle, &srv.url("https"), auth, "books")
        .add_root_certificate_pem(srv.ca_pem.as_bytes())
        .build()
        .unwrap();
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).await.unwrap());

    let rec = Rec::default();
    let c = Client::builder(Backend::Speedle, &srv.url("https"), Auth::none(), "books")
        .add_root_certificate_pem(srv.ca_pem.as_bytes())
        .hooks(rec.clone())
        .build()
        .unwrap();
    let e = c.check(&alice(), "get", "/r", &[]).await.err().unwrap();
    assert_eq!(e.kind(), ErrorKind::Network, "{e}");
    assert_eq!(rec.0.attempts.lock().unwrap().len(), 1, "a missing client certificate is not retried");
}

#[test]
fn futures_are_send() {
    fn send<T: Send>(_: T) {}
    let c = Client::builder(Backend::Speedle, "http://127.0.0.1:1", Auth::none(), "s").build().unwrap();
    let s = alice();
    send(c.is_allowed(&s, "a", "r", &[]));
    send(c.check_with(&s, "a", "r", &[], &CallOptions::default()));
    fn sync<T: Send + Sync + Clone>() {}
    sync::<Client>();
}

#[cfg(feature = "blocking")]
#[test]
fn blocking_client() {
    // The server runs on its own runtime's worker threads; the blocking
    // client must not be called from inside a runtime.
    let rt = tokio::runtime::Runtime::new().unwrap();
    let srv = rt.block_on(plain(vec![reply(503, ""), reply(200, OK)]));
    let c = blocking::Client::builder(Backend::Speedle, &srv.url("http"), Auth::none(), "books")
        .retry(RetryPolicy { base_delay: Duration::from_millis(1), ..Default::default() })
        .build()
        .unwrap();
    assert!(c.is_allowed(&alice(), "get", "/r", &[]).unwrap());
    assert_eq!(srv.count(), 2);
    let c2 = c.clone();
    let d = std::thread::spawn(move || c2.check(&alice(), "get", "/r", &[])).join().unwrap();
    assert_eq!(d.err().map(|e| e.kind()), Some(ErrorKind::Server), "unscripted 599");
}
