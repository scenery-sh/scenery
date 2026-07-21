import {
  borderVars,
  colorVars,
  radiusVars,
  spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import { Badge } from "@astryxdesign/core/Badge";
import { Icon } from "@astryxdesign/core/Icon";
import * as stylex from "@stylexjs/stylex";
import {
  type CSSProperties,
  Fragment,
  type ReactNode,
  useState,
} from "react";
import { TableEmptyRow } from "./QueryState.js";

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
  empty?: ReactNode;
}) {
  const [collapsedSections, setCollapsedSections] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
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
        {hideHeader ? (
          // Column width hints normally live on the header cells; without a
          // header they move to a colgroup so fixed widths still apply.
          <colgroup>
            {columns.map((column) => (
              <col
                key={column.key}
                style={column.width ? { width: column.width } : undefined}
              />
            ))}
          </colgroup>
        ) : null}
        {hideHeader ? null : (
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
        )}
        <tbody>
          {rows.length === 0 && (!sections || sections.length === 0) ? (
            <TableEmptyRow columns={columns.length}>{empty}</TableEmptyRow>
          ) : sections ? (
            sections.map((section, sectionIndex) => {
              const collapsed = collapsedSections.has(section.key);
              const rowOffset = sections
                .slice(0, sectionIndex)
                .reduce((total, candidate) => total + candidate.rows.length, 0);
              return (
                <Fragment key={section.key}>
                  <tr
                    aria-expanded={!collapsed}
                    onClick={() =>
                      setCollapsedSections((current) => {
                        const next = new Set(current);
                        if (next.has(section.key)) next.delete(section.key);
                        else next.add(section.key);
                        return next;
                      })
                    }
                    onKeyDown={(event) => {
                      if (event.key !== "Enter" && event.key !== " ") return;
                      event.preventDefault();
                      event.currentTarget.click();
                    }}
                    role="button"
                    tabIndex={0}
                    {...stylex.props(styles.sectionRow)}
                  >
                    <td colSpan={columns.length} {...stylex.props(styles.sectionCell)}>
                      <span {...stylex.props(styles.sectionLabel)}>
                        <Icon
                          icon={collapsed ? "chevronRight" : "chevronDown"}
                          size="sm"
                        />
                        <span>{section.label}</span>
                        <Badge label={section.rows.length} variant="neutral" />
                      </span>
                    </td>
                  </tr>
                  {collapsed
                    ? null
                    : section.rows.map((row, index) =>
                        renderRow({
                          columns,
                          expandedKey,
                          getRowKey,
                          onRowClick,
                          renderExpanded,
                          row,
                          rowIndex: rowOffset + index,
                          rowLabel,
                          selectedKey,
                        }),
                      )}
                </Fragment>
              );
            })
          ) : (
            rows.map((row, index) =>
              renderRow({
                columns,
                expandedKey,
                getRowKey,
                onRowClick,
                renderExpanded,
                row,
                rowIndex: index,
                rowLabel,
                selectedKey,
              }),
            )
          )}
        </tbody>
      </table>
    </div>
  );
}

function renderRow<T>({
  columns,
  expandedKey,
  getRowKey,
  onRowClick,
  renderExpanded,
  row,
  rowIndex,
  rowLabel,
  selectedKey,
}: {
  columns: readonly Column<T>[];
  expandedKey?: string | null;
  getRowKey: (row: T, index: number) => string;
  onRowClick?: (row: T, index: number) => void;
  renderExpanded?: (row: T) => ReactNode;
  row: T;
  rowIndex: number;
  rowLabel?: (row: T) => string;
  selectedKey?: string | null;
}) {
  const key = getRowKey(row, rowIndex);
  const clickable = Boolean(onRowClick);
  const selected = selectedKey != null && selectedKey === key;
  const expanded =
    renderExpanded && expandedKey != null && expandedKey === key;
  return (
    <Fragment key={key}>
      <tr
        aria-label={clickable ? rowLabel?.(row) : undefined}
        onClick={
          onRowClick
            ? (event) => {
                if (interactiveTarget(event.target)) return;
                onRowClick(row, rowIndex);
              }
            : undefined
        }
        onKeyDown={
          onRowClick
            ? (event) => {
                if (event.target !== event.currentTarget) return;
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  onRowClick(row, rowIndex);
                }
              }
            : undefined
        }
        tabIndex={clickable ? 0 : undefined}
        {...stylex.props(clickable && styles.clickableRow)}
      >
        {columns.map((column, columnIndex) => (
          <td
            key={column.key}
            {...stylex.props(
              styles.cell,
              alignStyle(column.align),
              column.mono && styles.mono,
              column.nowrap && styles.nowrap,
              selected && styles.selectedCell,
              selected && columnIndex === 0 && styles.selectedFirstCell,
            )}
          >
            {column.render
              ? column.render(row, rowIndex)
              : cellText(row, column.key)}
          </td>
        ))}
      </tr>
      {expanded ? (
        <tr>
          <td colSpan={columns.length} {...stylex.props(styles.expandedCell)}>
            {renderExpanded(row)}
          </td>
        </tr>
      ) : null}
    </Fragment>
  );
}

function interactiveTarget(target: EventTarget | null) {
  return (
    target instanceof Element &&
    target.closest("a, button, input, select, textarea, [role='button']") != null
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
  selectedCell: {
    backgroundColor: colorVars["--color-accent-muted"],
  },
  selectedFirstCell: {
    boxShadow: `inset 2px 0 0 ${colorVars["--color-accent"]}`,
  },
  sectionRow: {
    cursor: "pointer",
    outline: { default: "none", ":focus-visible": "2px solid" },
    outlineColor: colorVars["--color-accent"],
    outlineOffset: -2,
  },
  sectionCell: {
    padding: spacingVars["--spacing-2"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    backgroundColor: colorVars["--color-background-muted"],
  },
  sectionLabel: {
    display: "inline-flex",
    alignItems: "center",
    gap: spacingVars["--spacing-2"],
    fontWeight: 600,
  },
  expandedCell: {
    padding: spacingVars["--spacing-4"],
    borderBottomColor: colorVars["--color-border"],
    borderBottomStyle: "solid",
    borderBottomWidth: borderVars["--border-width"],
    backgroundColor: colorVars["--color-background-muted"],
  },
});
