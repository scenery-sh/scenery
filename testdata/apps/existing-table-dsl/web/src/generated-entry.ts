import {
  createGeneratedRoutes,
  createGeneratedRuntime,
  customerListCollection,
  CustomerListPage,
  registerGeneratedRoutes,
  type CustomerListRecord,
  type CustomerRow,
} from "@scenery/generated"
import type { CollectionPageRoute } from "@scenery/layout-kit"

export function generatedRouteSummary(rows: readonly CustomerRow[]) {
  const runtime = createGeneratedRuntime({
    rows: { customerList: rows },
  })
  const records = runtime.collections.customerList.materialize()
  const [route] = createGeneratedRoutes(runtime)
  const registered: CollectionPageRoute[] = []
  registerGeneratedRoutes((item) => registered.push(item), runtime)
  const rendered = route.component() as { rowCount?: number }
  const page = CustomerListPage({ rows: records }) as { rowCount?: number }
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
    materialized: records satisfies CustomerListRecord[],
    filters: customerListCollection.filters,
    sorts: customerListCollection.sorts,
    columns: customerListCollection.columns,
    rendered,
  }
}
