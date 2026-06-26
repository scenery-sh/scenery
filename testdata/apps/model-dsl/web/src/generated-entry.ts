import { createGeneratedRoutes, registerGeneratedRoutes, TaskListPage, type TaskListRecord } from "@scenery/generated"
import type { CollectionPageRoute } from "@scenery/layout-kit"

export function generatedRouteSummary(records: readonly TaskListRecord[]) {
  const [route] = createGeneratedRoutes()
  const registered: CollectionPageRoute[] = []
  registerGeneratedRoutes((item) => registered.push(item))
  const rendered = route.component({ rows: records }) as { rowCount?: number }
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
    rendered,
  }
}
