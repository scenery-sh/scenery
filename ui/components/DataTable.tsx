import { Badge } from "@astryxdesign/core/Badge";
import { EmptyState } from "@astryxdesign/core/EmptyState";
import {
  type BodyCellRenderProps,
  type BodyRowRenderProps,
  type HeaderCellRenderProps,
  type HeaderRowRenderProps,
  pixel,
  proportional,
  type ScrollWrapperRenderProps,
  Table,
  TableCell,
  type TableColumn,
  type TablePlugin,
  type TableRenderProps,
  useTableGroupedRows,
  useTableRowIndex,
  useTableSortable,
} from "@astryxdesign/core/Table";
import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  useCallback,
  useMemo,
  useState,
} from "react";

export type Align = "left" | "right" | "center";
export type SortDirection = "asc" | "desc";
export type SortState = { key: string; direction: SortDirection };

export type DataTableSection<T> = {
  key: string;
  label: ReactNode;
  rows: readonly T[];
};

export type Column<T> = {
  key: string;
  header: ReactNode;
  render?: (row: T, index: number) => ReactNode;
  align?: Align;
  mono?: boolean;
  nowrap?: boolean;
  width?: string;
  sortKey?: string;
};

type TableRowRecord<T> = Record<string, unknown> & {
  kind: "row";
  row: T;
  rowIndex: number;
  rowKey: string;
  groupKey: string;
};

type ExpandedRowRecord<T> = Record<string, unknown> & {
  kind: "expanded";
  row: T;
  rowIndex: number;
  rowKey: string;
  groupKey: string;
};

type InternalRow<T> = TableRowRecord<T> | ExpandedRowRecord<T>;

export function DataTable<T>({
  columns,
  rows,
  sections,
  getRowKey,
  minWidth,
  sticky,
  framed,
  fill,
  hideHeader,
  layout = "auto",
  onRowClick,
  rowLabel,
  sort,
  onSort,
  expandedKey,
  renderExpanded,
  selectedKey,
  numbered,
  empty = "No results",
}: {
  columns: readonly Column<T>[];
  rows: readonly T[];
  sections?: readonly DataTableSection<T>[];
  getRowKey: (row: T, index: number) => string;
  minWidth?: number;
  sticky?: boolean;
  framed?: boolean;
  fill?: boolean;
  hideHeader?: boolean;
  layout?: "auto" | "fixed";
  onRowClick?: (row: T, index: number) => void;
  rowLabel?: (row: T) => string;
  sort?: SortState;
  onSort?: (sortKey: string) => void;
  expandedKey?: string | null;
  renderExpanded?: (row: T) => ReactNode;
  selectedKey?: string | null;
  numbered?: boolean;
  empty?: string;
}) {
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(
    () => new Set(),
  );
  const sourceRows = useMemo<InternalRow<T>[]>(() => {
    if (!sections) {
      return rows.map((row, rowIndex) => ({
        kind: "row",
        row,
        rowIndex,
        rowKey: getRowKey(row, rowIndex),
        groupKey: "",
      }));
    }
    let rowIndex = 0;
    return sections.flatMap((section) =>
      section.rows.map((row) => {
        const record: InternalRow<T> = {
          kind: "row",
          row,
          rowIndex,
          rowKey: getRowKey(row, rowIndex),
          groupKey: section.key,
        };
        rowIndex++;
        return record;
      }),
    );
  }, [getRowKey, rows, sections]);
  const sectionLabels = useMemo(
    () => new Map(sections?.map((section) => [section.key, section.label])),
    [sections],
  );
  const internalRowKey = useCallback((item: InternalRow<T>) => item.rowKey, []);
  const grouped = useTableGroupedRows<InternalRow<T>>({
    data: sourceRows,
    groupBy: (item: InternalRow<T>) => item.groupKey,
    collapsedGroups,
    onToggleGroup: (groupKey: string) =>
      setCollapsedGroups((current) => {
        const next = new Set(current);
        if (next.has(groupKey)) next.delete(groupKey);
        else next.add(groupKey);
        return next;
      }),
    renderGroupHeader: (groupKey: string, count: number) => (
      <span {...stylex.props(styles.groupLabel)}>
        <span>{sectionLabels.get(groupKey) ?? groupKey}</span>
        <Badge label={count} variant="neutral" />
      </span>
    ),
    getRowKey: internalRowKey,
    groupOrder: sections?.map((section) => section.key),
  });
  const groupedRows: InternalRow<T>[] = sections
    ? (grouped.data as InternalRow<T>[])
    : sourceRows;
  const tableRows = useMemo<InternalRow<T>[]>(() => {
    if (!renderExpanded || expandedKey == null) return groupedRows;
    return groupedRows.flatMap((item: InternalRow<T>) => {
      if (item.kind !== "row" || item.rowKey !== expandedKey) return [item];
      return [
        item,
        {
          kind: "expanded",
          row: item.row,
          rowIndex: item.rowIndex,
          rowKey: `__expanded_${item.rowKey}`,
          groupKey: item.groupKey,
        },
      ];
    });
  }, [expandedKey, groupedRows, renderExpanded]);
  const tableColumns = useMemo<TableColumn<InternalRow<T>>[]>(
    () =>
      columns.map((column) => ({
        key: column.key,
        header: column.header,
        align: tableAlign(column.align),
        width: tableWidth(column.width, minWidth, columns.length),
        sortable:
          column.sortKey && onSort ? { sortKey: column.sortKey } : undefined,
        renderCell: (item: InternalRow<T>) =>
          item.kind === "row"
            ? column.render
              ? column.render(item.row, item.rowIndex)
              : cellText(item.row, column.key)
            : null,
      })),
    [columns, minWidth, onSort],
  );
  const rowIndexPlugin = useTableRowIndex<InternalRow<T>>({
    data: sourceRows,
    getRowKey: internalRowKey,
  });
  const sortPlugin = useTableSortable<InternalRow<T>>({
    sort: sort
      ? [
          {
            sortKey: sort.key,
            direction: sort.direction === "asc" ? "ascending" : "descending",
          },
        ]
      : [],
    onSortChange: (next: readonly { sortKey: string }[]) => {
      if (next[0]) onSort?.(next[0].sortKey);
    },
    allowUnsortedState: false,
  });
  const behaviorPlugin = useMemo<TablePlugin<InternalRow<T>>>(() => {
    const columnsByKey = new Map(columns.map((column) => [column.key, column]));
    return {
      transformHeaderRow: (props: HeaderRowRenderProps) =>
        hideHeader
          ? {
              ...props,
              htmlProps: { ...props.htmlProps, "aria-hidden": true },
              xstyle: [...props.xstyle, styles.hiddenHeader],
            }
          : props,
      transformHeaderCell: (props: HeaderCellRenderProps) => ({
        ...props,
        xstyle: [...props.xstyle, sticky && styles.stickyHeader].filter(
          Boolean,
        ),
      }),
      transformBodyCell: (
        props: BodyCellRenderProps,
        column: TableColumn<InternalRow<T>>,
        item: InternalRow<T>,
        columnIndex: number,
      ) => {
        if (item.kind !== "row") return props;
        const source = columnsByKey.get(column.key);
        const selected = selectedKey != null && selectedKey === item.rowKey;
        return {
          ...props,
          xstyle: [
            ...props.xstyle,
            source?.mono && styles.mono,
            source?.nowrap && styles.nowrap,
            selected && styles.selectedCell,
            selected && columnIndex === 0 && styles.selectedFirstCell,
          ].filter(Boolean),
        };
      },
      transformBodyRow: (props: BodyRowRenderProps, item: InternalRow<T>) => {
        if (item.kind === "expanded") {
          return {
            htmlProps: {},
            xstyle: [],
            children: (
              <TableCell colSpan={999} xstyle={styles.expandedCell}>
                {renderExpanded?.(item.row)}
              </TableCell>
            ),
          };
        }
        if (item.kind !== "row" || !onRowClick) return props;
        return {
          ...props,
          htmlProps: {
            ...props.htmlProps,
            "aria-label": rowLabel?.(item.row),
            onClick: (event: ReactMouseEvent<HTMLTableRowElement>) => {
              if (interactiveTarget(event.target)) return;
              onRowClick(item.row, item.rowIndex);
            },
            onKeyDown: (event: ReactKeyboardEvent<HTMLTableRowElement>) => {
              if (event.target !== event.currentTarget) return;
              if (event.key !== "Enter" && event.key !== " ") return;
              event.preventDefault();
              onRowClick(item.row, item.rowIndex);
            },
            tabIndex: 0,
          },
          xstyle: [...props.xstyle, styles.clickableRow],
        };
      },
      transformScrollWrapper: (props: ScrollWrapperRenderProps) => ({
        ...props,
        xstyle: [
          ...props.xstyle,
          styles.tableScroller,
          fill && styles.tableScrollerFill,
        ].filter(Boolean),
      }),
      transformTable: (props: TableRenderProps) => ({
        ...props,
        xstyle: [
          ...props.xstyle,
          styles.table,
          layout === "auto" && styles.autoLayout,
        ].filter(Boolean),
      }),
    };
  }, [
    columns,
    fill,
    hideHeader,
    layout,
    onRowClick,
    renderExpanded,
    rowLabel,
    selectedKey,
    sticky,
  ]);
  const plugins = useMemo(
    () => ({
      ...(sections ? { grouped: grouped.plugin } : {}),
      ...(numbered ? { rowIndex: rowIndexPlugin } : {}),
      ...(onSort ? { sort: sortPlugin } : {}),
      behavior: behaviorPlugin,
    }),
    [
      behaviorPlugin,
      grouped.plugin,
      numbered,
      onSort,
      rowIndexPlugin,
      sections,
      sortPlugin,
    ],
  );
  const rootStyle = minWidth
    ? ({ "--table-min-width": `${minWidth}px` } as CSSProperties)
    : undefined;

  return (
    <div
      {...stylex.props(
        styles.root,
        fill && styles.rootFill,
        framed && styles.framed,
      )}
      style={rootStyle}
    >
      <Table
        columns={tableColumns}
        data={tableRows}
        density="balanced"
        dividers="rows"
        emptyState={<EmptyState isCompact title={empty} />}
        hasHover={Boolean(onRowClick)}
        idKey={sections ? grouped.idKey : internalRowKey}
        plugins={plugins}
        textOverflow="wrap"
      />
    </div>
  );
}

function interactiveTarget(target: EventTarget | null) {
  return (
    target instanceof Element &&
    target.closest("a, button, input, select, textarea, [role='button']") !=
      null
  );
}

function cellText(row: unknown, key: string) {
  const value = (row as Record<string, unknown>)[key];
  return value == null || value === "" ? "—" : String(value);
}

function tableAlign(align?: Align) {
  return align === "right" ? "end" : align === "center" ? "center" : "start";
}

function tableWidth(
  width: string | undefined,
  minWidth: number | undefined,
  columnCount: number,
) {
  const minimumShare = minWidth
    ? Math.max(120, Math.ceil(minWidth / Math.max(columnCount, 1)))
    : 120;
  if (!width) return proportional(1, { minWidth: minimumShare });
  const pixels = /^([0-9]+(?:\.[0-9]+)?)px$/.exec(width);
  if (pixels) return pixel(Number(pixels[1]));
  const percent = /^([0-9]+(?:\.[0-9]+)?)%$/.exec(width);
  if (percent)
    return proportional(Number(percent[1]), { minWidth: minimumShare });
  return proportional(1, { minWidth: minimumShare });
}

const styles = stylex.create({
  root: { minWidth: 0 },
  rootFill: { flex: 1, minHeight: 0, display: "flex" },
  framed: {
    borderColor: colorVars["--color-border"],
    borderStyle: "solid",
    borderWidth: borderVars["--border-width"],
    borderRadius: radiusVars["--radius-container"],
    overflow: "hidden",
  },
  tableScroller: {
    minWidth: 0,
    width: "100%",
    scrollbarColor: `${colorVars["--color-text-secondary"]} transparent`,
    scrollbarWidth: "thin",
  },
  tableScrollerFill: { flex: 1, minHeight: 0, overflow: "auto" },
  table: {
    width: "100%",
    minWidth: "var(--table-min-width, 720px)",
  },
  autoLayout: { tableLayout: "auto" },
  hiddenHeader: {
    border: 0,
    clip: "rect(0 0 0 0)",
    clipPath: "inset(50%)",
    height: 1,
    overflow: "hidden",
    position: "absolute",
    whiteSpace: "nowrap",
    width: 1,
  },
  stickyHeader: {
    position: "sticky",
    top: 0,
    zIndex: 1,
    backgroundColor: colorVars["--color-background-body"],
  },
  mono: { fontVariantNumeric: "tabular-nums" },
  nowrap: { whiteSpace: "nowrap" },
  clickableRow: { cursor: "pointer" },
  selectedCell: { backgroundColor: colorVars["--color-accent-muted"] },
  selectedFirstCell: {
    boxShadow: `inset 2px 0 0 ${colorVars["--color-accent"]}`,
  },
  groupLabel: {
    display: "inline-flex",
    alignItems: "center",
    gap: spacingVars["--spacing-2"],
    fontWeight: 600,
  },
  expandedCell: {
    padding: spacingVars["--spacing-4"],
    backgroundColor: colorVars["--color-background-muted"],
  },
});
