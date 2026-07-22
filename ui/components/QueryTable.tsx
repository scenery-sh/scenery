import type { ISODateTimeString } from "@astryxdesign/core/DateTimeInput";
import { DateTimeInput } from "@astryxdesign/core/DateTimeInput";
import { DropdownMenu } from "@astryxdesign/core/DropdownMenu";
import { EmptyState } from "@astryxdesign/core/EmptyState";
import { Icon } from "@astryxdesign/core/Icon";
import { IconButton } from "@astryxdesign/core/IconButton";
import { Link } from "@astryxdesign/core/Link";
import { Pagination } from "@astryxdesign/core/Pagination";
import { ResizeHandle, useResizable } from "@astryxdesign/core/Resizable";
import { Selector } from "@astryxdesign/core/Selector";
import { Text } from "@astryxdesign/core/Text";
import {
	borderVars,
	colorVars,
	durationVars,
	easeVars,
	radiusVars,
	shadowVars,
	spacingVars,
} from "@astryxdesign/core/theme/tokens.stylex";
import * as stylex from "@stylexjs/stylex";
import { useQuery } from "@tanstack/react-query";
import {
	type ComponentType,
	type MouseEvent,
	type ReactNode,
	useCallback,
	useEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import { type Column, DataTable, type DataTableSection } from "./DataTable.js";
import {
	FilterToolbar,
	type FilterToolbarActiveFilter,
	type FilterToolbarFilter,
} from "./FilterToolbar.js";
import { QueryState } from "./QueryState.js";
import {
	type Problem,
	queryStateProps,
	type RequestState,
	requestStateFromQuery,
} from "./request-state.js";
import { StatusBadge, type StatusMap } from "./StatusBadge.js";

export type TablePageAppearance =
	| "auto"
	| "text"
	| "number"
	| "datetime"
	| "badge";
export type TablePageDirection = "asc" | "desc";
export type TablePageProblem = Problem;
export type TablePageResult<
	Row,
	Metadata extends object = Record<string, never>,
> = RequestState<{
	readonly items: readonly Row[];
	readonly nextCursor?: string;
	readonly total?: number;
	readonly truncated?: boolean;
	readonly metadata?: Metadata;
}>;

export interface TablePageQuery {
	readonly search?: string;
	readonly filters: Readonly<
		Record<string, string | readonly string[] | undefined>
	>;
	readonly sort?: string;
	readonly direction: TablePageDirection;
	readonly cursor?: string;
	readonly page: number;
	readonly limit: number;
}

export type TablePageFilterValue = string | readonly string[] | undefined;

export interface TablePageQueryControls {
	/** Set app-owned search text using the table's debounce and pagination reset semantics. */
	readonly setSearch: (value: string) => void;
	/** Set one declared enum filter and return to the first page. */
	readonly setFilter: (field: string, value: TablePageFilterValue) => void;
	/** Clear one declared enum filter and return to the first page. */
	readonly clearFilter: (field: string) => void;
	/** Reload the current query without changing its search, filters, or page. */
	readonly refresh: () => Promise<void>;
}

export interface TablePageResultContext<
	Row,
	Metadata extends object = Record<string, never>,
> {
	readonly rows: readonly Row[];
	readonly total?: number;
	readonly truncated?: boolean;
	/** Typed auxiliary fields projected from a binding result record. */
	readonly metadata?: Metadata;
	readonly filtered: boolean;
	readonly query: TablePageQuery;
	readonly controls: TablePageQueryControls;
}

export interface TablePageCellProps<Row, Value> {
	readonly row: Row;
	readonly value: Value;
}

export interface TablePageFilterProps<
	Value,
	Row extends object = object,
	Metadata extends object = Record<string, never>,
> {
	readonly value: Value | undefined;
	readonly onChange: (value: Value | undefined) => void;
	readonly label: string;
	readonly context: TablePageResultContext<Row, Metadata>;
}

export interface TablePageDateTimeRange {
	readonly from?: string;
	readonly to?: string;
}

export interface TablePageEmptyProps<
	Row extends object = object,
	Metadata extends object = Record<string, never>,
> {
	readonly filtered: boolean;
	readonly context: TablePageResultContext<Row, Metadata>;
}

export interface TablePageToolbarProps<
	Row extends object = object,
	Metadata extends object = Record<string, never>,
> {
	readonly context?: TablePageResultContext<Row, Metadata>;
}

export interface TablePageFooterProps<
	Row extends object = object,
	Metadata extends object = Record<string, never>,
> {
	readonly context: TablePageResultContext<Row, Metadata>;
}

export interface TablePageRowDetailProps<Row> {
	readonly row: Row;
}

export interface TablePageDetailPanelProps<Row> {
	readonly row: Row;
	readonly onClose: () => void;
}

export type TablePageRowActionProps<Row> = TablePageDetailPanelProps<Row>;

export type TablePageColumn<Row> = {
	readonly [Key in keyof Row]: {
		readonly field: Key;
		readonly label: string;
		readonly appearance: TablePageAppearance;
		readonly component?: ComponentType<TablePageCellProps<Row, Row[Key]>>;
		readonly statusMap?: StatusMap;
		readonly hidden?: boolean;
		readonly export?: boolean;
	};
}[keyof Row];

export type TablePageFilter<
	Row extends object = object,
	Metadata extends object = Record<string, never>,
> =
	| {
			readonly field: string;
			readonly label: string;
			readonly kind: "enum";
			readonly options: readonly (
				| string
				| { readonly value: string; readonly label: string }
			)[];
			readonly component?: ComponentType<
				TablePageFilterProps<string, Row, Metadata>
			>;
			readonly pinned?: boolean;
			readonly hidden?: boolean;
	  }
	| {
			readonly field: string;
			readonly label: string;
			readonly kind: "datetime";
			readonly component?: ComponentType<
				TablePageFilterProps<TablePageDateTimeRange, Row, Metadata>
			>;
			readonly pinned?: boolean;
			readonly hidden?: boolean;
	  };

export interface TablePageSort {
	readonly field: string;
	readonly label: string;
	readonly default?: TablePageDirection;
}

export interface TablePageGroup {
	readonly field: string;
	readonly label: string;
	readonly order?: readonly string[];
	readonly default?: boolean;
}

export interface TablePageSlots<
	Row extends object,
	CellKey extends keyof Row = never,
	FilterValues extends object = Record<never, never>,
	Metadata extends object = Record<string, never>,
> {
	readonly cells?: {
		readonly [Key in CellKey]?: ComponentType<
			TablePageCellProps<Row, Row[Key]>
		>;
	};
	readonly filters?: {
		readonly [Key in keyof FilterValues]?: ComponentType<
			TablePageFilterProps<FilterValues[Key], Row, Metadata>
		>;
	};
	readonly toolbar?: ComponentType<TablePageToolbarProps<Row, Metadata>>;
	readonly footer?: ComponentType<TablePageFooterProps<Row, Metadata>>;
	readonly empty?: ComponentType<TablePageEmptyProps<Row, Metadata>>;
	readonly rowDetail?: ComponentType<TablePageRowDetailProps<Row>>;
	readonly detailPanel?: ComponentType<TablePageDetailPanelProps<Row>>;
	readonly rowAction?: ComponentType<TablePageRowActionProps<Row>>;
}

type Exact<Shape, Actual extends Shape> = Actual &
	Record<Exclude<keyof Actual, keyof Shape>, never>;

export function defineTablePageSlots<
	Row extends object,
	CellKey extends keyof Row = never,
	FilterValues extends object = Record<never, never>,
	Metadata extends object = Record<string, never>,
>() {
	return <Actual extends TablePageSlots<Row, CellKey, FilterValues, Metadata>>(
		slots: Exact<TablePageSlots<Row, CellKey, FilterValues, Metadata>, Actual>,
	): Actual => slots;
}

export interface QueryTableProps<
	Row extends object,
	Metadata extends object = Record<string, never>,
> {
	readonly resource: string;
	readonly description?: string;
	readonly columns: readonly TablePageColumn<Row>[];
	readonly filters: readonly TablePageFilter<Row, Metadata>[];
	readonly sorts: readonly TablePageSort[];
	readonly searchable?: boolean;
	readonly hideSearch?: boolean;
	readonly rowLink?: (row: Row) => string;
	readonly rowDetail?: ComponentType<TablePageRowDetailProps<Row>>;
	readonly detailPanel?: ComponentType<TablePageDetailPanelProps<Row>>;
	readonly rowAction?: ComponentType<TablePageRowActionProps<Row>>;
	readonly detailPanelWidth?: number;
	readonly detailTitle?: (row: Row) => ReactNode;
	readonly rowDetailAction?: (row: Row) => ReactNode;
	readonly emptyAction?: ReactNode;
	readonly exportAction?: {
		readonly label?: string;
		readonly fileName: string;
		readonly icon?: ReactNode;
	};
	readonly pagination?: "cursor" | "page";
	readonly hideHeader?: boolean;
	readonly fill?: boolean;
	readonly numbered?: boolean;
	readonly groups?: readonly TablePageGroup[];
	readonly pageSize: number;
	readonly queryKey: readonly unknown[];
	readonly load: (
		query: TablePageQuery,
		signal?: AbortSignal,
	) => Promise<TablePageResult<Row, Metadata>>;
	readonly empty?: ComponentType<TablePageEmptyProps<Row, Metadata>>;
	readonly footer?: ComponentType<TablePageFooterProps<Row, Metadata>>;
	readonly onResultContextChange?: (
		context: TablePageResultContext<Row, Metadata>,
	) => void;
}

// Keystrokes update the visible input immediately; the query key only moves
// after this idle window, so typing does not launch one request per character.
const searchDebounceMilliseconds = 250;
const noGroupValue = "__scenery_no_group__";
const emptyTableRows: readonly never[] = [];

export function QueryTable<
	Row extends object,
	Metadata extends object = Record<string, never>,
>({
	resource,
	description,
	columns,
	filters: declaredFilters,
	sorts,
	searchable,
	hideSearch,
	rowLink,
	rowDetail: RowDetail,
	detailPanel: DetailPanel,
	rowAction: RowAction,
	detailPanelWidth,
	detailTitle,
	rowDetailAction,
	emptyAction,
	exportAction,
	pagination,
	hideHeader,
	fill,
	numbered,
	groups = [],
	pageSize,
	queryKey,
	load,
	empty: Empty,
	footer: Footer,
	onResultContextChange,
}: QueryTableProps<Row, Metadata>) {
	const defaultSort = sorts.find((sort) => sort.default);
	const [search, setSearch] = useState("");
	const [debouncedSearch, setDebouncedSearch] = useState("");
	const [filters, setFilters] = useState<
		Readonly<Record<string, string | readonly string[] | undefined>>
	>({});
	const [sort, setSort] = useState(defaultSort?.field);
	const [direction, setDirection] = useState<TablePageDirection>(
		defaultSort?.default ?? "asc",
	);
	const [cursor, setCursor] = useState<string>();
	const [history, setHistory] = useState<readonly (string | undefined)[]>([]);
	const [page, setPage] = useState(1);
	const [expandedKey, setExpandedKey] = useState<string | null>(null);
	const [selectedRow, setSelectedRow] = useState<{
		readonly key: string;
		readonly row: Row;
	} | null>(null);
	const declaredFiltersRef = useRef(declaredFilters);
	declaredFiltersRef.current = declaredFilters;
	const allowedGroups = pagination ? [] : groups;
	const [activeGroupField, setActiveGroupField] = useState(
		() => allowedGroups.find((group) => group.default)?.field ?? noGroupValue,
	);
	const panel = useResizable({
		defaultSize: detailPanelWidth ?? 360,
		minSizePx: 280,
		maxSizePx: 560,
	});
	const warnedPaginatedGroups = useRef(false);
	const warnedDetailConflict = useRef(false);
	const warnedRowActionConflict = useRef(false);

	useEffect(() => {
		const timer = setTimeout(
			() => setDebouncedSearch(search),
			searchDebounceMilliseconds,
		);
		return () => clearTimeout(timer);
	}, [search]);
	useEffect(() => {
		if (!isDevelopmentBuild()) return;
		if (pagination && groups.length > 0 && !warnedPaginatedGroups.current) {
			warnedPaginatedGroups.current = true;
			console.warn(
				"QueryTable ignores groups for paginated data because section counts would only describe one page.",
			);
		}
		if (DetailPanel && RowDetail && !warnedDetailConflict.current) {
			warnedDetailConflict.current = true;
			console.warn(
				"QueryTable received both detailPanel and rowDetail; detailPanel takes precedence.",
			);
		}
		if (
			RowAction &&
			(DetailPanel || RowDetail) &&
			!warnedRowActionConflict.current
		) {
			warnedRowActionConflict.current = true;
			console.warn(
				"QueryTable received rowAction with rowDetail or detailPanel; rowAction takes precedence.",
			);
		}
	}, [DetailPanel, RowAction, RowDetail, groups.length, pagination]);
	const query = useMemo<TablePageQuery>(
		() => ({
			search: debouncedSearch.trim() || undefined,
			filters,
			sort,
			direction,
			cursor,
			page,
			limit: pageSize,
		}),
		[cursor, debouncedSearch, direction, filters, page, pageSize, sort],
	);
	const resultQuery = useQuery({
		queryKey: [...queryKey, query],
		queryFn: ({ signal }) => load(query, signal),
	});
	const result = requestStateFromQuery<{
		readonly items: readonly Row[];
		readonly nextCursor?: string;
		readonly total?: number;
		readonly truncated?: boolean;
		readonly metadata?: Metadata;
	}>(resultQuery);

	const resetQuery = useCallback(() => {
		setCursor(undefined);
		setHistory([]);
		setPage(1);
		setExpandedKey(null);
		setSelectedRow(null);
	}, []);
	const setQueryFilter = useCallback(
		(field: string, value: TablePageFilterValue) => {
			const declared = declaredFiltersRef.current.find(
				(candidate) => candidate.field === field,
			);
			if (declared?.kind !== "enum") {
				if (isDevelopmentBuild()) {
					console.warn(
						`QueryTable toolbar ignored unknown or non-enum filter "${field}".`,
					);
				}
				return;
			}
			const normalized =
				typeof value === "string"
					? value === ""
						? undefined
						: [value]
					: value && value.length > 0
						? value
						: undefined;
			setFilters((current) => ({ ...current, [field]: normalized }));
			resetQuery();
		},
		[resetQuery],
	);
	const clearQueryFilter = useCallback(
		(field: string) => setQueryFilter(field, undefined),
		[setQueryFilter],
	);
	const setQuerySearch = useCallback(
		(value: string) => {
			setSearch(value);
			resetQuery();
		},
		[resetQuery],
	);
	const refreshQuery = useCallback(async () => {
		setExpandedKey(null);
		setSelectedRow(null);
		await resultQuery.refetch();
	}, [resultQuery.refetch]);
	const queryControls = useMemo<TablePageQueryControls>(
		() => ({
			clearFilter: clearQueryFilter,
			refresh: refreshQuery,
			setFilter: setQueryFilter,
			setSearch: setQuerySearch,
		}),
		[clearQueryFilter, refreshQuery, setQueryFilter, setQuerySearch],
	);
	const filtered =
		debouncedSearch.trim() !== "" ||
		Object.values(filters).some(
			(value) =>
				value !== undefined &&
				value !== "" &&
				(!Array.isArray(value) || value.length > 0),
		);
	const items: readonly Row[] =
		result.kind === "result" ? result.items : emptyTableRows;
	const total = result.kind === "result" ? result.total : undefined;
	const truncated = result.kind === "result" ? result.truncated : undefined;
	const metadata = result.kind === "result" ? result.metadata : undefined;
	const resultContext = useMemo<TablePageResultContext<Row, Metadata>>(
		() => ({
			rows: items,
			total,
			truncated,
			metadata,
			filtered,
			query,
			controls: queryControls,
		}),
		[filtered, items, metadata, query, queryControls, total, truncated],
	);
	useEffect(() => {
		onResultContextChange?.(resultContext);
	}, [onResultContextChange, resultContext]);
	const rowKey = (row: Row, index: number) => rowLink?.(row) ?? String(index);
	const visibleColumns = columns.filter((column) => !column.hidden);
	const visibleDeclaredFilters = declaredFilters.filter(
		(filter) => !filter.hidden,
	);
	const activeGroup = allowedGroups.find(
		(group) => group.field === activeGroupField,
	);
	const sections = useMemo<readonly DataTableSection<Row>[] | undefined>(() => {
		if (!activeGroup) return undefined;
		return groupRows(items, activeGroup, columns);
	}, [activeGroup, columns, items]);
	// Rows in display order: grouped sections flatten to the same indices
	// DataTable hands to getRowKey, so arrow-key selection stays aligned.
	const orderedRows = useMemo<readonly Row[]>(
		() => (sections ? sections.flatMap((section) => section.rows) : items),
		[items, sections],
	);
	useEffect(() => {
		if (!selectedRow || (!DetailPanel && !RowAction)) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (event.key === "Escape") {
				setSelectedRow(null);
				return;
			}
			if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
			if (
				event.target instanceof Element &&
				event.target.closest("input, textarea, select, [contenteditable]")
			) {
				return;
			}
			event.preventDefault();
			const currentIndex = orderedRows.findIndex(
				(row, index) => rowKey(row, index) === selectedRow.key,
			);
			if (currentIndex === -1) return;
			const nextIndex = currentIndex + (event.key === "ArrowDown" ? 1 : -1);
			const next = orderedRows[nextIndex];
			if (next) setSelectedRow({ key: rowKey(next, nextIndex), row: next });
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	});
	const applySort = (field: string) => {
		if (field === sort) {
			setDirection((current) => (current === "asc" ? "desc" : "asc"));
		} else {
			setSort(field);
			setDirection(
				sorts.find((item) => item.field === field)?.default ?? "asc",
			);
		}
		resetQuery();
	};
	const dataColumns = visibleColumns.map<Column<Row>>(
		(column, columnIndex) => ({
			key: String(column.field),
			header: column.label,
			align: column.appearance === "number" ? "right" : "left",
			nowrap:
				column.appearance === "datetime" || column.appearance === "number",
			sortKey: sorts.some((item) => item.field === String(column.field))
				? String(column.field)
				: undefined,
			render: (row) => {
				const value = renderCell(column, row);
				const href = rowLink?.(row);
				return href && columnIndex === 0 ? (
					<Link href={href}>{value}</Link>
				) : (
					value
				);
			},
		}),
	);
	if (RowDetail && !DetailPanel && !RowAction) {
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
	const toolbarFilters: FilterToolbarFilter[] = visibleDeclaredFilters
		.filter(
			(
				filter,
			): filter is Extract<
				TablePageFilter<Row, Metadata>,
				{ readonly kind: "enum" }
			> => filter.kind === "enum",
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
	const activeDateTimeFilters: FilterToolbarActiveFilter[] =
		visibleDeclaredFilters
			.filter(
				(
					filter,
				): filter is Extract<
					TablePageFilter<Row, Metadata>,
					{ readonly kind: "datetime" }
				> => filter.kind === "datetime",
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
		<section
			aria-label={resource}
			{...stylex.props(styles.root, fill && styles.rootFill)}
		>
			{description ? (
				<Text color="secondary" type="supporting">
					{description}
				</Text>
			) : null}
			{searchable ||
			visibleDeclaredFilters.length > 0 ||
			sorts.length > 0 ||
			allowedGroups.length > 0 ||
			result.kind === "result" ||
			exportAction ? (
				<FilterToolbar
					activeFilterItems={activeDateTimeFilters}
					exportLabel={exportAction?.label}
					exportIcon={exportAction?.icon}
					filters={toolbarFilters}
					onExport={
						exportAction && result.kind === "result"
							? () =>
									exportRows(
										exportAction.fileName,
										columns.filter((column) => column.export !== false),
										items,
									)
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
						searchable && !hideSearch ? setQuerySearch : undefined
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
					filterContent={visibleDeclaredFilters.map((filter) => {
						if (filter.kind === "enum") {
							if (!filter.component) return null;
							const current = filters[filter.field];
							return (
								<EnumFilter
									context={resultContext}
									filter={filter}
									key={filter.field}
									onChange={(value: string | undefined) => {
										setFilters((values) => ({
											...values,
											[filter.field]: value ? [value] : undefined,
										}));
										resetQuery();
									}}
									value={Array.isArray(current) ? current[0] : undefined}
								/>
							);
						}
						const range = {
							from: filters[`${filter.field}_from`] as string | undefined,
							to: filters[`${filter.field}_to`] as string | undefined,
						};
						return (
							<DateTimeFilter
								context={resultContext}
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
				>
					{sorts.length > 0 ? (
						// One compact control instead of Sort + Direction selects:
						// the button shows the active sort and order; choosing the
						// active entry again flips the order. Sortable column
						// headers drive the same state.
						<DropdownMenu
							button={{
								label: `Sort: ${
									sorts.find((item) => item.field === sort)?.label ?? "None"
								} ${direction === "asc" ? "↑" : "↓"}`,
								size: "sm",
								variant: "secondary",
							}}
							items={sorts.map((item) => ({
								label:
									item.field === sort
										? `${item.label} ${direction === "asc" ? "↑" : "↓"}`
										: item.label,
								onClick: () => applySort(item.field),
							}))}
							menuWidth={200}
						/>
					) : null}
					{allowedGroups.length > 0 ? (
						// Same compact shape as the sort menu: one button showing the
						// active grouping, a menu with None plus the declared groups.
						<DropdownMenu
							button={{
								label: `Group: ${
									allowedGroups.find(
										(group) => group.field === activeGroupField,
									)?.label ?? "None"
								}`,
								size: "sm",
								variant: "secondary",
							}}
							items={[
								{ label: "None", value: noGroupValue },
								...allowedGroups.map((group) => ({
									label: group.label,
									value: group.field,
								})),
							].map((option) => ({
								label:
									option.value === activeGroupField
										? `${option.label} ✓`
										: option.label,
								onClick: () => {
									setActiveGroupField(option.value);
									setExpandedKey(null);
									setSelectedRow(null);
								},
							}))}
							menuWidth={200}
						/>
					) : null}
				</FilterToolbar>
			) : null}
			<div {...stylex.props(styles.workspace, fill && styles.workspaceFill)}>
				<div {...stylex.props(styles.content, fill && styles.contentFill)}>
					<QueryState
						{...queryStateProps(result, resource)}
						empty={
							Empty ? (
								<Empty context={resultContext} filtered={filtered} />
							) : filtered ? (
								<EmptyState title="No matching results." />
							) : emptyAction ? (
								<EmptyState actions={emptyAction} title="No results yet." />
							) : (
								<EmptyState title="No results yet." />
							)
						}
						isEmpty={result.kind === "result" && result.items.length === 0}
						retry={() => void resultQuery.refetch()}
					>
						<DataTable
							key={activeGroupField}
							columns={dataColumns}
							expandedKey={expandedKey}
							fill={fill}
							framed
							getRowKey={rowKey}
							hideHeader={hideHeader}
							minWidth={720}
							numbered={numbered}
							onSort={sorts.length > 0 ? applySort : undefined}
							sort={sort ? { key: sort, direction } : undefined}
							onRowClick={
								DetailPanel || RowAction
									? (row, index) => {
											const key = rowKey(row, index);
											setSelectedRow((current) =>
												current?.key === key ? null : { key, row },
											);
										}
									: undefined
							}
							renderExpanded={
								RowDetail && !DetailPanel && !RowAction
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
							sections={sections}
							selectedKey={
								DetailPanel || RowAction ? (selectedRow?.key ?? null) : null
							}
							sticky
						/>
					</QueryState>
					{Footer && result.kind === "result" ? (
						<Footer context={resultContext} />
					) : null}
					{pagination && result.kind === "result" ? (
						<div {...stylex.props(styles.pagination)}>
							<Pagination
								hasMore={
									pagination === "cursor"
										? Boolean(result.nextCursor)
										: undefined
								}
								isDisabled={false}
								label={`${resource} pagination`}
								onChange={(nextPage: number) => {
									if (pagination === "page") {
										setPage(nextPage);
										setExpandedKey(null);
										setSelectedRow(null);
										return;
									}
									const currentPage = history.length + 1;
									if (nextPage === currentPage - 1 && history.length > 0) {
										const previous = history.at(-1);
										setHistory((value) => value.slice(0, -1));
										setCursor(previous);
										setExpandedKey(null);
										setSelectedRow(null);
									} else if (
										nextPage === currentPage + 1 &&
										result.nextCursor
									) {
										setHistory((value) => [...value, cursor]);
										setCursor(result.nextCursor);
										setExpandedKey(null);
										setSelectedRow(null);
									}
								}}
								page={pagination === "page" ? page : history.length + 1}
								pageSize={pageSize}
								totalItems={pagination === "page" ? result.total : undefined}
								size="sm"
								variant={pagination === "page" ? "pages" : "none"}
							/>
						</div>
					) : null}
				</div>
				{DetailPanel && !RowAction && selectedRow ? (
					<div
						style={{ width: panel.size }}
						{...stylex.props(
							styles.detailPanelColumn,
							fill && styles.detailPanelColumnFill,
						)}
					>
						<aside
							aria-label={`${singular(resource)} details`}
							{...stylex.props(
								styles.detailPanel,
								fill && styles.detailPanelFill,
							)}
						>
							<ResizeHandle
								isAlwaysVisible={false}
								isReversed
								label="Resize detail panel"
								position="overlay"
								resizable={panel.props}
								xstyle={styles.overlayResizeHandle}
							/>
							<div {...stylex.props(styles.detailPanelHeader)}>
								<span {...stylex.props(styles.detailPanelTitle)}>
									{detailTitle
										? detailTitle(selectedRow.row)
										: firstColumnText(selectedRow.row, visibleColumns)}
								</span>
								<IconButton
									icon={<Icon icon="close" size="sm" />}
									label="Close detail panel"
									onClick={() => setSelectedRow(null)}
									variant="ghost"
								/>
							</div>
							<div {...stylex.props(styles.detailPanelBody)}>
								<DetailPanel
									key={selectedRow.key}
									onClose={() => setSelectedRow(null)}
									row={selectedRow.row}
								/>
							</div>
						</aside>
					</div>
				) : null}
			</div>
			{RowAction && selectedRow ? (
				<RowAction
					key={selectedRow.key}
					onClose={() => setSelectedRow(null)}
					row={selectedRow.row}
				/>
			) : null}
		</section>
	);
}

function EnumFilter<
	Row extends object,
	Metadata extends object = Record<string, never>,
>({
	filter,
	value,
	onChange,
	context,
}: {
	filter: Extract<TablePageFilter<Row, Metadata>, { readonly kind: "enum" }>;
	value: string | undefined;
	onChange: (value: string | undefined) => void;
	context: TablePageResultContext<Row, Metadata>;
}) {
	if (filter.component) {
		const Component = filter.component;
		return (
			<Component
				context={context}
				label={filter.label}
				onChange={onChange}
				value={value}
			/>
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

function DateTimeFilter<
	Row extends object,
	Metadata extends object = Record<string, never>,
>({
	filter,
	value,
	onChange,
	context,
}: {
	filter: Extract<
		TablePageFilter<Row, Metadata>,
		{ readonly kind: "datetime" }
	>;
	value: TablePageDateTimeRange;
	onChange: (value: TablePageDateTimeRange) => void;
	context: TablePageResultContext<Row, Metadata>;
}) {
	if (filter.component) {
		const Component = filter.component;
		return (
			<Component
				context={context}
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

function groupRows<Row extends object>(
	rows: readonly Row[],
	group: TablePageGroup,
	columns: readonly TablePageColumn<Row>[],
): readonly DataTableSection<Row>[] {
	const buckets = new Map<string, Row[]>();
	for (const row of rows) {
		const value = (row as Record<string, unknown>)[group.field];
		const key =
			value === null || value === undefined || value === ""
				? ""
				: typeof value === "object"
					? JSON.stringify(value)
					: String(value);
		const bucket = buckets.get(key);
		if (bucket) bucket.push(row);
		else buckets.set(key, [row]);
	}

	const ordered: string[] = [];
	for (const key of group.order ?? []) {
		if (key !== "" && buckets.has(key) && !ordered.includes(key)) {
			ordered.push(key);
		}
	}
	ordered.push(
		...[...buckets.keys()]
			.filter((key) => key !== "" && !ordered.includes(key))
			.sort((left, right) => left.localeCompare(right)),
	);
	if (buckets.has("")) ordered.push("");

	const column = columns.find(
		(candidate) => String(candidate.field) === group.field,
	);
	return ordered.map((key) => ({
		key,
		label: cellText(key, column?.statusMap),
		rows: buckets.get(key) ?? [],
	}));
}

function firstColumnText<Row extends object>(
	row: Row,
	columns: readonly TablePageColumn<Row>[],
): string | null {
	const column = columns[0];
	if (!column) return null;
	return cellText(row[column.field], column.statusMap);
}

function isDevelopmentBuild() {
	return (
		(import.meta as ImportMeta & { readonly env?: { readonly DEV?: boolean } })
			.env?.DEV ?? false
	);
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
			columns
				.map((column) => csvCell(cellText(row[column.field], column.statusMap)))
				.join(","),
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
	return `"${value.replaceAll('"', '""')}"`;
}

function cellText(value: unknown, statusMap?: StatusMap): string {
	if (value === null || value === undefined || value === "") return "—";
	if (statusMap && typeof value === "string") {
		const label = statusMap[value]?.label;
		return typeof label === "string" || typeof label === "number"
			? String(label)
			: value;
	}
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

const panelSlideIn = stylex.keyframes({
	from: { opacity: 0, transform: "translateX(16px)" },
	to: { opacity: 1, transform: "translateX(0)" },
});

const styles = stylex.create({
	root: {
		display: "flex",
		flexDirection: "column",
		gap: spacingVars["--spacing-4"],
		minWidth: 0,
	},
	workspace: {
		display: "grid",
		gridTemplateColumns: "minmax(0, 1fr)",
		alignItems: "stretch",
		minWidth: 0,
		position: "relative",
	},
	content: {
		display: "flex",
		flex: 1,
		flexDirection: "column",
		gap: spacingVars["--spacing-4"],
		gridArea: "1 / 1",
		minWidth: 0,
	},
	// Fill mode (Linear-style scrolling): the section flex-fills its page,
	// nothing above the grid moves, and the grid scroller plus the detail
	// panel body scroll independently.
	rootFill: { flex: 1, minHeight: 0 },
	workspaceFill: { flex: 1, minHeight: 0 },
	contentFill: { minHeight: 0 },
	detailPanelColumnFill: {
		display: "flex",
		flexDirection: "column",
		minHeight: 0,
	},
	detailPanelFill: {
		// Stays positioned so the overlay resize handle keeps its anchor.
		position: "relative",
		top: "auto",
		maxHeight: "100%",
		flex: 1,
		minHeight: 0,
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
	rowDetail: {
		display: "flex",
		flexDirection: "column",
		gap: spacingVars["--spacing-3"],
	},
	rowDetailAction: {
		display: "flex",
		justifyContent: "flex-end",
	},
	detailPanelColumn: {
		gridArea: "1 / 1",
		justifySelf: "end",
		flexShrink: 0,
		maxWidth: "calc(100% - 48px)",
		zIndex: 1,
	},
	// Content-sized with a scrollport cap: the panel never forces the page
	// taller than its scroll container, and while a long table scrolls past
	// it pins at the top and scrolls internally. 100cqh reads the PageLayout
	// scroll area's height (a size container) and degrades to viewport units
	// when QueryTable renders outside one.
	detailPanel: {
		boxSizing: "border-box",
		position: "sticky",
		top: 12,
		maxHeight: "calc(100cqh - 24px)",
		display: "flex",
		flexDirection: "column",
		backgroundColor: colorVars["--color-background-card"],
		borderColor: colorVars["--color-border"],
		borderStyle: "solid",
		borderWidth: borderVars["--border-width"],
		borderRadius: radiusVars["--radius-container"],
		boxShadow: shadowVars["--shadow-med"],
		animationName: {
			default: panelSlideIn,
			"@media (prefers-reduced-motion: reduce)": "none",
		},
		animationDuration: durationVars["--duration-medium"],
		animationTimingFunction: easeVars["--ease-standard"],
	},
	overlayResizeHandle: {
		insetInlineEnd: "auto",
		insetInlineStart: 0,
	},
	detailPanelHeader: {
		display: "flex",
		alignItems: "center",
		justifyContent: "space-between",
		gap: spacingVars["--spacing-2"],
		flexShrink: 0,
		padding: `${spacingVars["--spacing-2"]} ${spacingVars["--spacing-3"]}`,
		borderBottomColor: colorVars["--color-border"],
		borderBottomStyle: "solid",
		borderBottomWidth: borderVars["--border-width"],
	},
	detailPanelTitle: {
		fontSize: 13,
		fontWeight: 600,
		minWidth: 0,
		overflow: "hidden",
		textOverflow: "ellipsis",
		whiteSpace: "nowrap",
	},
	detailPanelBody: {
		minHeight: 0,
		overflowY: "auto",
		padding: spacingVars["--spacing-4"],
		scrollbarColor: `${colorVars["--color-text-secondary"]} transparent`,
		scrollbarWidth: "thin",
	},
});
