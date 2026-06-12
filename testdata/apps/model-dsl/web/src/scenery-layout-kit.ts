export interface CollectionPageRouteProps<Row = unknown> {
  rows?: readonly Row[]
}

export interface CollectionPageRoute<Row = unknown> {
  id: string
  kind: "collection"
  path: string
  title: string
  entity: string
  collection: string
  component: (props?: CollectionPageRouteProps<Row>) => unknown
  generated: true
}

export type ComponentSlot<Row> = (props: { row: Row }) => unknown

export interface CollectionPageInput<Row, Slots> {
  collection: {
    id: string
    route: string
    title: string
    materialize: (rows: Iterable<Row>) => Row[]
  }
  rows: readonly Row[]
  slots: Slots
}

export function createCollectionPage<Row, Slots>(input: CollectionPageInput<Row, Slots>) {
  const rows = input.collection.materialize(input.rows)
  return {
    kind: "scenery.collection",
    route: input.collection.route,
    title: input.collection.title,
    rowCount: rows.length,
    slots: input.slots,
  }
}
