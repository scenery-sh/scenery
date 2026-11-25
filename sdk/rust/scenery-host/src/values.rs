use std::collections::{BTreeMap, BTreeSet};
use std::str::FromStr;
use std::sync::OnceLock;

use anyhow::{Context, Result, bail, ensure};
use base64::Engine;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use serde::Deserialize;
use serde_json::{Map, Number, Value, value::RawValue};
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;
use unicode_normalization::UnicodeNormalization;

use crate::plan::nullable;

pub(crate) type Types = BTreeMap<String, Shape>;

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
pub(crate) struct Shape {
    kind: String,
    #[serde(deserialize_with = "nullable")]
    fields: Vec<Field>,
    unknown_fields: String,
    open: bool,
    #[serde(deserialize_with = "nullable")]
    values: Vec<String>,
    discriminator: String,
    #[serde(deserialize_with = "nullable")]
    variants: BTreeMap<String, String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "PascalCase")]
struct Field {
    name: String,
    #[serde(rename = "Type")]
    ty: String,
    #[serde(
        rename = "default",
        default,
        deserialize_with = "scenery_wire::present"
    )]
    default: Option<Box<RawValue>>,
    constraints: Constraints,
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "PascalCase")]
struct Constraints {
    minimum: Option<String>,
    maximum: Option<String>,
    min_length: Option<usize>,
    max_length: Option<usize>,
    pattern: String,
    format: String,
    min_items: Option<usize>,
    max_items: Option<usize>,
    unique_items: bool,
    /// The compiled `pattern`, built on first use and kept for the bundle's
    /// lifetime. An invalid pattern matches nothing.
    #[serde(skip)]
    matcher: OnceLock<Option<regex::Regex>>,
}

pub(crate) use crate::json::{encode, encode_exact, parse};
use crate::json::{exact_decimal, expansion_budget};

/// Normalizes `value` to the canonical wire form of `expression`. Free-form
/// JSON inside it shares one growth budget sized from the whole input, so
/// exponent-form numbers cannot amplify a request.
pub(crate) fn normalize(types: &Types, expression: &str, value: Value) -> Result<Value> {
    let mut budget = expansion_budget(encoded_size(&value));
    normalize_value(
        types,
        expression,
        value,
        &Constraints::default(),
        0,
        &mut budget,
    )
}

/// Approximates the encoded length of `value` without building it.
fn encoded_size(value: &Value) -> usize {
    match value {
        Value::Null => 4,
        Value::Bool(_) => 5,
        Value::Number(number) => number.as_str().len(),
        Value::String(text) => text.len() + 2,
        Value::Array(items) => 2 + items.len() + items.iter().map(encoded_size).sum::<usize>(),
        Value::Object(fields) => {
            2 + fields
                .iter()
                .map(|(key, value)| key.len() + 4 + encoded_size(value))
                .sum::<usize>()
        }
    }
}

fn type_parts(expression: &str) -> Result<(&str, Vec<&str>)> {
    let expression = expression.trim();
    let Some((name, args)) = expression.split_once('(') else {
        return Ok((expression, vec![]));
    };
    let args = args.strip_suffix(')').context("invalid type expression")?;
    let mut level = 0;
    let mut begin = 0;
    let mut parts = Vec::new();
    for (i, byte) in args.bytes().enumerate() {
        match byte {
            b'(' => level += 1,
            b')' => level -= 1,
            b',' if level == 0 => {
                parts.push(args[begin..i].trim());
                begin = i + 1;
            }
            _ => {}
        }
    }
    parts.push(args[begin..].trim());
    Ok((name.trim(), parts))
}

fn normalize_value(
    types: &Types,
    expression: &str,
    value: Value,
    rules: &Constraints,
    depth: usize,
    budget: &mut usize,
) -> Result<Value> {
    ensure!(depth < 128, "value exceeds nesting limit");
    let (name, args) = type_parts(expression)?;
    let first = || args.first().copied().context("missing element type");
    let plain = Constraints::default();
    match name {
        "nullable" if value.is_null() => Ok(value),
        "optional" | "nullable" => {
            normalize_value(types, first()?, value, rules, depth + 1, budget)
        }
        "json" => normalize_json(value, budget),
        "list" | "set" | "tuple" => {
            let Value::Array(items) = value else {
                bail!("expected array")
            };
            ensure!(
                name != "tuple" || items.len() == args.len(),
                "wrong tuple length"
            );
            length(items.len(), rules.min_items, rules.max_items)?;
            let mut normalized = Vec::with_capacity(items.len());
            let mut unique = BTreeSet::new();
            for (index, item) in items.into_iter().enumerate() {
                let inner = if name == "tuple" {
                    args[index]
                } else {
                    first()?
                };
                let item = normalize_value(types, inner, item, &plain, depth + 1, budget)?;
                if name == "set" || rules.unique_items {
                    ensure!(unique.insert(encode(&item)), "duplicate collection item");
                }
                normalized.push(item);
            }
            if name == "set" {
                normalized.sort_by_cached_key(encode);
            }
            Ok(Value::Array(normalized))
        }
        "map" => {
            let Value::Object(fields) = value else {
                bail!("expected object")
            };
            length(fields.len(), rules.min_items, rules.max_items)?;
            let ty = first()?;
            let mut output = Map::new();
            for (key, value) in fields {
                let value = normalize_value(types, ty, value, &plain, depth + 1, budget)?;
                output.insert(key, value);
            }
            Ok(Value::Object(output))
        }
        _ => match types.get(name) {
            Some(shape) if shape.kind == "record" => {
                let Value::Object(mut fields) = value else {
                    bail!("expected record")
                };
                let mut output = Map::new();
                for field in &shape.fields {
                    let input = match fields.remove(&field.name) {
                        Some(value) => value,
                        None if field.default.is_some() => {
                            parse(field.default.as_ref().unwrap().get().as_bytes())?
                        }
                        None if type_parts(&field.ty)?.0 == "optional" => continue,
                        None => bail!("missing required field"),
                    };
                    output.insert(
                        field.name.clone(),
                        normalize_value(
                            types,
                            &field.ty,
                            input,
                            &field.constraints,
                            depth + 1,
                            budget,
                        )?,
                    );
                }
                match shape.unknown_fields.as_str() {
                    "ignore" => {}
                    "preserve" => {
                        for (key, value) in fields {
                            output.insert(key, normalize_json(value, budget)?);
                        }
                    }
                    _ => ensure!(fields.is_empty(), "unexpected field"),
                }
                Ok(Value::Object(output))
            }
            Some(shape) if shape.kind == "enum" => {
                let text = value.as_str().context("expected enum string")?;
                ensure!(
                    shape.open || shape.values.iter().any(|v| v == text),
                    "invalid enum value"
                );
                normalize_scalar("string", value, rules)
            }
            Some(shape) if shape.kind == "union" => {
                let Value::Object(mut fields) = value else {
                    bail!("expected union object")
                };
                let tag = fields
                    .remove(&shape.discriminator)
                    .context("missing union tag")?;
                let name = tag.as_str().context("invalid union tag")?;
                let mut result = match shape.variants.get(name) {
                    Some(variant) => normalize_value(
                        types,
                        variant,
                        Value::Object(fields),
                        &plain,
                        depth + 1,
                        budget,
                    )?,
                    None if shape.open => normalize_json(Value::Object(fields), budget)?,
                    None => bail!("unknown union variant"),
                };
                result
                    .as_object_mut()
                    .context("invalid union variant")?
                    .insert(shape.discriminator.clone(), tag);
                Ok(result)
            }
            Some(_) => bail!("unsupported named type"),
            None => normalize_scalar(name, value, rules),
        },
    }
}

fn length(value: usize, min: Option<usize>, max: Option<usize>) -> Result<()> {
    ensure!(
        min.is_none_or(|min| value >= min) && max.is_none_or(|max| value <= max),
        "value exceeds length limits"
    );
    Ok(())
}

/// Canonicalizes free-form JSON. Go bounds each such value by its own size;
/// `shared` also bounds every such value of one request together.
fn normalize_json(mut value: Value, shared: &mut usize) -> Result<Value> {
    fn charge(budgets: &mut [&mut usize; 2], bytes: usize) -> Result<()> {
        for budget in budgets {
            **budget = budget
                .checked_sub(bytes)
                .context("canonical JSON exceeds limit")?;
        }
        Ok(())
    }
    fn visit(value: &mut Value, budgets: &mut [&mut usize; 2]) -> Result<()> {
        match value {
            Value::Null => charge(budgets, 4),
            Value::Bool(_) => charge(budgets, 5),
            Value::String(text) => charge(budgets, text.len() + 2),
            Value::Number(number) => {
                let limit = (*budgets[0]).min(*budgets[1]);
                let encoded = exact_decimal(number.as_str(), limit)?;
                charge(budgets, encoded.len())?;
                *number = Number::from_string_unchecked(encoded);
                Ok(())
            }
            Value::Object(object) => {
                charge(budgets, 2)?;
                for (key, value) in object.iter_mut() {
                    charge(budgets, key.len() + 4)?;
                    visit(value, budgets)?;
                }
                Ok(())
            }
            Value::Array(array) => {
                charge(budgets, 2 + array.len())?;
                for value in array {
                    visit(value, budgets)?;
                }
                Ok(())
            }
        }
    }
    let mut own = expansion_budget(encoded_size(&value));
    visit(&mut value, &mut [&mut own, shared])?;
    Ok(value)
}

fn normalize_scalar(name: &str, value: Value, rules: &Constraints) -> Result<Value> {
    match name {
        "bool" => ensure!(value.is_boolean(), "expected boolean"),
        "int32" => ensure!(
            value.as_i64().and_then(|n| i32::try_from(n).ok()).is_some(),
            "expected int32"
        ),
        "uint32" => ensure!(
            value.as_u64().and_then(|n| u32::try_from(n).ok()).is_some(),
            "expected uint32"
        ),
        "float32" | "float64" => validate_float(&value, name == "float32")?,
        "int64" => canonical_integer::<i64>(&value)?,
        "uint64" => canonical_integer::<u64>(&value)?,
        "int" => {
            let text = value.as_str().context("expected integer string")?;
            ensure!(canonical_digits(text, true), "invalid integer");
        }
        // An exact byte count without a machine width: Go keeps it in a big
        // integer of at most a million digits.
        "size" => {
            let text = value.as_str().context("expected size string")?;
            ensure!(
                canonical_digits(text, false) && text.len() <= 1_000_000,
                "invalid size"
            );
        }
        "std.type.unit" => ensure!(
            value.as_object().is_some_and(Map::is_empty),
            "expected unit object"
        ),
        "std.type.problem" => {
            let object = value.as_object().context("expected problem object")?;
            let mut result = Map::new();
            for key in ["code", "message", "path"] {
                let text = object
                    .get(key)
                    .map(|value| value.as_str().context("invalid problem field"))
                    .transpose()?
                    .unwrap_or_default();
                if key != "path" || !text.is_empty() {
                    result.insert(key.into(), Value::String(text.into()));
                }
            }
            return Ok(Value::Object(result));
        }
        _ => {
            let text = value.as_str().context("expected string")?;
            if rules.min_length.is_some() || rules.max_length.is_some() {
                length(text.chars().count(), rules.min_length, rules.max_length)?;
            }
            let canonical = validate_string(name, text)?;
            if !rules.pattern.is_empty() {
                let pattern = rules
                    .matcher
                    .get_or_init(|| regex::Regex::new(&rules.pattern).ok());
                ensure!(
                    pattern
                        .as_ref()
                        .is_some_and(|pattern| pattern.is_match(&canonical)),
                    "string does not match declared pattern"
                );
            }
            if !rules.format.is_empty() {
                validate_string(&rules.format, &canonical)?;
            }
            bounds(&value, rules)?;
            return Ok(Value::String(canonical));
        }
    }
    bounds(&value, rules)?;
    Ok(value)
}

fn bounds(value: &Value, rules: &Constraints) -> Result<()> {
    use std::cmp::Ordering;
    if rules.minimum.is_none() && rules.maximum.is_none() {
        return Ok(());
    }
    let number = match value {
        Value::String(text) => text.as_str(),
        Value::Number(number) => number.as_str(),
        _ => bail!("numeric limits require a number"),
    };
    for (bound, excluded) in [
        (&rules.minimum, Ordering::Less),
        (&rules.maximum, Ordering::Greater),
    ] {
        if let Some(bound) = bound {
            ensure!(
                crate::policy::compare_numbers(number, bound)? != excluded,
                "numeric value exceeds limits"
            );
        }
    }
    Ok(())
}

fn canonical_digits(text: &str, signed: bool) -> bool {
    let digits = if signed {
        text.strip_prefix('-').unwrap_or(text)
    } else {
        text
    };
    text == "0"
        || !digits.is_empty()
            && !digits.starts_with('0')
            && digits.bytes().all(|byte| byte.is_ascii_digit())
}

/// Accepts only Go's `strconv.FormatFloat(value, 'g', -1, bits)` spelling,
/// which the SDKs write through the same shared formatter.
fn validate_float(value: &Value, narrow: bool) -> Result<()> {
    let raw = value.as_number().context("expected float number")?.as_str();
    let canonical = if narrow {
        scenery_wire::canonical_float(raw.parse::<f32>()?, 9)
    } else {
        scenery_wire::canonical_float(raw.parse::<f64>()?, 17)
    };
    ensure!(canonical.as_deref() == Some(raw), "noncanonical float");
    Ok(())
}

fn validate_string(ty: &str, text: &str) -> Result<String> {
    use std::sync::LazyLock;
    static DURATION: LazyLock<regex::Regex> = LazyLock::new(|| {
        regex::Regex::new(r"^(-)?P(?:([1-9][0-9]*)D)?(?:T(?:([1-9]|1[0-9]|2[0-3])H)?(?:([1-9]|[1-5][0-9])M)?(?:(0|[1-9]|[1-5][0-9])(?:\.([0-9]{0,8}[1-9]))?S)?)?$").expect("valid duration pattern")
    });
    static UUID: LazyLock<regex::Regex> = LazyLock::new(|| {
        regex::Regex::new(r"^[0-9a-f]{8}(?:-[0-9a-f]{4}){2}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
            .expect("valid UUID pattern")
    });
    match ty {
        "string" => {}
        "bytes" => {
            let decoded = STANDARD.decode(text)?;
            ensure!(STANDARD.encode(decoded) == text, "invalid byte encoding");
        }
        "timestamp" | "datetime" => {
            let time = OffsetDateTime::parse(text, &Rfc3339)?;
            let encoded = if ty == "datetime" {
                time.to_offset(time::UtcOffset::UTC)
            } else {
                time
            }
            .format(&Rfc3339)?;
            ensure!(ty != "datetime" || text == encoded, "noncanonical datetime");
            return Ok(encoded);
        }
        "decimal" => {
            ensure!(
                text.len() <= 1_000_001
                    && !text.contains(['e', 'E'])
                    && exact_decimal(text, usize::MAX)? == text,
                "invalid decimal"
            );
        }
        "uuid" => ensure!(UUID.is_match(text), "invalid UUID"),
        "url" => {
            let url = url::Url::parse(text)?;
            ensure!(url.host().is_some(), "URL requires an authority");
            let canonical = url.as_str();
            ensure!(
                canonical == text
                    || (url.path() == "/" && canonical.strip_suffix('/') == Some(text)),
                "noncanonical URL"
            );
        }
        "relative_path" => {
            ensure!(clean_path(text, false), "invalid relative path");
            return Ok(text.nfc().collect());
        }
        "host_path" => ensure!(clean_path(text, true), "invalid host path"),
        "date" => {
            time::Date::parse(
                text,
                time::macros::format_description!("[year]-[month]-[day]"),
            )?;
        }
        "duration" => {
            let captures = DURATION.captures(text).context("invalid duration")?;
            let date = captures.get(2).is_some();
            let time = (3..=6).any(|index| captures.get(index).is_some());
            ensure!(
                (date || time) && (!text.contains('T') || time),
                "invalid duration"
            );
            ensure!(
                captures.get(5).is_none_or(|v| v.as_str() != "0")
                    || captures.get(6).is_some()
                    || text == "PT0S",
                "noncanonical duration"
            );
        }
        _ => bail!("unsupported scalar type {ty}"),
    }
    Ok(text.into())
}

fn clean_path(text: &str, absolute: bool) -> bool {
    if text.contains('\0') || text.starts_with('/') != absolute {
        return false;
    }
    if absolute && text == "/" || !absolute && text == "." {
        return true;
    }
    let path = text.strip_prefix('/').unwrap_or(text);
    !path.is_empty()
        && (absolute || !path.contains('\\'))
        && path.split('/').all(|part| !matches!(part, "" | "." | ".."))
}

fn canonical_integer<T: FromStr + ToString>(value: &Value) -> Result<()> {
    let text = value.as_str().context("expected integer string")?;
    let parsed = text
        .parse::<T>()
        .map_err(|_| anyhow::anyhow!("invalid integer"))?;
    ensure!(parsed.to_string() == text, "noncanonical integer");
    Ok(())
}

pub(crate) fn query_pairs(query: Option<&str>) -> BTreeMap<String, Vec<String>> {
    let mut pairs: BTreeMap<_, Vec<_>> = BTreeMap::new();
    for (name, value) in url::form_urlencoded::parse(query.unwrap_or_default().as_bytes()) {
        pairs
            .entry(name.into_owned())
            .or_default()
            .push(value.into_owned());
    }
    pairs
}

pub(crate) fn base64url(value: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(value)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn enforces_map_item_bounds() {
        let types = Types::from([(
            "Request".into(),
            Shape {
                kind: "record".into(),
                fields: vec![Field {
                    name: "limits".into(),
                    ty: "map(int32)".into(),
                    default: None,
                    constraints: Constraints {
                        min_items: Some(1),
                        max_items: Some(2),
                        ..Constraints::default()
                    },
                }],
                unknown_fields: "reject".into(),
                open: false,
                values: vec![],
                discriminator: String::new(),
                variants: BTreeMap::new(),
            },
        )]);
        for (input, ok) in [
            (serde_json::json!({}), false),
            (serde_json::json!({"a":1}), true),
            (serde_json::json!({"a":1,"b":2}), true),
            (serde_json::json!({"a":1,"b":2,"c":3}), false),
        ] {
            assert_eq!(
                normalize(&types, "Request", serde_json::json!({"limits":input})).is_ok(),
                ok
            );
        }
    }

    #[test]
    fn preserves_wire_precision_and_rejects_invalid_members() {
        let types = Types::new();
        let raw = parse(br#"{"b":-0.0000,"a":9007199254740993,"c":2.10300e-10}"#).unwrap();
        let value = normalize(&types, "json", raw).unwrap();
        assert_eq!(
            encode(&value),
            r#"{"a":9007199254740993,"b":0,"c":0.0000000002103}"#
        );
        assert!(parse(br#"{"object":{"same":1,"same":2}}"#).is_err());
        assert!(normalize(&types, "float64", parse(b"1e+06").unwrap()).is_ok());
        assert!(normalize(&types, "float64", parse(b"1000000").unwrap()).is_err());
        assert!(normalize(&types, "float32", parse(b"-0").unwrap()).is_err());
        assert!(normalize(&types, "json", parse(b"1e1000001").unwrap()).is_err());
        assert!(normalize(&types, "duration", parse(br#""PT0.000000001S""#).unwrap()).is_ok());
        assert!(normalize(&types, "duration", parse(br#""PT60S""#).unwrap()).is_err());
    }

    fn record(fields: Vec<Field>, unknown_fields: &str) -> Shape {
        Shape {
            kind: "record".into(),
            fields,
            unknown_fields: unknown_fields.into(),
            open: false,
            values: vec![],
            discriminator: String::new(),
            variants: BTreeMap::new(),
        }
    }

    fn field(name: &str, ty: &str, constraints: Constraints) -> Field {
        Field {
            name: name.into(),
            ty: ty.into(),
            default: None,
            constraints,
        }
    }

    #[test]
    fn free_form_json_cannot_amplify_a_request() {
        let types = Types::from([(
            "Note".into(),
            record(
                vec![field("text", "string", Constraints::default())],
                "preserve",
            ),
        )]);
        // Each element used to get its own 32 MiB: 901 bytes became 100 MB.
        let exponents = format!("[{}]", vec!["1e999999"; 100].join(","));
        let started = std::time::Instant::now();
        for (ty, body) in [
            ("list(json)", exponents.clone()),
            ("map(json)", r#"{"a":1e999999}"#.into()),
            ("json", exponents.clone()),
            ("Note", format!(r#"{{"text":"a","extra":{exponents}}}"#)),
            ("list(Note)", r#"[{"text":"a","extra":1e999999}]"#.into()),
        ] {
            let value = parse(body.as_bytes()).unwrap();
            assert!(normalize(&types, ty, value).is_err(), "{ty} accepted");
        }
        assert!(started.elapsed() < std::time::Duration::from_millis(250));
        // Many small expansions share the budget of the whole request.
        let small = format!("[{}]", vec!["1e999"; 2000].join(","));
        let value = parse(small.as_bytes()).unwrap();
        assert!(normalize(&types, "list(json)", value).is_err());
        // Ordinary exponent forms still fit: growth stays within the bound.
        let value = parse(br#"{"n":[1e3,2.5e-3,-1E+2],"s":"<text>"}"#).unwrap();
        assert_eq!(
            encode(&normalize(&types, "json", value).unwrap()),
            r#"{"n":[1000,0.0025,-100],"s":"\u003ctext\u003e"}"#
        );
    }

    #[test]
    fn patterns_compile_once_and_limits_compare_exact_decimals() {
        let types = Types::from([
            (
                "Request".into(),
                record(
                    vec![
                        field("slugs", "list(Slug)", Constraints::default()),
                        field(
                            "amount",
                            "optional(decimal)",
                            Constraints {
                                minimum: Some("010".into()),
                                maximum: Some("1000.5".into()),
                                ..Constraints::default()
                            },
                        ),
                    ],
                    "reject",
                ),
            ),
            (
                "Slug".into(),
                record(
                    vec![field(
                        "id",
                        "string",
                        Constraints {
                            pattern: r"^\w+(-\w+)*$".into(),
                            ..Constraints::default()
                        },
                    )],
                    "reject",
                ),
            ),
        ]);
        let slugs: Vec<_> = (0..20_000)
            .map(|n| serde_json::json!({"id": format!("item-{n}")}))
            .collect();
        let started = std::time::Instant::now();
        let value = serde_json::json!({"slugs": slugs});
        assert!(normalize(&types, "Request", value).is_ok());
        // Compiling this pattern per value took about 400 µs each.
        assert!(started.elapsed() < std::time::Duration::from_secs(2));
        let amount = |text: &str| {
            normalize(
                &types,
                "Request",
                serde_json::json!({"slugs": [], "amount": text}),
            )
            .is_ok()
        };
        assert!(amount("10") && amount("1000.5") && amount("999.99999999999999999"));
        assert!(
            !amount("9.99999999999999999") && !amount("1000.50000000000000001") && !amount("8")
        );
        let invalid = Types::from([(
            "Broken".into(),
            record(
                vec![field(
                    "id",
                    "string",
                    Constraints {
                        pattern: "(".into(),
                        ..Constraints::default()
                    },
                )],
                "reject",
            ),
        )]);
        assert!(normalize(&invalid, "Broken", serde_json::json!({"id": "("})).is_err());
    }

    /// The cases of Go's `TestManifestNormalizesSizeLikeNativeContracts`.
    #[test]
    fn sizes_are_exact_byte_counts_within_their_limits() {
        let types = Types::from([(
            "files/record/Upload".into(),
            record(
                vec![
                    field(
                        "limit",
                        "size",
                        Constraints {
                            minimum: Some("1".into()),
                            maximum: Some("18446744073709551616".into()),
                            ..Constraints::default()
                        },
                    ),
                    field("quota", "optional(size)", Constraints::default()),
                ],
                "reject",
            ),
        )]);
        for (input, valid) in [
            (r#"{"limit":"1024"}"#, true),
            // A size stays exact beyond the machine integer range.
            (r#"{"limit":"18446744073709551616","quota":"0"}"#, true),
            (r#"{"limit":1024}"#, false),
            (r#"{"limit":"1KiB"}"#, false),
            (r#"{"limit":"01"}"#, false),
            (r#"{"limit":"-1"}"#, false),
            (r#"{"limit":"0"}"#, false),
            (r#"{"limit":"18446744073709551617"}"#, false),
        ] {
            let result = parse(input.as_bytes())
                .and_then(|value| normalize(&types, "files/record/Upload", value))
                .map(|value| encode(&value));
            if valid {
                assert_eq!(result.unwrap(), input);
            } else {
                assert!(result.is_err(), "accepted {input}");
            }
        }
        let size = |text: &str| normalize(&types, "size", Value::String(text.into())).is_ok();
        assert!(size("0") && !size("") && !size("-0") && !size("+1") && !size("1e3"));
        assert!(!size("1.0") && !size("1B") && !size(" 1"));
        // Go's exact decimals end at a million digits.
        assert!(size(&"9".repeat(1_000_000)) && !size(&"9".repeat(1_000_001)));
    }

    #[test]
    fn worker_wire_corpus_matches_the_shared_codecs() {
        #[derive(Deserialize)]
        struct Case {
            name: String,
            #[serde(rename = "type")]
            ty: String,
            wire: String,
            #[serde(default)]
            reject: bool,
        }
        let optional = |name: &str, ty: &str| field(name, ty, Constraints::default());
        let types = Types::from([
            (
                "probe/record/patch".into(),
                record(
                    vec![optional("note", "optional(nullable(string))")],
                    "reject",
                ),
            ),
            (
                "probe/record/note".into(),
                record(
                    vec![
                        optional("note", "optional(nullable(string))"),
                        optional("ratio", "optional(float64)"),
                        optional("text", "string"),
                    ],
                    "preserve",
                ),
            ),
            (
                "probe/enum/status".into(),
                Shape {
                    kind: "enum".into(),
                    values: vec!["in_review".into()],
                    ..record(vec![], "")
                },
            ),
        ]);
        let cases: Vec<Case> = serde_json::from_str(include_str!(
            "../../../../internal/contract/testdata/worker-wire-v1.json"
        ))
        .unwrap();
        let mut checked = 0;
        for case in cases {
            // Outcome envelopes belong to the wire crate, not to a type.
            if case.ty == "probe/outcome/quote" {
                continue;
            }
            checked += 1;
            let result = parse(case.wire.as_bytes())
                .and_then(|value| normalize(&types, &case.ty, value))
                .map(|value| encode(&value));
            if case.reject {
                assert!(result.is_err(), "accepted {}", case.name);
            } else {
                assert_eq!(result.unwrap(), case.wire, "{}", case.name);
            }
        }
        assert!(checked >= 30);
    }
}
