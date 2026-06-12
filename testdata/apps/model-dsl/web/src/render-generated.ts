import { generatedRouteSummary } from "./generated-entry"

const rendered = generatedRouteSummary([
  {
    id: "render-task-1",
    tenant_id: "00000000-0000-0000-0000-000000000001",
    title: "Render generated page",
    status: "todo",
    project_id: "render-project",
    created_at: "2026-06-12T12:00:00Z",
  },
])

if (
  rendered.id !== "TaskList" ||
  rendered.kind !== "collection" ||
  rendered.path !== "/tasks" ||
  rendered.title !== "Tasks" ||
  rendered.entity !== "Task" ||
  rendered.collection !== "TaskList" ||
  rendered.shapeURL !== "https://electric.local/v1/shape?table=tasks" ||
  rendered.rowCount !== 1 ||
  rendered.registeredCount !== 1
) {
  throw new Error(`unexpected generated route render: ${JSON.stringify(rendered)}`)
}

console.log(JSON.stringify({ ok: true, path: rendered.path, title: rendered.title, row_count: rendered.rowCount, shape_url: rendered.shapeURL }))
