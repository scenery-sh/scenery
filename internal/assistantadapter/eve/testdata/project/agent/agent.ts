import { defineAgent } from "eve";
import { mockModel } from "eve/evals";

// eve exposes the deterministic mock model in-process. The model context
// override avoids any AI Gateway metadata lookup during the fixture build
// while keeping the real Eve runtime and event stream active.
export default defineAgent({
  model: mockModel({
    modelId: "scenery-fixture-model",
    provider: "scenery-fixture",
    respond: ({ lastUserMessage, toolResults, tools }) => {
      const prompt = lastUserMessage ?? "";
      const localDone = toolResults.some((result) => result.name === "local");
      const searchDone = toolResults.some((result) => result.name === "connection_search");
      const mcpDone = toolResults.some((result) => result.name === "scenery__echo");
      if (prompt.includes("local") && !localDone) {
        return { toolCalls: [{ name: "local", input: { value: "fixture-local" } }] };
      }
      if (prompt.includes("mcp") && !mcpDone) {
        if (!searchDone && tools.some((tool) => tool.name === "connection_search")) {
          return { toolCalls: [{ name: "connection_search", input: { connection: "scenery", keywords: "echo" } }] };
        }
        if (tools.some((tool) => tool.name === "scenery__echo")) {
          return { toolCalls: [{ name: "scenery__echo", input: { value: "fixture-mcp" } }] };
        }
      }
      return `fixture:${prompt}:${JSON.stringify(toolResults.map((result) => result.output))}`;
    },
  }),
  modelContextWindowTokens: 4096,
});
