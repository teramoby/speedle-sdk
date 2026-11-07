// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! The conformance runner (design §11): runs every vector in
//! `../conformance/vectors` against the shared mock in `../conformance/mock`,
//! with the virtual clock and fixed random source. It is in the crate so that
//! it can inject the clock seam, which is not public.
//!
//! `cargo test --lib conformance -- --ignored`. `SPEEDLE_MOCK_DIR` overrides
//! the mock's source directory.

use std::io::{BufRead, BufReader};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde_json::{json, Map, Value};

use crate::clock::{FixedRandom, VirtualClock};
use crate::*;

struct Mock {
    child: Child,
    endpoint: String,
    control: String,
    ca_pem: String,
}

impl Drop for Mock {
    fn drop(&mut self) {
        drop(self.child.stdin.take()); // -exit-on-stdin-close
        let _ = self.child.wait();
    }
}

fn root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("..")
}

fn start_mock() -> Mock {
    let dir =
        std::env::var_os("SPEEDLE_MOCK_DIR").map(PathBuf::from).unwrap_or_else(|| root().join("conformance/mock"));
    assert!(dir.join("go.mod").exists(), "conformance mock not found in {}", dir.display());
    let bin = std::env::temp_dir().join(format!("speedle-mock-rust-{}", std::process::id()));
    let status =
        Command::new("go").arg("build").arg("-o").arg(&bin).arg(".").current_dir(&dir).status().expect("run go build");
    assert!(status.success(), "go build of the mock failed");
    let mut child = Command::new(&bin)
        .arg("-exit-on-stdin-close")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .expect("start the mock");
    let mut line = String::new();
    BufReader::new(child.stdout.take().unwrap()).read_line(&mut line).expect("mock start-up line");
    let info: Value = serde_json::from_str(&line).expect("mock start-up JSON");
    let s = |k: &str| info[k].as_str().unwrap().to_owned();
    Mock { endpoint: s("endpoint"), control: s("control"), ca_pem: s("caPem"), child }
}

#[derive(Default)]
struct Events(Mutex<Vec<Value>>);

#[derive(Clone, Default)]
struct Recorder(Arc<Events>);

fn opt_kind(k: Option<ErrorKind>) -> Value {
    k.map_or(Value::Null, |k| json!(k.name()))
}

fn ms(d: Duration) -> u64 {
    (d.as_nanos() as f64 / 1e6).round() as u64
}

impl Hooks for Recorder {
    fn on_attempt(&self, e: &AttemptEvent) {
        self.0 .0.lock().unwrap().push(
            json!({"type": "attempt", "attempt": e.attempt, "status": e.status, "errorType": opt_kind(e.error_kind)}),
        );
    }
    fn on_retry(&self, e: &RetryEvent) {
        self.0 .0.lock().unwrap().push(
            json!({"type": "retry", "attempt": e.attempt, "delayMs": ms(e.delay), "errorType": e.error_kind.name()}),
        );
    }
    fn on_give_up(&self, e: &GiveUpEvent<'_>) {
        self.0 .0.lock().unwrap().push(json!({"type": "giveUp", "attempts": e.attempts, "reason": e.reason.name(), "errorType": e.error.kind().name()}));
    }
    fn on_breaker_state_change(&self, e: &BreakerStateChangeEvent) {
        self.0 .0.lock().unwrap().push(json!({"type": "breaker", "from": e.from.name(), "to": e.to.name()}));
    }
}

/// Parses `YYYY-MM-DDTHH:MM:SSZ`.
fn parse_time(s: &str) -> SystemTime {
    let n = |r: std::ops::Range<usize>| s[r].parse::<i64>().unwrap();
    let (y, m, d) = (n(0..4), n(5..7), n(8..10));
    let (y, m) = if m <= 2 { (y - 1, m + 9) } else { (y, m - 3) };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let doy = (153 * m + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146_097 + doe - 719_468;
    let secs = days * 86_400 + n(11..13) * 3600 + n(14..16) * 60 + n(17..19);
    UNIX_EPOCH + Duration::from_secs(secs as u64)
}

fn typed(a: &Value) -> Attribute {
    let name = a["name"].as_str().unwrap();
    let v = &a["value"];
    let arr = v.as_array();
    match (a["type"].as_str().unwrap(), arr) {
        ("string", None) => Attribute::string(name, v.as_str().unwrap()),
        ("string", Some(xs)) => Attribute::strings(name, xs.iter().map(|x| x.as_str().unwrap())),
        ("numeric", None) => Attribute::numeric(name, v.as_f64().unwrap()),
        ("numeric", Some(xs)) => Attribute::numerics(name, xs.iter().map(|x| x.as_f64().unwrap())),
        ("bool", None) => Attribute::bool(name, v.as_bool().unwrap()),
        ("bool", Some(xs)) => Attribute::bools(name, xs.iter().map(|x| x.as_bool().unwrap())),
        ("datetime", None) => Attribute::datetime(name, parse_time(v.as_str().unwrap())),
        ("datetime", Some(xs)) => Attribute::datetimes(name, xs.iter().map(|x| parse_time(x.as_str().unwrap()))),
        (t, _) => panic!("attribute type {t}"),
    }
}

enum Outcome {
    Result(bool),
    Decision(Decision),
    Error(Error),
}

async fn run_call(c: &Client, call: &Value) -> Outcome {
    let subject = match &call["subject"] {
        Value::String(s) => Subject::user(s.as_str()),
        Value::Array(ps) => Subject::of(ps.iter().map(|p| Principal {
            kind: p["type"].as_str().unwrap_or_default().to_owned(),
            name: p["name"].as_str().unwrap().to_owned(),
            idd: p["idd"].as_str().map(str::to_owned),
        })),
        other => panic!("subject {other}"),
    };
    let attrs = if let Some(m) = call["inferredAttributes"].as_object() {
        match Attribute::infer(m) {
            Ok(a) => a,
            Err(e) => return Outcome::Error(e),
        }
    } else {
        call["attributes"].as_array().map(|a| a.iter().map(typed).collect()).unwrap_or_default()
    };
    let opts = CallOptions { service_name: call["serviceName"].as_str().map(str::to_owned) };
    let (action, resource) = (call["action"].as_str().unwrap(), call["resource"].as_str().unwrap());
    match call["method"].as_str().unwrap() {
        "isAllowed" => match c.is_allowed_with(&subject, action, resource, &attrs, &opts).await {
            Ok(b) => Outcome::Result(b),
            Err(e) => Outcome::Error(e),
        },
        "check" => match c.check_with(&subject, action, resource, &attrs, &opts).await {
            Ok(d) => Outcome::Decision(d),
            Err(e) => Outcome::Error(e),
        },
        m => panic!("method {m}"),
    }
}

/// Fields listed in `want` must match; null means empty; numbers compare by
/// value.
fn match_fields(what: &str, want: &Map<String, Value>, got: &Value, problems: &mut Vec<String>) {
    for (k, w) in want {
        let g = &got[k.as_str()];
        let equal = match (w.as_f64(), g.as_f64()) {
            (Some(a), Some(b)) => a == b,
            _ => w == g,
        };
        if !equal {
            problems.push(format!("{what}.{k}: got {g}, want {w}"));
        }
    }
}

fn compare(expect: &Value, outcome: &Outcome, sleeps: &[Duration], problems: &mut Vec<String>) {
    if let Some(want) = expect.get("delaysMs") {
        let got: Vec<u64> = sleeps.iter().map(|d| ms(*d)).collect();
        if json!(got) != *want {
            problems.push(format!("delaysMs: got {got:?}, want {want}"));
        }
    }
    match (expect.get("result"), expect.get("decision"), expect.get("error"), outcome) {
        (Some(want), _, _, Outcome::Result(b)) => {
            if want.as_bool() != Some(*b) {
                problems.push(format!("result: got {b}, want {want}"));
            }
        }
        (_, Some(want), _, Outcome::Decision(d)) => {
            let got = json!({
                "allowed": d.allowed, "reason": d.reason.code(),
                "errorMessage": d.error_message, "quotaDailyRemaining": d.quota_daily_remaining,
            });
            match_fields("decision", want.as_object().unwrap(), &got, problems);
        }
        (_, _, Some(want), Outcome::Error(e)) => {
            let got = json!({
                "type": e.kind().name(), "status": e.status(), "code": e.code(),
                "retryAfterSeconds": e.retry_after().map(|d| d.as_secs_f64()),
                "quotaDailyRemaining": e.quota_daily_remaining(),
            });
            match_fields("error", want.as_object().unwrap(), &got, problems);
        }
        (_, _, _, Outcome::Error(e)) => problems.push(format!("unexpected error: {e}")),
        (_, _, _, Outcome::Result(b)) => problems.push(format!("unexpected result {b}")),
        (_, _, _, Outcome::Decision(d)) => problems.push(format!("unexpected decision {d:?}")),
    }
}

async fn run_vector(mock: &Mock, control: &reqwest::Client, raw: &str) -> Vec<String> {
    let v: Value = serde_json::from_str(raw).unwrap();
    let resp = control.post(format!("{}/load", mock.control)).body(raw.to_owned()).send().await.unwrap();
    assert_eq!(resp.status().as_u16(), 204, "mock load: {}", resp.text().await.unwrap_or_default());

    let cfg = &v["config"];
    let backend = match v["backend"].as_str().unwrap() {
        "speedle-cloud" => Backend::SpeedleCloud,
        _ => Backend::Speedle,
    };
    let auth = match cfg["auth"]["type"].as_str().unwrap() {
        "bearer" => Auth::bearer(cfg["auth"]["token"].as_str().unwrap()),
        _ => Auth::none(),
    };
    let mut retry = RetryPolicy::default();
    let r = &cfg["retry"];
    let dur = |k: &str, d: Duration| r[k].as_u64().map_or(d, Duration::from_millis);
    retry.max_attempts = r["maxAttempts"].as_u64().map_or(retry.max_attempts, |n| n as u32);
    retry.base_delay = dur("baseDelayMs", retry.base_delay);
    retry.max_delay = dur("maxDelayMs", retry.max_delay);
    retry.attempt_timeout = dur("attemptTimeoutMs", retry.attempt_timeout);
    retry.deadline = dur("deadlineMs", retry.deadline);
    let rec = Recorder::default();
    let mut b = Client::builder(backend, &mock.endpoint, auth, cfg["serviceName"].as_str().unwrap())
        .add_root_certificate_pem(mock.ca_pem.as_bytes())
        .retry(retry)
        .hooks(rec.clone());
    if cfg["failureMode"] == "closed" {
        b = b.failure_mode(FailureMode::Closed);
    }
    if let Some(cb) = cfg["circuitBreaker"].as_object() {
        let d = CircuitBreaker::default();
        b = b.circuit_breaker(CircuitBreaker {
            failure_threshold: cb
                .get("failureThreshold")
                .and_then(Value::as_u64)
                .map_or(d.failure_threshold, |n| n as u32),
            cool_down: cb.get("coolDownMs").and_then(Value::as_u64).map_or(d.cool_down, Duration::from_millis),
        });
    }
    let clock = Arc::new(VirtualClock::default());
    let jitter = v["jitterFraction"].as_f64().unwrap_or(1.0);
    let client = b.build_with(clock.clone(), Arc::new(FixedRandom(jitter))).expect("build client");

    let steps: Vec<Value> = match v["steps"].as_array() {
        Some(s) => s.clone(),
        None => vec![json!({"call": v["call"], "expect": v["expect"]})],
    };
    let mut problems = Vec::new();
    for (i, step) in steps.iter().enumerate() {
        clock.advance(Duration::from_millis(step["advanceMs"].as_u64().unwrap_or(0)));
        clock.take_sleeps();
        let outcome = run_call(&client, &step["call"]).await;
        let sleeps = clock.take_sleeps();
        let mut p = Vec::new();
        compare(&step["expect"], &outcome, &sleeps, &mut p);
        problems.extend(p.into_iter().map(|p| format!("step {}: {p}", i + 1)));
    }
    if let Some(want) = v["events"].as_array() {
        let got = rec.0 .0.lock().unwrap().clone();
        if got.len() != want.len() {
            problems.push(format!("events: got {}, want {}\n got: {got:?}", got.len(), want.len()));
        } else {
            for (i, (w, g)) in want.iter().zip(&got).enumerate() {
                match_fields(&format!("event {}", i + 1), w.as_object().unwrap(), g, &mut problems);
            }
        }
    }
    let report = control.get(format!("{}/verify", mock.control)).send().await.unwrap().text().await.unwrap();
    let report: Value = serde_json::from_str(&report).unwrap();
    if report["ok"] != true {
        problems.push(format!("mock verify: {}", report["errors"]));
    }
    problems
}

#[tokio::test]
#[ignore = "needs Go and conformance/mock; run with `cargo test --lib conformance -- --ignored`"]
async fn conformance() {
    let mock = start_mock();
    let control = reqwest::Client::new();
    let mut files: Vec<PathBuf> = std::fs::read_dir(root().join("conformance/vectors"))
        .expect("vectors directory")
        .map(|e| e.unwrap().path())
        .filter(|p| p.extension().is_some_and(|x| x == "json"))
        .collect();
    files.sort();
    assert!(!files.is_empty(), "no vectors");
    let mut failed = Vec::new();
    for file in &files {
        let raw = std::fs::read_to_string(file).unwrap();
        let name = file.file_stem().unwrap().to_string_lossy().to_string();
        let problems = run_vector(&mock, &control, &raw).await;
        if problems.is_empty() {
            println!("PASS {name}");
        } else {
            println!("FAIL {name}\n  {}", problems.join("\n  "));
            failed.push(name);
        }
    }
    assert!(failed.is_empty(), "{} of {} vectors failed: {failed:?}", failed.len(), files.len());
}
