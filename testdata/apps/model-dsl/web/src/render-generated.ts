import { generatedRouteSummary } from "./generated-entry"

const rendered = generatedRouteSummary([
  {
    id: "render-task-1",
    title: "Render generated page",
    status: "todo",
    project_id: "render-project",
    created_at: "2026-06-12T12:00:00Z",
  },
])

if (rendered.path !== "/tasks" || rendered.title !== "Tasks" || rendered.rowCount !== 1) {
  throw new Error(`unexpected generated route render: ${JSON.stringify(rendered)}`)
}

console.log(JSON.stringify({ ok: true, path: rendered.path, title: rendered.title, row_count: rendered.rowCount }))
