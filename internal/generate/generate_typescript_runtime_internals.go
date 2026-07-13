package generate

func renderTSRuntimeInternals() string {
	return `function resolveDescriptor(descriptor: TypeDescriptor, registry: TypeRegistry, resolving: Set<string>): TypeDescriptor {
  if (descriptor.kind !== "named") return descriptor;
  if (resolving.has(descriptor.name)) invalid("$", "recursive named type descriptor");
  const resolved = registry[descriptor.name];
  if (resolved === undefined) invalid("$", "missing named type descriptor");
  const next = new Set(resolving);
  next.add(descriptor.name);
  return resolveDescriptor(resolved, registry, next);
}

function compareUTF8Bytewise(left: string, right: string): number {
  const encoder = new TextEncoder();
  const leftBytes = encoder.encode(left);
  const rightBytes = encoder.encode(right);
  const length = Math.min(leftBytes.length, rightBytes.length);
  for (let index = 0; index < length; index++) {
    const difference = (leftBytes[index] ?? 0) - (rightBytes[index] ?? 0);
    if (difference !== 0) return difference;
  }
  return leftBytes.length - rightBytes.length;
}

function orderedHTTPItems(
  value: unknown,
  descriptor: TypeDescriptor | undefined,
  registry: TypeRegistry,
  path: string,
): Readonly<{ items: readonly unknown[]; itemDescriptor: TypeDescriptor | undefined; collection: boolean }> {
  if (descriptor === undefined) return { items: Array.isArray(value) ? [...value] : [value], itemDescriptor: undefined, collection: Array.isArray(value) };
  let resolved = resolveDescriptor(descriptor, registry, new Set());
  while (resolved.kind === "optional" || resolved.kind === "nullable") resolved = resolveDescriptor(resolved.value, registry, new Set());
  if (resolved.kind !== "list" && resolved.kind !== "set") return { items: [value], itemDescriptor: resolved, collection: false };
  if (!Array.isArray(value)) invalid(path, "HTTP collection value must be an array");
  if (resolved.kind === "list") return { items: [...value], itemDescriptor: resolved.value, collection: true };
  const ordered = value.map((item, index) => ({ item, key: encodeTypedValue(item, resolved.value, registry, §${path}[${index}]§, new Set()) }));
  ordered.sort((left, right) => compareUTF8Bytewise(left.key, right.key));
  for (let index = 1; index < ordered.length; index++) if (ordered[index - 1]!.key === ordered[index]!.key) invalid(path, "duplicate set element");
  return { items: ordered.map((entry) => entry.item), itemDescriptor: resolved.value, collection: true };
}

function encodeTypedHTTPValue(value: unknown, descriptor: TypeDescriptor, registry: TypeRegistry, resolving: Set<string>): string {
  if (descriptor.kind === "named") {
    if (resolving.has(descriptor.name)) invalid("$", "recursive HTTP type descriptor");
    const resolved = registry[descriptor.name];
    if (resolved === undefined) invalid("$", "missing HTTP type descriptor");
    const next = new Set(resolving);
    next.add(descriptor.name);
    return encodeTypedHTTPValue(value, resolved, registry, next);
  }
  if (descriptor.kind === "optional") {
    if (value === undefined) invalid("$", "optional HTTP value is absent");
    return encodeTypedHTTPValue(value, descriptor.value, registry, resolving);
  }
  if (descriptor.kind === "nullable") {
    if (value === null) invalid("$", "HTTP scalar mapping cannot encode null");
    return encodeTypedHTTPValue(value, descriptor.value, registry, resolving);
  }
  if (descriptor.kind === "enum") {
    if (typeof value !== "string" || (!descriptor.open && !descriptor.values.includes(value))) invalid("$", "invalid HTTP enum value");
    return value;
  }
  if (descriptor.kind !== "primitive" || descriptor.name === "json" || descriptor.name === "bytes" || descriptor.name === "problem") invalid("$", "type has no scalar HTTP codec");
  encodePrimitive(value, descriptor.name, "$");
  return encodeHTTPValue(value);
}

function replayableRequestInit(init: RequestInit): RequestInit {
  const body = init.body;
  if (typeof ReadableStream !== "undefined" && body instanceof ReadableStream) {
    throw new SceneryClientError("invalid_options", "", "retry cannot replay a streaming request body");
  }
  let replayBody = body;
  if (body instanceof Uint8Array) replayBody = new Uint8Array(body);
  if (body instanceof URLSearchParams) replayBody = new URLSearchParams(body);
  return { ...init, headers: new Headers(init.headers), body: replayBody };
}

function retryDelay(retryAfter: string | null, now: number, maximum: number): number {
  if (retryAfter === null) return 0;
  if (/^\d+$/.test(retryAfter)) return Math.min(maximum, Number(retryAfter) * 1000);
  const parsed = Date.parse(retryAfter);
  if (!Number.isFinite(parsed)) return 0;
  return Math.max(0, Math.min(maximum, parsed - now));
}

function validateConstraints(
  value: unknown,
  constraints: ConstraintDescriptor | undefined,
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
  path: string,
): void {
  if (constraints === undefined || value === undefined || value === null) return;
  const length = typeof value === "string" ? [...value].length : Array.isArray(value) ? value.length : isObject(value) ? Object.keys(value).length : undefined;
  const minimumLength = constraintInteger(constraints.min_length ?? constraints.min_items);
  const maximumLength = constraintInteger(constraints.max_length ?? constraints.max_items);
  if (minimumLength !== undefined && (length === undefined || length < minimumLength)) invalid(path, "minimum length constraint failed");
  if (maximumLength !== undefined && (length === undefined || length > maximumLength)) invalid(path, "maximum length constraint failed");
  if (constraints.pattern !== undefined && (typeof value !== "string" || !new RegExp(constraints.pattern, "u").test(value))) invalid(path, "pattern constraint failed");
  if (constraints.format !== undefined && typeof value === "string") validateStringFormat(value, constraints.format, path);
  if (constraints.minimum !== undefined && compareExactNumbers(value, constraints.minimum, path) < 0) invalid(path, "minimum constraint failed");
  if (constraints.maximum !== undefined && compareExactNumbers(value, constraints.maximum, path) > 0) invalid(path, "maximum constraint failed");
  if (constraints.unique_items === true) {
    if (!Array.isArray(value)) invalid(path, "unique_items requires an array");
    const itemDescriptor: TypeDescriptor = descriptor.kind === "list" || descriptor.kind === "set" ? descriptor.value : { kind: "primitive", name: "json" };
    const encoded = value.map((item, index) => encodeTypedValue(item, itemDescriptor, registry, §${path}[${index}]§, new Set()));
    if (new Set(encoded).size !== encoded.length) invalid(path, "unique_items constraint failed");
  }
}

function constraintInteger(value: number | string | undefined): number | undefined {
  if (value === undefined) return undefined;
  const parsed = typeof value === "number" ? value : Number(value);
  return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : undefined;
}

function compareExactNumbers(value: unknown, limit: string, path: string): number {
  const left = exactNumberParts(value, path);
  const right = exactNumberParts(limit, path);
  const scale = Math.max(left.scale, right.scale);
  const leftValue = BigInt(left.coefficient) * 10n ** BigInt(scale - left.scale);
  const rightValue = BigInt(right.coefficient) * 10n ** BigInt(scale - right.scale);
  return leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : 0;
}

function exactNumberParts(value: unknown, path: string): { coefficient: string; scale: number } {
  if (isJsonNumber(value)) return normalizeJsonNumber(value.coefficient, value.scale);
  if (typeof value === "bigint") return { coefficient: value.toString(10), scale: 0 };
  if (typeof value === "number" && Number.isFinite(value) && !Object.is(value, -0)) return jsonNumberTokenParts(String(value), path);
  if (typeof value === "string" && /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$/.test(value)) return jsonNumberTokenParts(value, path);
  invalid(path, "numeric constraint requires a number");
}

function jsonNumberTokenParts(token: string, path: string): { coefficient: string; scale: number } {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(token);
  if (match === null) invalid(path, "invalid numeric constraint");
  const exponent = Number(match[4] ?? "0");
  if (!Number.isSafeInteger(exponent) || Math.abs(exponent) > 1_000_000) invalid(path, "numeric constraint exponent is out of range");
  const fraction = match[3] ?? "";
  return normalizeJsonNumber(§${match[1] ?? ""}${match[2] ?? ""}${fraction}§, fraction.length - exponent);
}

function validateStringFormat(value: string, format: string, path: string): void {
  const formats: Readonly<Record<string, string>> = { uuid: "uuid", date: "date", datetime: "datetime", duration: "duration", url: "url", relative_path: "relative_path" };
  const primitive = formats[format];
  if (primitive === undefined) invalid(path, "unknown string format");
  encodePrimitive(value, primitive, path);
}

function decodeTypedValue(
  value: unknown,
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
  path: string,
  resolving: Set<string>,
): unknown {
  if (descriptor.kind === "named") {
    const resolved = registry[descriptor.name];
    if (resolved === undefined || resolving.has(descriptor.name)) invalid(path, "invalid named type descriptor");
    const next = new Set(resolving);
    next.add(descriptor.name);
    return decodeTypedValue(value, resolved, registry, path, next);
  }
  if (descriptor.kind === "optional") return decodeTypedValue(value, descriptor.value, registry, path, resolving);
  if (descriptor.kind === "nullable") return value === null ? null : decodeTypedValue(value, descriptor.value, registry, path, resolving);
  if (descriptor.kind === "primitive") return decodePrimitive(value, descriptor.name, path);
  if (descriptor.kind === "list" || descriptor.kind === "set") {
    if (!Array.isArray(value)) invalid(path, "expected an array");
    const decoded = value.map((item, index) => decodeTypedValue(item, descriptor.value, registry, §${path}[${index}]§, resolving));
    if (descriptor.kind === "set") {
      const canonical = value.map((item, index) => encodeExactJSON(item as JsonValue, §${path}[${index}]§));
      for (let index = 1; index < canonical.length; index++) {
        if (compareUTF8Bytewise(canonical[index - 1] ?? "", canonical[index] ?? "") >= 0) invalid(path, "set is not in unique canonical order");
      }
    }
    return Object.freeze(decoded);
  }
  if (descriptor.kind === "map") {
    if (!isObject(value) || Array.isArray(value)) invalid(path, "expected a map");
    const decoded = Object.create(null) as Record<string, unknown>;
    for (const key of Object.keys(value)) decoded[key] = decodeTypedValue(value[key], descriptor.value, registry, §${path}.${key}§, resolving);
    return Object.freeze(decoded);
  }
  if (descriptor.kind === "tuple") {
    if (!Array.isArray(value) || value.length !== descriptor.values.length) invalid(path, "tuple length mismatch");
    return Object.freeze(descriptor.values.map((item, index) => decodeTypedValue(value[index], item, registry, §${path}[${index}]§, resolving)));
  }
  if (descriptor.kind === "enum") {
    if (typeof value !== "string" || (!descriptor.open && !descriptor.values.includes(value))) invalid(path, "invalid enum value");
    return value;
  }
  if (descriptor.kind === "union") {
    if (!isObject(value) || typeof value[descriptor.discriminator] !== "string") invalid(path, "invalid tagged union");
    const tag = value[descriptor.discriminator] as string;
    const payload = Object.create(null) as Record<string, unknown>;
    for (const [key, item] of Object.entries(value)) {
      if (key !== descriptor.discriminator) payload[key] = item;
    }
    const variant = descriptor.variants[tag];
    if (variant === undefined) {
      if (!descriptor.open) invalid(path, "unknown closed-union variant");
      return Object.freeze({ kind: tag, value: Object.freeze(payload) as JsonValue, unknown: true });
    }
    return Object.freeze({ kind: tag, value: decodeTypedValue(Object.freeze(payload), variant, registry, §${path}.value§, resolving) });
  }
  if (!isObject(value) || Array.isArray(value)) invalid(path, "expected a record");
  const byWire = new Map(descriptor.fields.map((field) => [field.wire, field] as const));
  const decoded = Object.create(null) as Record<string, unknown>;
  const unknown = Object.create(null) as Record<string, JsonValue>;
  for (const [wire, item] of Object.entries(value)) {
    const field = byWire.get(wire);
    if (field === undefined) {
      if (!descriptor.preserveUnknown) invalid(§${path}.${wire}§, "unknown record field");
      unknown[wire] = item as JsonValue;
      continue;
    }
    const decodedValue = decodeTypedValue(item, field.value, registry, §${path}.${field.property}§, resolving);
    validateConstraints(decodedValue, field.constraints, field.value, registry, §${path}.${field.property}§);
    decoded[field.property] = decodedValue;
  }
  for (const field of descriptor.fields) {
    if (!field.optional && !Object.prototype.hasOwnProperty.call(decoded, field.property)) invalid(§${path}.${field.property}§, "required field is absent");
  }
  if (descriptor.preserveUnknown) decoded.unknownFields = Object.freeze(unknown);
  validateRecordRules(decoded, descriptor, registry, path);
  return Object.freeze(decoded);
}

function encodePrimitive(value: unknown, name: string, path: string): string {
  switch (name) {
    case "json":
      return encodeExactJSON(value as JsonValue, path);
    case "bool":
      if (typeof value !== "boolean") invalid(path, "expected bool");
      return value ? "true" : "false";
    case "int":
    case "int64":
      if (typeof value !== "bigint") invalid(path, §expected ${name}§);
      return JSON.stringify(value.toString(10));
    case "uint64":
    case "size":
      if (typeof value !== "bigint" || value < 0n) invalid(path, §expected ${name}§);
      return JSON.stringify(value.toString(10));
    case "int32":
      return encodeBoundedNumber(value, path, -2147483648, 2147483647, true);
    case "uint32":
      return encodeBoundedNumber(value, path, 0, 4294967295, true);
    case "float32": {
      const encoded = encodeBoundedNumber(value, path, -3.4028234663852886e38, 3.4028234663852886e38, false);
      if (typeof value !== "number" || Math.fround(value) !== value) invalid(path, "float32 value is not exactly representable");
      return encoded;
    }
    case "float64":
      return encodeBoundedNumber(value, path, Number.NEGATIVE_INFINITY, Number.POSITIVE_INFINITY, false);
    case "bytes":
      if (!(value instanceof Uint8Array)) invalid(path, "expected bytes");
      return JSON.stringify(bytesToBase64(new Uint8Array(value)));
    case "decimal":
      if (typeof value !== "string" || !/^-?(?:0|[1-9][0-9]*)(?:\.[0-9]*[1-9])?$/.test(value) || value === "-0") invalid(path, "invalid canonical decimal");
      return JSON.stringify(value);
    case "uuid":
      if (typeof value !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)) invalid(path, "invalid UUID");
      return JSON.stringify(value);
    case "date":
      if (typeof value !== "string" || !validCalendarDate(value)) invalid(path, "invalid date");
      return JSON.stringify(value);
    case "datetime":
      if (typeof value !== "string" || !validDateTime(value)) invalid(path, "invalid normalized datetime");
      return JSON.stringify(value);
    case "duration":
      if (typeof value !== "string" || !/^-?P(?=\d+D|T\d)(?:\d+D)?(?:T(?=\d)(?:\d+H)?(?:\d+M)?(?:\d+(?:\.\d{1,9})?S)?)?$/.test(value)) invalid(path, "invalid duration");
      return JSON.stringify(value);
    case "url":
      if (typeof value !== "string" || !validCanonicalURL(value)) invalid(path, "invalid canonical URL");
      return JSON.stringify(value);
    case "relative_path":
      if (typeof value !== "string" || value === "" || value.startsWith("/") || value.includes("\\") || value.split("/").some((part) => part === "" || part === "." || part === "..")) invalid(path, "invalid relative path");
      return JSON.stringify(value);
    case "string":
      if (typeof value !== "string") invalid(path, "expected string");
      assertUnicodeScalarString(value);
      return JSON.stringify(value);
		case "problem":
			return encodeProblem(value, path);
		case "unit":
			if (!isObject(value) || Array.isArray(value) || Object.keys(value).length !== 0) invalid(path, "unit must be an empty object");
			return "{}";
		case "execution_receipt":
      if (!isObject(value) || typeof value.durableIdentity !== "string" || typeof value.executionId !== "string" || typeof value.acceptedRevision !== "string" || (value.statusUrl !== undefined && typeof value.statusUrl !== "string")) invalid(path, "invalid enqueue receipt");
      return §{"accepted_revision":${JSON.stringify(value.acceptedRevision)},"durable_identity":${JSON.stringify(value.durableIdentity)},"execution_id":${JSON.stringify(value.executionId)}${value.statusUrl === undefined ? "" : §,"status_url":${encodePrimitive(value.statusUrl, "url", path)}§}}§;
    default:
      invalid(path, "unsupported primitive type");
  }
}

function decodePrimitive(value: unknown, name: string, path: string): unknown {
  switch (name) {
    case "json":
      return value;
    case "bool":
      if (typeof value !== "boolean") invalid(path, "expected bool");
      return value;
    case "int":
    case "int64":
      return decodeBigInt(value, path, false);
    case "uint64":
    case "size":
      return decodeBigInt(value, path, true);
    case "int32":
      return decodeBoundedInteger(value, path, -2147483648, 2147483647);
    case "uint32":
      return decodeBoundedInteger(value, path, 0, 4294967295);
    case "float32":
    case "float64": {
      if (!isJsonNumber(value)) invalid(path, "expected JSON number");
      const decoded = Number(renderJsonNumber(value));
      if (!Number.isFinite(decoded) || Object.is(decoded, -0)) invalid(path, "lossy or non-finite float");
      const roundTrip = jsonNumberTokenParts(String(decoded), path);
      const original = normalizeJsonNumber(value.coefficient, value.scale);
      if (roundTrip.coefficient !== original.coefficient || roundTrip.scale !== original.scale) invalid(path, "lossy float representation");
      if (name === "float32" && Math.fround(decoded) !== decoded) invalid(path, "float32 value is not exactly representable");
      return decoded;
    }
    case "bytes":
      if (typeof value !== "string") invalid(path, "expected base64 string");
      return base64ToBytes(value, path);
    case "decimal":
    case "uuid":
    case "date":
    case "datetime":
    case "duration":
    case "url":
    case "relative_path":
    case "string":
      if (typeof value !== "string") invalid(path, §expected ${name}§);
      encodePrimitive(value, name, path);
      return value;
		case "problem":
			if (!isObject(value) || typeof value.code !== "string" || typeof value.message !== "string" || (value.path !== undefined && typeof value.path !== "string")) invalid(path, "invalid problem");
			return Object.freeze({ code: value.code, message: value.message, ...(value.path === undefined ? {} : { path: value.path }) });
		case "unit":
			if (!isObject(value) || Array.isArray(value) || Object.keys(value).length !== 0) invalid(path, "unit must be an empty object");
			return Object.freeze({});
		case "execution_receipt":
      if (!isObject(value) || typeof value.durable_identity !== "string" || typeof value.execution_id !== "string" || typeof value.accepted_revision !== "string" || (value.status_url !== undefined && typeof value.status_url !== "string")) invalid(path, "invalid enqueue receipt");
      if (value.status_url !== undefined) encodePrimitive(value.status_url, "url", path);
      return Object.freeze({ durableIdentity: value.durable_identity, executionId: value.execution_id, acceptedRevision: value.accepted_revision, ...(value.status_url === undefined ? {} : { statusUrl: value.status_url }) });
    default:
      invalid(path, "unsupported primitive type");
  }
}

function encodeProblem(value: unknown, path: string): string {
  if (!isObject(value) || typeof value.code !== "string" || typeof value.message !== "string" || (value.path !== undefined && typeof value.path !== "string")) invalid(path, "invalid problem");
  const members = [§"code":${JSON.stringify(value.code)}§, §"message":${JSON.stringify(value.message)}§];
  if (value.path !== undefined) members.push(§"path":${JSON.stringify(value.path)}§);
  return §{${members.join(",")}}§;
}

function encodeBoundedNumber(value: unknown, path: string, minimum: number, maximum: number, integer: boolean): string {
  if (typeof value !== "number" || !Number.isFinite(value) || Object.is(value, -0) || value < minimum || value > maximum || (integer && !Number.isInteger(value))) invalid(path, "invalid numeric value");
  return JSON.stringify(value);
}

function decodeBoundedInteger(value: unknown, path: string, minimum: number, maximum: number): number {
  if (!isJsonNumber(value) || value.scale !== 0) invalid(path, "expected integral JSON number");
  const decoded = Number(value.coefficient);
  if (!Number.isSafeInteger(decoded) || decoded < minimum || decoded > maximum) invalid(path, "integer is out of range");
  return decoded;
}

function decodeBigInt(value: unknown, path: string, unsigned: boolean): bigint {
  if (typeof value !== "string" || !/^-?(?:0|[1-9][0-9]*)$/.test(value)) invalid(path, "expected canonical integer string");
  const decoded = BigInt(value);
  if (unsigned && decoded < 0n) invalid(path, "unsigned integer is negative");
  return decoded;
}

function encodeExactJSON(value: JsonValue, path: string): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "string") {
    assertUnicodeScalarString(value);
    return JSON.stringify(value);
  }
  if (isJsonNumber(value)) return renderJsonNumber(value);
  if (Array.isArray(value)) return §[${value.map((item, index) => encodeExactJSON(item, §${path}[${index}]§)).join(",")}]§;
  if (!isObject(value)) invalid(path, "invalid exact JSON value");
  const members: string[] = [];
  for (const key of Object.keys(value).sort()) {
    assertSafeKey(key);
    members.push(§${JSON.stringify(key)}:${encodeExactJSON(value[key] as JsonValue, §${path}.${key}§)}§);
  }
  return §{${members.join(",")}}§;
}

function jsonNumberFromToken(token: string): JsonNumber {
  const match = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/.exec(token);
  if (match === null) invalid("$", "invalid JSON number");
  const exponent = Number(match[4] ?? "0");
  if (!Number.isSafeInteger(exponent) || Math.abs(exponent) > 1_000_000) invalid("$", "JSON number exponent is out of range");
  const fraction = match[3] ?? "";
  return jsonNumber(§${match[1] ?? ""}${match[2] ?? ""}${fraction}§, fraction.length - exponent);
}

function normalizeJsonNumber(coefficient: string, scale: number): { coefficient: string; scale: number } {
  if (!/^-?[0-9]+$/.test(coefficient) || !Number.isSafeInteger(scale) || Math.abs(scale) > 1_000_000) invalid("$", "invalid exact JSON number");
  const negative = coefficient.startsWith("-");
  let digits = coefficient.replace(/^-/, "").replace(/^0+(?=\d)/, "");
  if (/^0+$/.test(digits)) return { coefficient: "0", scale: 0 };
  if (scale < 0) {
    digits += "0".repeat(-scale);
    scale = 0;
  }
  while (scale > 0 && digits.endsWith("0")) {
    digits = digits.slice(0, -1);
    scale--;
  }
  return { coefficient: §${negative ? "-" : ""}${digits}§, scale };
}

function renderJsonNumber(value: JsonNumber): string {
  const normalized = normalizeJsonNumber(value.coefficient, value.scale);
  const negative = normalized.coefficient.startsWith("-");
  const digits = normalized.coefficient.replace(/^-/, "");
  if (normalized.scale === 0) return normalized.coefficient;
  const prefix = negative ? "-" : "";
  if (normalized.scale >= digits.length) return §${prefix}0.${"0".repeat(normalized.scale - digits.length)}${digits}§;
  return §${prefix}${digits.slice(0, digits.length - normalized.scale)}.${digits.slice(digits.length - normalized.scale)}§;
}

function validCalendarDate(value: string): boolean {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (match === null) return false;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  if (month < 1 || month > 12 || day < 1) return false;
  const days = [31, (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0 ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return day <= (days[month - 1] ?? 0);
}

function validDateTime(value: string): boolean {
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/.exec(value);
  if (match === null || !validCalendarDate(match[1] ?? "")) return false;
  const hour = Number(match[2]);
  const minute = Number(match[3]);
  const second = Number(match[4]);
  if (hour > 23 || minute > 59 || second > 59) return false;
  const fraction = match[5];
  return fraction === undefined || !fraction.endsWith("0");
}

function validCanonicalURL(value: string): boolean {
  if (/[^\x21-\x7e]/.test(value) || value.includes("\\")) return false;
	try { new URL(value); } catch { return false; }
  const match = /^([a-z][a-z0-9+.-]*):\/\/([^/?#]+)(\/[^?#]*)?(?:\?[^#]*)?(?:#.*)?$/.exec(value);
  if (match === null) return false;
  const scheme = match[1] ?? "";
  const authority = match[2] ?? "";
	const at = authority.lastIndexOf("@");
	const userInfo = at < 0 ? "" : authority.slice(0, at);
	const hostAuthority = at < 0 ? authority : authority.slice(at + 1);
	if (userInfo !== "" && !/^[A-Za-z0-9._~!$&'()*+,;=:%-]+$/.test(userInfo)) return false;
  if (hostAuthority.includes("%")) return false;
  const hostPort = /^\[([0-9a-f:.]+)\](?::(\d+))?$/.exec(hostAuthority) ?? /^([a-z0-9.-]+)(?::(\d+))?$/.exec(hostAuthority);
  if (hostPort === null || (hostPort[1] ?? "") === "" || (hostPort[1] ?? "").includes("..")) return false;
  const port = hostPort[2];
  if ((scheme === "http" && port === "80") || (scheme === "https" && port === "443")) return false;
  const path = match[3] ?? "/";
  if (path.split("/").some((segment) => segment === "." || segment === "..")) return false;
  for (const escape of value.matchAll(/%([0-9A-Fa-f]{2})/g)) {
    const hex = escape[1] ?? "";
    if (hex !== hex.toUpperCase() || /[A-Za-z0-9._~-]/.test(String.fromCharCode(Number.parseInt(hex, 16)))) return false;
  }
  return !/%(?![0-9A-F]{2})/.test(value);
}

function assertSafeKey(key: string): void {
  if (forbiddenObjectKeys.has(key)) throw new SceneryClientError("invalid_input", "", "unsafe object key");
  assertUnicodeScalarString(key);
}

function assertUnicodeScalarString(value: string): void {
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(++index);
      if (!(next >= 0xdc00 && next <= 0xdfff)) invalid("$", "unpaired Unicode surrogate");
    } else if (code >= 0xdc00 && code <= 0xdfff) {
      invalid("$", "unpaired Unicode surrogate");
    }
  }
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function base64ToBytes(value: string, path: string): Uint8Array {
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) invalid(path, "invalid padded base64");
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index++) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function invalid(path: string, message: string): never {
  throw new SceneryClientError("invalid_input", "", §${path}: ${message}§);
}

function safeCause(cause: unknown): unknown {
  return cause instanceof SceneryClientError ? cause : undefined;
}
`
}
