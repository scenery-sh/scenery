package generate

const tsRuntimeJSON = `const jsonNumbers = new WeakSet<object>();
const forbiddenObjectKeys = new Set(["__proto__", "prototype", "constructor"]);
const frameworkHeaders = new Set([
  "authorization",
  "connection",
  "content-length",
  "content-type",
  "cookie",
  "forwarded",
  "host",
  "traceparent",
  "tracestate",
]);

export function jsonNumber(coefficient: string, scale: number): JsonNumber {
  const normalized = normalizeJsonNumber(coefficient, scale);
  const value = Object.freeze({ coefficient: normalized.coefficient, scale: normalized.scale }) as JsonNumber;
  jsonNumbers.add(value as object);
  return value;
}

export function isJsonNumber(value: unknown): value is JsonNumber {
  return typeof value === "object" && value !== null && jsonNumbers.has(value);
}

export function freezeMetadata<T>(value: T): Readonly<T> {
  if (typeof value !== "object" || value === null || Object.isFrozen(value)) return value as Readonly<T>;
  for (const item of Object.values(value)) freezeMetadata(item);
  return Object.freeze(value);
}

export function decimal(value: string): DecimalString {
  encodePrimitive(value, "decimal", "$.");
  return value as DecimalString;
}

export function uuid(value: string): UUIDString {
  encodePrimitive(value, "uuid", "$.");
  return value as UUIDString;
}

export function date(value: string): DateString {
  encodePrimitive(value, "date", "$.");
  return value as DateString;
}

export function dateTime(value: string): DateTimeString {
  encodePrimitive(value, "datetime", "$.");
  return value as DateTimeString;
}

export function duration(value: string): DurationString {
  encodePrimitive(value, "duration", "$.");
  return value as DurationString;
}

export function url(value: string): URLString {
  encodePrimitive(value, "url", "$.");
  return value as URLString;
}

export function relativePath(value: string): RelativePathString {
  encodePrimitive(value, "relative_path", "$.");
  return value as RelativePathString;
}

export function parseExactJSON(source: string): JsonValue {
  let offset = 0;
  const stringSpecial = /["\\\u0000-\u001f\ud800-\udfff]/g;
  const numberToken = /-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/y;
  const whitespace = () => {
    while (offset < source.length) {
      const code = source.charCodeAt(offset);
      if (code !== 32 && code !== 9 && code !== 10 && code !== 13) break;
      offset++;
    }
  };
  const fail = (message: string): never => {
    throw new SceneryClientError("contract_violation", "", message);
  };
  const parseString = (): string => {
    const start = offset;
    stringSpecial.lastIndex = start + 1;
    const special = stringSpecial.exec(source);
    if (special !== null && special[0] === '"') {
      offset = special.index + 1;
      return source.slice(start + 1, special.index);
    }
    offset++;
    let escaped = false;
    while (offset < source.length) {
      const character = source[offset] ?? "";
      if (!escaped && character === '"') {
        offset++;
        let value: string;
        try {
          value = JSON.parse(source.slice(start, offset)) as string;
        } catch {
          return fail("invalid JSON string");
        }
        assertUnicodeScalarString(value);
        return value;
      }
      if (!escaped && character.charCodeAt(0) < 0x20) return fail("invalid JSON string");
      escaped = !escaped && character === "\\";
      if (character !== "\\") escaped = false;
      offset++;
    }
    return fail("unterminated JSON string");
  };
  const parseValue = (): JsonValue => {
    whitespace();
    const character = source[offset];
    if (character === '"') return parseString();
    if (character === "[") {
      offset++;
      const values: JsonValue[] = [];
      whitespace();
      if (source[offset] === "]") {
        offset++;
        return Object.freeze(values);
      }
      while (true) {
        values.push(parseValue());
        whitespace();
        if (source[offset] === "]") {
          offset++;
          return Object.freeze(values);
        }
        if (source[offset] !== ",") return fail("invalid JSON array");
        offset++;
      }
    }
    if (character === "{") {
      offset++;
      const value = Object.create(null) as Record<string, JsonValue>;
      const names = new Set<string>();
      whitespace();
      if (source[offset] === "}") {
        offset++;
        return Object.freeze(value);
      }
      while (true) {
        whitespace();
        if (source[offset] !== '"') return fail("invalid JSON object member");
        const name = parseString();
        if (names.has(name)) return fail("duplicate object member");
        assertSafeKey(name);
        names.add(name);
        whitespace();
        if (source[offset] !== ":") return fail("invalid JSON object member");
        offset++;
        value[name] = parseValue();
        whitespace();
        if (source[offset] === "}") {
          offset++;
          return Object.freeze(value);
        }
        if (source[offset] !== ",") return fail("invalid JSON object");
        offset++;
      }
    }
    if (character === "t" && source.startsWith("true", offset)) {
      offset += 4;
      return true;
    }
    if (character === "f" && source.startsWith("false", offset)) {
      offset += 5;
      return false;
    }
    if (character === "n" && source.startsWith("null", offset)) {
      offset += 4;
      return null;
    }
    numberToken.lastIndex = offset;
    const match = numberToken.exec(source);
    if (match === null) return fail("invalid JSON value");
    offset += match[0].length;
    return jsonNumberFromToken(match[0]);
  };
  if (source.charCodeAt(0) === 0xfeff) fail("JSON byte-order mark is forbidden");
  const value = parseValue();
  whitespace();
  if (offset !== source.length) fail("trailing bytes after JSON value");
  return value;
}

export function encodeJSON(value: JsonValue): string {
  return encodeExactJSON(value, "$");
}

export function encodeTypedJSON(value: unknown, descriptor: TypeDescriptor, registry: TypeRegistry): string {
  return encodeTypedValue(value, descriptor, registry, "$", new Set());
}

export function encodeRequestBody(
  value: unknown,
  codec: string,
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
): BodyInit {
  if (codec === "json" || codec === "problem_json") return encodeTypedJSON(value, descriptor, registry);
  if (codec === "text") {
    if (typeof value !== "string") invalid("$", "text body requires a string");
    assertUnicodeScalarString(value);
    return value;
  }
  if (codec === "bytes") {
    if (!(value instanceof Uint8Array)) invalid("$", "bytes body requires Uint8Array");
    return ownedArrayBuffer(value);
  }
  const resolved = resolveDescriptor(descriptor, registry, new Set());
  if (resolved.kind !== "record" || !isObject(value)) invalid("$", §${codec} body requires a record§);
/*__scenery_runtime_validation_start__*/
  validateRecordRules(value, resolved, registry, "$");
/*__scenery_runtime_validation_end__*/
  if (codec === "form") {
    const form = new URLSearchParams();
    for (const field of resolved.fields) {
      const item = value[field.property];
      if (item === undefined && field.optional) continue;
      validateConstraints(item, field.constraints, field.value, registry, §$.${field.property}§);
      const collection = orderedHTTPItems(item, field.value, registry, §$.${field.property}§);
      for (const entry of collection.items) form.append(field.wire, encodeHTTPValue(entry, collection.itemDescriptor, registry));
    }
    return form;
  }
/*__scenery_runtime_multipart_start__*/
  if (codec === "multipart") invalid("$", "multipart requires a declared part schema");
/*__scenery_runtime_multipart_end__*/
  invalid("$", "unsupported request body codec");
}

/*__scenery_runtime_multipart_start__*/
export function encodeMultipartRequestBody(
  value: unknown,
  descriptor: MultipartBodyDescriptor,
  registry: TypeRegistry,
): Readonly<{ body: ArrayBuffer; contentType: string }> {
  if (!isObject(value) || Array.isArray(value)) invalid("$", "multipart body requires a record");
  encodeTypedValue(value, descriptor.value, registry, "$", new Set());
  if (!Number.isSafeInteger(descriptor.maxParts) || descriptor.maxParts <= 0) invalid("$", "invalid multipart part limit");
  if (!Number.isSafeInteger(descriptor.maxBytes) || descriptor.maxBytes <= 0) invalid("$", "invalid multipart body limit");
  const encoder = new TextEncoder();
  const encodedParts: Array<Readonly<{ header: string; bytes: Uint8Array }>> = [];
  for (const part of descriptor.parts) {
    const item = value[part.property];
    if (item === undefined) {
      if (part.optional) continue;
      invalid(§$.${part.property}§, §missing multipart part ${part.name}§);
    }
    const collection = orderedHTTPItems(item, part.value, registry, §$.${part.property}§);
    if (collection.collection !== part.multiple) invalid(§$.${part.property}§, "multipart multiplicity contradicts the contract");
    if (collection.items.length === 0 && !part.optional) invalid(§$.${part.property}§, "required multipart collection is empty");
    for (const entry of collection.items) {
      let bytes: Uint8Array;
      let filename: string | undefined;
      let contentType: string;
      if (part.kind === "text") {
        bytes = encoder.encode(encodeHTTPValue(entry, collection.itemDescriptor, registry));
        contentType = selectMultipartMediaType(part.mediaTypes, "text/plain");
      } else if (part.kind === "bytes") {
        if (!(entry instanceof Uint8Array)) invalid(§$.${part.property}§, "multipart byte part requires Uint8Array");
        bytes = new Uint8Array(entry);
        contentType = selectMultipartMediaType(part.mediaTypes, "application/octet-stream");
      } else {
        if (part.retainFilename) {
          if (!isObject(entry) || Array.isArray(entry) || part.fileProperties === undefined) invalid(§$.${part.property}§, "multipart file metadata is invalid");
          const rawBytes = entry[part.fileProperties.bytes];
          const rawFilename = entry[part.fileProperties.filename];
          const rawMediaType = entry[part.fileProperties.mediaType];
          if (!(rawBytes instanceof Uint8Array) || typeof rawFilename !== "string" || rawFilename === "" || typeof rawMediaType !== "string") invalid(§$.${part.property}§, "multipart file metadata is invalid");
          bytes = new Uint8Array(rawBytes);
          filename = rawFilename;
          contentType = canonicalMultipartMediaType(rawMediaType, part.mediaTypes);
        } else {
          if (!(entry instanceof Uint8Array)) invalid(§$.${part.property}§, "multipart file part requires Uint8Array");
          bytes = new Uint8Array(entry);
          filename = "blob";
          contentType = selectMultipartMediaType(part.mediaTypes, "application/octet-stream");
        }
      }
      if (bytes.byteLength > part.maxBytes) invalid(§$.${part.property}§, §multipart part ${part.name} exceeds its byte limit§);
      const disposition = §Content-Disposition: form-data; name="${multipartQuoted(part.name)}"${filename === undefined ? "" : §; filename="${multipartQuoted(filename)}"§}§;
      encodedParts.push({ header: §${disposition}\r\nContent-Type: ${contentType}§, bytes });
      if (encodedParts.length > descriptor.maxParts) invalid("$", "multipart part limit exceeded");
    }
  }
  let boundary = "scenery-boundary";
  while (encodedParts.some((part) => part.header.includes(boundary) || containsByteSequence(part.bytes, encoder.encode(boundary)))) boundary += "x";
  const chunks: Uint8Array[] = [];
  let total = 0;
  const append = (chunk: Uint8Array) => {
    total += chunk.byteLength;
    if (total > descriptor.maxBytes) invalid("$", "multipart body exceeds its byte limit");
    chunks.push(chunk);
  };
  for (const part of encodedParts) {
    append(encoder.encode(§--${boundary}\r\n${part.header}\r\n\r\n§));
    append(part.bytes);
    append(encoder.encode("\r\n"));
  }
  append(encoder.encode(§--${boundary}--\r\n§));
  const body = new ArrayBuffer(total);
  const bodyBytes = new Uint8Array(body);
  let offset = 0;
  for (const chunk of chunks) { bodyBytes.set(chunk, offset); offset += chunk.byteLength; }
  return Object.freeze({ body, contentType: §multipart/form-data; boundary=${boundary}§ });
}
/*__scenery_runtime_multipart_end__*/

function ownedArrayBuffer(value: Uint8Array): ArrayBuffer {
  const buffer = new ArrayBuffer(value.byteLength);
  new Uint8Array(buffer).set(value);
  return buffer;
}

/*__scenery_runtime_multipart_start__*/
function multipartQuoted(value: string): string {
  assertUnicodeScalarString(value);
  if (value === "" || /[\u0000-\u001f\u007f]/.test(value)) invalid("$", "invalid multipart name or filename");
  return value.replace(/(["\\])/g, "\\$1");
}

function canonicalMultipartMediaType(value: string, allowed: readonly string[]): string {
  const parsed = parseMediaType(value);
  if (parsed.base.includes("*") || Object.keys(parsed.parameters).length > 0 || !multipartMediaAllowed(parsed.base, allowed)) invalid("$", "multipart media type is not accepted");
  return parsed.base;
}

function selectMultipartMediaType(allowed: readonly string[], fallback: string): string {
  if (allowed.length === 0 || multipartMediaAllowed(fallback, allowed)) return fallback;
  for (const value of allowed) {
    const parsed = parseMediaType(value);
    if (!parsed.base.includes("*") && Object.keys(parsed.parameters).length === 0) return parsed.base;
  }
  invalid("$", "multipart media type requires caller metadata");
}

function multipartMediaAllowed(actual: string, allowed: readonly string[]): boolean {
  if (allowed.length === 0) return true;
  const actualParts = parseMediaType(actual).base.split("/");
  return allowed.some((candidate) => {
    const expected = parseMediaType(candidate).base.split("/");
    return (expected[0] === "*" || expected[0] === actualParts[0]) && (expected[1] === "*" || expected[1] === actualParts[1]);
  });
}

function containsByteSequence(value: Uint8Array, sequence: Uint8Array): boolean {
  outer: for (let offset = 0; offset + sequence.length <= value.length; offset++) {
    for (let index = 0; index < sequence.length; index++) if (value[offset + index] !== sequence[index]) continue outer;
    return true;
  }
  return false;
}
/*__scenery_runtime_multipart_end__*/

`
