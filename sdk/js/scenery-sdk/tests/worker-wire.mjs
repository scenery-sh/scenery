import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { outcomeCodec } from "../wire.mjs";

const codec = await import(pathToFileURL(resolve(process.argv[2])).href);
const corpus = new URL("../../../../internal/contract/testdata/worker-wire-v1.json", import.meta.url);
const examples = JSON.parse(await readFile(corpus, "utf8"));
const patch = { kind: "named", name: "probe/record/patch" };
const registry = {
  [patch.name]: {
    kind: "record", preserveUnknown: false,
    fields: [
      { property: "note", wire: "note", optional: true, value: {
        kind: "nullable", value: { kind: "primitive", name: "string" },
      } },
    ],
  },
  "probe/enum/status": { kind: "enum", values: ["in_review"], open: false },
  "probe/record/note": {
    kind: "record", preserveUnknown: true,
    fields: [
      { property: "note", wire: "note", optional: true, value: {
        kind: "nullable", value: { kind: "primitive", name: "string" },
      } },
      { property: "ratio", wire: "ratio", optional: true, value: { kind: "primitive", name: "float64" } },
      { property: "text", wire: "text", optional: false, value: { kind: "primitive", name: "string" } },
    ],
  },
};
const outcome = outcomeCodec(codec, registry, {
  "result.ok": patch, "error.negative_amount": { kind: "primitive", name: "problem" },
});

for (const { name, type, wire, reject } of examples) {
  const roundtrip = () => {
    const incoming = codec.parseExactJSON(wire);
    if (type === "probe/outcome/quote") return outcome.encode(outcome.decode(incoming));
    const descriptor = registry[type] ? { kind: "named", name: type } : { kind: "primitive", name: type };
    const value = codec.decodeWorkerValue(incoming, descriptor, registry);
    return codec.encodeTypedJSON(value, descriptor, registry);
  };
  if (reject) assert.throws(roundtrip, undefined, name);
  else assert.equal(roundtrip(), wire, name);
}
assert.equal(examples.length, 33);
console.log(`${examples.length}/33 Worker ABI JavaScript wire cases passed`);
