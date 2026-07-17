import type {
  ContentPageSlotProps,
  RequestState,
  SplitPageSlotProps,
  TablePageCellProps,
  TablePageDateTimeRange,
  TablePageFilterProps,
} from "../../../ui/index.js";
import {
  Button,
  defineContentPageSlots,
  defineSplitPageSlots,
  defineTablePageSlots,
  queryStateProps,
  SplitPage,
  Text,
  VStack,
} from "../../../ui/index.js";
import { t } from "../../../ui/tokens.stylex.js";
import * as stylex from "@stylexjs/stylex";

interface Row {
  readonly id: string;
  readonly status: "open" | "closed";
}

const requestState: RequestState<{ readonly data: Row }> = {
  kind: "loading",
};
const requestStateView = queryStateProps(requestState, "row");
const facadeStyles = stylex.create({
  surface: {
    backgroundColor: t.surface,
    padding: t.space4,
  },
});

const blessedPrimitives = (
  <VStack xstyle={facadeStyles.surface}>
    <Text>Catalog text</Text>
    <Button label="Catalog button" />
  </VStack>
);

function StatusCell(props: TablePageCellProps<Row, Row["status"]>) {
  return <span>{props.value}</span>;
}

function StatusFilter(props: TablePageFilterProps<string>) {
  return <button onClick={() => props.onChange("open")}>{props.label}</button>;
}

function CreatedFilter(props: TablePageFilterProps<TablePageDateTimeRange>) {
  return <button onClick={() => props.onChange({})}>{props.label}</button>;
}

defineTablePageSlots<
  Row,
  "status",
  { readonly status: string; readonly created: TablePageDateTimeRange }
>()({
  cells: { status: StatusCell },
  filters: { created: CreatedFilter, status: StatusFilter },
});

defineTablePageSlots<Row, "status", { readonly status: string }>()({
  cells: {
    // @ts-expect-error unknown cell slots fail closed.
    missing: StatusCell,
  },
});

defineTablePageSlots<Row, "status", { readonly status: string }>()({
  // @ts-expect-error unknown top-level slots fail closed.
  layout: StatusFilter,
});

function SplitSlot(props: SplitPageSlotProps<Row>) {
  return (
    <button onClick={() => props.onSelectionChange(null)}>
      {props.selection ?? "Nothing selected"}
    </button>
  );
}

function ContentSlot(props: ContentPageSlotProps<Row>) {
  return props.state.kind === "result" ? props.state.data.id : null;
}

defineContentPageSlots<Row>()({
  content: ContentSlot,
  actions: ContentSlot,
});

defineSplitPageSlots<Row>()({
  sidebar: SplitSlot,
  detail: SplitSlot,
});

const splitContent = <div />;

const splitWithDefaultLabels = (
  <SplitPage
    detail={splitContent}
    sidebar={splitContent}
    sidebarTitle="Projects"
  />
);

const splitWithCustomTitle = (
  <SplitPage
    ariaLabel="Projects split page"
    detail={splitContent}
    sidebar={splitContent}
    sidebarLabel="Projects"
    sidebarTitle={<code>sidebarTitle</code>}
  />
);

const splitWithUnlabeledCustomTitle = (
  // @ts-expect-error non-string titles require explicit landmark labels.
  <SplitPage
    detail={splitContent}
    sidebar={splitContent}
    sidebarTitle={<code>sidebarTitle</code>}
  />
);

void splitWithDefaultLabels;
void splitWithCustomTitle;
void splitWithUnlabeledCustomTitle;
void requestStateView;
void blessedPrimitives;
