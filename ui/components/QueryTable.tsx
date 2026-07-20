import { DateTimeInput } from "@astryxdesign/core/DateTimeInput";
import type { ISODateTimeString } from "@astryxdesign/core/DateTimeInput";
import { Icon } from "@astryxdesign/core/Icon";
import { IconButton } from "@astryxdesign/core/IconButton";
import { Link } from "@astryxdesign/core/Link";
import { Pagination } from "@astryxdesign/core/Pagination";
import { Selector } from "@astryxdesign/core/Selector";
import { Text } from "@astryxdesign/core/Text";
import { spacingVars } from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import { useQuery } from "@tanstack/react-query";
import {
  type ComponentType,
  type MouseEvent,
  type ReactNode,
  useCallback,
  useMemo,
  useState,
} from "react";
import { type Column, DataTable } from "./DataTable.js";
import {
  FilterToolbar,
  type FilterToolbarActiveFilter,
  type FilterToolbarFilter,
} from "./FilterToolbar.js";
import { QueryState } from "./QueryState.js";
import {
  type Problem,
  queryStateProps,
  requestStateFromQuery,
  type RequestState,
} from "./request-state.js";
import { type StatusMap, StatusBadge } from "./StatusBadge.js";

export type TablePageAppearance =
  | "auto"
  | "text"
  | "number"
  | "datetime"
  | "badge";
export type TablePageDirection = "asc" | "desc";
export type TablePageProblem = Problem;
export type TablePageResult<Row> = RequestState<{
  readonly items: readonly Row[];
  readonly nextCursor?: string;
}>;

export interface TablePageQuery {
  readonly search?: string;
  readonly filters: Readonly<
    Record<string, string | readonly string[] | undefined>
  >;
  readonly sort?: string;
  readonly direction: TablePageDirection;
  readonly cursor?: string;
  readonly limit: number;
}

export interface TablePageCellProps<Row, Value> {
  readonly row: Row;
  readonly value: Value;
}

export interface TablePageFilterProps<Value> {
  readonly value: Value | undefined;
  readonly onChange: (value: Value | undefined) => void;
  readonly label: string;
}

export interface TablePageDateTimeRange {
  readonly from?: string;
  readonly to?: string;
}

export interface TablePageEmptyProps {
  readonly filtered: boolean;
}

export interface TablePageRowDetailProps<Row> {
  readonly row: Row;
}

export type TablePageColumn<Row> = {
  readonly [Key in keyof Row]: {
    readonly field: Key;
    readonly label: string;
    readonly appearance: TablePageAppearance;
    readonly component?: ComponentType<TablePageCellProps<Row, Row[Key]>>;
    readonly statusMap?: StatusMap;
  };
}[keyof Row];

export type TablePageFilter =
  | {
      readonly field: string;
      readonly label: string;
      readonly kind: "enum";
      readonly options: readonly (
        | string
        | { readonly value: string; readonly label: string }
      )[];
      readonly component?: ComponentType<TablePageFilterProps<string>>;
      readonly pinned?: boolean;
    }
  | {
      readonly field: string;
      readonly label: string;
      readonly kind: "datetime";
      readonly component?: ComponentType<
        TablePageFilterProps<TablePageDateTimeRange>
      >;
      readonly pinned?: boolean;
    };

export interface TablePageSort {
  readonly field: string;
  readonly label: string;
  readonly default?: TablePageDirection;
}

export interface TablePageSlots<
  Row,
  CellKey extends keyof Row = never,
  FilterValues extends object = Record<never, never>,
> {
  readonly cells?: {
    readonly [Key in CellKey]?: ComponentType<
      TablePageCellProps<Row, Row[Key]>
    >;
  };
  readonly filters?: {
    readonly [Key in keyof FilterValues]?: ComponentType<
      TablePageFilterProps<FilterValues[Key]>
    >;
  };
  readonly toolbar?: ComponentType;
  readonly empty?: ComponentType<TablePageEmptyProps>;
  readonly rowDetail?: ComponentType<TablePageRowDetailProps<Row>>;
}

type Exact<Shape, Actual extends Shape> = Actual &
  Record<Exclude<keyof Actual, keyof Shape>, never>;

export function defineTablePageSlots<
  Row,
  CellKey extends keyof Row = never,
  FilterValues extends object = Record<never, never>,
>() {
  return <Actual extends TablePageSlots<Row, CellKey, FilterValues>>(
    slots: Exact<TablePageSlots<Row, CellKey, FilterValues>, Actual>,
  ): Actual => slots;
}

export interface QueryTableProps<Row extends object> {
  readonly resource: string;
  readonly description?: string;
  readonly columns: readonly TablePageColumn<Row>[];
  readonly filters: readonly TablePageFilter[];
  readonly sorts: readonly TablePageSort[];
  readonly searchable?: boolean;
  readonly rowLink?: (row: Row) => string;
  readonly rowDetail?: ComponentType<TablePageRowDetailProps<Row>>;
  readonly rowDetailAction?: (row: Row) => ReactNode;
  readonly emptyAction?: ReactNode;
  readonly exportAction?: {
    readonly label?: string;
    readonly fileName: string;
    readonly icon?: ReactNode;
  };
  readonly pageSize: number;
  readonly queryKey: readonly unknown[];
  readonly load: (query: TablePageQuery) => Promise<TablePageResult<Row>>;
  readonly empty?: ComponentType<TablePageEmptyProps>;
}

export function QueryTable<Row extends object>({
  resource,
  description,
  columns,
  filters: declaredFilters,
  sorts,
  searchable,
  rowLink,
  rowDetail: RowDetail,
  rowDetailAction,
  emptyAction,
  exportAction,
  pageSize,
  queryKey,
  load,
  empty: Empty,
}: QueryTableProps<Row>) {
  const defaultSort = sorts.find((sort) => sort.default);
  const [search, setSearch] = useState("");
  const [filters, setFilters] = useState<
    Readonly<Record<string, string | readonly string[] | undefined>>
  >({});
  const [sort, setSort] = useState(defaultSort?.field);
  const [direction, setDirection] = useState<TablePageDirection>(
    defaultSort?.default ?? "asc",
  );
  const [cursor, setCursor] = useState<string>();
  const [history, setHistory] = useState<readonly (string | undefined)[]>([]);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  const query = useMemo<TablePageQuery>(
    () => ({
      search: search.trim() || undefined,
      filters,
      sort,
      direction,
      cursor,
      limit: pageSize,
    }),
    [cursor, direction, filters, pageSize, search, sort],
  );
  const resultQuery = useQuery({
    queryKey: [...queryKey, query],
    queryFn: () => load(query),
  });
  const result = requestStateFromQuery<{
    readonly items: readonly Row[];
    readonly nextCursor?: string;
  }>(resultQuery);

  const resetQuery = useCallback(() => {
    setCursor(undefined);
    setHistory([]);
    setExpandedKey(null);
  }, []);
  const filtered =
    search.trim() !== "" ||
    Object.values(filters).some(
      (value) =>
        value !== undefined &&
        value !== "" &&
        (!Array.isArray(value) || value.length > 0),
    );
  const items = result.kind === "result" ? result.items : [];
  const rowKey = (row: Row, index: number) =>
    rowLink?.(row) ?? String(index);
  const dataColumns = columns.map<Column<Row>>((column, columnIndex) => ({
    key: String(column.field),
    header: column.label,
    align: column.appearance === "number" ? "right" : "left",
    nowrap:
      column.appearance === "datetime" || column.appearance === "number",
    render: (row) => {
      const value = renderCell(column, row);
      const href = rowLink?.(row);
      return href && columnIndex === 0 ? (
        <Link href={href}>{value}</Link>
      ) : (
        value
      );
    },
  }));
  if (RowDetail) {
    dataColumns.unshift({
      key: "__expand",
      header: "",
      width: "40px",
      render: (row, index) => {
        const key = rowKey(row, index);
        const expanded = expandedKey === key;
        return (
          <IconButton
            icon={
              <Icon
                icon={expanded ? "chevronDown" : "chevronRight"}
                size="sm"
              />
            }
            label={expanded ? "Collapse row" : "Expand row"}
            onClick={(event: MouseEvent<HTMLButtonElement>) => {
              event.stopPropagation();
              setExpandedKey(expanded ? null : key);
            }}
            variant="ghost"
          />
        );
      },
    });
  }
  const toolbarFilters: FilterToolbarFilter[] = declaredFilters
    .filter(
      (
        filter,
      ): filter is Extract<TablePageFilter, { readonly kind: "enum" }> =>
        filter.kind === "enum",
    )
    .map((filter) => ({
      custom: Boolean(filter.component),
      field: filter.field,
      label: filter.label,
      options: filter.options.map(normalizeFilterOption),
      pinned: filter.pinned,
    }));
  const toolbarValues = Object.fromEntries(
    toolbarFilters.map((filter) => {
      const value = filters[filter.field];
      return [filter.field, Array.isArray(value) ? value[0] : undefined];
    }),
  );
  const activeDateTimeFilters: FilterToolbarActiveFilter[] = declaredFilters
    .filter(
      (
        filter,
      ): filter is Extract<TablePageFilter, { readonly kind: "datetime" }> =>
        filter.kind === "datetime",
    )
    .flatMap((filter) => {
      const from = filters[`${filter.field}_from`] as string | undefined;
      const to = filters[`${filter.field}_to`] as string | undefined;
      if (!from && !to) return [];
      return [
        {
          field: filter.field,
          label: filter.label,
          valueLabel: `${from ?? "…"} – ${to ?? "…"}`,
          onClear: () => {
            setFilters((values) => ({
              ...values,
              [`${filter.field}_from`]: undefined,
              [`${filter.field}_to`]: undefined,
            }));
            resetQuery();
          },
        },
      ];
    });

  return (
    <section aria-label={resource} {...stylex.props(styles.root)}>
      {description ? (
        <Text color="secondary" type="supporting">
          {description}
        </Text>
      ) : null}
      {searchable ||
      declaredFilters.length > 0 ||
      sorts.length > 0 ||
      result.kind === "result" ||
      exportAction ? (
        <FilterToolbar
          activeFilterItems={activeDateTimeFilters}
          exportLabel={exportAction?.label}
          exportIcon={exportAction?.icon}
          filters={toolbarFilters}
          onExport={
            exportAction && result.kind === "result"
              ? () => exportRows(exportAction.fileName, columns, items)
              : undefined
          }
          onFilterChange={(field, value) => {
            setFilters((values) => ({
              ...values,
              [field]: value ? [value] : undefined,
            }));
            resetQuery();
          }}
          onSearchChange={
            searchable
              ? (value) => {
                  setSearch(value);
                  resetQuery();
                }
              : undefined
          }
          resultLabel={
            result.kind === "result"
              ? `${result.items.length} ${
                  result.items.length === 1
                    ? singular(resource)
                    : resource.toLocaleLowerCase()
                }`
              : undefined
          }
          search={search}
          searchLabel={`Search ${resource.toLocaleLowerCase()}`}
          values={toolbarValues}
          filterContent={
            <>
              {declaredFilters.map((filter) => {
                if (filter.kind === "enum") {
                  if (!filter.component) return null;
                  const current = filters[filter.field];
                  return (
                    <EnumFilter
                      filter={filter}
                      key={filter.field}
                      onChange={(value: string | undefined) => {
                        setFilters((values) => ({
                          ...values,
                          [filter.field]: value ? [value] : undefined,
                        }));
                        resetQuery();
                      }}
                      value={
                        Array.isArray(current) ? current[0] : undefined
                      }
                    />
                  );
                }
                const range = {
                  from: filters[`${filter.field}_from`] as string | undefined,
                  to: filters[`${filter.field}_to`] as string | undefined,
                };
                return (
                  <DateTimeFilter
                    filter={filter}
                    key={filter.field}
                    onChange={(value: TablePageDateTimeRange) => {
                      setFilters((values) => ({
                        ...values,
                        [`${filter.field}_from`]: value.from,
                        [`${filter.field}_to`]: value.to,
                      }));
                      resetQuery();
                    }}
                    value={range}
                  />
                );
              })}
            </>
          }
        >
          {sorts.length > 0 ? (
            <>
              <Selector
                label="Sort"
                onChange={(value: string) => {
                  setSort(value);
                  resetQuery();
                }}
                options={sorts.map((item) => ({
                  label: item.label,
                  value: item.field,
                }))}
                size="sm"
                value={sort}
                width={180}
              />
              <Selector
                label="Direction"
                onChange={(value: string) => {
                  setDirection(value === "desc" ? "desc" : "asc");
                  resetQuery();
                }}
                options={[
                  { label: "Ascending", value: "asc" },
                  { label: "Descending", value: "desc" },
                ]}
                size="sm"
                value={direction}
                width={150}
              />
            </>
          ) : null}
        </FilterToolbar>
      ) : null}
      <QueryState
        {...queryStateProps(result, resource)}
        empty={
          Empty ? (
            <Empty filtered={filtered} />
          ) : filtered ? (
            "No matching results."
          ) : emptyAction ? (
            <div {...stylex.props(styles.emptyWithAction)}>
              <Text color="secondary" type="supporting">
                No results yet.
              </Text>
              {emptyAction}
            </div>
          ) : (
            "No results yet."
          )
        }
        isEmpty={result.kind === "result" && result.items.length === 0}
        retry={() => void resultQuery.refetch()}
      >
        <DataTable
          columns={dataColumns}
          expandedKey={expandedKey}
          framed
          getRowKey={rowKey}
          minWidth={720}
          renderExpanded={
            RowDetail
              ? (row) => (
                  <div {...stylex.props(styles.rowDetail)}>
                    <RowDetail row={row} />
                    {rowDetailAction ? (
                      <div {...stylex.props(styles.rowDetailAction)}>
                        {rowDetailAction(row)}
                      </div>
                    ) : null}
                  </div>
                )
              : undefined
          }
          rows={items}
          sticky
        />
      </QueryState>
      {result.kind === "result" ? (
        <div {...stylex.props(styles.pagination)}>
          <Pagination
            hasMore={Boolean(result.nextCursor)}
            isDisabled={false}
            label={`${resource} pagination`}
            onChange={(nextPage: number) => {
              const page = history.length + 1;
              if (nextPage === page - 1 && history.length > 0) {
                const previous = history.at(-1);
                setHistory((value) => value.slice(0, -1));
                setCursor(previous);
              } else if (nextPage === page + 1 && result.nextCursor) {
                setHistory((value) => [...value, cursor]);
                setCursor(result.nextCursor);
              }
            }}
            page={history.length + 1}
            pageSize={pageSize}
            size="sm"
            variant="none"
          />
        </div>
      ) : null}
    </section>
  );
}

function EnumFilter({
  filter,
  value,
  onChange,
}: {
  filter: Extract<TablePageFilter, { readonly kind: "enum" }>;
  value: string | undefined;
  onChange: (value: string | undefined) => void;
}) {
  if (filter.component) {
    const Component = filter.component;
    return (
      <Component label={filter.label} onChange={onChange} value={value} />
    );
  }
  return (
    <Selector
      hasClear
      label={filter.label}
      onChange={(next: string | null) => onChange(next ?? undefined)}
      options={filter.options.map(normalizeFilterOption)}
      placeholder="All"
      size="sm"
      value={value ?? null}
      width={180}
    />
  );
}

function DateTimeFilter({
  filter,
  value,
  onChange,
}: {
  filter: Extract<TablePageFilter, { readonly kind: "datetime" }>;
  value: TablePageDateTimeRange;
  onChange: (value: TablePageDateTimeRange) => void;
}) {
  if (filter.component) {
    const Component = filter.component;
    return (
      <Component
        label={filter.label}
        onChange={(next) => onChange(next ?? {})}
        value={value}
      />
    );
  }
  return (
    <div {...stylex.props(styles.dateRange)}>
      <DateTimeInput
        hasClear
        label={`${filter.label} from`}
        onChange={(next: ISODateTimeString | undefined) =>
          onChange({ ...value, from: exactDateTime(next) })
        }
        size="sm"
        value={localDateTime(value.from)}
        width={240}
      />
      <DateTimeInput
        hasClear
        label={`${filter.label} to`}
        onChange={(next: ISODateTimeString | undefined) =>
          onChange({ ...value, to: exactDateTime(next) })
        }
        size="sm"
        value={localDateTime(value.to)}
        width={240}
      />
    </div>
  );
}

function renderCell<Row extends object>(
  column: TablePageColumn<Row>,
  row: Row,
): ReactNode {
  const value = row[column.field];
  if (column.component) {
    const Component = column.component;
    return <Component row={row} value={value} />;
  }
  if (column.appearance === "datetime" && typeof value === "string") {
    return <time dateTime={value}>{new Date(value).toLocaleString()}</time>;
  }
  if (column.appearance === "badge") {
    return column.statusMap && typeof value === "string" ? (
      <StatusBadge map={column.statusMap} status={value} />
    ) : (
      <StatusBadge map={{}} status={cellText(value)} />
    );
  }
  if (column.appearance === "number" && typeof value === "number") {
    return value.toLocaleString();
  }
  return cellText(value);
}

function normalizeFilterOption(
  option: string | { readonly value: string; readonly label: string },
) {
  return typeof option === "string"
    ? { label: option, value: option }
    : { label: option.label, value: option.value };
}

function singular(resource: string) {
  const value = resource.toLocaleLowerCase();
  return value.endsWith("s") ? value.slice(0, -1) : value;
}

function exportRows<Row extends object>(
  fileName: string,
  columns: readonly TablePageColumn<Row>[],
  rows: readonly Row[],
) {
  const csv = [
    columns.map((column) => csvCell(column.label)).join(","),
    ...rows.map((row) =>
      columns.map((column) => csvCell(cellText(row[column.field]))).join(","),
    ),
  ].join("\n");
  const href = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const link = document.createElement("a");
  link.href = href;
  link.download = fileName;
  link.click();
  URL.revokeObjectURL(href);
}

function csvCell(value: string) {
  return `"${value.replaceAll("\"", "\"\"")}"`;
}

function cellText(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function localDateTime(value: string | undefined) {
  if (!value) return undefined;
  const instant = new Date(value);
  if (Number.isNaN(instant.getTime())) return undefined;
  return new Date(instant.getTime() - instant.getTimezoneOffset() * 60_000)
    .toISOString()
    .slice(0, 16) as ISODateTimeString;
}

function exactDateTime(value: ISODateTimeString | undefined) {
  return value ? new Date(value).toISOString() : undefined;
}

const styles = stylex.create({
  root: {
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-4"],
    minWidth: 0,
  },
  dateRange: {
    display: "flex",
    alignItems: "flex-end",
    flexWrap: "wrap",
    gap: spacingVars["--spacing-2"],
  },
  pagination: {
    display: "flex",
    justifyContent: "flex-end",
  },
  emptyWithAction: {
    display: "flex",
    alignItems: "center",
    flexDirection: "column",
    gap: spacingVars["--spacing-3"],
  },
  rowDetail: {
    display: "flex",
    flexDirection: "column",
    gap: spacingVars["--spacing-3"],
  },
  rowDetailAction: {
    display: "flex",
    justifyContent: "flex-end",
  },
});
