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
    write(value, &mut output);
    output
}

fn write(value: &Value, output: &mut String) {
    match value {
        Value::Null => output.push_str("null"),
        Value::Bool(true) => output.push_str("true"),
        Value::Bool(false) => output.push_str("false"),
        Value::Number(number) => output.push_str(number.as_str()),
        Value::String(text) => quote(text, output),
        Value::Array(items) => {
            output.push('[');
            for (index, item) in items.iter().enumerate() {
                if index > 0 {
                    output.push(',');
                }
                write(item, output);
            }
            output.push(']');
        }
        Value::Object(fields) => {
            output.push('{');
            let mut member = |index: usize, key: &str, value: &Value| {
                if index > 0 {
                    output.push(',');
                }
                quote(key, output);
                output.push(':');
                write(value, output);
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
                    member(index, key, value);
                }
            } else {
                for (index, (key, value)) in fields.iter().enumerate() {
                    member(index, key, value);
                }
            }
            output.push('}');
        }
    }
}

fn quote(text: &str, output: &mut String) {
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
            0xe2 if bytes[index..].starts_with(&[0xe2, 0x80, 0xa8]) => ("\\u2028", 3),
            0xe2 if bytes[index..].starts_with(&[0xe2, 0x80, 0xa9]) => ("\\u2029", 3),
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
