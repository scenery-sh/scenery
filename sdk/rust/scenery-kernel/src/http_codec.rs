use std::cmp::Reverse;
use std::collections::BTreeMap;
use std::io::{Read, Write};

use anyhow::{Result, bail, ensure};
use base64::Engine;
use base64::engine::general_purpose::STANDARD;
use bytes::Bytes;
use flate2::{Compression, bufread::GzDecoder, write::GzEncoder};
use http_body_util::BodyExt;
use hyper::body::Incoming;
use hyper::header::{HeaderName, HeaderValue};
use hyper::{HeaderMap, Request, StatusCode};
use percent_encoding::percent_decode_str;
use serde_json::{Map, Value, json};

use crate::abi::Outcome;
use crate::auth::Claims;
use crate::host::{Call, Fault, Reply, body, empty, header};
use crate::plan::{Mapping, RequestPlan, ResponsePlan, Selection};
use crate::transport::{GzipStream, read_request};
use crate::values;

pub(crate) async fn decode_request(
    request: Request<Incoming>,
    path: &BTreeMap<&str, &str>,
    plan: &RequestPlan,
    call: &mut Call,
) -> Result<Value, Fault> {
    let mut mapped = Map::new();
    for mapping in &plan.mappings {
        let values = mapping_values(&request, path, mapping).map_err(|_| Fault::invalid())?;
        if values.is_empty() && mapping.optional {
            continue;
        }
        if mapped
            .insert(
                mapping.target.clone(),
                mapped_json(mapping, values).map_err(|_| Fault::invalid())?,
            )
            .is_some()
        {
            return Err(Fault::invalid());
        }
    }
    for mapping in &plan.context_mappings {
        let context = context_value(&mapping.source, call);
        if mapped.insert(mapping.target.clone(), context).is_some() {
            return Err(Fault::invalid());
        }
    }
    if let Some(body) = &plan.body {
        call.has_http_body = true;
        let payload = decode_body(request, body).await?;
        if matches!(body.codec.as_str(), "json" | "problem_json") {
            let encoded_payload = values::encode(&payload);
            if encoded_payload.len() <= 256 << 10 {
                call.raw_body = Some(encoded_payload);
            }
        }
        if !body.target.is_empty() {
            if mapped.insert(body.target.clone(), payload).is_some() {
                return Err(Fault::invalid());
            }
        } else if mapped.is_empty() && body.include.is_empty() && body.except.is_empty() {
            return Ok(payload);
        } else {
            let Value::Object(fields) = payload else {
                return Err(Fault::invalid());
            };
            for (name, value) in fields {
                if (!body.include.is_empty() && !body.include.contains(&name))
                    || body.except.contains(&name)
                    || mapped.insert(name, value).is_some()
                {
                    return Err(Fault::invalid());
                }
            }
        }
    } else {
        crate::http_timeout::request_body_finished(request.extensions());
    }
    Ok(Value::Object(mapped))
}

fn context_value(source: &str, call: &Call) -> Value {
    match source {
        "principal.id" | "principal.uid" => {
            json!(call.principal.as_ref().map_or("", |p| p.user.as_str()))
        }
        "principal.data" => call.principal.as_ref().map_or(Value::Null, Claims::data),
        "context.method" => json!(call.method),
        "context.path" => json!(call.path),
        "context.invocation_id" => json!(call.invocation_id),
        "context.trace_id" => json!(call.trace_id),
        "context.caller_binding" => json!(call.caller),
        "context.deadline" => json!(call.wire_deadline()),
        "context.execution_id" => json!(call.execution_id.as_deref().unwrap_or_default()),
        "context.locale" | "context.deployment" => json!(""),
        _ => {
            let path = source.strip_prefix("principal.").unwrap_or(source);
            let path = path.strip_prefix("data.").unwrap_or(path);
            let mut result = call.principal.as_ref().map_or(Value::Null, Claims::data);
            for item in path.split('.') {
                let pascal: String = item
                    .split('_')
                    .map(|part| {
                        let mut characters = part.chars();
                        characters
                            .next()
                            .map(|first| first.to_uppercase().to_string() + characters.as_str())
                            .unwrap_or_default()
                    })
                    .collect();
                let pascal = pascal.replace("Id", "ID");
                result = result.get(&pascal).cloned().unwrap_or(Value::Null);
            }
            result
        }
    }
}

/// Reports whether a decoded path segment stays one ordinary segment.
fn safe_segment(value: &str) -> bool {
    !matches!(value, "." | "..") && !value.contains(['/', '\\', '\0'])
}

pub(crate) fn decode_piece(value: &str) -> Result<String> {
    for (index, byte) in value.bytes().enumerate() {
        if byte == b'%' {
            ensure!(
                value
                    .as_bytes()
                    .get(index + 1..index + 3)
                    .is_some_and(|digits| digits.iter().all(u8::is_ascii_hexdigit)),
                "invalid escape"
            );
        }
    }
    Ok(percent_decode_str(value).decode_utf8()?.into_owned())
}

/// Decodes a path parameter into one safe segment, or a path tail into the
/// remainder of the path, whose every segment is decoded once and is safe.
fn path_values(path: &BTreeMap<&str, &str>, mapping: &Mapping) -> Result<Vec<String>> {
    let Some(value) = path.get(mapping.name.as_str()) else {
        return Ok(vec![]);
    };
    if mapping.source == "path" {
        let value = decode_piece(value)?;
        ensure!(safe_segment(&value), "invalid path segment");
        return Ok(vec![value]);
    }
    if value.is_empty() {
        // The compiler pins what an empty tail means to its type.
        return match mapping.ty.trim() {
            "string" => Ok(vec![String::new()]),
            "optional(relative_path)" => Ok(vec![]),
            _ => bail!("empty path tail"),
        };
    }
    let mut segments = Vec::new();
    for segment in value.split('/') {
        ensure!(
            !segment.is_empty() && !segment.contains('\\'),
            "invalid path-tail segment"
        );
        let decoded = decode_piece(segment)?;
        // A segment that decodes twice into a separator or a dot segment
        // would escape a handler that decodes again.
        ensure!(
            safe_segment(&decoded)
                && decode_piece(&decoded).map_or(true, |twice| safe_segment(&twice)),
            "invalid path-tail segment"
        );
        segments.push(decoded);
    }
    Ok(vec![segments.join("/")])
}

fn mapping_values(
    request: &Request<Incoming>,
    path: &BTreeMap<&str, &str>,
    mapping: &Mapping,
) -> Result<Vec<String>> {
    match mapping.source.as_str() {
        "path" | "path_tail" => path_values(path, mapping),
        "query" => {
            let mut values = Vec::new();
            for pair in request
                .uri()
                .query()
                .unwrap_or_default()
                .split('&')
                .filter(|pair| !pair.is_empty())
            {
                let (name, raw) = pair.split_once('=').unwrap_or((pair, ""));
                if decode_piece(name)? == mapping.name {
                    if mapping.encoding == "comma" {
                        for value in raw.split(',') {
                            values.push(decode_piece(value)?);
                        }
                    } else {
                        values.push(decode_piece(raw)?);
                    }
                }
            }
            Ok(values)
        }
        "header" => {
            let mut values = Vec::new();
            for value in request.headers().get_all(mapping.name.as_str()) {
                let value = value.to_str()?;
                if mapping.encoding == "comma" {
                    values.extend(value.split(',').map(|v| v.trim_matches([' ', '\t']).into()));
                } else {
                    values.push(value.trim_matches([' ', '\t']).into());
                }
            }
            Ok(values)
        }
        "cookie" => request
            .headers()
            .get_all("cookie")
            .iter()
            .map(|value| value.to_str().map_err(anyhow::Error::from))
            .collect::<Result<Vec<_>>>()?
            .into_iter()
            .flat_map(|header| header.split(';'))
            .filter_map(|part| part.trim().split_once('='))
            .filter(|(key, _)| *key == mapping.name)
            .map(|(_, value)| decode_piece(value))
            .collect(),
        _ => bail!("unsupported mapping"),
    }
}

fn mapped_json(mapping: &Mapping, values: Vec<String>) -> Result<Value> {
    if mapping.encoding == "json" {
        ensure!(values.len() == 1, "invalid JSON mapping");
        return crate::values::parse(values[0].as_bytes());
    }
    let mut ty = mapping.ty.trim();
    while let Some(inner) = ty
        .strip_prefix("optional(")
        .or_else(|| ty.strip_prefix("nullable("))
        .and_then(|t| t.strip_suffix(')'))
    {
        ty = inner.trim();
    }
    let scalar = |ty, text: String| -> Result<Value> {
        ensure!(
            mapping.enum_values.is_empty() || mapping.enum_values.contains(&text),
            "unknown enum value"
        );
        scalar_text(ty, text)
    };
    if let Some(inner) = ty
        .strip_prefix("list(")
        .or_else(|| ty.strip_prefix("set("))
        .and_then(|t| t.strip_suffix(')'))
    {
        Ok(Value::Array(
            values
                .into_iter()
                .map(|value| scalar(inner.trim(), value))
                .collect::<Result<_>>()?,
        ))
    } else {
        ensure!(values.len() == 1, "scalar mapping requires one value");
        scalar(ty, values.into_iter().next().expect("one value"))
    }
}

fn scalar_text(ty: &str, text: String) -> Result<Value> {
    match ty {
        "bool" => match text.as_str() {
            "true" => Ok(Value::Bool(true)),
            "false" => Ok(Value::Bool(false)),
            _ => bail!("invalid boolean"),
        },
        "int32" => {
            let value: i32 = text.parse()?;
            ensure!(value.to_string() == text, "noncanonical integer");
            Ok(json!(value))
        }
        "uint32" => {
            let value: u32 = text.parse()?;
            ensure!(value.to_string() == text, "noncanonical integer");
            Ok(json!(value))
        }
        "float32" | "float64" => {
            let value: serde_json::Number = text.parse()?;
            ensure!(value.as_f64().is_some_and(f64::is_finite), "invalid float");
            Ok(Value::Number(value))
        }
        _ => Ok(Value::String(text)),
    }
}

fn unsupported_media() -> Fault {
    Fault::new(
        415,
        "transport.unsupported_media_type",
        "unsupported request content type",
    )
}

/// Reads the declared media type of a request body. A body without one, or
/// in a charset other than UTF-8, is never decoded.
pub(crate) fn media_type(headers: &HeaderMap) -> Result<mime_guess::mime::Mime, Fault> {
    let mime = headers
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .map(str::parse::<mime_guess::mime::Mime>)
        .transpose()
        .map_err(|_| Fault::invalid())?
        .ok_or_else(unsupported_media)?;
    if mime
        .get_param("charset")
        .is_some_and(|value| !value.as_str().eq_ignore_ascii_case("utf-8"))
    {
        return Err(Fault::invalid());
    }
    Ok(mime)
}

/// Requires a JSON request body. Browsers can send other simple media types
/// cross-site without a preflight, so this is also the CSRF barrier of the
/// framework's own JSON routes.
pub(crate) fn require_json(headers: &HeaderMap) -> Result<(), Fault> {
    if media_type(headers)?.essence_str() == "application/json" {
        Ok(())
    } else {
        Err(unsupported_media())
    }
}

async fn decode_body(
    request: Request<Incoming>,
    plan: &crate::plan::BodyPlan,
) -> Result<Value, Fault> {
    let mime = media_type(request.headers())?;
    let default = match plan.codec.as_str() {
        "json" => "application/json",
        "problem_json" => "application/problem+json",
        "text" => "text/plain",
        "bytes" => "application/octet-stream",
        "form" => "application/x-www-form-urlencoded",
        "multipart" => "multipart/form-data",
        _ => return Err(Fault::internal("unsupported HTTP body codec")),
    };
    if !(if plan.accepted_media_types.is_empty() {
        request_media_matches(&mime, default)
    } else {
        plan.accepted_media_types
            .iter()
            .any(|value| request_media_matches(&mime, value))
    }) {
        return Err(unsupported_media());
    }
    let mut encodings = Vec::new();
    for value in request.headers().get_all("content-encoding") {
        let text = value.to_str().map_err(|_| Fault::invalid())?;
        for part in text
            .split(',')
            .map(str::trim)
            .filter(|part| !part.is_empty())
        {
            encodings.push(part.to_ascii_lowercase());
        }
    }
    let encoding = match encodings.as_slice() {
        [] => "identity",
        [single] => single.as_str(),
        _ => return Err(Fault::invalid()),
    };
    if encoding != "identity"
        && !plan
            .supported_content_encoding
            .iter()
            .any(|item| item.trim().eq_ignore_ascii_case(encoding))
    {
        return Err(Fault::invalid());
    }
    let limit = |value, default| if value > 0 { value } else { default };
    let compressed = limit(plan.max_compressed_bytes, 8 << 20);
    let decompressed = limit(plan.max_decompressed_bytes, 16 << 20);
    let mut payload = read_request(request, compressed)
        .await
        .map_err(|_| Fault::invalid())?;
    if encoding == "gzip" {
        let mut reader = payload.as_ref();
        let mut output = Vec::new();
        GzDecoder::new(&mut reader)
            .take(decompressed as u64 + 1)
            .read_to_end(&mut output)
            .map_err(|_| Fault::invalid())?;
        if !reader.is_empty() {
            return Err(Fault::invalid());
        }
        payload = output.into();
    } else if encoding != "identity" {
        return Err(Fault::invalid());
    }
    if payload.len() > decompressed {
        return Err(Fault::invalid());
    }
    match plan.codec.as_str() {
        "json" | "problem_json" => values::parse(&payload).map_err(|_| Fault::invalid()),
        "text" => String::from_utf8(payload.to_vec())
            .map(Value::String)
            .map_err(|_| Fault::invalid()),
        "bytes" => Ok(json!(STANDARD.encode(payload))),
        "form" => {
            let text = std::str::from_utf8(&payload).map_err(|_| Fault::invalid())?;
            let mut form = values::query_pairs(Some(text));
            let mut result = Map::new();
            for field in &plan.fields {
                match form.remove(&field.name) {
                    Some(values) => {
                        result.insert(
                            field.target.clone(),
                            mapped_json(field, values).map_err(|_| Fault::invalid())?,
                        );
                    }
                    None if field.optional => {}
                    None => return Err(Fault::invalid()),
                }
            }
            if !form.is_empty() {
                return Err(Fault::invalid());
            }
            Ok(Value::Object(result))
        }
        "multipart" => {
            let boundary = mime
                .get_param("boundary")
                .map(|value| value.as_str())
                .ok_or_else(Fault::invalid)?;
            multipart(payload, boundary, plan).await
        }
        _ => Err(Fault::new(
            415,
            "transport.unsupported_media_type",
            "unsupported request body",
        )),
    }
}

async fn multipart(
    payload: Bytes,
    boundary: &str,
    plan: &crate::plan::BodyPlan,
) -> Result<Value, Fault> {
    let input = futures_util::stream::once(async move { Ok::<_, std::io::Error>(payload) });
    let mut multipart = multer::Multipart::new(input, boundary);
    let mut result: BTreeMap<&str, Vec<Value>> = BTreeMap::new();
    let limit = if plan.max_multipart_parts == 0 {
        128
    } else {
        plan.max_multipart_parts
    };
    let mut seen = 0;
    while let Some(mut field) = multipart.next_field().await.map_err(|_| Fault::invalid())? {
        seen += 1;
        if seen > limit {
            return Err(Fault::invalid());
        }
        let name = field.name().ok_or_else(Fault::invalid)?;
        let spec = plan
            .multipart_parts
            .iter()
            .find(|part| part.name == name)
            .ok_or_else(Fault::invalid)?;
        let data = result.entry(spec.name.as_str()).or_default();
        if !spec.multiple && !data.is_empty() {
            return Err(Fault::invalid());
        }
        let filename = field
            .file_name()
            .map(|filename| filename.rsplit('/').next().unwrap_or_default().to_owned());
        if (spec.kind == "file") != filename.as_ref().is_some_and(|file| !file.is_empty()) {
            return Err(Fault::invalid());
        }
        let media = field
            .content_type()
            .map_or("text/plain", |ty| ty.essence_str())
            .to_owned();
        if !spec.media_types.is_empty()
            && !spec
                .media_types
                .iter()
                .any(|allowed| media_matches(allowed, &media))
        {
            return Err(Fault::invalid());
        }
        let mut bytes = Vec::new();
        let max_bytes = if spec.max_bytes == 0 {
            8 << 20
        } else {
            spec.max_bytes
        };
        while let Some(chunk) = field.chunk().await.map_err(|_| Fault::invalid())? {
            if bytes.len().saturating_add(chunk.len()) > max_bytes {
                return Err(Fault::invalid());
            }
            bytes.extend_from_slice(&chunk);
        }
        let value = match spec.kind.as_str() {
            "text" => scalar_text(
                &spec.ty,
                String::from_utf8(bytes).map_err(|_| Fault::invalid())?,
            )
            .map_err(|_| Fault::invalid())?,
            "bytes" => json!(STANDARD.encode(bytes)),
            "file" if spec.retain_filename => {
                json!({"bytes": STANDARD.encode(bytes), "filename": filename, "media_type": media})
            }
            "file" => json!(STANDARD.encode(bytes)),
            _ => return Err(Fault::internal("invalid multipart part projection")),
        };
        data.push(value);
    }
    let mut fields = Map::new();
    for spec in &plan.multipart_parts {
        let target = if spec.target.is_empty() {
            &spec.name
        } else {
            &spec.target
        };
        match result.remove(spec.name.as_str()) {
            Some(list) if spec.multiple => {
                fields.insert(target.clone(), Value::Array(list));
            }
            Some(mut list) => {
                fields.insert(target.clone(), list.pop().expect("part has a value"));
            }
            None if spec.optional => {}
            None => return Err(Fault::invalid()),
        }
    }
    Ok(Value::Object(fields))
}

fn normalized_media_params(
    mime: &mime_guess::mime::Mime,
    ignore_boundary: bool,
) -> BTreeMap<String, String> {
    mime.params()
        .filter_map(|(name, value)| {
            let key = name.as_str().trim().to_ascii_lowercase();
            if ignore_boundary && key == "boundary" {
                return None;
            }
            let val = value.as_str().trim();
            if key == "charset" {
                let lower = val.to_ascii_lowercase();
                return (!lower.is_empty() && lower != "utf-8").then_some((key, lower));
            }
            Some((key, val.to_owned()))
        })
        .collect()
}

fn type_matches(range: &mime_guess::mime::Mime, target: &mime_guess::mime::Mime) -> Option<u8> {
    let (major, minor) = range.essence_str().split_once('/')?;
    let (target_major, target_minor) = target.essence_str().split_once('/')?;
    if (major == "*" && minor != "*")
        || (major != "*" && !major.eq_ignore_ascii_case(target_major))
        || (minor != "*" && !minor.eq_ignore_ascii_case(target_minor))
    {
        return None;
    }
    Some(if major == "*" {
        0
    } else if minor == "*" {
        1
    } else {
        2
    })
}

fn media_matches(pattern: &str, media: &str) -> bool {
    pattern
        .trim()
        .parse::<mime_guess::mime::Mime>()
        .ok()
        .zip(media.trim().parse::<mime_guess::mime::Mime>().ok())
        .is_some_and(|(range, target)| type_matches(&range, &target).is_some())
}

fn request_media_matches(actual: &mime_guess::mime::Mime, pattern: &str) -> bool {
    let Ok(expected) = pattern.trim().parse::<mime_guess::mime::Mime>() else {
        return false;
    };
    let ignore_boundary = actual
        .essence_str()
        .eq_ignore_ascii_case("multipart/form-data");
    type_matches(&expected, actual).is_some()
        && normalized_media_params(actual, ignore_boundary)
            == normalized_media_params(&expected, ignore_boundary)
}

pub(crate) fn negotiate_media<'a>(headers: &HeaderMap, produced: &'a [String]) -> Option<&'a str> {
    if produced.is_empty() {
        return None;
    }
    let mut accept_parts = Vec::new();
    for raw in headers.get_all("accept") {
        let text = raw.to_str().ok()?.trim();
        if !text.is_empty() {
            accept_parts.push(text);
        }
    }
    if accept_parts.is_empty() {
        return Some(produced[0].as_str());
    }
    let parsed_produced: Vec<mime_guess::mime::Mime> = produced
        .iter()
        .map(|c| c.parse())
        .collect::<std::result::Result<_, _>>()
        .ok()?;
    let mut best: Option<(&str, f64, u8, usize)> = None;
    for raw_range in accept_parts.join(",").split(',') {
        let range = raw_range.trim().parse::<mime_guess::mime::Mime>().ok()?;
        let weight = match range.get_param("q") {
            Some(q) => {
                Some(q.as_str().trim().parse::<f64>().ok()?).filter(|v| (0.0..=1.0).contains(v))?
            }
            None => 1.0,
        };
        let (major, minor) = range.essence_str().split_once('/')?;
        if major == "*" && minor != "*" {
            return None;
        }
        if weight == 0.0 {
            continue;
        }
        let mut accepted = normalized_media_params(&range, false);
        accepted.remove("q");
        for (order, (candidate, target)) in produced.iter().zip(&parsed_produced).enumerate() {
            let Some(spec) = type_matches(&range, target) else {
                continue;
            };
            let prod_params = normalized_media_params(target, false);
            if !accepted.iter().all(|(k, v)| prod_params.get(k) == Some(v)) {
                continue;
            }
            let better = best.is_none_or(|(bm, bw, bs, bo)| {
                (weight, spec, Reverse(order)) > (bw, bs, Reverse(bo))
                    || ((weight, spec, order) == (bw, bs, bo) && candidate.as_str() < bm)
            });
            if better {
                best = Some((candidate.as_str(), weight, spec, order));
            }
        }
    }
    best.map(|(candidate, _, _, _)| candidate)
}

fn selection(payload: &Value, select: &Selection) -> Result<Option<Value>, Fault> {
    let mut value = payload;
    for (index, part) in select.path.iter().enumerate() {
        match value.get(part) {
            Some(next) => value = next,
            None if index + 1 == select.path.len() && select.default_empty => {
                return Ok(Some(json!("")));
            }
            None if index + 1 == select.path.len() && select.optional => return Ok(None),
            None => {
                return Err(Fault::internal(
                    "response projection missing declared field",
                ));
            }
        }
    }
    Ok(Some(value.clone()))
}

pub(crate) fn not_acceptable() -> Fault {
    Fault::new(
        406,
        "transport.not_acceptable",
        "no acceptable response representation",
    )
}

pub(crate) fn encode_response(
    plan: &ResponsePlan,
    outcome: Outcome,
    request: &HeaderMap,
) -> Result<Reply, Fault> {
    let status = StatusCode::from_u16(plan.status).map_err(Fault::internal)?;
    let mut response = empty(status);
    if plan.stream != outcome.stream.is_some() || plan.events || outcome.events.is_some() {
        return Err(Fault::internal(
            "worker's response mode differs from mapping",
        ));
    }
    if !plan.codec.is_empty() {
        let media = negotiate_media(request, &plan.media).ok_or_else(not_acceptable)?;
        header(response.headers_mut(), "content-type", media);
        if let Some(stream) = outcome.stream {
            if plan.options.max_bytes > 0 && stream.length() > plan.options.max_bytes {
                return Err(Fault::internal("response stream exceeds body limit"));
            }
            let length = usize::try_from(stream.length()).unwrap_or(usize::MAX);
            if negotiate_gzip(plan, request, &mut response, length)? {
                header(response.headers_mut(), "content-encoding", "gzip");
                *response.body_mut() = GzipStream::new(stream).boxed_unsync();
            } else {
                header(
                    response.headers_mut(),
                    "content-length",
                    &stream.length().to_string(),
                );
                *response.body_mut() = stream.boxed_unsync();
            }
        } else {
            let selected = selection(&outcome.payload, &plan.body)?
                .ok_or_else(|| Fault::internal("response body is missing"))?;
            let mut data = match plan.codec.as_str() {
                "json" | "problem_json" => values::encode(&selected).into_bytes(),
                "text" => selected
                    .as_str()
                    .ok_or_else(|| Fault::internal("text response is not a string"))?
                    .as_bytes()
                    .to_vec(),
                "bytes" => STANDARD
                    .decode(selected.as_str().unwrap_or_default())
                    .map_err(Fault::internal)?,
                _ => return Err(Fault::internal("unknown response codec")),
            };
            if plan.options.max_bytes > 0 && data.len() as u64 > plan.options.max_bytes {
                return Err(Fault::internal("response body exceeds declared limit"));
            }
            if negotiate_gzip(plan, request, &mut response, data.len())? {
                let mut compressor = GzEncoder::new(Vec::new(), Compression::default());
                compressor.write_all(&data).map_err(Fault::internal)?;
                data = compressor.finish().map_err(Fault::internal)?;
                header(response.headers_mut(), "content-encoding", "gzip");
            }
            header(
                response.headers_mut(),
                "content-length",
                &data.len().to_string(),
            );
            *response.body_mut() = body(data);
        }
    }
    for spec in &plan.headers {
        let Some(value) = selection(&outcome.payload, &spec.value)? else {
            continue;
        };
        let name = HeaderName::from_bytes(spec.name.as_bytes()).map_err(Fault::internal)?;
        let strings = match (spec.encoding.as_str(), value) {
            ("json", value) => vec![values::encode(&value)],
            ("comma", Value::Array(values)) => {
                vec![values.iter().map(wire_text).collect::<Vec<_>>().join(",")]
            }
            (_, Value::Array(values)) => values.iter().map(wire_text).collect(),
            (_, value) => vec![wire_text(&value)],
        };
        for text in strings {
            response.headers_mut().append(
                name.clone(),
                HeaderValue::from_str(&text).map_err(Fault::internal)?,
            );
        }
    }
    for spec in &plan.cookies {
        let Some(value) = selection(&outcome.payload, &spec.value)? else {
            continue;
        };
        let cookie = &spec.cookie;
        let encoded: String = wire_text(&value)
            .bytes()
            .map(|b| {
                if b.is_ascii_alphanumeric() || b"-._~".contains(&b) {
                    char::from(b).to_string()
                } else {
                    format!("%{b:02X}")
                }
            })
            .collect();
        let path = if cookie.path.is_empty() {
            "/"
        } else {
            &cookie.path
        };
        let mut field = format!("{}={encoded}; Path={path}", cookie.name);
        if !cookie.domain.is_empty() {
            field.push_str(&format!(
                "; Domain={}",
                cookie.domain.trim_start_matches('.')
            ));
        }
        if !cookie.expires.is_empty() {
            use time::{OffsetDateTime, UtcOffset, format_description::well_known::Rfc3339};
            let exp = OffsetDateTime::parse(&cookie.expires, &Rfc3339)
                .map_err(Fault::internal)?
                .to_offset(UtcOffset::UTC);
            field.push_str(&format!(
                "; Expires={}, {:02} {} {:04} {:02}:{:02}:{:02} GMT",
                &exp.weekday().to_string()[..3],
                exp.day(),
                &exp.month().to_string()[..3],
                exp.year(),
                exp.hour(),
                exp.minute(),
                exp.second()
            ));
        }
        if cookie.max_age != 0 {
            field.push_str(&format!("; Max-Age={}", cookie.max_age.max(0)));
        }
        if cookie.http_only {
            field.push_str("; HttpOnly");
        }
        if cookie.secure {
            field.push_str("; Secure");
        }
        if let Some(v) = match cookie.same_site {
            2 => Some("Lax"),
            3 => Some("Strict"),
            4 => Some("None"),
            _ => None,
        } {
            field.push_str(&format!("; SameSite={v}"));
        }
        response.headers_mut().append(
            "set-cookie",
            HeaderValue::from_str(&field).map_err(Fault::internal)?,
        );
    }
    Ok(response)
}

fn wire_text(value: &Value) -> String {
    value
        .as_str()
        .map(str::to_owned)
        .unwrap_or_else(|| values::encode(value))
}

fn negotiate_gzip(
    plan: &ResponsePlan,
    request: &HeaderMap,
    response: &mut Reply,
    payload_len: usize,
) -> Result<bool, Fault> {
    let mut gzip_allowed = false;
    for algorithm in &plan.options.compression_algorithms {
        if !algorithm.trim().eq_ignore_ascii_case("gzip") {
            return Err(Fault::internal("unsupported configured response encoding"));
        }
        gzip_allowed = true;
    }
    if gzip_allowed {
        response
            .headers_mut()
            .append("vary", HeaderValue::from_static("Accept-Encoding"));
    }
    let mut header_parts = Vec::new();
    for raw in request.get_all("accept-encoding") {
        let text = raw.to_str().map_err(|_| not_acceptable())?.trim();
        if !text.is_empty() {
            header_parts.push(text);
        }
    }
    if header_parts.is_empty() {
        return Ok(false);
    }
    let (mut gzip_q, mut id_q) = (0.0_f64, 1.0_f64);
    let (mut gzip_explicit, mut id_explicit) = (false, false);
    let mut wildcard: Option<f64> = None;
    for field in header_parts.join(",").split(',') {
        let (raw_name, params) = field
            .trim()
            .split_once(';')
            .map_or((field.trim(), None), |(name, rest)| {
                (name.trim(), Some(rest))
            });
        let name = raw_name.to_ascii_lowercase();
        let mut q = 1.0_f64;
        if let Some(params) = params {
            for param in params.split(';') {
                let (key, value) = param.trim().split_once('=').ok_or_else(not_acceptable)?;
                if !key.trim().eq_ignore_ascii_case("q") {
                    return Err(not_acceptable());
                }
                let parsed = value.trim().parse::<f64>().map_err(|_| not_acceptable())?;
                if !(0.0..=1.0).contains(&parsed) {
                    return Err(not_acceptable());
                }
                q = parsed;
            }
        }
        match name.as_str() {
            "*" => wildcard = Some(q),
            "gzip" => {
                gzip_q = q;
                gzip_explicit = true;
            }
            "identity" => {
                id_q = q;
                id_explicit = true;
            }
            _ => {}
        }
    }
    if let Some(q) = wildcard {
        if !gzip_explicit {
            gzip_q = q;
        }
        if !id_explicit {
            id_q = q;
        }
    }
    if gzip_allowed && gzip_q > 0.0 && (id_q <= 0.0 || gzip_q >= id_q) {
        return Ok(id_q <= 0.0 || payload_len >= plan.options.compression_threshold);
    }
    if id_q > 0.0 {
        return Ok(false);
    }
    Err(not_acceptable())
}

#[cfg(test)]
#[path = "http_codec_test.rs"]
mod tests;
