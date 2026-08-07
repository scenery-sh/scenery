import { defineAgent } from "eve";
import { mockModel } from "eve/evals";

export default defineAgent({
  model: mockModel({
    modelId: "scenery-fixture-model",
    provider: "scenery-fixture",
    respond: ({ lastUserMessage, toolResults, tools }) => {
      const prompt = lastUserMessage ?? "";
      const localDone = toolResults.some((result) => result.name === "local");
      const searchDone = toolResults.some((result) => result.name === "connection_search");
      const localMCPDone = toolResults.some((result) => result.name === "scenery__house__process_scene");
      const durableResult = toolResults.find((result) => result.name === "scenery__house__process_scene_durable");
      const durableStatusDone = toolResults.some((result) => result.name === "scenery__scenery_execution_status");
      const durableCancelDone = toolResults.some((result) => result.name === "scenery__scenery_execution_cancel");
      const remoteDone = toolResults.some((result) => result.name === "scenery__docs__search");
      if (prompt.includes("provider-local") && !localDone) {
        return { toolCalls: [{ name: "local", input: { value: "fixture-local" } }] };
      }
      if ((prompt.includes("local-mcp") || prompt.includes("declared-error") || prompt.includes("durable") || prompt.includes("external-mcp")) &&
          !searchDone && tools.some((tool) => tool.name === "connection_search")) {
        const keywords = prompt.includes("external-mcp") ? "search" : "process scene";
        return { toolCalls: [{ name: "connection_search", input: { connection: "scenery", keywords } }] };
      }
      if (prompt.includes("local-mcp") && !localMCPDone && tools.some((tool) => tool.name === "scenery__house__process_scene")) {
        return { toolCalls: [{ name: "scenery__house__process_scene", input: { scene_id: "acceptance-scene" } }] };
      }
      if (prompt.includes("declared-error") && !localMCPDone && tools.some((tool) => tool.name === "scenery__house__process_scene")) {
        return { toolCalls: [{ name: "scenery__house__process_scene", input: { scene_id: "declared-error" } }] };
      }
      if (prompt.includes("durable") && !durableResult && tools.some((tool) => tool.name === "scenery__house__process_scene_durable")) {
        return { toolCalls: [{ name: "scenery__house__process_scene_durable", input: { scene_id: "durable-scene" } }] };
      }
      // Eve represents an approval answer as a new user message. Continue a
      // durable workflow from its accumulated tool results instead of relying
      // on the original prompt still being the latest user message.
      if (durableResult) {
        const executionId = JSON.stringify(durableResult.output).match(/\"execution_id\"\s*:\s*\"([^\"]+)\"/)?.[1];
        if (executionId && !durableStatusDone && tools.some((tool) => tool.name === "scenery__scenery_execution_status")) {
          return { toolCalls: [{ name: "scenery__scenery_execution_status", input: { execution_id: executionId } }] };
        }
        if (executionId && durableStatusDone && !durableCancelDone && tools.some((tool) => tool.name === "scenery__scenery_execution_cancel")) {
          return { toolCalls: [{ name: "scenery__scenery_execution_cancel", input: { execution_id: executionId } }] };
        }
      }
      if (prompt.includes("external-mcp") && !remoteDone && tools.some((tool) => tool.name === "scenery__docs__search")) {
        return { toolCalls: [{ name: "scenery__docs__search", input: { query: "acceptance" } }] };
      }
      return `fixture:${prompt}:${JSON.stringify(toolResults.map((result) => result.output))}`;
    },
  }),
  modelContextWindowTokens: 4096,
});
