import {
  createGeneratedRoutes,
  createGeneratedRuntime,
  registerGeneratedRoutes,
  TaskListPage,
  taskListCollection,
  type TaskListRecord,
  type TaskRow,
} from "@scenery/generated"
import type { CollectionPageRoute } from "@scenery/layout-kit"

export function generatedRouteSummary(rows: readonly TaskRow[]) {
  const runtime = createGeneratedRuntime({
    rows: { taskList: rows },
  })
  const records = runtime.collections.taskList.materialize()
  const [route] = createGeneratedRoutes(runtime)
  const registered: CollectionPageRoute[] = []
  registerGeneratedRoutes((item) => registered.push(item), runtime)
  const rendered = route.component() as { rowCount?: number }
  const page = TaskListPage({ rows: records }) as { rowCount?: number }
  return {
    id: route.id,
    kind: route.kind,
    path: route.path,
    title: route.title,
    entity: route.entity,
    collection: route.collection,
    rowCount: rendered.rowCount,
    pageRowCount: page.rowCount,
    registeredCount: registered.length,
    materialized: records satisfies TaskListRecord[],
    filters: taskListCollection.filters,
    sorts: taskListCollection.sorts,
    columns: taskListCollection.columns,
    rendered,
  }
}
