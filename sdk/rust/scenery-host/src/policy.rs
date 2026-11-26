use std::cmp::Ordering;

use anyhow::{Context, Result, bail, ensure};
use serde_json::Value;

#[derive(PartialEq)]
enum Operand {
    Json(Value),
    Number(Decimal),
    String(Vec<u8>),
    List(Vec<Operand>),
}

#[derive(PartialEq)]
struct Decimal {
    negative: bool,
    digits: String,
    point: i64,
}

impl Decimal {
    /// Reads a decimal like Go's `big.Rat.SetString`: a leading zero is a
    /// decimal digit, never an octal prefix, and the decimal exponent of the
    /// value may not exceed one million in either direction.
    fn parse(source: &str) -> Result<Self> {
        let negative = source.starts_with('-');
        let source = source.strip_prefix('-').unwrap_or(source);
        let (mantissa, exponent) = source
            .split_once(['e', 'E'])
            .map_or(Ok((source, 0_i64)), |(m, e)| e.parse().map(|e| (m, e)))?;
        let (integer, fraction) = mantissa.split_once('.').unwrap_or((mantissa, ""));
        ensure!(
            !integer.is_empty()
                && integer
                    .bytes()
                    .chain(fraction.bytes())
                    .all(|c| c.is_ascii_digit()),
            "invalid authorization number"
        );
        let digits = format!("{integer}{fraction}");
        let trim = digits.trim_start_matches('0');
        if trim.is_empty() {
            return Ok(Self {
                negative: false,
                digits: String::new(),
                point: 0,
            });
        }
        ensure!(
            exponent
                .checked_sub(fraction.len() as i64)
                .is_some_and(|scale| scale.unsigned_abs() <= 1_000_000),
            "authorization number exponent exceeds range"
        );
        let point = exponent + integer.len() as i64 - (digits.len() - trim.len()) as i64;
        Ok(Self {
            negative,
            digits: trim.trim_end_matches('0').into(),
            point,
        })
    }

    fn compare(&self, other: &Self) -> Ordering {
        if self.digits.is_empty() {
            return if other.digits.is_empty() {
                Ordering::Equal
            } else if other.negative {
                Ordering::Greater
            } else {
                Ordering::Less
            };
        }
        if other.digits.is_empty() {
            return other.compare(self).reverse();
        }
        match self.negative.cmp(&other.negative) {
            Ordering::Equal => {
                let order = (self.point, &self.digits).cmp(&(other.point, &other.digits));
                if self.negative {
                    order.reverse()
                } else {
                    order
                }
            }
            order => order.reverse(),
        }
    }
}

/// Orders two decimal numbers exactly, without a policy expression.
pub(crate) fn compare_numbers(left: &str, right: &str) -> Result<Ordering> {
    Ok(Decimal::parse(left)?.compare(&Decimal::parse(right)?))
}

impl Operand {
    fn from_json(value: Value) -> Self {
        match value {
            Value::String(text) => Self::String(text.into_bytes()),
            Value::Array(items) => Self::List(items.into_iter().map(Self::from_json).collect()),
            value => Self::Json(value),
        }
    }

    fn boolean(self) -> Result<bool> {
        match self {
            Self::Json(Value::Bool(value)) => Ok(value),
            _ => bail!("authorization requires a boolean"),
        }
    }

    fn number(&self) -> Result<Option<Decimal>> {
        match self {
            Self::Number(value) => Ok(Some(Decimal {
                negative: value.negative,
                digits: value.digits.clone(),
                point: value.point,
            })),
            Self::Json(Value::Number(value)) => Decimal::parse(&value.to_string()).map(Some),
            _ => Ok(None),
        }
    }

    fn equal(&self, other: &Self) -> Result<bool> {
        if let (Some(left), Some(right)) = (self.number()?, other.number()?) {
            return Ok(left.compare(&right).is_eq());
        }
        match (self, other) {
            (Self::Json(Value::Null), _) | (_, Self::Json(Value::Null)) => Ok(self == other),
            (Self::Json(left), Self::Json(right))
                if std::mem::discriminant(left) == std::mem::discriminant(right) =>
            {
                Ok(left == right)
            }
            (Self::List(left), Self::List(right)) => Ok(left == right),
            (Self::String(left), Self::String(right)) => Ok(left == right),
            _ => bail!("incompatible authorization operands"),
        }
    }

    fn order(&self, other: &Self) -> Result<Ordering> {
        if let (Some(left), Some(right)) = (self.number()?, other.number()?) {
            return Ok(left.compare(&right));
        }
        match (self, other) {
            (Self::String(left), Self::String(right)) => Ok(left.cmp(right)),
            _ => bail!("authorization ordering requires numbers or strings"),
        }
    }
}

struct Parser<'a> {
    source: &'a str,
    variables: &'a Value,
    depth: usize,
}

pub(crate) fn evaluate(source: &str, variables: &Value) -> Result<bool> {
    let mut parser = Parser {
        source,
        variables,
        depth: 0,
    };
    let result = parser.expression(0)?.boolean()?;
    ensure!(
        skip_space(parser.source).is_empty(),
        "unexpected authorization token"
    );
    Ok(result)
}

/// Go's lexer skips single bytes, so only ASCII whitespace separates tokens.
fn skip_space(source: &str) -> &str {
    source.trim_start_matches([' ', '\t', '\n', '\r', '\x0b', '\x0c'])
}

impl Parser<'_> {
    fn take(&mut self, prefix: &str) -> bool {
        self.source = skip_space(self.source);
        if let Some(rest) = self.source.strip_prefix(prefix) {
            self.source = rest;
            return true;
        }
        false
    }

    fn expression(&mut self, minimum: u8) -> Result<Operand> {
        const OPS: [(&str, u8); 8] = [
            ("||", 1),
            ("&&", 2),
            ("==", 3),
            ("!=", 3),
            ("<=", 3),
            (">=", 3),
            ("<", 3),
            (">", 3),
        ];
        self.depth += 1;
        ensure!(
            self.depth < 128,
            "authorization expression is too deeply nested"
        );
        let mut left = self.primary()?;
        let mut compared = false;
        loop {
            self.source = skip_space(self.source);
            let Some((op, precedence)) = OPS
                .into_iter()
                .find(|(op, precedence)| *precedence >= minimum && self.source.starts_with(op))
            else {
                break;
            };
            ensure!(!(precedence == 3 && compared), "unexpected comparison");
            self.take(op);
            let right = self.expression(precedence + 1)?;
            let value = match op {
                "||" => left.boolean()? | right.boolean()?,
                "&&" => left.boolean()? & right.boolean()?,
                "==" => left.equal(&right)?,
                "!=" => !left.equal(&right)?,
                "<=" => !left.order(&right)?.is_gt(),
                ">=" => !left.order(&right)?.is_lt(),
                "<" => left.order(&right)?.is_lt(),
                ">" => left.order(&right)?.is_gt(),
                _ => unreachable!(),
            };
            left = Operand::Json(Value::Bool(value));
            compared = precedence == 3;
        }
        self.depth -= 1;
        Ok(left)
    }

    fn primary(&mut self) -> Result<Operand> {
        if self.take("!") {
            return Ok(Operand::Json(Value::Bool(!self.expression(4)?.boolean()?)));
        }
        if self.take("(") {
            let value = self.expression(0)?;
            ensure!(self.take(")"), "unclosed authorization parentheses");
            return Ok(value);
        }
        if self.take("[") {
            let mut values = Vec::new();
            if !self.take("]") {
                loop {
                    values.push(self.expression(0)?);
                    if self.take("]") {
                        break;
                    }
                    ensure!(self.take(","), "authorization list requires comma");
                }
            }
            return Ok(Operand::List(values));
        }
        let first = self
            .source
            .as_bytes()
            .first()
            .context("missing authorization operand")?;
        if *first == b'"' {
            let mut escaped = false;
            for (offset, c) in self.source.bytes().enumerate().skip(1) {
                if c == b'"' && !escaped {
                    let (raw, rest) = self.source.split_at(offset + 1);
                    self.source = rest;
                    return Ok(Operand::String(unquote(raw)?));
                }
                escaped = c == b'\\' && !escaped;
            }
            bail!("unterminated authorization string");
        }
        if first.is_ascii_digit()
            || *first == b'-'
                && self
                    .source
                    .as_bytes()
                    .get(1)
                    .is_some_and(u8::is_ascii_digit)
        {
            let len = self
                .source
                .bytes()
                .take_while(|c| c.is_ascii_digit() || b".eE+-".contains(c))
                .count();
            let (raw, rest) = self.source.split_at(len);
            self.source = rest;
            return Ok(Operand::Number(Decimal::parse(raw)?));
        }
        ensure!(
            first.is_ascii_alphabetic() || *first == b'_',
            "invalid authorization operand"
        );
        let len = self
            .source
            .bytes()
            .take_while(|c| c.is_ascii_alphanumeric() || b"_.".contains(c))
            .count();
        let (name, rest) = self.source.split_at(len);
        self.source = rest;
        match name {
            "true" => Ok(Operand::Json(Value::Bool(true))),
            "false" => Ok(Operand::Json(Value::Bool(false))),
            "null" => Ok(Operand::Json(Value::Null)),
            _ if self.take("(") => {
                ensure!(name == "contains", "unsupported authorization function");
                let collection = self.expression(0)?;
                ensure!(self.take(","), "contains requires two arguments");
                let needle = self.expression(0)?;
                ensure!(self.take(")"), "contains requires two arguments");
                let Operand::List(items) = collection else {
                    bail!("contains requires a list")
                };
                for item in items {
                    if item.equal(&needle)? {
                        return Ok(Operand::Json(Value::Bool(true)));
                    }
                }
                Ok(Operand::Json(Value::Bool(false)))
            }
            _ => {
                ensure!(
                    name.contains('.'),
                    "authorization variable requires a root and a field"
                );
                let mut current = self.variables;
                for part in name.split('.') {
                    current = current
                        .as_object()
                        .and_then(|object| object.get(part))
                        .context("authorization variable is unavailable")?;
                }
                Ok(Operand::from_json(current.clone()))
            }
        }
    }
}

fn unquote(raw: &str) -> Result<Vec<u8>> {
    let mut result = Vec::new();
    let mut bytes = raw.as_bytes()[1..raw.len() - 1].iter().copied();
    while let Some(byte) = bytes.next() {
        if byte != b'\\' {
            ensure!(byte != b'\n', "invalid authorization string");
            result.push(byte);
            continue;
        }
        match bytes.next().context("incomplete authorization escape")? {
            b'a' => result.push(7),
            b'b' => result.push(8),
            b'f' => result.push(12),
            b'n' => result.push(b'\n'),
            b'r' => result.push(b'\r'),
            b't' => result.push(b'\t'),
            b'v' => result.push(11),
            value @ (b'"' | b'\\') => result.push(value),
            escape @ (b'x' | b'u' | b'U' | b'0'..=b'7') => {
                let (radix, count, mut code) = match escape {
                    b'x' => (16, 2, 0),
                    b'u' => (16, 4, 0),
                    b'U' => (16, 8, 0),
                    _ => (8, 2, u32::from(escape - b'0')),
                };
                for _ in 0..count {
                    let digit = bytes
                        .next()
                        .and_then(|v| char::from(v).to_digit(radix))
                        .context("invalid authorization escape")?;
                    code = code * radix + digit;
                }
                if matches!(escape, b'u' | b'U') {
                    let mut buffer = [0; 4];
                    let value = char::from_u32(code).context("invalid authorization character")?;
                    result.extend_from_slice(value.encode_utf8(&mut buffer).as_bytes());
                } else {
                    result.push(u8::try_from(code)?);
                }
            }
            _ => bail!("invalid authorization escape"),
        }
    }
    Ok(result)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn evaluates_operators_without_coercion_or_short_circuiting() {
        let variables =
            json!({"input": {"items": ["owner"], "count": 9007199254740993_u64, "empty": []}});
        for expression in [
            "true || false && false",
            "!(false || !true)",
            "input.count == 9007199254740993.00",
            r#""\xFF" == "\377" && "\377" > "é""#,
            "010 == 10 && 08 == 8 && -010 == -10 && 01.5 == 1.5",
            "1e1000000 > 1 && 1.5e1000001 > 1 && 0e9999999 == 0 && 1. == 1 && 1.e2 == 100",
            "!(010 == 8) && !(-010 == -8)",
            "input.count != 9007199254740992",
            "-0.001 >= -1e-3 && 0 < 1e-99 && 10 >= 9",
            "\"abc\" <= \"b\" && \"a\" < \"b\" && \"b\" > \"a\"",
            r#"contains(input.items, "\x6fwner") && contains([true, null], true)"#,
            "input.empty == [] && !contains([], null) && null != true",
            r#""\xFF" == "\xFF" && "\xFF" > "é""#,
        ] {
            assert!(evaluate(expression, &variables).unwrap(), "{expression}");
        }
        for expression in [
            "true || input.missing",
            "false && 10",
            "!(1)",
            "1 < 2 < 3",
            "1 == \"1\"",
            "false < true",
            "null >= 0",
            "contains(\"a\", \"a\")",
            "contains([1], \"1\")",
            "contains([1], 1, 1)",
            "[1,]",
            "bogus(1)",
            "true = true",
            "1.2.3 == 1",
            "input",
            "1e1000001 == 1",
            "10e-1000001 == 1",
            "1e9223372036854775806 == 1",
            "true &&\u{a0}true",
            "true &&\u{85}true",
            "true\u{2003}&& true",
            "- 1 == 1",
        ] {
            assert!(evaluate(expression, &variables).is_err(), "{expression}");
        }
    }
}
