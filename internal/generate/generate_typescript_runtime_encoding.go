package generate

const tsRuntimeEncoding = `function encodeTypedValue(
  value: unknown,
  descriptor: TypeDescriptor,
  registry: TypeRegistry,
  path: string,
  resolving: Set<string>,
): string {
  if (descriptor.kind === "named") {
    const resolved = registry[descriptor.name];
    if (resolved === undefined || resolving.has(descriptor.name)) invalid(path, "invalid named type descriptor");
    const next = new Set(resolving);
    next.add(descriptor.name);
    return encodeTypedValue(value, resolved, registry, path, next);
  }
  if (descriptor.kind === "optional") {
    if (value === undefined) invalid(path, "optional value is absent outside a record field");
    return encodeTypedValue(value, descriptor.value, registry, path, resolving);
  }
  if (descriptor.kind === "nullable") {
    return value === null ? "null" : encodeTypedValue(value, descriptor.value, registry, path, resolving);
  }
  if (descriptor.kind === "primitive") return encodePrimitive(value, descriptor.name, path);
  if (descriptor.kind === "list" || descriptor.kind === "set") {
    if (!Array.isArray(value)) invalid(path, "expected an array");
    const encoded = value.map((item, index) => encodeTypedValue(item, descriptor.value, registry, §${path}[${index}]§, resolving));
    if (descriptor.kind === "set") {
      encoded.sort(compareUTF8Bytewise);
      for (let index = 1; index < encoded.length; index++) {
        if (encoded[index] === encoded[index - 1]) invalid(path, "duplicate canonical set element");
      }
    }
    return §[${encoded.join(",")}]§;
  }
  if (descriptor.kind === "map") {
    if (!isObject(value) || Array.isArray(value)) invalid(path, "expected a map");
    const members: string[] = [];
    for (const key of Object.keys(value).sort()) {
      assertSafeKey(key);
      members.push(§${JSON.stringify(key)}:${encodeTypedValue(value[key], descriptor.value, registry, §${path}.${key}§, resolving)}§);
    }
    return §{${members.join(",")}}§;
  }
  if (descriptor.kind === "tuple") {
    if (!Array.isArray(value) || value.length !== descriptor.values.length) invalid(path, "tuple length mismatch");
    return §[${descriptor.values.map((item, index) => encodeTypedValue(value[index], item, registry, §${path}[${index}]§, resolving)).join(",")}]§;
  }
  if (descriptor.kind === "enum") {
    if (typeof value !== "string" || (!descriptor.open && !descriptor.values.includes(value))) invalid(path, "invalid enum value");
    return JSON.stringify(value);
  }
  if (descriptor.kind === "union") {
    if (!isObject(value) || typeof value.kind !== "string") invalid(path, "invalid tagged union");
    const variant = descriptor.variants[value.kind];
    let encodedPayload: string;
    if (variant === undefined) {
      if (!descriptor.open || value.unknown !== true) invalid(path, "unknown closed-union variant");
      encodedPayload = encodeExactJSON(value.value as JsonValue, §${path}.value§);
    } else {
      if (value.unknown === true) invalid(path, "known union variant cannot be unknown");
      encodedPayload = encodeTypedValue(value.value, variant, registry, §${path}.value§, resolving);
    }
    if (!encodedPayload.startsWith("{") || !encodedPayload.endsWith("}")) invalid(path, "union payload must encode as a record");
    const payloadMembers = encodedPayload.slice(1, -1);
    const discriminator = §${JSON.stringify(descriptor.discriminator)}:${JSON.stringify(value.kind)}§;
    return §{${discriminator}${payloadMembers === "" ? "" : "," + payloadMembers}}§;
  }
  if (!isObject(value) || Array.isArray(value)) invalid(path, "expected a record");
  const known = new Set(descriptor.fields.map((field) => field.property));
  const members = new Map<string, string>();
  for (const field of descriptor.fields) {
    const present = Object.prototype.hasOwnProperty.call(value, field.property) && value[field.property] !== undefined;
    if (!present) {
      if (!field.optional) invalid(§${path}.${field.property}§, "required field is absent");
      continue;
    }
    validateConstraints(value[field.property], field.constraints, field.value, registry, §${path}.${field.property}§);
    members.set(field.wire, encodeTypedValue(value[field.property], field.value, registry, §${path}.${field.property}§, resolving));
  }
  for (const property of Object.keys(value)) {
    if (!known.has(property) && property !== "unknownFields") invalid(§${path}.${property}§, "unknown record property");
  }
/*__scenery_runtime_validation_start__*/
  validateRecordRules(value, descriptor, registry, path);
/*__scenery_runtime_validation_end__*/
  if (descriptor.preserveUnknown) {
    const unknown = value.unknownFields;
    if (unknown !== undefined) {
      if (!isObject(unknown) || Array.isArray(unknown)) invalid(§${path}.unknownFields§, "invalid unknown-field map");
      for (const key of Object.keys(unknown).sort()) {
        assertSafeKey(key);
        if (members.has(key)) invalid(§${path}.unknownFields.${key}§, "unknown field collides with a declared wire name");
        members.set(key, encodeExactJSON(unknown[key] as JsonValue, §${path}.unknownFields.${key}§));
      }
    }
  }
  return §{${[...members].sort(([left], [right]) => left < right ? -1 : left > right ? 1 : 0).map(([key, encoded]) => §${JSON.stringify(key)}:${encoded}§).join(",")}}§;
}

`
