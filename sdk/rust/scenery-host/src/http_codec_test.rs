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
