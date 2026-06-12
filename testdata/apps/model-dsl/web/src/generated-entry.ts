import { generatedRoutes, taskListCollection, type TaskRow } from "@scenery/generated"

export function generatedRouteSummary(rows: readonly TaskRow[]) {
  const [route] = generatedRoutes
  return {
    path: route.path,
    title: route.title,
    rowCount: taskListCollection.materialize(rows).length,
    rendered: route.component({ rows }),
  }
}
