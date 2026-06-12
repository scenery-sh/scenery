import { createGeneratedRoutes, createGeneratedRuntime, registerGeneratedRoutes, type TaskRow } from "@scenery/generated"
import type { CollectionPageRoute } from "@scenery/layout-kit"

export function generatedRouteSummary(rows: readonly TaskRow[]) {
  const runtime = createGeneratedRuntime({
    electric: { baseURL: "https://electric.local" },
    rows: { taskList: rows },
  })
  const [route] = createGeneratedRoutes(runtime)
  const registered: CollectionPageRoute[] = []
  registerGeneratedRoutes((item) => registered.push(item), runtime)
  return {
    id: route.id,
    kind: route.kind,
    path: route.path,
    title: route.title,
    entity: route.entity,
    collection: route.collection,
    shapeURL: runtime.collections.taskList.shapeURL,
    rowCount: runtime.collections.taskList.materialize().length,
    registeredCount: registered.length,
    rendered: route.component(),
  }
}
