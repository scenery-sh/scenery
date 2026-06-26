import { generatedRouteSummary } from "./generated-entry"

const rendered = generatedRouteSummary([
  {
    id: "render-task-1",
    tenant_id: "tenant-1",
    title: "Render generated page",
    status: "todo",
    priority: "normal",
    assignee_name: "Dev User",
    due_at: "2026-06-15T09:00:00Z",
    project_id: "project-1",
    created_at: "2026-06-12T12:00:00Z",
    updated_at: "2026-06-13T12:00:00Z",
  },
  {
    id: "closed-task",
    tenant_id: "tenant-1",
    title: "Closed task",
    status: "done",
    priority: "low",
    assignee_name: "Dev User",
    due_at: "2026-06-14T09:00:00Z",
    project_id: "project-1",
    created_at: "2026-06-11T12:00:00Z",
    updated_at: "2026-06-13T12:00:00Z",
  },
  {
    id: "urgent-task",
    tenant_id: "tenant-1",
    title: "Urgent task",
    status: "doing",
    priority: "high",
    assignee_name: "Dev User",
    due_at: "2026-06-14T09:00:00Z",
    project_id: "project-1",
    created_at: "2026-06-13T12:00:00Z",
    updated_at: "2026-06-13T12:30:00Z",
  },
])

if (
  rendered.id !== "TaskList" ||
  rendered.kind !== "collection" ||
  rendered.path !== "/tasks" ||
  rendered.title !== "Tasks" ||
  rendered.entity !== "Task" ||
  rendered.collection !== "TaskList" ||
  rendered.rowCount !== 2 ||
  rendered.pageRowCount !== 2 ||
  rendered.registeredCount !== 1 ||
  rendered.materialized.map((row) => row.id).join(",") !== "urgent-task,render-task-1" ||
  rendered.filters[0]?.field !== "status" ||
  rendered.filters[0]?.op !== "neq" ||
  rendered.filters[0]?.value !== "done" ||
  rendered.sorts.map((sort) => `${sort.field}:${sort.direction}`).join(",") !== "due_at:asc,created_at:desc" ||
  rendered.columns.find((column) => column.field === "status")?.display !== "badge" ||
  rendered.columns.find((column) => column.field === "due_at")?.display !== "datetime"
) {
  throw new Error(`unexpected generated route render: ${JSON.stringify(rendered)}`)
}

console.log(JSON.stringify({ ok: true, path: rendered.path, title: rendered.title, row_count: rendered.rowCount }))
