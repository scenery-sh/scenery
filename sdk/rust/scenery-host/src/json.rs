//! Exact JSON reading and canonical writing for wire values.

use std::fmt::Write;

use anyhow::{Result, bail, ensure};
use serde_json::{Map, Number, Value};

const MAX_DEPTH: usize = 128;

/// Parses one JSON document in a single pass over its bytes. Numbers keep
/// their source text, duplicate object keys are rejected and nesting is
/// bounded, so the cost is linear in the input whatever its shape.
pub(crate) fn parse(bytes: &[u8]) -> Result<Value> {
    let mut reader = Reader {
        text: std::str::from_utf8(bytes)?,
        at: 0,
    };
    let value = reader.value(0)?;
    reader.space();
    ensure!(reader.at == bytes.len(), "trailing characters after JSON");
    Ok(value)
}

struct Reader<'a> {
    text: &'a str,
    at: usize,
}

impl Reader<'_> {
    fn peek(&self) -> Option<u8> {
        self.text.as_bytes().get(self.at).copied()
    }

    fn space(&mut self) {
        while matches!(self.peek(), Some(b' ' | b'\t' | b'\n' | b'\r')) {
            self.at += 1;
        }
    }

    fn value(&mut self, depth: usize) -> Result<Value> {
        ensure!(depth < MAX_DEPTH, "JSON exceeds nesting limit");
        self.space();
        match self.peek() {
            Some(b'{') => {
                self.at += 1;
                let mut fields = Map::new();
                self.space();
                if self.peek() == Some(b'}') {
                    self.at += 1;
                    return Ok(Value::Object(fields));
                }
                loop {
                    self.space();
                    let key = self.string()?;
                    self.space();
                    ensure!(self.peek() == Some(b':'), "invalid JSON object");
                    self.at += 1;
                    let value = self.value(depth + 1)?;
                    ensure!(fields.insert(key, value).is_none(), "duplicate object key");
                    if self.separator(b'}')? {
                        return Ok(Value::Object(fields));
                    }
                }
            }
            Some(b'[') => {
                self.at += 1;
                let mut items = Vec::new();
                self.space();
                if self.peek() == Some(b']') {
                    self.at += 1;
                    return Ok(Value::Array(items));
                }
                loop {
                    items.push(self.value(depth + 1)?);
                    if self.separator(b']')? {
                        return Ok(Value::Array(items));
                    }
                }
            }
            Some(b'"') => Ok(Value::String(self.string()?)),
            Some(b't') => self.literal("true", Value::Bool(true)),
            Some(b'f') => self.literal("false", Value::Bool(false)),
            Some(b'n') => self.literal("null", Value::Null),
            Some(b'-' | b'0'..=b'9') => self.number(),
            _ => bail!("invalid JSON value"),
        }
    }

    /// Consumes the comma or the closing bracket after a member and
    /// reports whether the container ended.
    fn separator(&mut self, close: u8) -> Result<bool> {
        self.space();
        let next = self.peek();
        self.at += 1;
        match next {
            Some(b',') => Ok(false),
            Some(byte) if byte == close => Ok(true),
            _ => bail!("invalid JSON container"),
        }
    }

    fn literal(&mut self, word: &str, value: Value) -> Result<Value> {
        ensure!(self.text[self.at..].starts_with(word), "invalid JSON value");
        self.at += word.len();
        Ok(value)
    }

    fn digits(&mut self) -> usize {
        let start = self.at;
        while matches!(self.peek(), Some(b'0'..=b'9')) {
            self.at += 1;
        }
        self.at - start
    }

    fn number(&mut self) -> Result<Value> {
        let start = self.at;
        if self.peek() == Some(b'-') {
            self.at += 1;
        }
        match self.peek() {
            Some(b'0') => self.at += 1,
            Some(b'1'..=b'9') => {
                self.digits();
            }
            _ => bail!("invalid JSON number"),
        }
        if self.peek() == Some(b'.') {
            self.at += 1;
            ensure!(self.digits() > 0, "invalid JSON number");
        }
        if matches!(self.peek(), Some(b'e' | b'E')) {
            self.at += 1;
            if matches!(self.peek(), Some(b'+' | b'-')) {
                self.at += 1;
            }
            ensure!(self.digits() > 0, "invalid JSON number");
        }
        Ok(Value::Number(Number::from_string_unchecked(
            self.text[start..self.at].into(),
        )))
    }

    fn string(&mut self) -> Result<String> {
        ensure!(self.peek() == Some(b'"'), "invalid JSON string");
        let start = self.at;
        let mut escaped = false;
        self.at += 1;
        loop {
            match self.peek() {
                Some(b'"') => break,
                Some(b'\\') => {
                    escaped = true;
                    self.at += 2;
                }
                Some(0x20..) => self.at += 1,
                _ => bail!("invalid JSON string"),
            }
        }
        self.at += 1;
        let token = &self.text[start..self.at];
        if escaped {
            // serde_json owns the escape and surrogate rules.
            return Ok(serde_json::from_str(token)?);
        }
        Ok(token[1..token.len() - 1].to_owned())
    }
}

/// Writes the canonical wire form: object keys in UTF-16 order, no
/// insignificant whitespace, and HTML-sensitive characters escaped.
pub(crate) fn encode(value: &Value) -> String {
    let mut output = String::new();
    write(value, &mut output, Form::Wire).expect("the wire form writes every value");
    output
}

/// Writes the exact canonical form of `value`, as Go's
/// `canonicalizeExactJSON` writes its wire form: every number is an exact
/// decimal without an exponent, and U+2028 and U+2029 stay unescaped.
/// Composite keys are built from this form. Like Go it refuses a form that
/// outgrows the budget of the wire form it comes from.
pub(crate) fn encode_exact(value: &Value) -> Result<String> {
    let limit = expansion_budget(encode(value).len());
    let mut output = String::new();
    write(value, &mut output, Form::Exact(limit))?;
    ensure!(output.len() <= limit, "canonical JSON exceeds limit");
    Ok(output)
}

/// Go's `exactCanonicalJSONBudget`: canonical JSON may reach eight times its
/// input plus a small constant, and never more than 16 MiB beyond it.
pub(crate) fn expansion_budget(input: usize) -> usize {
    input
        .saturating_mul(8)
        .saturating_add(1024)
        .min(input.saturating_add(16 << 20))
}

/// The two spellings of one value.
#[derive(Clone, Copy)]
enum Form {
    Wire,
    /// The exact canonical form, at most this many bytes long.
    Exact(usize),
}

fn write(value: &Value, output: &mut String, form: Form) -> Result<()> {
    match value {
        Value::Null => output.push_str("null"),
        Value::Bool(true) => output.push_str("true"),
        Value::Bool(false) => output.push_str("false"),
        Value::Number(number) => match form {
            Form::Wire => output.push_str(number.as_str()),
            Form::Exact(limit) => {
                let room = limit.saturating_sub(output.len());
                output.push_str(&exact_decimal(number.as_str(), room)?);
            }
        },
        Value::String(text) => quote(text, output, form),
        Value::Array(items) => {
            output.push('[');
            for (index, item) in items.iter().enumerate() {
                if index > 0 {
                    output.push(',');
                }
                write(item, output, form)?;
            }
            output.push(']');
        }
        Value::Object(fields) => {
            output.push('{');
            let mut member = |index: usize, key: &str, value: &Value| {
                if index > 0 {
                    output.push(',');
                }
                quote(key, output, form);
                output.push(':');
                write(value, output, form)
            };
            // The map iterates in UTF-8 byte order. That is UTF-16 order
            // too unless a key holds a character beyond the BMP.
            if fields
                .keys()
                .any(|key| key.bytes().any(|byte| byte >= 0xf0))
            {
                let mut ordered: Vec<_> = fields.iter().collect();
                ordered.sort_by(|(a, _), (b, _)| a.encode_utf16().cmp(b.encode_utf16()));
                for (index, (key, value)) in ordered.into_iter().enumerate() {
                    member(index, key, value)?;
                }
            } else {
                for (index, (key, value)) in fields.iter().enumerate() {
                    member(index, key, value)?;
                }
            }
            output.push('}');
        }
    }
    Ok(())
}

/// Writes a JSON number as an exact decimal without an exponent, refusing
/// before it materializes more than `limit` bytes.
pub(crate) fn exact_decimal(text: &str, limit: usize) -> Result<String> {
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
    // `scale` counts the digits behind the point, or below zero the zeros to
    // append. Zeros that end the fraction carry no value.
    let scale = fraction.len() as i64 - i64::from(exponent);
    let trailing = (digits.len() - digits.trim_end_matches('0').len()) as i64;
    let trailing = trailing.min(scale).max(0);
    let digits = &digits[..digits.len() - trailing as usize];
    let scale = scale - trailing;
    // An exponent only moves the point, so the growth is zero padding: zeros
    // behind the digits, or a point with the zeros that lead up to them.
    let padding = if scale <= 0 {
        scale.unsigned_abs()
    } else if scale as usize >= digits.len() {
        scale as u64 - digits.len() as u64 + 2
    } else {
        1
    };
    ensure!(
        (negative.len() + digits.len()) as u64 + padding <= limit as u64,
        "canonical JSON exceeds limit"
    );
    if scale <= 0 {
        return Ok(format!(
            "{negative}{digits}{}",
            "0".repeat((-scale) as usize)
        ));
    }
    let scale = scale as usize;
    if scale >= digits.len() {
        return Ok(format!(
            "{negative}0.{}{digits}",
            "0".repeat(scale - digits.len())
        ));
    }
    let (integer, decimal) = digits.split_at(digits.len() - scale);
    Ok(format!("{negative}{integer}.{decimal}"))
}

fn quote(text: &str, output: &mut String, form: Form) {
    // Go's exact canonical form leaves the line separators as they are.
    let separators = matches!(form, Form::Wire);
    output.push('"');
    let bytes = text.as_bytes();
    let mut start = 0;
    for (index, byte) in bytes.iter().enumerate() {
        let (escape, width) = match byte {
            b'"' => ("\\\"", 1),
            b'\\' => ("\\\\", 1),
            0x08 => ("\\b", 1),
            0x0c => ("\\f", 1),
            b'\n' => ("\\n", 1),
            b'\r' => ("\\r", 1),
            b'\t' => ("\\t", 1),
            b'<' => ("\\u003c", 1),
            b'>' => ("\\u003e", 1),
            b'&' => ("\\u0026", 1),
            0xe2 if separators && bytes[index..].starts_with(&[0xe2, 0x80, 0xa8]) => ("\\u2028", 3),
            0xe2 if separators && bytes[index..].starts_with(&[0xe2, 0x80, 0xa9]) => ("\\u2029", 3),
            0x00..=0x1f => ("", 1),
            _ => continue,
        };
        output.push_str(&text[start..index]);
        if escape.is_empty() {
            write!(output, "\\u{byte:04x}").expect("writing to String");
        } else {
            output.push_str(escape);
        }
        start = index + width;
    }
    output.push_str(&text[start..]);
    output.push('"');
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The previous encoder, kept as the reference for byte equality.
    fn reference(value: &Value) -> String {
        fn quote(value: &str) -> String {
            serde_json::to_string(value)
                .unwrap()
                .replace('<', r"\u003c")
                .replace('>', r"\u003e")
                .replace('&', r"\u0026")
                .replace('\u{2028}', "\\u2028")
                .replace('\u{2029}', "\\u2029")
        }
        match value {
            Value::Object(fields) => {
                let mut fields: Vec<_> = fields.iter().collect();
                fields.sort_by(|(a, _), (b, _)| a.encode_utf16().cmp(b.encode_utf16()));
                let fields: Vec<_> = fields
                    .into_iter()
                    .map(|(key, value)| format!("{}:{}", quote(key), reference(value)))
                    .collect();
                format!("{{{}}}", fields.join(","))
            }
            Value::Array(items) => {
                let items: Vec<_> = items.iter().map(reference).collect();
                format!("[{}]", items.join(","))
            }
            Value::String(value) => quote(value),
            _ => value.to_string(),
        }
    }

    #[test]
    fn reads_like_serde_and_writes_the_canonical_form() {
        let documents = [
            r#"{"b":[1,2.50,-0,1e+06,9007199254740993],"a":{"x":null,"y":[true,false]}}"#,
            "  [ ]  ",
            "{ }",
            r#""plain""#,
            r#""quote \" slash \\ \/ \b\f\n\r\t \u00e9 \ud83d\ude00 \u2028 <>&""#,
            "{\"\u{e000}\":1,\"\u{1f600}\":2,\"z\":3,\"\\u0000\":4,\"\u{7f}\u{2029}\":5}",
            "-12.5e-3",
            "0",
        ];
        for document in documents {
            let value = parse(document.as_bytes()).unwrap();
            let expected: Value = serde_json::from_str(document).unwrap();
            assert_eq!(value, expected, "{document}");
            assert_eq!(encode(&value), reference(&expected), "{document}");
        }
        // A number keeps its source spelling until a type normalizes it.
        assert_eq!(encode(&parse(b" -12.50E-3 ").unwrap()), "-12.50E-3");
    }

    #[test]
    fn exact_form_spells_numbers_out_within_the_budget_of_go() {
        let exact = |document: &str| encode_exact(&parse(document.as_bytes()).unwrap());
        assert_eq!(
            exact(r#"{"b":[1e+06,-2.50E-3,-0,12.0],"a":"<\u2028\u2029"}"#).unwrap(),
            "{\"a\":\"\\u003c\u{2028}\u{2029}\",\"b\":[1000000,-0.0025,0,12]}"
        );
        // A decimal is refused exactly when it is longer than the limit.
        for (number, decimal) in [
            ("1.234e-10", "0.0000000001234"),
            ("-1200e-2", "-12"),
            ("1e3", "1000"),
            ("123.45", "123.45"),
            ("0.50", "0.5"),
        ] {
            assert_eq!(exact_decimal(number, decimal.len()).unwrap(), decimal);
            assert!(
                exact_decimal(number, decimal.len() - 1).is_err(),
                "{number}"
            );
        }
        // Eight times the wire form and 1024 bytes: 1256 and 1312 here.
        let large = |count: usize| exact(&format!("[{}]", vec!["1e+308"; count].join(",")));
        assert_eq!(large(4).unwrap().len(), 1241);
        assert!(large(5).is_err());
    }

    #[test]
    fn rejects_what_the_wire_forbids() {
        for document in [
            "",
            "01",
            "1.",
            ".5",
            "-",
            "1e",
            "+1",
            "tru",
            "truex",
            "nulll",
            "[1,]",
            "[1 2]",
            "{\"a\":1,}",
            "{\"a\" 1}",
            "{a:1}",
            "{\"a\":1,\"a\":2}",
            "{\"a\":{\"b\":1,\"\\u0062\":2}}",
            "\"unterminated",
            "\"tab\there\"",
            "\"\\x\"",
            "\"\\ud800\"",
            "1 2",
            "[1]]",
            "\"a\"\"b\"",
        ] {
            assert!(parse(document.as_bytes()).is_err(), "accepted {document:?}");
        }
        assert!(parse(&[b'"', 0xff, b'"']).is_err());
        let nested = |depth: usize| format!("{}1{}", "[".repeat(depth), "]".repeat(depth));
        assert!(parse(nested(127).as_bytes()).is_ok());
        assert!(parse(nested(128).as_bytes()).is_err());
    }

    #[test]
    fn object_named_like_the_number_marker_stays_an_object() {
        let document = r#"{"$serde_json::private::Number":"12"}"#;
        let value = parse(document.as_bytes()).unwrap();
        assert!(value.is_object());
        assert_eq!(encode(&value), document);
    }

    #[test]
    fn deep_values_parse_in_linear_time() {
        // Every level used to copy and rescan its whole subtree.
        let mut document = "[".repeat(120);
        document.push('"');
        document.push_str(&"x".repeat(4 << 20));
        document.push('"');
        document.push_str(&"]".repeat(120));
        let started = std::time::Instant::now();
        let value = parse(document.as_bytes()).unwrap();
        assert!(started.elapsed() < std::time::Duration::from_millis(500));
        assert_eq!(encode(&value).len(), document.len());
    }
}
