import { defineTool } from "eve/tools";

export default defineTool({
  description: "Return a deterministic local fixture value.",
  inputSchema: {
    type: "object",
    properties: { value: { type: "string" } },
    additionalProperties: false,
  },
  async execute(input) {
    return { value: typeof input.value === "string" ? input.value : "fixture" };
  },
});
