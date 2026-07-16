import type { TablePageCellProps, TablePageFilterProps } from "../catalog/index.js";
import { defineTablePageSlots } from "../catalog/index.js";

interface Row {
  readonly id: string;
  readonly status: "open" | "closed";
}

function StatusCell(props: TablePageCellProps<Row, Row["status"]>) {
  return <span>{props.value}</span>;
}

function StatusFilter(props: TablePageFilterProps<string>) {
  return <button onClick={() => props.onChange("open")}>{props.label}</button>;
}

defineTablePageSlots<Row, "status", "status">()({
  cells: { status: StatusCell },
  filters: { status: StatusFilter },
});

defineTablePageSlots<Row, "status", "status">()({
  cells: {
    // @ts-expect-error unknown cell slots fail closed.
    missing: StatusCell,
  },
});

defineTablePageSlots<Row, "status", "status">()({
  // @ts-expect-error unknown top-level slots fail closed.
  layout: StatusFilter,
});
