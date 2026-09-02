package generate

func renderTSRuntimeInvoke() string {
	return `
export async function invoke(
  transport: InvokeTransport,
  binding: BindingCall,
  input: unknown,
  options: CallOptions,
  registry: TypeRegistry,
): Promise<unknown> {
  if (options.signal?.aborted) throw new SceneryClientError("cancelled", binding.address, "request cancelled");
  const path = buildBindingPath(binding, input, registry);
  const headers = mergeHeaders(transport.headers, options.headers, binding.address);
  if (transport.authentication?.authorization !== undefined) headers.set("authorization", transport.authentication.authorization);
/*__scenery_runtime_request_header_start__*/
  for (const mapping of binding.headers ?? []) {
    appendHeader(headers, mapping.name, bindingInputField(input, mapping.property), mapping.encoding, mapping.value, registry);
  }
/*__scenery_runtime_request_header_end__*/
/*__scenery_runtime_request_cookie_start__*/
  const cookies: string[] = [];
  for (const mapping of binding.cookies ?? []) {
    appendCookie(cookies, mapping.name, bindingInputField(input, mapping.property), mapping.value, registry);
  }
  if (cookies.length > 0) headers.set("cookie", cookies.join("; "));
/*__scenery_runtime_request_cookie_end__*/
  const body = encodeBindingBody(binding, input, headers, registry);
  const requestInit: RequestInit = {
    method: binding.method,
    signal: options.signal,
    headers,
    body,
    credentials: transport.authentication?.credentials,
  };
  let response: Response;
  try {
/*__scenery_runtime_retry_start__*/
    if (transport.retry !== undefined && transport.retryRuntime !== undefined) {
      response = await fetchWithRetry(transport.fetch, transport.baseUrl + path, requestInit, options.signal, transport.retryRuntime, transport.retry);
    } else {
      response = await transport.fetch(transport.baseUrl + path, requestInit);
    }
/*__scenery_runtime_retry_end__*/
/*__scenery_runtime_no_retry_start__*/
    response = await transport.fetch(transport.baseUrl + path, requestInit);
/*__scenery_runtime_no_retry_end__*/
  } catch (cause) {
    throw new SceneryClientError(options.signal?.aborted ? "cancelled" : "network", binding.address, "request failed", cause);
  }
  return matchResponse(response, binding, registry);
}

export async function matchResponse(response: Response, binding: BindingCall, registry: TypeRegistry): Promise<unknown> {
  const cases = binding.responses.filter((candidate) => candidate.status === response.status);
  if (cases.length === 0) {
    throw new SceneryClientError("contract_violation", binding.address, §unexpected response ${response.status}§);
  }
  for (const candidate of cases) {
    if (candidate.role !== "failure") continue;
    try {
      const payload = await decodeBindingResponse(response.clone(), candidate, binding, registry);
      if (candidate.problemCode === undefined || !isProblemCode(payload, candidate.problemCode)) continue;
      if (candidate.throwOnMatch) throw new SceneryClientError("server", binding.address, §server returned ${candidate.problemCode}§);
      return { kind: "failure", name: candidate.name, problem: payload };
    } catch (cause) {
      if (!(cause instanceof SceneryClientError) || cause.code !== "contract_violation") throw cause;
    }
  }
  const completionMatches: unknown[] = [];
  for (const candidate of cases) {
    if (candidate.role !== "completion") continue;
    try {
      completionMatches.push(bindingCompletionOutcome(candidate, await decodeBindingResponse(response.clone(), candidate, binding, registry)));
    } catch (cause) {
      if (!(cause instanceof SceneryClientError) || cause.code !== "contract_violation") throw cause;
    }
  }
  if (completionMatches.length === 1) return completionMatches[0];
  throw new SceneryClientError("contract_violation", binding.address, "response body contradicts the contract");
}

function bindingInputField(input: unknown, property: string): unknown {
  if (!isObject(input) || Array.isArray(input)) return undefined;
  return input[property];
}

function buildBindingPath(binding: BindingCall, input: unknown, registry: TypeRegistry): string {
  let path = binding.path;
  for (const mapping of binding.pathParameters ?? []) {
    path = path.replace(§{${mapping.name}}§, encodeRFC3986(encodeHTTPValue(bindingInputField(input, mapping.property), mapping.value, registry)));
  }
  if (binding.pathTail !== undefined) {
    path = appendPathTail(path, bindingInputField(input, binding.pathTail.property), binding.pathTail.value, registry);
  }
/*__scenery_runtime_query_start__*/
  const query: string[] = [];
  for (const mapping of binding.query ?? []) {
    appendQuery(query, mapping.name, bindingInputField(input, mapping.property), mapping.encoding, mapping.value, registry);
  }
  if (query.length > 0) path += §?${query.join("&")}§;
/*__scenery_runtime_query_end__*/
  return path;
}

function encodeBindingBody(binding: BindingCall, input: unknown, headers: Headers, registry: TypeRegistry): BodyInit | undefined {
  const body = binding.body;
  if (body === undefined) return undefined;
  const value = bindingRequestValue(body, input);
/*__scenery_runtime_multipart_start__*/
  if (body.codec === "multipart") {
    if (body.multipart === undefined) throw new SceneryClientError("invalid_options", binding.address, "multipart requires a declared part schema");
    const encoded = encodeMultipartRequestBody(value, body.multipart, registry);
    headers.set("content-type", encoded.contentType);
    return encoded.body;
  }
/*__scenery_runtime_multipart_end__*/
  if (body.contentType !== undefined) headers.set("content-type", body.contentType);
  return encodeRequestBody(value, body.codec, body.value, registry);
}

function bindingRequestValue(body: BindingRequestBody, input: unknown): unknown {
  if (body.select !== undefined) {
    const record = isObject(input) && !Array.isArray(input) ? input : Object.create(null) as Record<string, unknown>;
    const selected: Record<string, unknown> = {};
    for (const property of body.select) selected[property] = record[property];
    return selected;
  }
  if (body.property !== undefined) return bindingInputField(input, body.property);
  return input;
}

async function decodeBindingResponse(
  response: Response,
  candidate: BindingResponseCase,
  binding: BindingCall,
  registry: TypeRegistry,
): Promise<unknown> {
  let payload: unknown = undefined;
  if (candidate.body === undefined) {
    await assertEmptyResponse(response, binding.address, binding.responseLimitBytes);
  } else {
    payload = mergeResponseValue(
      payload,
      candidate.body.path,
      await decodeResponseBody(
        response,
        candidate.body.codec,
        candidate.body.producedMediaTypes,
        candidate.body.value,
        registry,
        binding.address,
        binding.responseLimitBytes,
      ),
      binding.address,
    );
  }
/*__scenery_runtime_response_header_start__*/
  for (const header of candidate.headers ?? []) {
    payload = mergeResponseValue(
      payload,
      header.path,
      decodeResponseHeader(response, header.name, header.encoding, header.value, registry, binding.address),
      binding.address,
    );
  }
/*__scenery_runtime_response_header_end__*/
/*__scenery_runtime_response_cookie_start__*/
  for (const cookie of candidate.cookies ?? []) {
    payload = mergeResponseValue(
      payload,
      cookie.path,
      decodeResponseCookie(response, cookie.name, cookie.value, registry, binding.address),
      binding.address,
    );
  }
/*__scenery_runtime_response_cookie_end__*/
  return payload === undefined ? {} : payload;
}

function bindingCompletionOutcome(candidate: BindingResponseCase, payload: unknown): unknown {
  if (candidate.kind === "error") return { kind: "error", name: candidate.name, problem: payload };
  if (candidate.kind === "enqueue") return { kind: "enqueue", name: candidate.name, receipt: payload };
  if (candidate.kind === "failure") return { kind: "failure", name: candidate.name, problem: payload };
  return { kind: "result", name: candidate.name, value: payload };
}

`
}
