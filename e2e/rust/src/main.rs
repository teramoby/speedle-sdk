// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/

//! Speedle SDK end-to-end runner for Rust. Runs every vector in
//! e2e/vectors.json with the blocking `Client::check` and fails on any
//! mismatch. The contract is in e2e/README.md.

use std::process::exit;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde_json::Value;
use speedle::blocking::Client;
use speedle::{Attribute, Auth, Backend, Principal, Subject};

fn env(name: &str, required: bool) -> Option<String> {
    match std::env::var(name) {
        Ok(v) if !v.is_empty() => Some(v),
        _ if required => usage(&format!("{name} is required")),
        _ => None,
    }
}

fn usage(message: &str) -> ! {
    eprintln!("rust e2e: {message}");
    exit(2)
}

fn main() {
    let endpoint = env("SPEEDLE_E2E_ENDPOINT", true).unwrap();
    let service = env("SPEEDLE_E2E_SERVICE", true).unwrap();
    let backend = match env("SPEEDLE_E2E_BACKEND", false).as_deref() {
        None | Some("speedle-cloud") => Backend::SpeedleCloud,
        Some("speedle-plus") => Backend::SpeedlePlus,
        Some(_) => usage("SPEEDLE_E2E_BACKEND must be speedle-cloud or speedle-plus"),
    };
    let token = env("SPEEDLE_E2E_TOKEN", false);
    if matches!(backend, Backend::SpeedleCloud) && token.is_none() {
        usage("SPEEDLE_E2E_TOKEN is required for speedle-cloud");
    }
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 2 {
        usage("usage: speedle-e2e VECTORS_JSON");
    }
    let text = std::fs::read_to_string(&args[1]).unwrap_or_else(|e| usage(&e.to_string()));
    let file: Value = serde_json::from_str(&text).unwrap_or_else(|e| usage(&e.to_string()));
    let vectors = file["vectors"].as_array().cloned().unwrap_or_default();
    if vectors.is_empty() {
        usage(&format!("{} has no vectors", args[1]));
    }

    let auth = match token {
        Some(t) => Auth::bearer(t),
        None => Auth::none(),
    };
    let client = Client::builder(backend, &endpoint, auth, service)
        .user_agent_suffix("speedle-sdk-e2e")
        .build()
        .unwrap_or_else(|e| usage(&e.to_string()));

    let mut failed = 0;
    for vector in &vectors {
        let name = vector["name"].as_str().unwrap_or_default();
        let expect = &vector["expect"];
        let want = expected(expect);
        let got = run(&client, &vector["request"]);
        if want == got {
            println!("PASS {name}");
        } else {
            failed += 1;
            println!("FAIL {name}: want {want}, got {got}");
        }
    }
    println!(
        "rust: {} passed, {} failed against {}",
        vectors.len() - failed,
        failed,
        endpoint
    );
    exit(if failed == 0 { 0 } else { 1 });
}

fn run(client: &Client, request: &Value) -> String {
    let principals = request["subject"]["principals"]
        .as_array()
        .cloned()
        .unwrap_or_default()
        .iter()
        .map(|p| Principal {
            kind: p["type"].as_str().unwrap_or_default().to_string(),
            name: p["name"].as_str().unwrap_or_default().to_string(),
            idd: p["idd"].as_str().map(str::to_string),
        })
        .collect::<Vec<_>>();
    let attributes = match request["attributes"].as_array() {
        Some(items) => match items.iter().map(attribute).collect::<Result<Vec<_>, _>>() {
            Ok(a) => a,
            Err(e) => return e,
        },
        None => Vec::new(),
    };
    match client.check(
        &Subject::of(principals),
        request["action"].as_str().unwrap_or_default(),
        request["resource"].as_str().unwrap_or_default(),
        &attributes,
    ) {
        Ok(d) => format_decision(d.allowed, d.reason.code(), d.error_message.as_deref()),
        Err(e) => e.to_string(),
    }
}

fn attribute(a: &Value) -> Result<Attribute, String> {
    let name = a["name"].as_str().unwrap_or_default();
    let value = &a["value"];
    let items = match value.as_array() {
        Some(items) => items.clone(),
        None => vec![value.clone()],
    };
    let array = value.is_array();
    let bad = || format!("attribute {name}: bad value {value}");
    Ok(match a["type"].as_str() {
        Some("string") => {
            let v = items
                .iter()
                .map(|i| i.as_str().map(str::to_string))
                .collect::<Option<Vec<_>>>()
                .ok_or_else(bad)?;
            if array {
                Attribute::strings(name, v)
            } else {
                Attribute::string(name, v[0].clone())
            }
        }
        Some("numeric") => {
            let v = items
                .iter()
                .map(Value::as_f64)
                .collect::<Option<Vec<_>>>()
                .ok_or_else(bad)?;
            if array {
                Attribute::numerics(name, v)
            } else {
                Attribute::numeric(name, v[0])
            }
        }
        Some("bool") => {
            let v = items
                .iter()
                .map(Value::as_bool)
                .collect::<Option<Vec<_>>>()
                .ok_or_else(bad)?;
            if array {
                Attribute::bools(name, v)
            } else {
                Attribute::bool(name, v[0])
            }
        }
        Some("datetime") => {
            let v = items
                .iter()
                .map(|i| i.as_str().and_then(parse_rfc3339_utc))
                .collect::<Option<Vec<_>>>()
                .ok_or_else(bad)?;
            if array {
                Attribute::datetimes(name, v)
            } else {
                Attribute::datetime(name, v[0])
            }
        }
        _ => return Err(bad()),
    })
}

/// Parses the vector form `YYYY-MM-DDTHH:MM:SS[.fff]Z` (UTC only, which the
/// vector checker enforces), without a date-time dependency.
fn parse_rfc3339_utc(s: &str) -> Option<SystemTime> {
    let s = s.strip_suffix('Z')?;
    let (date, time) = s.split_once('T')?;
    let mut d = date.splitn(3, '-').map(|p| p.parse::<i64>().ok());
    let (y, m, day) = (d.next()??, d.next()??, d.next()??);
    let (hms, frac) = time.split_once('.').unwrap_or((time, ""));
    let mut t = hms.splitn(3, ':').map(|p| p.parse::<u64>().ok());
    let (hh, mm, ss) = (t.next()??, t.next()??, t.next()??);
    let nanos = if frac.is_empty() {
        0
    } else {
        format!("{frac:0<9}").get(..9)?.parse::<u32>().ok()?
    };
    // Days from civil (Howard Hinnant's algorithm).
    let y = if m <= 2 { y - 1 } else { y };
    let era = y.div_euclid(400);
    let yoe = y - era * 400;
    let mp = (m + 9) % 12;
    let doy = (153 * mp + 2) / 5 + day - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = u64::try_from(era * 146_097 + doe - 719_468).ok()?;
    Some(UNIX_EPOCH + Duration::new(days * 86_400 + hh * 3_600 + mm * 60 + ss, nanos))
}

fn expected(expect: &Value) -> String {
    if expect.is_null() {
        return "no expectation".to_string();
    }
    let message = &expect["errorMessage"];
    match (expect["allowed"].as_bool(), expect["reason"].as_i64()) {
        (Some(allowed), Some(reason)) if message.is_null() || message.is_string() => {
            format_decision(allowed, reason, message.as_str())
        }
        _ => format!("invalid expectation {expect}"),
    }
}

fn format_decision(allowed: bool, reason: i64, error_message: Option<&str>) -> String {
    let s = format!("{{allowed: {allowed}, reason: {reason}");
    match error_message {
        None => s + "}",
        Some(m) => format!("{s}, errorMessage: {}}}", Value::from(m)),
    }
}
