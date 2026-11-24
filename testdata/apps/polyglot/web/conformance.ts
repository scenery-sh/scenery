import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { decodeResponseBody, encodeTypedJSON } from "./generated/runtime";
import type { TypeDescriptor } from "./generated/runtime";

interface Example {
  name: string;
  type: string;
  wire: string;
  reject?: boolean;
}

const corpus = new URL("../../../../internal/contract/testdata/worker-wire-v1.json", import.meta.url);
const examples = JSON.parse(await readFile(corpus, "utf8")) as Example[];
const types: Record<string, TypeDescriptor> = {
  "probe/record/patch": {
    kind: "record",
    preserveUnknown: false,
    fields: [{
      property: "note",
      wire: "note",
      optional: true,
      value: { kind: "nullable", value: { kind: "primitive", name: "string" } },
    }],
  },
  "probe/enum/status": { kind: "enum", values: ["in_review"], open: false },
};

for (const example of examples) {
  const descriptor = types[example.type] ?? { kind: "primitive", name: example.type };
  const response = new Response(example.wire, { headers: { "Content-Type": "application/json" } });
  const decode = decodeResponseBody(response, "json", ["application/json"], descriptor, types, example.name, 1024);
  if (example.reject) {
    await assert.rejects(decode, undefined, example.name);
    continue;
  }
  const value = await decode;
  assert.equal(encodeTypedJSON(value, descriptor, types), example.wire, example.name);
}
console.log(`${examples.length} Worker ABI wire cases passed`);
