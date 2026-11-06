// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

use std::collections::HashSet;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::Serialize;
use serde_json::{Map, Value};

use crate::Error;

/// One of the four Speedle attribute types. Wire names are `"string"`,
/// `"numeric"`, `"bool"` and `"datetime"`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum AttributeType {
    String,
    Numeric,
    Bool,
    Datetime,
}

impl AttributeType {
    /// The wire name.
    pub fn as_str(self) -> &'static str {
        match self {
            AttributeType::String => "string",
            AttributeType::Numeric => "numeric",
            AttributeType::Bool => "bool",
            AttributeType::Datetime => "datetime",
        }
    }
}

/// The value of one JsonAttribute: the type and the single or array form.
#[derive(Clone, Debug, PartialEq)]
pub enum AttributeValue {
    String(String),
    Strings(Vec<String>),
    /// NaN and infinities fail the call. Self-hosted Speedle decodes numbers as
    /// `f64`.
    Numeric(f64),
    Numerics(Vec<f64>),
    Bool(bool),
    Bools(Vec<bool>),
    /// Sent as RFC 3339 in UTC, with fractional seconds only when they are
    /// not zero. Self-hosted Speedle stores Unix seconds and drops the
    /// fraction.
    Datetime(SystemTime),
    Datetimes(Vec<SystemTime>),
}

impl AttributeValue {
    /// The Speedle type.
    pub fn attribute_type(&self) -> AttributeType {
        match self {
            AttributeValue::String(_) | AttributeValue::Strings(_) => AttributeType::String,
            AttributeValue::Numeric(_) | AttributeValue::Numerics(_) => AttributeType::Numeric,
            AttributeValue::Bool(_) | AttributeValue::Bools(_) => AttributeType::Bool,
            AttributeValue::Datetime(_) | AttributeValue::Datetimes(_) => AttributeType::Datetime,
        }
    }

    /// Whether this is the array form.
    pub fn is_array(&self) -> bool {
        matches!(
            self,
            AttributeValue::Strings(_)
                | AttributeValue::Numerics(_)
                | AttributeValue::Bools(_)
                | AttributeValue::Datetimes(_)
        )
    }
}

/// One Speedle JsonAttribute `{name, type, value}`. Attributes are sent in
/// the caller's order.
#[derive(Clone, Debug, PartialEq)]
pub struct Attribute {
    pub name: String,
    pub value: AttributeValue,
}

impl Attribute {
    pub fn string(name: impl Into<String>, value: impl Into<String>) -> Self {
        Attribute { name: name.into(), value: AttributeValue::String(value.into()) }
    }

    /// No values gives an empty string array.
    pub fn strings(name: impl Into<String>, values: impl IntoIterator<Item = impl Into<String>>) -> Self {
        let values = values.into_iter().map(Into::into).collect();
        Attribute { name: name.into(), value: AttributeValue::Strings(values) }
    }

    pub fn numeric(name: impl Into<String>, value: f64) -> Self {
        Attribute { name: name.into(), value: AttributeValue::Numeric(value) }
    }

    pub fn numerics(name: impl Into<String>, values: impl IntoIterator<Item = f64>) -> Self {
        Attribute { name: name.into(), value: AttributeValue::Numerics(values.into_iter().collect()) }
    }

    pub fn bool(name: impl Into<String>, value: bool) -> Self {
        Attribute { name: name.into(), value: AttributeValue::Bool(value) }
    }

    pub fn bools(name: impl Into<String>, values: impl IntoIterator<Item = bool>) -> Self {
        Attribute { name: name.into(), value: AttributeValue::Bools(values.into_iter().collect()) }
    }

    pub fn datetime(name: impl Into<String>, value: SystemTime) -> Self {
        Attribute { name: name.into(), value: AttributeValue::Datetime(value) }
    }

    pub fn datetimes(name: impl Into<String>, values: impl IntoIterator<Item = SystemTime>) -> Self {
        Attribute { name: name.into(), value: AttributeValue::Datetimes(values.into_iter().collect()) }
    }

    /// Inferred form (§3.2.1) from JSON values: strings, numbers, booleans and
    /// non-empty homogeneous arrays of one of these. JSON has no time type, so
    /// this never yields `datetime`; use [`Attribute::datetime`]. A string is
    /// always `string`, even if it looks like a date. `null`, an empty array,
    /// a mixed array and an object return
    /// [`ErrorKind::InvalidRequest`](crate::ErrorKind::InvalidRequest). The result is sorted by name in
    /// Unicode code point order.
    pub fn infer(map: &Map<String, Value>) -> Result<Vec<Attribute>, Error> {
        let mut names: Vec<&String> = map.keys().collect();
        names.sort(); // byte order of UTF-8 is code point order
        names.into_iter().map(|name| infer_one(name, &map[name.as_str()])).collect()
    }
}

fn describe(v: &Value) -> &'static str {
    match v {
        Value::Null => "null",
        Value::Bool(_) => "a boolean",
        Value::Number(_) => "a number",
        Value::String(_) => "a string",
        Value::Array(_) => "an array",
        Value::Object(_) => "an object",
    }
}

fn infer_one(name: &str, v: &Value) -> Result<Attribute, Error> {
    if name.is_empty() {
        return Err(Error::invalid_request("attribute with an empty name"));
    }
    let reject = |what: &str| {
        Error::invalid_request(format!("attribute {name:?}: cannot infer a type from {what}; use a typed attribute"))
    };
    let number = |n: &serde_json::Number| n.as_f64().ok_or_else(|| reject("a number that is not an f64"));
    let value = match v {
        Value::String(s) => AttributeValue::String(s.clone()),
        Value::Bool(b) => AttributeValue::Bool(*b),
        Value::Number(n) => AttributeValue::Numeric(number(n)?),
        Value::Array(items) => match items.first() {
            None => return Err(reject("an empty array")),
            Some(Value::String(_)) => AttributeValue::Strings(
                items
                    .iter()
                    .map(|i| i.as_str().map(str::to_owned).ok_or_else(|| reject("a mixed array")))
                    .collect::<Result<_, _>>()?,
            ),
            Some(Value::Bool(_)) => AttributeValue::Bools(
                items.iter().map(|i| i.as_bool().ok_or_else(|| reject("a mixed array"))).collect::<Result<_, _>>()?,
            ),
            Some(Value::Number(_)) => AttributeValue::Numerics(
                items
                    .iter()
                    .map(|i| match i {
                        Value::Number(n) => number(n),
                        _ => Err(reject("a mixed array")),
                    })
                    .collect::<Result<_, _>>()?,
            ),
            Some(other) => return Err(reject(&format!("an array of {}", describe(other)))),
        },
        other => return Err(reject(describe(other))),
    };
    Ok(Attribute { name: name.to_owned(), value })
}

/// The JsonAttribute on the wire.
#[derive(Serialize)]
pub(crate) struct WireAttribute<'a> {
    name: &'a str,
    #[serde(rename = "type")]
    typ: &'static str,
    value: WireValue<'a>,
}

#[derive(Serialize)]
#[serde(untagged)]
enum WireValue<'a> {
    Str(&'a str),
    Strs(&'a [String]),
    Num(f64),
    Nums(&'a [f64]),
    Bool(bool),
    Bools(&'a [bool]),
    Time(String),
    Times(Vec<String>),
}

/// Runs the §3.2.1 checks and builds the wire form.
pub(crate) fn to_wire(attrs: &[Attribute]) -> Result<Vec<WireAttribute<'_>>, Error> {
    let mut seen = HashSet::with_capacity(attrs.len());
    attrs
        .iter()
        .enumerate()
        .map(|(i, a)| {
            if a.name.is_empty() {
                return Err(Error::invalid_request(format!("attribute {i} has an empty name")));
            }
            if !seen.insert(a.name.as_str()) {
                return Err(Error::invalid_request(format!("attribute {:?} is given more than once", a.name)));
            }
            let not_finite = || Error::invalid_request(format!("attribute {:?} is not a finite number", a.name));
            let value = match &a.value {
                AttributeValue::String(s) => WireValue::Str(s),
                AttributeValue::Strings(s) => WireValue::Strs(s),
                AttributeValue::Numeric(n) => {
                    if !n.is_finite() {
                        return Err(not_finite());
                    }
                    WireValue::Num(*n)
                }
                AttributeValue::Numerics(ns) => {
                    if ns.iter().any(|n| !n.is_finite()) {
                        return Err(not_finite());
                    }
                    WireValue::Nums(ns)
                }
                AttributeValue::Bool(b) => WireValue::Bool(*b),
                AttributeValue::Bools(b) => WireValue::Bools(b),
                AttributeValue::Datetime(t) => WireValue::Time(format_rfc3339(*t)),
                AttributeValue::Datetimes(ts) => WireValue::Times(ts.iter().map(|t| format_rfc3339(*t)).collect()),
            };
            Ok(WireAttribute { name: &a.name, typ: a.value.attribute_type().as_str(), value })
        })
        .collect()
}

/// Formats an instant as RFC 3339 in UTC, with fractional seconds only when
/// they are not zero (trailing zeros dropped), like Go's `RFC3339Nano`.
pub(crate) fn format_rfc3339(t: SystemTime) -> String {
    let (secs, nanos) = match t.duration_since(UNIX_EPOCH) {
        Ok(d) => (d.as_secs() as i64, d.subsec_nanos()),
        Err(e) => {
            let d = e.duration();
            let (s, n) = (d.as_secs() as i64, d.subsec_nanos());
            if n == 0 {
                (-s, 0)
            } else {
                (-s - 1, 1_000_000_000 - n)
            }
        }
    };
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    let (y, m, d) = civil_from_days(days);
    let mut out = format!("{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}", rem / 3600, rem % 3600 / 60, rem % 60);
    if nanos != 0 {
        let frac = format!("{nanos:09}");
        out.push('.');
        out.push_str(frac.trim_end_matches('0'));
    }
    out.push('Z');
    out
}

/// Days since 1970-01-01 to a proleptic Gregorian date (H. Hinnant).
fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    let y = yoe + era * 400 + i64::from(m <= 2);
    (y, m, d)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    fn wire(attrs: &[Attribute]) -> String {
        serde_json::to_string(&to_wire(attrs).unwrap()).unwrap()
    }

    #[test]
    fn wire_form_of_every_type() {
        let at = UNIX_EPOCH + Duration::from_secs(1_793_847_600); // 2026-11-05T03:00:00Z
        let got = wire(&[
            Attribute::string("s", "x"),
            Attribute::strings("ss", ["a", "b"]),
            Attribute::strings("empty", Vec::<String>::new()),
            Attribute::numeric("n", 4.5),
            Attribute::numerics("ns", [1.5, 2.0]),
            Attribute::bool("b", false),
            Attribute::bools("bs", [true, false]),
            Attribute::datetime("d", at),
            Attribute::datetimes("ds", [at, at + Duration::from_millis(1500)]),
        ]);
        let want = r#"[{"name":"s","type":"string","value":"x"},{"name":"ss","type":"string","value":["a","b"]},{"name":"empty","type":"string","value":[]},{"name":"n","type":"numeric","value":4.5},{"name":"ns","type":"numeric","value":[1.5,2.0]},{"name":"b","type":"bool","value":false},{"name":"bs","type":"bool","value":[true,false]},{"name":"d","type":"datetime","value":"2026-11-05T03:00:00Z"},{"name":"ds","type":"datetime","value":["2026-11-05T03:00:00Z","2026-11-05T03:00:01.5Z"]}]"#;
        assert_eq!(got, want);
    }

    #[test]
    fn rfc3339_formatting() {
        assert_eq!(format_rfc3339(UNIX_EPOCH), "1970-01-01T00:00:00Z");
        assert_eq!(format_rfc3339(UNIX_EPOCH + Duration::new(951_782_400, 120)), "2000-02-29T00:00:00.00000012Z");
        assert_eq!(format_rfc3339(UNIX_EPOCH - Duration::from_millis(500)), "1969-12-31T23:59:59.5Z");
        assert_eq!(format_rfc3339(UNIX_EPOCH + Duration::from_secs(4_102_444_799)), "2099-12-31T23:59:59Z");
    }

    #[test]
    fn checks_before_sending() {
        let cases: Vec<Vec<Attribute>> = vec![
            vec![Attribute::string("", "x")],
            vec![Attribute::string("a", "x"), Attribute::numeric("a", 1.0)],
            vec![Attribute::numeric("n", f64::NAN)],
            vec![Attribute::numerics("n", [1.0, f64::INFINITY])],
        ];
        for attrs in cases {
            let err = to_wire(&attrs).err().expect("rejected");
            assert_eq!(err.kind(), crate::ErrorKind::InvalidRequest, "{attrs:?}");
        }
    }

    #[test]
    fn infer_sorts_and_types() {
        let map: Map<String, Value> = serde_json::from_str(
            r#"{"vip":true,"team":"eng","looks":"2026-11-05T03:00:00Z","n":4,"tags":["a","b"],"ns":[1,2.5],"é":"x","Z":"y","bs":[false]}"#,
        )
        .unwrap();
        let got = Attribute::infer(&map).unwrap();
        let names: Vec<&str> = got.iter().map(|a| a.name.as_str()).collect();
        assert_eq!(names, ["Z", "bs", "looks", "n", "ns", "tags", "team", "vip", "é"]);
        assert_eq!(got[2].value, AttributeValue::String("2026-11-05T03:00:00Z".into()));
        assert_eq!(got[3].value, AttributeValue::Numeric(4.0));
        assert_eq!(got[4].value, AttributeValue::Numerics(vec![1.0, 2.5]));
        assert_eq!(got[1].value, AttributeValue::Bools(vec![false]));
        assert!(got[5].value.is_array() && got[5].value.attribute_type() == AttributeType::String);
    }

    #[test]
    fn infer_rejects() {
        for json in [
            r#"{"x":null}"#,
            r#"{"x":[]}"#,
            r#"{"x":["a",1]}"#,
            r#"{"x":[1,"a"]}"#,
            r#"{"x":[true,null]}"#,
            r#"{"x":{"a":1}}"#,
            r#"{"x":[[1]]}"#,
            r#"{"":"x"}"#,
        ] {
            let map: Map<String, Value> = serde_json::from_str(json).unwrap();
            let err = Attribute::infer(&map).expect_err(json);
            assert_eq!(err.kind(), crate::ErrorKind::InvalidRequest, "{json}");
        }
    }
}
