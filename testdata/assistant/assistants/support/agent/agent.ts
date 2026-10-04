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
      const durableResult = executed("house__process_scene_durable");
      const durableStatusDone = executed("scenery_execution_status");
      const durableCancelDone = executed("scenery_execution_cancel");
      const remoteDone = executed("docs__search");
      const canExecute = tools.some((tool) => tool.name === "connection_execute");
      // Eve exposes connection tools through one stable execution tool. The
      // chosen call ID lets the mock recognize each operation's later result.
      const execute = (tool: string, input: unknown) => ({
        toolCalls: [{ name: "connection_execute", id: tool, input: { connection: "scenery", tool, input } }],
      });
      if (prompt.includes("provider-local") && !localDone) {
        return { toolCalls: [{ name: "local", input: { value: "fixture-local" } }] };
      }
      if ((prompt.includes("local-mcp") || prompt.includes("declared-error") || prompt.includes("durable") || prompt.includes("external-mcp")) &&
          !searchDone && tools.some((tool) => tool.name === "connection_search")) {
        const query = prompt.includes("external-mcp") ? "search" : "process scene";
        return { toolCalls: [{ name: "connection_search", input: { connection: "scenery", query } }] };
      }
      if (prompt.includes("local-mcp") && !localMCPDone && canExecute) {
        return execute("house__process_scene", { scene_id: "acceptance-scene" });
      }
      if (prompt.includes("declared-error") && !localMCPDone && canExecute) {
        return execute("house__process_scene", { scene_id: "declared-error" });
      }
      if (prompt.includes("durable") && !durableResult && canExecute) {
        return execute("house__process_scene_durable", { scene_id: "durable-scene" });
      }
      // Eve represents an approval answer as a new user message. Continue a
      // durable workflow from its accumulated tool results instead of relying
      // on the original prompt still being the latest user message.
      if (durableResult) {
        const executionId = JSON.stringify(durableResult.output).match(/\"execution_id\"\s*:\s*\"([^\"]+)\"/)?.[1];
        if (executionId && !durableStatusDone && canExecute) {
          return execute("scenery_execution_status", { execution_id: executionId });
        }
        if (executionId && durableStatusDone && !durableCancelDone && canExecute) {
          return execute("scenery_execution_cancel", { execution_id: executionId });
        }
      }
      if (prompt.includes("external-mcp") && !remoteDone && canExecute) {
        return execute("docs__search", { query: "acceptance" });
      }
      return `fixture:${prompt}:${JSON.stringify(toolResults.map((result) => result.output))}`;
    },
  }),
  modelContextWindowTokens: 4096,
});
