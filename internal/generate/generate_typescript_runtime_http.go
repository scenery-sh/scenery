package generate

const tsRuntimeHTTP = `type ParsedMediaType = { readonly base: string; readonly parameters: Readonly<Record<string, string>> };

function splitMediaType(value: string): string[] {
  const parts: string[] = [];
  let start = 0;
  let quoted = false;
  let escaped = false;
  for (let index = 0; index < value.length; index++) {
    const character = value[index]!;
    if (escaped) { escaped = false; continue; }
    if (quoted && character === "\\") { escaped = true; continue; }
    if (character === '"') { quoted = !quoted; continue; }
    if (character === ";" && !quoted) { parts.push(value.slice(start, index)); start = index + 1; }
  }
  if (quoted || escaped) throw new Error("invalid quoted media type parameter");
  parts.push(value.slice(start));
  return parts;
}

function mediaTypeToken(value: string): boolean {
  return /^[!#$%&'*+.^_\x60|~0-9A-Za-z-]+$/.test(value);
}

function decodeMediaTypeParameter(value: string): string {
  if (!value.startsWith('"')) {
    if (!mediaTypeToken(value)) throw new Error("invalid media type parameter value");
    return value;
  }
  if (!value.endsWith('"')) throw new Error("invalid quoted media type parameter");
  let decoded = "";
  for (let index = 1; index < value.length - 1; index++) {
    const character = value[index]!;
    if (character === "\\") {
      index++;
      if (index >= value.length - 1) throw new Error("invalid quoted media type escape");
      decoded += value[index]!;
      continue;
    }
    if (character === '"' || character.charCodeAt(0) < 0x20 || character.charCodeAt(0) === 0x7f) throw new Error("invalid quoted media type parameter");
    decoded += character;
  }
  return decoded;
}

function parseMediaType(value: string): ParsedMediaType {
  const parts = splitMediaType(value);
  const base = (parts.shift() ?? "").trim().toLowerCase();
  const baseParts = base.split("/");
  if (baseParts.length !== 2 || !mediaTypeToken(baseParts[0]!) || !mediaTypeToken(baseParts[1]!)) throw new Error("invalid media type");
  const parameters: Record<string, string> = {};
  for (const raw of parts) {
    const separator = raw.indexOf("=");
    if (separator <= 0) throw new Error("invalid media type parameter");
    const name = raw.slice(0, separator).trim().toLowerCase();
    if (!mediaTypeToken(name) || Object.prototype.hasOwnProperty.call(parameters, name)) throw new Error("invalid or duplicate media type parameter");
    let parameter = decodeMediaTypeParameter(raw.slice(separator + 1).trim());
    parameters[name] = name === "charset" ? parameter.toLowerCase() : parameter;
  }
  if (parameters.charset === "utf-8" || parameters.charset === "") delete parameters.charset;
  return { base, parameters };
}

function mediaTypeMatches(actual: string, expected: string): boolean {
  const left = parseMediaType(actual);
  const right = parseMediaType(expected);
  if (left.base !== right.base) return false;
  const leftEntries = Object.entries(left.parameters).sort();
  const rightEntries = Object.entries(right.parameters).sort();
  return JSON.stringify(leftEntries) === JSON.stringify(rightEntries);
}

async function readBoundedResponse(response: Response, maximumBytes: number, bindingAddress: string): Promise<Uint8Array> {
  const declared = response.headers.get("content-length");
  if (declared !== null && (!/^(0|[1-9][0-9]*)$/.test(declared) || BigInt(declared) > BigInt(maximumBytes))) {
    throw new SceneryClientError("contract_violation", bindingAddress, "response exceeds the contract byte limit");
  }
  if (response.body === null) return new Uint8Array();
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (value === undefined) continue;
    total += value.byteLength;
    if (total > maximumBytes) {
      await reader.cancel();
      throw new SceneryClientError("contract_violation", bindingAddress, "response exceeds the contract byte limit");
    }
    chunks.push(value);
  }
  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) { output.set(chunk, offset); offset += chunk.byteLength; }
  return output;
}

export function mergeResponseValue(current: unknown, path: readonly string[], value: unknown, bindingAddress: string): unknown {
	if (value === undefined && path.length > 0) return current;
  if (path.length === 0) {
    if (current !== undefined) throw new SceneryClientError("contract_violation", bindingAddress, "response mappings overlap");
    return value;
  }
  const root = current === undefined ? Object.create(null) as Record<string, unknown> : current;
  if (!isObject(root) || Array.isArray(root)) throw new SceneryClientError("contract_violation", bindingAddress, "response mappings overlap");
  let target = root as Record<string, unknown>;
  for (let index = 0; index < path.length - 1; index++) {
    const segment = path[index]!;
    assertSafeKey(segment);
    const existing = target[segment];
    if (existing === undefined) target[segment] = Object.create(null) as Record<string, unknown>;
    else if (!isObject(existing) || Array.isArray(existing)) throw new SceneryClientError("contract_violation", bindingAddress, "response mappings overlap");
    target = target[segment] as Record<string, unknown>;
  }
  const leaf = path[path.length - 1]!;
  assertSafeKey(leaf);
  if (Object.prototype.hasOwnProperty.call(target, leaf)) throw new SceneryClientError("contract_violation", bindingAddress, "response mappings overlap");
  target[leaf] = value;
  return root;
}

/*__scenery_runtime_response_metadata_start__*/
type ResponseHeaderValues = { readonly values: readonly string[]; readonly preservesRepetition: boolean };

function responseHeaderValues(headers: Headers, name: string): ResponseHeaderValues {
  const extended = headers as Headers & {
    getAll?: (name: string) => string[];
/*__scenery_runtime_response_cookie_start__*/
    getSetCookie?: () => string[];
/*__scenery_runtime_response_cookie_end__*/
    raw?: () => Record<string, string[]>;
  };
/*__scenery_runtime_response_cookie_start__*/
  if (name.toLowerCase() === "set-cookie" && typeof extended.getSetCookie === "function") {
    return { values: extended.getSetCookie(), preservesRepetition: true };
  }
/*__scenery_runtime_response_cookie_end__*/
  if (typeof extended.getAll === "function") {
    try { return { values: extended.getAll(name), preservesRepetition: true }; } catch { /* Bun and Cloudflare Workers accept only Set-Cookie. */ }
  }
  if (typeof extended.raw === "function") {
    const raw = extended.raw();
    return { values: raw[name.toLowerCase()] ?? raw[name] ?? [], preservesRepetition: true };
  }
  const value = headers.get(name);
  return { values: value === null ? [] : [value], preservesRepetition: false };
}

function responseHTTPWireValue(value: string, descriptor: TypeDescriptor, registry: TypeRegistry, bindingAddress: string): unknown {
  const resolved = unwrapResponseMetadataDescriptor(descriptor, registry, new Set());
  if (resolved.kind === "enum") return value;
  if (resolved.kind !== "primitive") throw new SceneryClientError("contract_violation", bindingAddress, "response metadata requires a scalar codec");
  switch (resolved.name) {
    case "bool":
      if (value !== "true" && value !== "false") throw new SceneryClientError("contract_violation", bindingAddress, "malformed boolean response metadata");
      return value === "true";
    case "int32":
    case "uint32":
    case "float32":
    case "float64":
      return parseExactJSON(value);
    case "json":
      return parseExactJSON(value);
    default:
      return value;
  }
}

function unwrapResponseMetadataDescriptor(descriptor: TypeDescriptor, registry: TypeRegistry, resolving: Set<string>): TypeDescriptor {
  if (descriptor.kind === "named") {
    if (resolving.has(descriptor.name)) throw new Error("recursive response metadata type descriptor");
    const resolved = registry[descriptor.name];
    if (resolved === undefined) throw new Error("missing response metadata type descriptor");
    const next = new Set(resolving);
    next.add(descriptor.name);
    return unwrapResponseMetadataDescriptor(resolved, registry, next);
  }
  if (descriptor.kind === "optional" || descriptor.kind === "nullable") return unwrapResponseMetadataDescriptor(descriptor.value, registry, resolving);
  return descriptor;
}

function responseMetadataIsOptional(descriptor: TypeDescriptor, registry: TypeRegistry, resolving: Set<string>): boolean {
	if (descriptor.kind === "named") {
		if (resolving.has(descriptor.name)) throw new Error("recursive response metadata type descriptor");
		const resolved = registry[descriptor.name];
		if (resolved === undefined) throw new Error("missing response metadata type descriptor");
		const next = new Set(resolving);
		next.add(descriptor.name);
		return responseMetadataIsOptional(resolved, registry, next);
	}
	return descriptor.kind === "optional";
}
/*__scenery_runtime_response_metadata_end__*/

/*__scenery_runtime_response_header_start__*/
export function decodeResponseHeader(
  response: Response,
  name: string,
  encoding: string,
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
  bindingAddress: string,
): unknown {
	try {
		const raw = responseHeaderValues(response.headers, name);
		if (raw.values.length === 0) {
			if (responseMetadataIsOptional(descriptor, registry, new Set())) return undefined;
			throw new SceneryClientError("contract_violation", bindingAddress, §required response header ${name} is absent§);
		}
    const resolved = unwrapResponseMetadataDescriptor(descriptor, registry, new Set());
    const collection = resolved.kind === "list" || resolved.kind === "set";
    if (encoding === "json") {
      if (raw.values.length !== 1) throw new SceneryClientError("contract_violation", bindingAddress, §response header ${name} is repeated§);
      return decodeTypedValue(parseExactJSON(raw.values[0]!), descriptor, registry, §$headers.${name}§, new Set());
    }
    let values = raw.values;
    if (encoding === "comma") values = raw.values.flatMap((item) => item.split(",").map((part) => part.trim()));
    else if (encoding !== "repeated") throw new SceneryClientError("contract_violation", bindingAddress, "unsupported response header encoding");
		if (encoding === "repeated" && collection && !raw.preservesRepetition) {
			throw new SceneryClientError("unsupported_runtime", bindingAddress, §fetch runtime cannot preserve repeated response header ${name}§);
		}
    if (!collection && values.length !== 1) throw new SceneryClientError("contract_violation", bindingAddress, §scalar response header ${name} is repeated§);
    const itemDescriptor = collection ? resolved.value : descriptor;
    const wire = values.map((item) => responseHTTPWireValue(item, itemDescriptor, registry, bindingAddress));
    return decodeTypedValue(collection ? wire : wire[0], descriptor, registry, §$headers.${name}§, new Set());
  } catch (cause) {
    if (cause instanceof SceneryClientError && cause.bindingAddress === bindingAddress) throw cause;
    throw new SceneryClientError("contract_violation", bindingAddress, §malformed response header ${name}§, safeCause(cause));
  }
}
/*__scenery_runtime_response_header_end__*/

/*__scenery_runtime_response_cookie_start__*/
export function decodeResponseCookie(
  response: Response,
  name: string,
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
  bindingAddress: string,
): unknown {
	try {
		const raw = responseHeaderValues(response.headers, "set-cookie");
		if (!raw.preservesRepetition) throw new SceneryClientError("unsupported_runtime", bindingAddress, "fetch runtime does not expose Set-Cookie values");
    const prefix = §${name}=§;
    const matches = raw.values.flatMap((cookie) => {
      const pair = cookie.split(";", 1)[0]!.trim();
      return pair.startsWith(prefix) ? [pair.slice(prefix.length)] : [];
    });
		if (matches.length === 0 && responseMetadataIsOptional(descriptor, registry, new Set())) return undefined;
		if (matches.length !== 1) throw new SceneryClientError("contract_violation", bindingAddress, §response cookie ${name} is absent or duplicated§);
    const decoded = decodeURIComponent(matches[0]!);
    const wire = responseHTTPWireValue(decoded, descriptor, registry, bindingAddress);
    return decodeTypedValue(wire, descriptor, registry, §$cookies.${name}§, new Set());
  } catch (cause) {
    if (cause instanceof SceneryClientError && cause.bindingAddress === bindingAddress) throw cause;
    throw new SceneryClientError("contract_violation", bindingAddress, §malformed response cookie ${name}§, safeCause(cause));
  }
}
/*__scenery_runtime_response_cookie_end__*/

export async function decodeResponseBody(
  response: Response,
  codec: string,
  producedMediaTypes: readonly string[],
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
  bindingAddress: string,
  maximumBytes: number,
): Promise<unknown> {
  try {
    const media = response.headers.get("content-type") ?? "";
    if (!producedMediaTypes.some((expected) => mediaTypeMatches(media, expected))) {
      throw new SceneryClientError("contract_violation", bindingAddress, "response content type contradicts the contract");
    }
    const bytes = await readBoundedResponse(response, maximumBytes, bindingAddress);
    if (codec === "bytes") {
      const resolved = resolveDescriptor(descriptor, registry, new Set());
      if (resolved.kind !== "primitive" || resolved.name !== "bytes") throw new SceneryClientError("contract_violation", bindingAddress, "bytes response contradicts its type");
      return bytes;
    }
    const source = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    if (codec === "text") {
      assertUnicodeScalarString(source);
      return decodeTypedValue(source, descriptor, registry, "$", new Set());
    }
    if (codec !== "json" && codec !== "problem_json") {
      throw new SceneryClientError("contract_violation", bindingAddress, "unsupported response codec");
    }
    return decodeTypedValue(parseExactJSON(source), descriptor, registry, "$", new Set());
  } catch (cause) {
    if (cause instanceof SceneryClientError && cause.bindingAddress === bindingAddress) throw cause;
    throw new SceneryClientError("contract_violation", bindingAddress, "malformed response body", safeCause(cause));
  }
}

export async function assertEmptyResponse(response: Response, bindingAddress: string, maximumBytes: number): Promise<void> {
  if ((await readBoundedResponse(response, maximumBytes, bindingAddress)).byteLength !== 0) {
    throw new SceneryClientError("contract_violation", bindingAddress, "response body contradicts the contract");
  }
}

/*__scenery_runtime_retry_start__*/
export async function fetchWithRetry(
  fetchImplementation: typeof globalThis.fetch,
  url: string,
  init: RequestInit,
  signal: AbortSignal | undefined,
  runtime: RetryRuntime,
  policy: RetryPolicy,
): Promise<Response> {
  let lastCause: unknown;
  for (let attempt = 1; attempt <= policy.maximumAttempts; attempt++) {
    if (signal?.aborted) throw new SceneryClientError("cancelled", "", "request cancelled", lastCause);
    try {
      const response = await fetchImplementation(url, replayableRequestInit(init));
      if (attempt === policy.maximumAttempts || !policy.statuses.includes(response.status)) return response;
      const delay = retryDelay(response.headers.get("retry-after"), runtime.now(), policy.maximumDelayMilliseconds);
      await response.body?.cancel();
      await runtime.sleep(delay, signal);
    } catch (cause) {
      if (signal?.aborted) throw new SceneryClientError("cancelled", "", "request cancelled", cause);
      lastCause = cause;
      if (attempt === policy.maximumAttempts) throw cause;
      await runtime.sleep(0, signal);
    }
  }
  throw lastCause;
}
/*__scenery_runtime_retry_end__*/

export function mergeHeaders(
  defaults: Readonly<Record<string, string>> | undefined,
  call: Readonly<Record<string, string>> | undefined,
  bindingAddress: string,
): Headers {
  const result = new Headers();
  for (const source of [defaults, call]) {
    for (const [rawName, value] of Object.entries(source ?? {})) {
      const name = rawName.toLowerCase();
      if (frameworkHeaders.has(name) || name.startsWith("x-forwarded-")) {
        throw new SceneryClientError("invalid_options", bindingAddress, "a generic header attempted to override a framework-owned header");
      }
      if (!/^[!#$%&'*+.^_§|~0-9a-z-]+$/.test(name) || /[\r\n]/.test(value)) {
        throw new SceneryClientError("invalid_options", bindingAddress, "invalid request header");
      }
      result.set(name, value);
    }
  }
  return result;
}

export function encodeHTTPValue(value: unknown, descriptor?: TypeDescriptor, registry: TypeRegistry = Object.freeze({})): string {
  if (descriptor !== undefined) return encodeTypedHTTPValue(value, descriptor, registry, new Set());
  if (typeof value === "string") {
    assertUnicodeScalarString(value);
    return value;
  }
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "bigint") return value.toString(10);
  if (typeof value === "number" && Number.isFinite(value) && !Object.is(value, -0)) return String(value);
  throw new SceneryClientError("invalid_input", "", "HTTP scalar value is missing or invalid");
}

export function encodeRFC3986(value: string): string {
  return encodeURIComponent(value).replace(/[!'()*]/g, (character) =>
    §%${character.charCodeAt(0).toString(16).toUpperCase()}§,
  );
}

export function appendPathTail(prefix: string, value: unknown, descriptor: TypeDescriptor, registry: TypeRegistry = Object.freeze({})): string {
  let semantic: string;
  if (value === undefined) {
    const resolved = resolveDescriptor(descriptor, registry, new Set());
    if (resolved.kind !== "optional") throw new SceneryClientError("invalid_input", "", "path tail is missing");
    return prefix === "" ? "/" : prefix;
  }
  semantic = encodeHTTPValue(value, descriptor, registry);
  if (semantic === "") return prefix === "" ? "/" : prefix;
  const segments = semantic.split("/");
  if (segments.some((segment) => segment === "" || segment === "." || segment === ".." || segment.includes("\\"))) {
    throw new SceneryClientError("invalid_input", "", "path tail contains an invalid segment");
  }
  for (const segment of segments) {
    try {
      const decodedAgain = decodeURIComponent(segment);
      if (decodedAgain === "." || decodedAgain === ".." || /[\\/\0]/.test(decodedAgain)) {
        throw new SceneryClientError("invalid_input", "", "path tail contains a hazardous encoded segment");
      }
    } catch (error) {
      if (error instanceof SceneryClientError) throw error;
    }
  }
  return §${prefix}/${segments.map(encodeRFC3986).join("/")}§;
}

/*__scenery_runtime_query_start__*/
export function appendQuery(target: string[], name: string, value: unknown, encoding = "repeated", descriptor?: TypeDescriptor, registry: TypeRegistry = Object.freeze({})): void {
  if (value === undefined) return;
  const encodedName = encodeRFC3986(name);
  if (encoding === "json") {
    const encoded = descriptor === undefined ? encodeJSON(value as JsonValue) : encodeTypedJSON(value, descriptor, registry);
    target.push(§${encodedName}=${encodeRFC3986(encoded)}§);
    return;
  }
  const collection = orderedHTTPItems(value, descriptor, registry, "$query");
  const values = collection.items.map((item) => encodeHTTPValue(item, collection.itemDescriptor, registry));
  if (encoding === "comma") {
    target.push(§${encodedName}=${values.map(encodeRFC3986).join(",")}§);
    return;
  }
  for (const item of values) target.push(§${encodedName}=${encodeRFC3986(item)}§);
}
/*__scenery_runtime_query_end__*/

/*__scenery_runtime_request_header_start__*/
export function appendHeader(target: Headers, name: string, value: unknown, encoding = "repeated", descriptor?: TypeDescriptor, registry: TypeRegistry = Object.freeze({})): void {
  if (value === undefined) return;
  const collection = orderedHTTPItems(value, descriptor, registry, "$headers");
  const values = collection.items.map((item) => encodeHTTPValue(item, collection.itemDescriptor, registry));
  if (encoding === "comma") target.set(name, values.join(","));
	else {
		if (values.length > 1) throw new SceneryClientError("unsupported_runtime", "", "fetch cannot preserve repeated request header field lines");
		if (values.length === 1) target.set(name, values[0]!);
	}
}
/*__scenery_runtime_request_header_end__*/

/*__scenery_runtime_request_cookie_start__*/
export function appendCookie(target: string[], name: string, value: unknown, descriptor?: TypeDescriptor, registry: TypeRegistry = Object.freeze({})): void {
  if (value === undefined) return;
  if (!/^[!#$%&'*+.^_§|~0-9A-Za-z-]+$/.test(name)) {
    throw new SceneryClientError("invalid_input", "", "invalid cookie name");
  }
  target.push(§${name}=${encodeRFC3986(encodeHTTPValue(value, descriptor, registry))}§);
}
/*__scenery_runtime_request_cookie_end__*/

export function isProblemCode(value: unknown, code: string): boolean {
  return isObject(value) && value.code === code;
}

`
