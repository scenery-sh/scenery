use std::collections::BTreeMap;
use std::fmt::LowerExp;
use std::str::FromStr;

use serde::de::DeserializeOwned;
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::value::RawValue;
use serde_json::{Map, Value};

/// A field explicitly present on the wire, including a nullable value.
pub fn present<'de, T: Deserialize<'de>, D: Deserializer<'de>>(
    deserializer: D,
) -> Result<Option<T>, D::Error> {
    T::deserialize(deserializer).map(Some)
}

macro_rules! wire_integer {
    ($name:ident, $primitive:ty) => {
        #[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
        pub struct $name(pub $primitive);

        impl Serialize for $name {
            fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
                serializer.collect_str(&self.0)
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
                let encoded = String::deserialize(deserializer)?;
                let parsed: $primitive = encoded.parse().map_err(serde::de::Error::custom)?;
                if encoded != parsed.to_string() {
                    return Err(serde::de::Error::custom("non-canonical integer"));
                }
                Ok(Self(parsed))
            }
        }
    };
}

wire_integer!(Int64, i64);
wire_integer!(UInt64, u64);

macro_rules! wire_string {
    ($name:ident) => {
        #[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
        #[serde(transparent)]
        pub struct $name(pub String);
    };
}

wire_string!(Decimal);
wire_string!(Timestamp);
wire_string!(Duration);
wire_string!(ContractBytes);

macro_rules! wire_float {
    ($name:ident, $primitive:ty, $digits:expr) => {
        /// A finite contract float. Its wire text is Go's
        /// `strconv.FormatFloat(value, 'g', -1, bits)`; decoding rejects any
        /// other spelling of the value.
        #[derive(Clone, Copy, Debug, PartialEq, PartialOrd)]
        pub struct $name(pub $primitive);

        impl Serialize for $name {
            fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
                let text = canonical_float(self.0, $digits).ok_or_else(|| {
                    serde::ser::Error::custom("float must be finite and not negative zero")
                })?;
                RawValue::from_string(text)
                    .map_err(serde::ser::Error::custom)?
                    .serialize(serializer)
            }
        }

        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
                // Numbers keep their source text, also inside buffered union
                // and outcome payloads.
                let text = serde_json::Number::deserialize(deserializer)?.to_string();
                let value: $primitive = text.parse().map_err(serde::de::Error::custom)?;
                if canonical_float(value, $digits).as_deref() != Some(text.as_str()) {
                    return Err(serde::de::Error::custom("non-canonical float"));
                }
                Ok(Self(value))
            }
        }
    };
}

wire_float!(Float32, f32, 9);
wire_float!(Float64, f64, 17);

/// Formats `value` like Go's `strconv.FormatFloat(value, 'g', -1, bits)`: the
/// shortest digits that parse back to `value` (of the two candidates at that
/// length the nearer one, ties to even), in exponent form when the decimal
/// exponent is below -4 or at least 6. NaN, infinities and -0 have no form.
pub fn canonical_float<F>(value: F, max_digits: usize) -> Option<String>
where
    F: Copy + PartialEq + LowerExp + FromStr + Into<f64>,
{
    let wide: f64 = value.into();
    if !wide.is_finite() || wide == 0.0 && wide.is_sign_negative() {
        return None;
    }
    for digits in 1..=max_digits {
        // A fixed precision rounds to nearest with ties to even, as Go does.
        let nearest = format!("{value:.*e}", digits - 1);
        let (mantissa, exponent) = nearest.split_once('e')?;
        let scale = exponent.parse::<i32>().ok()? - (digits as i32 - 1);
        let units: i64 = mantissa.replace('.', "").parse().ok()?;
        if nearest.parse::<F>().ok() == Some(value) {
            return Some(float_layout(units, scale));
        }
        // Near a power of two only the farther candidate may parse back.
        let other = if nearest.parse::<f64>().ok()? < wide {
            units + 1
        } else {
            units - 1
        };
        if format!("{other}e{scale}").parse::<F>().ok() == Some(value) {
            return Some(float_layout(other, scale));
        }
    }
    None
}

/// Writes `units * 10^scale` in Go's 'g' layout without trailing zeros.
fn float_layout(units: i64, mut scale: i32) -> String {
    let mut digits = units.unsigned_abs().to_string();
    while digits.len() > 1 && digits.ends_with('0') {
        digits.pop();
        scale += 1;
    }
    let exponent = scale + digits.len() as i32 - 1;
    let mut text = String::from(if units < 0 { "-" } else { "" });
    if !(-4..6).contains(&exponent) {
        text.push_str(&digits[..1]);
        if digits.len() > 1 {
            text.push('.');
            text.push_str(&digits[1..]);
        }
        let sign = if exponent < 0 { '-' } else { '+' };
        text.push_str(&format!("e{sign}{:02}", exponent.unsigned_abs()));
    } else if exponent < 0 {
        text.push_str("0.");
        text.push_str(&"0".repeat((-exponent - 1) as usize));
        text.push_str(&digits);
    } else if digits.len() <= exponent as usize + 1 {
        text.push_str(&digits);
        text.push_str(&"0".repeat(exponent as usize + 1 - digits.len()));
    } else {
        let (whole, fraction) = digits.split_at(exponent as usize + 1);
        text.push_str(whole);
        text.push('.');
        text.push_str(fraction);
    }
    text
}

// Generated records with `unknown_fields = "preserve"` map their wire object
// with these helpers: serde cannot combine `flatten` with `RawValue`.

/// Decodes a required declared member of a preserving record.
pub fn take_field<T: DeserializeOwned, E: serde::de::Error>(
    object: &mut Map<String, Value>,
    name: &'static str,
) -> Result<T, E> {
    let value = object.remove(name).ok_or_else(|| E::missing_field(name))?;
    serde_json::from_value(value).map_err(E::custom)
}

/// Decodes an optional declared member: `None` when absent, `Some` when
/// present, including an explicit null of a nullable type.
pub fn take_optional_field<T: DeserializeOwned, E: serde::de::Error>(
    object: &mut Map<String, Value>,
    name: &str,
) -> Result<Option<T>, E> {
    object
        .remove(name)
        .map(|value| serde_json::from_value(value).map_err(E::custom))
        .transpose()
}

/// Keeps the members a preserving record does not declare.
pub fn unknown_fields<E: serde::de::Error>(
    object: Map<String, Value>,
) -> Result<BTreeMap<String, Box<RawValue>>, E> {
    object
        .into_iter()
        .map(|(name, value)| {
            Ok((
                name,
                serde_json::value::to_raw_value(&value).map_err(E::custom)?,
            ))
        })
        .collect()
}

/// Starts a preserving record's wire object from its unknown members.
pub fn record_object<E: serde::ser::Error>(
    unknown: &BTreeMap<String, Box<RawValue>>,
) -> Result<Map<String, Value>, E> {
    unknown
        .iter()
        .map(|(name, raw)| {
            Ok((
                name.clone(),
                serde_json::from_str(raw.get()).map_err(E::custom)?,
            ))
        })
        .collect()
}

/// Adds a declared member to a preserving record's wire object.
pub fn put_field<T: Serialize + ?Sized, E: serde::ser::Error>(
    object: &mut Map<String, Value>,
    name: &str,
    value: &T,
) -> Result<(), E> {
    object.insert(
        name.to_owned(),
        serde_json::to_value(value).map_err(E::custom)?,
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{Float32, Float64};

    // The literals with a digit beyond their shortest form are exact
    // halfway cases: they pin the tie that Go breaks toward the even digit.
    #[test]
    #[allow(clippy::excessive_precision)]
    fn floats_use_go_canonical_text() {
        for (value, text) in [
            (1.0, "1"),
            (1e6, "1e+06"),
            (1.5e-7, "1.5e-07"),
            (0.0001, "0.0001"),
            (0.00001, "1e-05"),
            (123456.7, "123456.7"),
            (-2.5e21, "-2.5e+21"),
            (562949953421312.25, "5.629499534213122e+14"),
            (5e-324, "5e-324"),
            (f64::MAX, "1.7976931348623157e+308"),
        ] {
            assert_eq!(serde_json::to_string(&Float64(value)).unwrap(), text);
            assert_eq!(
                serde_json::from_str::<Float64>(text).unwrap(),
                Float64(value)
            );
        }
        for (value, text) in [
            (0.1f32, "0.1"),
            (1.0, "1"),
            (1048576.25, "1.0485762e+06"),
            (3.4028235e38, "3.4028235e+38"),
            (f32::from_bits(1), "1e-45"),
        ] {
            assert_eq!(serde_json::to_string(&Float32(value)).unwrap(), text);
            assert_eq!(
                serde_json::from_str::<Float32>(text).unwrap(),
                Float32(value)
            );
        }
        for text in ["1000000", "1.0", "1E6", "-0", "0.10000000000000001"] {
            assert!(
                serde_json::from_str::<Float64>(text).is_err(),
                "accepted {text}"
            );
        }
        assert!(serde_json::from_str::<Float32>("0.10000000149011612").is_err());
        assert!(serde_json::to_string(&Float64(f64::NAN)).is_err());
        assert!(serde_json::to_string(&Float64(-0.0)).is_err());
    }
}
