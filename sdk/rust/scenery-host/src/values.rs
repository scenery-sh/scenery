use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::str::FromStr;

use anyhow::{Context, Result, bail, ensure};
use base64::Engine;
use base64::engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD};
use serde::de::{MapAccess, Visitor};
use serde::{Deserialize, Deserializer};
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
}

struct UniqueObject(BTreeMap<String, Box<RawValue>>);

impl<'de> Deserialize<'de> for UniqueObject {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct ObjectVisitor;
        impl<'de> Visitor<'de> for ObjectVisitor {
            type Value = UniqueObject;
            fn expecting(&self, formatter: &mut fmt::Formatter) -> fmt::Result {
                formatter.write_str("an object with unique keys")
            }
            fn visit_map<M: MapAccess<'de>>(self, mut map: M) -> Result<Self::Value, M::Error> {
                let mut object = BTreeMap::new();
                while let Some((key, value)) = map.next_entry()? {
                    if object.insert(key, value).is_some() {
                        return Err(serde::de::Error::custom("duplicate object key"));
                    }
                }
                Ok(UniqueObject(object))
            }
        }
        d.deserialize_map(ObjectVisitor)
    }
}

pub(crate) fn parse(bytes: &[u8]) -> Result<Value> {
    fn from_raw(raw: &RawValue, depth: usize) -> Result<Value> {
        ensure!(depth < 128, "JSON exceeds nesting limit");
        let raw = raw.get();
        match raw.as_bytes()[0] {
            b'{' => {
                let UniqueObject(fields) = serde_json::from_str(raw)?;
                let fields = fields
                    .into_iter()
                    .map(|(key, value)| Ok((key, from_raw(&value, depth + 1)?)))
                    .collect::<Result<Map<_, _>>>()?;
                Ok(Value::Object(fields))
            }
            b'[' => {
                let raw: Vec<Box<RawValue>> = serde_json::from_str(raw)?;
                let items = raw
                    .iter()
                    .map(|value| from_raw(value, depth + 1))
                    .collect::<Result<_>>()?;
                Ok(Value::Array(items))
            }
            b'-' | b'0'..=b'9' => Ok(Value::Number(Number::from_string_unchecked(raw.into()))),
            _ => Ok(serde_json::from_str(raw)?),
        }
    }
    let raw: &RawValue = serde_json::from_slice(bytes)?;
    from_raw(raw, 0)
}

pub(crate) fn encode(value: &Value) -> String {
    match value {
        Value::Object(fields) => {
            let mut fields: Vec<_> = fields.iter().collect();
            fields.sort_by(|(a, _), (b, _)| a.encode_utf16().cmp(b.encode_utf16()));
            let fields = fields
                .into_iter()
                .map(|(key, value)| format!("{}:{}", quote(key), encode(value)))
                .collect::<Vec<_>>();
            format!("{{{}}}", fields.join(","))
        }
        Value::Array(items) => format!(
            "[{}]",
            items.iter().map(encode).collect::<Vec<_>>().join(",")
        ),
        Value::String(value) => quote(value),
        _ => value.to_string(),
    }
}

fn quote(value: &str) -> String {
    serde_json::to_string(value)
        .expect("string can be serialized")
        .replace('<', r"\u003c")
        .replace('>', r"\u003e")
        .replace('&', r"\u0026")
        .replace('\u{2028}', "\\u2028")
        .replace('\u{2029}', "\\u2029")
}

pub(crate) fn normalize(types: &Types, expression: &str, value: Value) -> Result<Value> {
    normalize_value(types, expression, value, &Constraints::default(), 0)
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
) -> Result<Value> {
    ensure!(depth < 128, "value exceeds nesting limit");
    let (name, args) = type_parts(expression)?;
    let first = || args.first().copied().context("missing element type");
    let nested = |ty, value| normalize_value(types, ty, value, &Constraints::default(), depth + 1);
    match name {
        "nullable" if value.is_null() => Ok(value),
        "optional" | "nullable" => normalize_value(types, first()?, value, rules, depth + 1),
        "json" => normalize_json(value),
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
                let item = nested(inner, item)?;
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
            Ok(Value::Object(
                fields
                    .into_iter()
                    .map(|(key, value)| Ok((key, nested(ty, value)?)))
                    .collect::<Result<_>>()?,
            ))
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
                        normalize_value(types, &field.ty, input, &field.constraints, depth + 1)?,
                    );
                }
                match shape.unknown_fields.as_str() {
                    "ignore" => {}
                    "preserve" => {
                        for (key, value) in fields {
                            output.insert(key, normalize_json(value)?);
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
                    Some(variant) => nested(variant, Value::Object(fields))?,
                    None if shape.open => normalize_json(Value::Object(fields))?,
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

fn normalize_json(mut value: Value) -> Result<Value> {
    fn visit(value: &mut Value, budget: &mut usize) -> Result<()> {
        match value {
            Value::Number(number) => {
                let encoded = exact_decimal(&number.to_string())?;
                *budget = budget
                    .checked_sub(encoded.len())
                    .context("canonical JSON exceeds limit")?;
                *number = Number::from_string_unchecked(encoded);
            }
            Value::Object(object) => {
                for value in object.values_mut() {
                    visit(value, budget)?;
                }
            }
            Value::Array(array) => {
                for value in array {
                    visit(value, budget)?;
                }
            }
            _ => {}
        }
        Ok(())
    }
    visit(&mut value, &mut (32 << 20))?;
    Ok(value)
}

fn exact_decimal(text: &str) -> Result<String> {
    let (negative, unsigned) = text
        .strip_prefix('-')
        .map_or(("", text), |rest| ("-", rest));
    let (mantissa, exponent) = unsigned
        .split_once(['e', 'E'])
        .map_or(Ok((unsigned, 0)), |(base, exponent)| {
            Ok::<_, anyhow::Error>((base, exponent.parse::<i32>()?))
        })?;
    ensure!(
        (-1_000_000..=1_000_000).contains(&exponent),
        "number exponent exceeds limit"
    );
    let (integer, fraction) = mantissa.split_once('.').unwrap_or((mantissa, ""));
    ensure!(
        !integer.is_empty()
            && integer
                .bytes()
                .chain(fraction.bytes())
                .all(|c| c.is_ascii_digit()),
        "invalid decimal digits"
    );
    let digits = format!("{integer}{fraction}");
    let digits = digits.trim_start_matches('0');
    if digits.is_empty() {
        return Ok("0".into());
    }
    let scale = fraction.len() as i64 - i64::from(exponent);
    if scale <= 0 {
        return Ok(format!(
            "{negative}{digits}{}",
            "0".repeat((-scale) as usize)
        ));
    }
    let trailing = (digits.len() - digits.trim_end_matches('0').len()).min(scale as usize);
    let scale = scale as usize - trailing;
    let digits = &digits[..digits.len() - trailing];
    if scale == 0 {
        return Ok(format!("{negative}{digits}"));
    }
    if scale >= digits.len() {
        return Ok(format!(
            "{negative}0.{}{digits}",
            "0".repeat(scale - digits.len())
        ));
    }
    let (integer, decimal) = digits.split_at(digits.len() - scale);
    Ok(format!("{negative}{integer}.{decimal}"))
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
            length(text.chars().count(), rules.min_length, rules.max_length)?;
            let canonical = validate_string(name, text)?;
            if !rules.pattern.is_empty() {
                ensure!(
                    regex::Regex::new(&rules.pattern)?.is_match(&canonical),
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
    if rules.minimum.is_some() || rules.maximum.is_some() {
        let number = value
            .as_str()
            .map(str::to_owned)
            .unwrap_or_else(|| value.to_string());
        for (bound, operator) in [(&rules.minimum, ">="), (&rules.maximum, "<=")] {
            if let Some(bound) = bound {
                ensure!(
                    crate::policy::evaluate(&format!("{number} {operator} {bound}"), &Value::Null)?,
                    "numeric value exceeds limits"
                );
            }
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

fn validate_float(value: &Value, narrow: bool) -> Result<()> {
    let raw = value
        .as_number()
        .context("expected float number")?
        .to_string();
    let (number, fixed, scientific) = if narrow {
        let number = raw.parse::<f32>()?;
        (f64::from(number), number.to_string(), format!("{number:e}"))
    } else {
        let number = raw.parse::<f64>()?;
        (number, number.to_string(), format!("{number:e}"))
    };
    ensure!(
        number.is_finite() && !(number == 0.0 && number.is_sign_negative()),
        "invalid float"
    );
    let (mantissa, exponent) = scientific
        .split_once('e')
        .context("invalid float encoding")?;
    let exponent = exponent.parse::<i32>()?;
    let canonical = if !(-4..6).contains(&exponent) {
        format!(
            "{mantissa}e{}{exp:02}",
            if exponent >= 0 { '+' } else { '-' },
            exp = exponent.unsigned_abs()
        )
    } else {
        fixed
    };
    ensure!(canonical == raw, "noncanonical float");
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
                    && exact_decimal(text)? == text,
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
}
