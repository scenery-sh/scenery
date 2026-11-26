use super::*;

impl Host {
    pub(crate) async fn control(self: &Arc<Self>, mut request: Request<Incoming>) -> Reply {
        if !authorized(request.headers(), &self.control_token) {
            return empty(StatusCode::UNAUTHORIZED);
        }
        if !self.development {
            return empty(StatusCode::NOT_FOUND);
        }
        let query = values::query_pairs(request.uri().query());
        if request.method() == Method::GET
            && request.uri().path() == "/__scenery/process/v1/telemetry"
        {
            let id = query
                .get("id")
                .and_then(|ids| ids.first())
                .map(String::as_str);
            return json_reply(StatusCode::OK, &self.telemetry.snapshot(id));
        }
        if request.method() == Method::GET && request.uri().path() == "/__scenery/process/v1/source"
        {
            let Some(status) = &self.dev_status else {
                return empty(StatusCode::NOT_FOUND);
            };
            return status
                .reply(request.uri().query(), &self.generation)
                .await
                .unwrap_or_else(|_| empty(StatusCode::SERVICE_UNAVAILABLE));
        }
        if request.method() != Method::POST {
            return empty(StatusCode::NOT_FOUND);
        }
        let path = request.uri().path().to_owned();
        forward_invocation_authorization(request.headers_mut());
        match path.as_str() {
            "/__scenery/process/v1/shutdown" => crate::dev::shutdown(self.stop.clone()),
            "/__scenery/process/v1/worker-reports" => {
                let result = match read(request.into_body(), 1 << 20).await {
                    Ok(data) => self.telemetry.ingest(&data),
                    Err(error) => Err(error),
                };
                if result.is_ok() {
                    empty(StatusCode::NO_CONTENT)
                } else {
                    empty(StatusCode::BAD_REQUEST)
                }
            }
            "/__scenery/process/v1/dev-durable" => self.engine.dev_run(self, request).await,
            "/__scenery/process/v1/dev-call" => {
                let get = |key: &str| {
                    query
                        .get(key)
                        .and_then(|values| values.first())
                        .map(String::as_str)
                };
                let Some(generation) = self.generation() else {
                    return Fault::unavailable(Delivery::NotSent).reply(self, None);
                };
                if get("generation")
                    .is_some_and(|value| value.parse::<u64>().ok() != Some(generation.number))
                {
                    return empty(StatusCode::CONFLICT);
                }
                if let Some(address) = get("binding") {
                    let allow_all = get("replay") == Some("1");
                    return gateway::dev_binding(self, generation, request, address, allow_all).await;
                }
                let Some((method, uri)) =
                    get("method").zip(get("path")).and_then(|(method, path)| {
                        let method = Method::from_bytes(method.as_bytes()).ok()?;
                        let uri: hyper::Uri = path.parse().ok()?;
                        (uri.scheme().is_none()
                            && uri.authority().is_none()
                            && uri.path().starts_with('/'))
                        .then_some((method, uri))
                    })
                else {
                    return Fault::invalid().reply(self, None);
                };
                *request.method_mut() = method;
                *request.uri_mut() = uri;
                gateway::ingress(self, generation, request, true).await
            }
            _ => empty(StatusCode::NOT_FOUND),
        }
    }
}

/// Replaces the control credential with the caller's own for the invoked
/// binding. A caller without one sends the header empty or not at all, and
/// both leave the invocation unauthenticated.
pub(super) fn forward_invocation_authorization(headers: &mut HeaderMap) {
    headers.remove("authorization");
    if let Some(credential) = headers
        .remove("x-app-invocation-authorization")
        .filter(|value| !value.as_bytes().trim_ascii().is_empty())
    {
        headers.insert("authorization", credential);
    }
}
