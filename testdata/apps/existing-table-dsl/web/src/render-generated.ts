import { generatedRouteSummary } from "./generated-entry"

const rendered = generatedRouteSummary([
  {
    id: "customer-basic",
    email: "basic@example.test",
    full_name: "Basic Customer",
    plan_status: "active",
    created_at: "2026-06-12T12:00:00Z",
  },
  {
    id: "customer-cancelled",
    email: "cancelled@example.test",
    full_name: "Cancelled Customer",
    plan_status: "cancelled",
    created_at: "2026-06-13T12:00:00Z",
  },
  {
    id: "customer-pro",
    email: "pro@example.test",
    full_name: "Pro Customer",
    plan_status: "active",
    created_at: "2026-06-14T12:00:00Z",
  },
])

if (
  rendered.id !== "CustomerList" ||
  rendered.kind !== "collection" ||
  rendered.path !== "/customers" ||
  rendered.title !== "Customers" ||
  rendered.entity !== "Customer" ||
  rendered.collection !== "CustomerList" ||
  rendered.rowCount !== 2 ||
  rendered.pageRowCount !== 2 ||
  rendered.registeredCount !== 1 ||
  rendered.materialized.map((row) => row.id).join(",") !== "customer-pro,customer-basic" ||
  rendered.filters[0]?.field !== "plan_status" ||
  rendered.filters[0]?.op !== "neq" ||
  rendered.filters[0]?.value !== "cancelled" ||
  rendered.sorts.map((sort) => `${sort.field}:${sort.direction}`).join(",") !== "created_at:desc" ||
  rendered.columns.find((column) => column.field === "plan_status")?.display !== "badge" ||
  rendered.columns.find((column) => column.field === "created_at")?.display !== "datetime"
) {
  throw new Error(`unexpected generated route render: ${JSON.stringify(rendered)}`)
}

console.log(JSON.stringify({ ok: true, path: rendered.path, title: rendered.title, row_count: rendered.rowCount }))
