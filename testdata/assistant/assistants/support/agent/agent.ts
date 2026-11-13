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
      const executed = (tool: string) => toolResults.find((result) => result.name === "connection_execute" && result.id === tool);
      const localMCPDone = executed("house__process_scene");
      const canExecute = tools.some((tool) => tool.name === "connection_execute");
      // Eve exposes connection tools through one stable execution tool. The
      // chosen call ID lets the mock recognize each operation's later result.
      const execute = (tool: string, input: unknown) => ({
        toolCalls: [{ name: "connection_execute", id: tool, input: { connection: "scenery", tool, input } }],
      });
      if (prompt.includes("provider-local") && !localDone) {
        return { toolCalls: [{ name: "local", input: { value: "fixture-local" } }] };
      }
      if ((prompt.includes("local-mcp") || prompt.includes("declared-error")) &&
          !searchDone && tools.some((tool) => tool.name === "connection_search")) {
        return { toolCalls: [{ name: "connection_search", input: { connection: "scenery", query: "process scene" } }] };
      }
      if (prompt.includes("local-mcp") && !localMCPDone && canExecute) {
        return execute("house__process_scene", { scene_id: "acceptance-scene" });
      }
      if (prompt.includes("declared-error") && !localMCPDone && canExecute) {
        return execute("house__process_scene", { scene_id: "declared-error" });
      }
      return `fixture:${prompt}:${JSON.stringify(toolResults.map((result) => result.output))}`;
    },
  }),
  modelContextWindowTokens: 4096,
});
