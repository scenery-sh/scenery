// The adapter supplies the same scalar and record codecs as the generated client.
export function outcomeCodec(codec, registry, variants) {
  function variant(outcome) {
    if (outcome === null || typeof outcome !== "object" ||
        (outcome.kind !== "result" && outcome.kind !== "error") ||
        typeof outcome.name !== "string") {
      throw new TypeError("invalid worker outcome");
    }
    const descriptor = variants[outcome.kind + "." + outcome.name];
    const field = outcome.kind === "error" ? "problem" : "value";
    if (!descriptor || Object.keys(outcome).length !== 3 || !Object.hasOwn(outcome, field)) {
      throw new TypeError("undeclared or malformed worker outcome");
    }
    return { descriptor, field };
  }

  return {
    decode(outcome) {
      const { descriptor, field } = variant(outcome);
      return {
        kind: outcome.kind, name: outcome.name,
        [field]: codec.decodeWorkerValue(outcome[field], descriptor, registry),
      };
    },
    encode(outcome) {
      const { descriptor, field } = variant(outcome);
      const payload = codec.encodeTypedJSON(outcome[field], descriptor, registry);
      return '{"kind":' + JSON.stringify(outcome.kind) +
        ',"name":' + JSON.stringify(outcome.name) + ',"' + field + '":' + payload + '}';
    },
  };
}
