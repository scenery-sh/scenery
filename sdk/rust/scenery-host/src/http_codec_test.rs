use super::*;
use http_body_util::Full;
use hyper::service::service_fn;
use hyper_util::rt::TokioIo;
use tokio::net::TcpListener;

fn mapping(source: &str, ty: &str) -> Mapping {
    serde_json::from_value(json!({
        "Source": source, "Name": "rest", "Target": "rest", "Type": ty,
        "Encoding": "", "Optional": ty.starts_with("optional("), "EnumValues": [],
    }))
    .unwrap()
}

/// Decodes a captured path value the way `decode_request` maps it.
fn mapped(source: &str, ty: &str, captured: &str) -> Result<Option<Value>> {
    let mapping = mapping(source, ty);
    let values = path_values(&BTreeMap::from([("rest", captured)]), &mapping)?;
    if values.is_empty() && mapping.optional {
        return Ok(None);
    }
    mapped_json(&mapping, values).map(Some)
}

#[test]
fn path_tails_join_their_segments_and_map_the_empty_capture_by_type() {
    // A `string` tail used to answer 400 for anything but one segment.
    assert_eq!(
        mapped("path_tail", "string", "a/b").unwrap(),
        Some(json!("a/b"))
    );
    assert_eq!(mapped("path_tail", "string", "").unwrap(), Some(json!("")));
    assert_eq!(
        mapped("path_tail", "string", "a").unwrap(),
        Some(json!("a"))
    );
    assert_eq!(
        mapped("path_tail", "relative_path", "docs/a%20b.txt").unwrap(),
        Some(json!("docs/a b.txt"))
    );
    assert!(mapped("path_tail", "relative_path", "").is_err());
    assert_eq!(
        mapped("path_tail", "optional(relative_path)", "").unwrap(),
        None
    );
    for hazard in [
        "a//b",
        "a/./b",
        "a/../b",
        "a/%2e%2e/b",
        "a/b%2Fc",
        "a/b%5Cc",
        "a\\b",
        "a/%00",
        // Decoding once more would produce a dot segment or a separator.
        "a/%252e%252e/b",
        "a/b%252Fc",
        "a/%zz",
    ] {
        assert!(
            mapped("path_tail", "string", hazard).is_err(),
            "accepted {hazard}"
        );
    }
    // A percent sign that decodes no further is ordinary content.
    assert_eq!(
        mapped("path_tail", "string", "50%25/off").unwrap(),
        Some(json!("50%/off"))
    );
}

#[test]
fn path_parameters_are_one_safe_segment() {
    assert_eq!(
        mapped("path", "string", "a%20b").unwrap(),
        Some(json!("a b"))
    );
    for hazard in [".", "..", "%2E%2E", "a%2Fb", "a%5Cb", "%00"] {
        assert!(
            mapped("path", "string", hazard).is_err(),
            "accepted {hazard}"
        );
    }
}

#[test]
fn framework_json_routes_require_the_json_media_type() {
    let status = |content_type: Option<&'static str>| {
        let mut headers = HeaderMap::new();
        if let Some(value) = content_type {
            headers.insert("content-type", HeaderValue::from_static(value));
        }
        require_json(&headers)
            .err()
            .map(|fault| fault.status.as_u16())
    };
    assert_eq!(status(Some("application/json")), None);
    assert_eq!(status(Some("application/json; charset=UTF-8")), None);
    // What a cross-site form can send without a preflight is refused,
    // so it cannot plant a session in the victim's browser.
    assert_eq!(status(Some("text/plain")), Some(415));
    assert_eq!(status(Some("application/x-www-form-urlencoded")), Some(415));
    assert_eq!(status(Some("multipart/form-data; boundary=x")), Some(415));
    assert_eq!(status(None), Some(415));
    assert_eq!(status(Some("application/json; charset=latin1")), Some(400));
    assert_eq!(status(Some("not a media type")), Some(400));
}

#[tokio::test]
async fn response_projects_exact_header_numbers_and_cookie_defaults() {
    let plan = serde_json::from_value(json!({
        "Status": 200, "Codec": "json", "Media": ["application/json"],
        "Body": {"Path": ["data"], "Optional": false, "DefaultEmpty": false},
        "Stream": false,
        "Options": {"MaxBytes": 1024, "CompressionAlgorithms": [], "CompressionThreshold": 0},
        "Headers": [
            {"Name": "X-Ids", "Encoding": "repeated", "Value": {"Path": ["data","ids"],"Optional": false,"DefaultEmpty":false}},
            {"Name":"X-Json","Encoding":"json","Value":{"Path":["data"],"Optional":false,"DefaultEmpty":false}},
            {"Name":"X-Missing","Encoding":"repeated","Value":{"Path":["absent"],"Optional":true,"DefaultEmpty":false}}
        ],
        "Cookies": [{
            "Cookie": {"Name":"quote","Path":"","Domain":"","MaxAge":0,"Expires":"","Secure":true,"HTTPOnly":true,"SameSite":2},
            "Value": {"Path":["cookie"],"Optional":false,"DefaultEmpty":false}
        }]
    })).unwrap();
    let outcome = Outcome {
        kind: "result".into(),
        name: "ok".into(),
        payload: values::parse(br#"{"cookie":"two words","data":{"ids":[9007199254740993,2]}}"#)
            .unwrap(),
        stream: None,
        worker: None,
    };
    let response = encode_response(&plan, outcome, &HeaderMap::new())
        .unwrap_or_else(|fault| panic!("encoding returned {}", fault.code));
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()["x-json"],
        r#"{"ids":[9007199254740993,2]}"#
    );
    assert_eq!(
        response
            .headers()
            .get_all("x-ids")
            .iter()
            .collect::<Vec<_>>(),
        ["9007199254740993", "2"]
    );
    assert!(!response.headers().contains_key("x-missing"));
    assert_eq!(
        response.headers()["set-cookie"],
        "quote=two%20words; Path=/; HttpOnly; Secure; SameSite=Lax"
    );
    assert_eq!(
        response.into_body().collect().await.unwrap().to_bytes(),
        r#"{"ids":[9007199254740993,2]}"#
    );
}

async fn incoming_bytes(payload: Bytes) -> Result<Incoming> {
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let addr = listener.local_addr()?;
    let (tx, rx) = tokio::sync::oneshot::channel();
    let server = tokio::spawn(async move {
        let (socket, _) = listener.accept().await.unwrap();
        let tx = std::sync::Mutex::new(Some(tx));
        let service = service_fn(move |req: hyper::Request<Incoming>| {
            if let Some(tx) = tx.lock().unwrap().take() {
                let _ = tx.send(req.into_body());
            }
            async { Ok::<_, std::convert::Infallible>(crate::host::empty(StatusCode::OK)) }
        });
        let _ = hyper::server::conn::http1::Builder::new()
            .serve_connection(TokioIo::new(socket), service)
            .await;
    });
    let stream = tokio::net::TcpStream::connect(addr).await?;
    let (mut sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream)).await?;
    tokio::spawn(conn);
    let req = hyper::Request::post("http://localhost/").body(Full::new(payload))?;
    let _ = sender.send_request(req).await;
    let body = rx.await?;
    server.abort();
    Ok(body)
}

#[tokio::test]
async fn streaming_responses_negotiate_gzip_and_reject_unacceptable_encodings() -> Result<()> {
    let plan: ResponsePlan = serde_json::from_value(json!({
        "Status": 200, "Codec": "bytes", "Media": ["text/plain"],
        "Body": {"Path": [], "Optional": false, "DefaultEmpty": false},
        "Stream": true,
        "Options": {"MaxBytes": 16384, "CompressionAlgorithms": ["gzip"], "CompressionThreshold": 16},
        "Headers": [], "Cookies": []
    }))?;
    let raw = Bytes::from("hello streaming gzip compression world ".repeat(20));

    let incoming = incoming_bytes(raw.clone()).await?;
    let outcome = Outcome {
        kind: "result".into(),
        name: "ok".into(),
        payload: json!(null),
        stream: Some(crate::abi::Stream::for_test(incoming, raw.len() as u64)),
        worker: None,
    };
    let mut headers = HeaderMap::new();
    headers.insert("accept-encoding", HeaderValue::from_static("gzip"));
    let response = encode_response(&plan, outcome, &headers)
        .unwrap_or_else(|fault| panic!("streaming gzip failed: {}", fault.code));
    assert_eq!(response.headers()["content-encoding"], "gzip");
    assert_eq!(response.headers()["vary"], "Accept-Encoding");
    assert!(!response.headers().contains_key("content-length"));
    let compressed = response.into_body().collect().await?.to_bytes();
    let mut decoded = Vec::new();
    GzDecoder::new(&compressed[..]).read_to_end(&mut decoded)?;
    assert_eq!(decoded, raw);

    let incoming = incoming_bytes(raw.clone()).await?;
    let outcome = Outcome {
        kind: "result".into(),
        name: "ok".into(),
        payload: json!(null),
        stream: Some(crate::abi::Stream::for_test(incoming, raw.len() as u64)),
        worker: None,
    };
    let mut reject = HeaderMap::new();
    reject.insert(
        "accept-encoding",
        HeaderValue::from_static("br;q=1, identity;q=0"),
    );
    let Err(fault) = encode_response(&plan, outcome, &reject) else {
        panic!("expected 406 not acceptable for identity;q=0 without gzip");
    };
    assert_eq!(fault.status, StatusCode::NOT_ACCEPTABLE);
    Ok(())
}

#[tokio::test]
async fn request_and_response_media_type_parameters_are_matched() -> Result<()> {
    let body_plan: crate::plan::BodyPlan = serde_json::from_value(json!({
        "Codec": "json",
        "Target": "",
        "Include": [],
        "Except": [],
        "AcceptedMediaTypes": ["application/json; profile=v1"],
        "SupportedContentEncoding": [],
        "MaxCompressedBytes": 0,
        "MaxDecompressedBytes": 0,
        "MaxMultipartParts": 0,
        "Fields": [],
        "MultipartParts": []
    }))?;

    let mut mismatch = Request::builder()
        .method("POST")
        .uri("/")
        .header(
            "content-type",
            "application/json; charset=utf-8; profile=v2",
        )
        .body(incoming_bytes(Bytes::from_static(br#"{"ok":true}"#)).await?)?;
    let Err(fault) = decode_body(mismatch, &body_plan).await else {
        panic!("mismatched media type parameter must return 415");
    };
    assert_eq!(fault.status, StatusCode::UNSUPPORTED_MEDIA_TYPE);

    mismatch = Request::builder()
        .method("POST")
        .uri("/")
        .header(
            "content-type",
            "application/json; profile=v1; charset=utf-8",
        )
        .body(incoming_bytes(Bytes::from_static(br#"{"ok":true}"#)).await?)?;
    let decoded = decode_body(mismatch, &body_plan)
        .await
        .unwrap_or_else(|fault| panic!("matching parameter failed: {}", fault.code));
    assert_eq!(decoded["ok"], true);

    let response_plan: ResponsePlan = serde_json::from_value(json!({
        "Status": 200, "Codec": "json", "Media": ["application/json; profile=v1"],
        "Body": {"Path": ["data"], "Optional": false, "DefaultEmpty": false},
        "Stream": false,
        "Options": {"MaxBytes": 1024, "CompressionAlgorithms": [], "CompressionThreshold": 0},
        "Headers": [], "Cookies": []
    }))?;
    let make_outcome = || Outcome {
        kind: "result".into(),
        name: "ok".into(),
        payload: values::parse(br#"{"data":{"ok":true}}"#).unwrap(),
        stream: None,
        worker: None,
    };

    let mut wrong_accept = HeaderMap::new();
    wrong_accept.insert(
        "accept",
        HeaderValue::from_static("application/json; profile=v2"),
    );
    let Err(fault) = encode_response(&response_plan, make_outcome(), &wrong_accept) else {
        panic!("mismatched Accept parameter must return 406");
    };
    assert_eq!(fault.status, StatusCode::NOT_ACCEPTABLE);

    for accept in ["application/json; profile=v1", "application/json"] {
        let mut headers = HeaderMap::new();
        headers.insert("accept", HeaderValue::from_static(accept));
        let reply = encode_response(&response_plan, make_outcome(), &headers)
            .unwrap_or_else(|fault| panic!("Accept={accept} failed: {}", fault.code));
        assert_eq!(
            reply.headers()["content-type"],
            "application/json; profile=v1"
        );
    }
    Ok(())
}

#[tokio::test]
async fn gzip_request_body_rejects_trailing_bytes_and_second_member() -> Result<()> {
    let body_plan: crate::plan::BodyPlan = serde_json::from_value(json!({
        "Codec": "json",
        "Target": "",
        "Include": [],
        "Except": [],
        "AcceptedMediaTypes": ["application/json"],
        "SupportedContentEncoding": ["gzip"],
        "MaxCompressedBytes": 4096,
        "MaxDecompressedBytes": 4096,
        "MaxMultipartParts": 0,
        "Fields": [],
        "MultipartParts": []
    }))?;
    let gzip_member = |raw: &[u8]| -> Vec<u8> {
        let mut encoder = GzEncoder::new(Vec::new(), Compression::default());
        encoder.write_all(raw).unwrap();
        encoder.finish().unwrap()
    };
    let valid = gzip_member(br#"{"ok":true}"#);

    let ok_req = Request::builder()
        .method("POST")
        .uri("/")
        .header("content-type", "application/json")
        .header("content-encoding", "gzip")
        .body(incoming_bytes(Bytes::from(valid.clone())).await?)?;
    let decoded = decode_body(ok_req, &body_plan)
        .await
        .unwrap_or_else(|fault| panic!("valid gzip failed: {}", fault.code));
    assert_eq!(decoded["ok"], true);

    let mut with_trailing = valid.clone();
    with_trailing.extend_from_slice(b"trailing");
    let trailing_req = Request::builder()
        .method("POST")
        .uri("/")
        .header("content-type", "application/json")
        .header("content-encoding", "gzip")
        .body(incoming_bytes(Bytes::from(with_trailing)).await?)?;
    let Err(fault) = decode_body(trailing_req, &body_plan).await else {
        panic!("gzip payload with trailing bytes was accepted");
    };
    assert_eq!(fault.status, StatusCode::BAD_REQUEST);

    let mut two_members = valid;
    two_members.extend_from_slice(&gzip_member(br#"{"second":true}"#));
    let multi_req = Request::builder()
        .method("POST")
        .uri("/")
        .header("content-type", "application/json")
        .header("content-encoding", "gzip")
        .body(incoming_bytes(Bytes::from(two_members)).await?)?;
    let Err(fault) = decode_body(multi_req, &body_plan).await else {
        panic!("multi-member gzip payload was accepted");
    };
    assert_eq!(fault.status, StatusCode::BAD_REQUEST);
    Ok(())
}

#[tokio::test]
async fn mime_structured_suffix_and_encoding_negotiation_match_contract_rules() -> Result<()> {
    let problem_body: crate::plan::BodyPlan = serde_json::from_value(json!({
        "Codec": "problem_json",
        "Target": "",
        "Include": [],
        "Except": [],
        "AcceptedMediaTypes": ["application/problem+json"],
        "SupportedContentEncoding": ["gzip"],
        "MaxCompressedBytes": 4096,
        "MaxDecompressedBytes": 4096,
        "MaxMultipartParts": 0,
        "Fields": [],
        "MultipartParts": []
    }))?;
    let wrong_suffix = Request::builder()
        .method("POST")
        .uri("/")
        .header("content-type", "application/problem+xml")
        .body(incoming_bytes(Bytes::from_static(br#"{"title":"err"}"#)).await?)?;
    let Err(fault) = decode_body(wrong_suffix, &problem_body).await else {
        panic!("application/problem+xml must not match application/problem+json");
    };
    assert_eq!(fault.status, StatusCode::UNSUPPORTED_MEDIA_TYPE);

    for bad_encodings in [vec!["gzip", "gzip"], vec!["gzip, identity"]] {
        let mut builder = Request::builder()
            .method("POST")
            .uri("/")
            .header("content-type", "application/problem+json");
        for enc in bad_encodings {
            builder = builder.header("content-encoding", enc);
        }
        let req = builder.body(incoming_bytes(Bytes::from_static(br#"{"ok":true}"#)).await?)?;
        let Err(fault) = decode_body(req, &problem_body).await else {
            panic!("multiple content-encodings must be rejected");
        };
        assert_eq!(fault.status, StatusCode::BAD_REQUEST);
    }

    let problem_resp: ResponsePlan = serde_json::from_value(json!({
        "Status": 400, "Codec": "problem_json", "Media": ["application/problem+json"],
        "Body": {"Path": ["data"], "Optional": false, "DefaultEmpty": false},
        "Stream": false,
        "Options": {"MaxBytes": 1024, "CompressionAlgorithms": ["gzip"], "CompressionThreshold": 8},
        "Headers": [], "Cookies": []
    }))?;
    let make_outcome = || Outcome {
        kind: "result".into(),
        name: "ok".into(),
        payload: values::parse(br#"{"data":{"detail":"long enough for compression threshold"}}"#)
            .unwrap(),
        stream: None,
        worker: None,
    };

    let mut accept_xml = HeaderMap::new();
    accept_xml.insert(
        "accept",
        HeaderValue::from_static("application/problem+xml"),
    );
    assert!(encode_response(&problem_resp, make_outcome(), &accept_xml).is_err());

    // gzip;q=0.5 leaves implicit identity at q=1.0, so identity wins over gzip.
    let mut low_gzip = HeaderMap::new();
    low_gzip.insert("accept-encoding", HeaderValue::from_static("gzip;q=0.5"));
    let reply = encode_response(&problem_resp, make_outcome(), &low_gzip)
        .unwrap_or_else(|fault| panic!("gzip;q=0.5 failed: {}", fault.code));
    assert!(!reply.headers().contains_key("content-encoding"));

    // Duplicate coding uses the last occurrence: q=1 then q=0 leaves identity.
    let mut last_wins = HeaderMap::new();
    last_wins.insert(
        "accept-encoding",
        HeaderValue::from_static("gzip;q=1, gzip;q=0"),
    );
    let reply = encode_response(&problem_resp, make_outcome(), &last_wins)
        .unwrap_or_else(|fault| panic!("duplicate gzip failed: {}", fault.code));
    assert!(!reply.headers().contains_key("content-encoding"));

    // Unknown parameter -> 406.
    let mut bad_param = HeaderMap::new();
    bad_param.insert("accept-encoding", HeaderValue::from_static("br;other=x"));
    assert_eq!(
        encode_response(&problem_resp, make_outcome(), &bad_param)
            .err()
            .map(|f| f.status),
        Some(StatusCode::NOT_ACCEPTABLE)
    );

    // Case-insensitive Q=0 across multiple Accept-Encoding header lines -> 406.
    let mut upper_q = HeaderMap::new();
    upper_q.append("accept-encoding", HeaderValue::from_static("gzip; Q=0"));
    upper_q.append("accept-encoding", HeaderValue::from_static("identity; Q=0"));
    assert_eq!(
        encode_response(&problem_resp, make_outcome(), &upper_q)
            .err()
            .map(|f| f.status),
        Some(StatusCode::NOT_ACCEPTABLE)
    );
    let mut nan_q = HeaderMap::new();
    nan_q.insert("accept-encoding", HeaderValue::from_static("gzip;q=NaN"));
    assert_eq!(
        encode_response(&problem_resp, make_outcome(), &nan_q)
            .err()
            .map(|f| f.status),
        Some(StatusCode::NOT_ACCEPTABLE)
    );

    // When identity is rejected (q=0), gzip is required even below threshold.
    let small_resp: ResponsePlan = serde_json::from_value(json!({
        "Status": 200, "Codec": "json", "Media": ["application/json"],
        "Body": {"Path": ["data"], "Optional": false, "DefaultEmpty": false},
        "Stream": false,
        "Options": {"MaxBytes": 1024, "CompressionAlgorithms": ["gzip"], "CompressionThreshold": 4096},
        "Headers": [], "Cookies": []
    }))?;
    let mut force_gzip = HeaderMap::new();
    force_gzip.insert(
        "accept-encoding",
        HeaderValue::from_static("gzip;q=0.5, identity;q=0"),
    );
    let reply = encode_response(&small_resp, make_outcome(), &force_gzip)
        .unwrap_or_else(|fault| panic!("forced gzip failed: {}", fault.code));
    assert_eq!(reply.headers()["content-encoding"], "gzip");
    Ok(())
}
