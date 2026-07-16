import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import { type CSSProperties, Fragment, type ReactNode } from "react";
import { TableEmptyRow } from "./QueryState.js";

export type Align = "left" | "right" | "center";
export type SortDirection = "asc" | "desc";
export type SortState = { key: string; direction: SortDirection };

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

export function DataTable<T>({
  columns,
  rows,
  getRowKey,
  minWidth,
  sticky,
  framed,
  fill,
  layout = "auto",
  onRowClick,
  rowLabel,
  sort,
  onSort,
  expandedKey,
  renderExpanded,
  empty = "No results",
}: {
  columns: readonly Column<T>[];
  rows: readonly T[];
  getRowKey: (row: T, index: number) => string;
  minWidth?: number;
  sticky?: boolean;
  framed?: boolean;
  fill?: boolean;
  layout?: "auto" | "fixed";
  onRowClick?: (row: T, index: number) => void;
  rowLabel?: (row: T) => string;
  sort?: SortState;
  onSort?: (sortKey: string) => void;
  expandedKey?: string | null;
  renderExpanded?: (row: T) => ReactNode;
  empty?: ReactNode;
}) {
  const style = minWidth
    ? ({ "--table-min-width": `${minWidth}px` } as CSSProperties)
    : undefined;
  return (
    <div
      {...stylex.props(
        styles.scroller,
        fill && styles.scrollerFill,
        framed && styles.framed,
      )}
    >
      <table
        {...stylex.props(styles.table, layout === "fixed" && styles.fixed)}
        style={style}
      >
        <thead>
          <tr>
            {columns.map((column) => {
              const sortable = column.sortKey && onSort;
              const active = sort?.key === column.sortKey;
              return (
                <th
                  key={column.key}
                  aria-sort={
                    active
                      ? sort?.direction === "asc"
                        ? "ascending"
                        : "descending"
                      : undefined
                  }
                  {...stylex.props(
                    styles.headCell,
                    sticky && styles.sticky,
                    alignStyle(column.align),
                  )}
                  style={column.width ? { width: column.width } : undefined}
                >
                  {sortable ? (
                    <button
                      onClick={() => onSort(column.sortKey as string)}
                      type="button"
                      {...stylex.props(styles.sortButton, alignStyle(column.align))}
                    >
                      {column.header}
                      <SortIndicator
                        active={Boolean(active)}
                        direction={sort?.direction}
                      />
                    </button>
                  ) : (
                    column.header
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? (
            <TableEmptyRow columns={columns.length}>{empty}</TableEmptyRow>
          ) : (
            rows.map((row, index) => {
              const key = getRowKey(row, index);
              const clickable = Boolean(onRowClick);
              const expanded =
                renderExpanded && expandedKey != null && expandedKey === key;
              return (
                <Fragment key={key}>
                  <tr
                    aria-label={clickable ? rowLabel?.(row) : undefined}
                    onClick={onRowClick ? () => onRowClick(row, index) : undefined}
                    onKeyDown={
                      onRowClick
                        ? (event) => {
                            if (event.target !== event.currentTarget) return;
                            if (event.key === "Enter" || event.key === " ") {
                              event.preventDefault();
                              onRowClick(row, index);
                            }
                          }
                        : undefined
                    }
                    tabIndex={clickable ? 0 : undefined}
                    {...stylex.props(clickable && styles.clickableRow)}
                  >
                    {columns.map((column) => (
                      <td
                        key={column.key}
                        {...stylex.props(
                          styles.cell,
                          alignStyle(column.align),
                          column.mono && styles.mono,
                          column.nowrap && styles.nowrap,
                        )}
                      >
                        {column.render
                          ? column.render(row, index)
                          : cellText(row, column.key)}
                      </td>
                    ))}
                  </tr>
                  {expanded ? (
                    <tr>
                      <td
                        colSpan={columns.length}
                        {...stylex.props(styles.expandedCell)}
                      >
                        {renderExpanded(row)}
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}

function SortIndicator({
  active,
  direction,
}: {
  active: boolean;
  direction?: SortDirection;
}) {
  return (
    <span
      aria-hidden
      {...stylex.props(styles.sortIndicator, !active && styles.sortIdle)}
    >
      {active && direction === "asc" ? "▲" : "▼"}
    </span>
  );
}

function cellText(row: unknown, key: string) {
  const value = (row as Record<string, unknown>)[key];
  return value == null || value === "" ? "—" : String(value);
}

function alignStyle(align?: Align) {
  return align === "right"
    ? styles.alignRight
    : align === "center"
      ? styles.alignCenter
      : null;
}

const styles = stylex.create({
  scroller: {
    minWidth: 0,
    overflowX: "auto",
    scrollbarColor: `${colorVars["--color-text-secondary"]} transparent`,
    scrollbarWidth: "thin",
  },
  scrollerFill: { flex: 1, minHeight: 0, overflow: "auto" },
  framed: {
    borderColor: colorVars["--color-border"],
    borderStyle: "solid",
    borderWidth: borderVars["--border-width"],
    borderRadius: radiusVars["--radius-container"],
  },
  table: {
    width: "100%",
    minWidth: "var(--table-min-width, 720px)",
    borderCollapse: "collapse",
    fontSize: 12,
  },
  fixed: { tableLayout: "fixed" },
  headCell: {
    padding: spacingVars["--spacing-3"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    color: colorVars["--color-text-secondary"],
    fontWeight: "inherit",
    textAlign: "left",
    whiteSpace: "nowrap",
  },
  sticky: {
    position: "sticky",
    top: 0,
    zIndex: 1,
    backgroundColor: colorVars["--color-background-body"],
  },
  sortButton: {
    appearance: "none",
    display: "inline-flex",
    alignItems: "center",
    gap: spacingVars["--spacing-1"],
    padding: 0,
    border: 0,
    backgroundColor: "transparent",
    color: "inherit",
    cursor: "pointer",
    font: "inherit",
    whiteSpace: "nowrap",
  },
  sortIndicator: { fontSize: 8, lineHeight: 1 },
  sortIdle: { opacity: 0.35 },
  cell: {
    padding: spacingVars["--spacing-3"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    textAlign: "left",
    verticalAlign: "middle",
  },
  mono: { fontVariantNumeric: "tabular-nums" },
  nowrap: { whiteSpace: "nowrap" },
  alignRight: { textAlign: "right", justifyContent: "flex-end" },
  alignCenter: { textAlign: "center", justifyContent: "center" },
  clickableRow: {
    cursor: "pointer",
    ":hover": { backgroundColor: colorVars["--color-background-muted"] },
  },
  expandedCell: {
    padding: spacingVars["--spacing-4"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    backgroundColor: colorVars["--color-background-muted"],
  },
});
