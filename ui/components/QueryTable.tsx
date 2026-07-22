import { DropdownMenu } from "@astryxdesign/core/DropdownMenu";
import { EmptyState } from "@astryxdesign/core/EmptyState";
import { Icon } from "@astryxdesign/core/Icon";
import { IconButton } from "@astryxdesign/core/IconButton";
import { Link } from "@astryxdesign/core/Link";
import { Pagination } from "@astryxdesign/core/Pagination";
import { ResizeHandle, useResizable } from "@astryxdesign/core/Resizable";
import { Text } from "@astryxdesign/core/Text";
import * as stylex from "@stylexjs/stylex";
import { hashKey, useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	type Column,
	DataTable,
	type DataTableSection,
	type SortState,
} from "./DataTable.js";
import {
	FilterToolbar,
	type FilterToolbarActiveFilter,
	type FilterToolbarFilter,
} from "./FilterToolbar.js";
import { QueryState } from "./QueryState.js";
import {
	firstColumnText,
	groupTableRows,
	normalizeFilterOption,
	renderTableCell,
} from "./query-table-cells.js";
import type {
	QueryTableProps,
	TablePageDateTimeRange,
	TablePageDirection,
	TablePageFilter,
	TablePageFilterValue,
	TablePageGroup,
	TablePageQuery,
	TablePageQueryControls,
	TablePageResultContext,
} from "./query-table-contract.js";
import { queryStateProps, requestStateFromQuery } from "./request-state.js";

export * from "./query-table-contract.js";

import { exportRows } from "./query-table-export.js";
import {
	DateTimeFilter,
	EnumFilter,
	formatLocalDateTime,
} from "./query-table-filters.js";
import { queryTableStyles as styles } from "./query-table-styles.js";

// Keystrokes update the visible input immediately; the query key only moves
// after this idle window, so typing does not launch one request per character.
const searchDebounceMilliseconds = 250;
const noGroupValue = "__scenery_no_group__";
const emptyTableRows: readonly never[] = [];
const emptyTableGroups: readonly TablePageGroup[] = [];

export function QueryTable<
	Row extends object,
	Metadata extends object = Record<string, never>,
>(props: QueryTableProps<Row, Metadata>) {
	return <QueryTableScope key={hashKey(props.queryKey)} {...props} />;
}

function QueryTableScope<
	Row extends object,
	Metadata extends object = Record<string, never>,
>({
	resource,
	resourceSingular,
	description,
	loadingLabel,
	errorTitle,
	columns,
	filters: declaredFilters,
	sorts,
	searchable,
	hideSearch,
	rowLink,
	rowDetail: RowDetail,
	detailPanel: DetailPanel,
	rowAction: RowAction,
	onRowIntent,
	detailPanelWidth,
	detailTitle,
	rowDetailAction,
	emptyAction,
	exportAction,
	pagination,
	hideHeader,
	fill,
	numbered,
	groups = emptyTableGroups,
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
	const [collapsedGroups, setCollapsedGroups] = useState<ReadonlySet<string>>(
		() => new Set(),
	);
	const allowedGroups = useMemo<readonly TablePageGroup[]>(
		() => (pagination ? [] : groups),
		[groups, pagination],
	);
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
	const resetQuery = useCallback(() => {
		setCursor(undefined);
		setHistory([]);
		setPage(1);
		setExpandedKey(null);
		setSelectedRow(null);
	}, []);
	useEffect(() => {
		if (debouncedSearch === search) return;
		const timer = setTimeout(() => {
			resetQuery();
			setDebouncedSearch(search);
		}, searchDebounceMilliseconds);
		return () => clearTimeout(timer);
	}, [debouncedSearch, resetQuery, search]);
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
		placeholderData: (previous) => previous,
	});
	const result = requestStateFromQuery<{
		readonly items: readonly Row[];
		readonly nextCursor?: string;
		readonly total?: number;
		readonly truncated?: boolean;
		readonly metadata?: Metadata;
	}>({
		...resultQuery,
		// A failed replacement request must surface its error instead of leaving
		// the retained result looking current.
		data: resultQuery.error ? undefined : resultQuery.data,
	});

	const setQueryFilter = useCallback(
		(field: string, value: TablePageFilterValue) => {
			const declared = declaredFilters.find(
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
				value === "" || value === undefined ? undefined : [value];
			setFilters((current) => ({ ...current, [field]: normalized }));
			resetQuery();
		},
		[declaredFilters, resetQuery],
	);
	const clearQueryFilter = useCallback(
		(field: string) => setQueryFilter(field, undefined),
		[setQueryFilter],
	);
	const setQuerySearch = useCallback((value: string) => setSearch(value), []);
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
			isPlaceholderData: resultQuery.isPlaceholderData,
			isRefreshing: resultQuery.isFetching && resultQuery.data !== undefined,
		}),
		[
			filtered,
			items,
			metadata,
			query,
			queryControls,
			resultQuery.isPlaceholderData,
			resultQuery.data,
			resultQuery.isFetching,
			total,
			truncated,
		],
	);
	useEffect(() => {
		onResultContextChange?.(resultContext);
	}, [onResultContextChange, resultContext]);
	const rowKey = useCallback(
		(row: Row, index: number) =>
			rowLink ? `${rowLink(row)}#${index}` : String(index),
		[rowLink],
	);
	const rowIntentState = useRef<{
		generation: number;
		keys: Set<string>;
	}>({ generation: resultQuery.dataUpdatedAt, keys: new Set() });
	const emitRowIntent = useCallback(
		(row: Row, index: number) => {
			if (!onRowIntent || resultQuery.isPlaceholderData) return;
			if (rowIntentState.current.generation !== resultQuery.dataUpdatedAt) {
				rowIntentState.current = {
					generation: resultQuery.dataUpdatedAt,
					keys: new Set(),
				};
			}
			const key = rowKey(row, index);
			if (rowIntentState.current.keys.has(key)) return;
			rowIntentState.current.keys.add(key);
			Promise.resolve()
				.then(() => onRowIntent(row))
				.catch(() => {
					if (rowIntentState.current.generation === resultQuery.dataUpdatedAt) {
						rowIntentState.current.keys.delete(key);
					}
				});
		},
		[
			onRowIntent,
			resultQuery.dataUpdatedAt,
			resultQuery.isPlaceholderData,
			rowKey,
		],
	);
	const visibleColumns = useMemo(
		() => columns.filter((column) => !column.hidden),
		[columns],
	);
	const visibleDeclaredFilters = useMemo(
		() => declaredFilters.filter((filter) => !filter.hidden),
		[declaredFilters],
	);
	const activeGroup = useMemo(
		() => allowedGroups.find((group) => group.field === activeGroupField),
		[activeGroupField, allowedGroups],
	);
	const sections = useMemo<readonly DataTableSection<Row>[] | undefined>(() => {
		if (!activeGroup) return undefined;
		return groupTableRows(items, activeGroup, columns);
	}, [activeGroup, columns, items]);
	// Rows in display order: grouped sections flatten to the same indices
	// DataTable hands to getRowKey, so arrow-key selection stays aligned.
	const orderedRows = useMemo<
		readonly { readonly row: Row; readonly index: number }[]
	>(() => {
		if (!sections) return items.map((row, index) => ({ row, index }));
		let index = 0;
		return sections.flatMap((section) => {
			const entries = section.rows.map((row) => ({ row, index: index++ }));
			return collapsedGroups.has(section.key) ? [] : entries;
		});
	}, [collapsedGroups, items, sections]);
	useEffect(() => {
		if (
			expandedKey &&
			!orderedRows.some(({ row, index }) => rowKey(row, index) === expandedKey)
		) {
			setExpandedKey(null);
		}
		if (!selectedRow) return;
		const next = orderedRows.find(
			({ row, index }) => rowKey(row, index) === selectedRow.key,
		);
		if (!next) {
			setSelectedRow(null);
			return;
		}
		if (next.row !== selectedRow.row) {
			setSelectedRow({ key: selectedRow.key, row: next.row });
		}
	}, [expandedKey, orderedRows, rowKey, selectedRow]);
	useEffect(() => {
		if (!selectedRow || (!DetailPanel && !RowAction)) return;
		const onKeyDown = (event: KeyboardEvent) => {
			if (
				event.defaultPrevented ||
				event.metaKey ||
				event.ctrlKey ||
				event.altKey ||
				isEditableTarget(event.target)
			) {
				return;
			}
			if (event.key === "Escape") {
				setSelectedRow(null);
				return;
			}
			if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
			event.preventDefault();
			const currentIndex = orderedRows.findIndex(
				({ row, index }) => rowKey(row, index) === selectedRow.key,
			);
			if (currentIndex === -1) return;
			const nextIndex = currentIndex + (event.key === "ArrowDown" ? 1 : -1);
			const next = orderedRows[nextIndex];
			if (next) {
				setSelectedRow({ key: rowKey(next.row, next.index), row: next.row });
			}
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [DetailPanel, RowAction, orderedRows, rowKey, selectedRow]);
	const applySort = useCallback(
		(field: string) => {
			if (field === sort) {
				setDirection((current) => (current === "asc" ? "desc" : "asc"));
			} else {
				setSort(field);
				setDirection(
					sorts.find((item) => item.field === field)?.default ?? "asc",
				);
			}
			resetQuery();
		},
		[resetQuery, sort, sorts],
	);
	const hasInlineDetail = Boolean(RowDetail && !DetailPanel && !RowAction);
	const dataColumns = useMemo<readonly Column<Row>[]>(
		() =>
			visibleColumns.map<Column<Row>>((column, columnIndex) => ({
				key: String(column.field),
				header: column.label,
				align: column.appearance === "number" ? "right" : "left",
				nowrap:
					column.appearance === "datetime" || column.appearance === "number",
				sortKey: sorts.some((item) => item.field === String(column.field))
					? String(column.field)
					: undefined,
				render: (row) => {
					const value = renderTableCell(column, row);
					const href = rowLink?.(row);
					return href && columnIndex === 0 ? (
						<Link href={href}>{value}</Link>
					) : (
						value
					);
				},
			})),
		[rowLink, sorts, visibleColumns],
	);
	const toolbarFilters = useMemo<readonly FilterToolbarFilter[]>(
		() =>
			visibleDeclaredFilters
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
				})),
		[visibleDeclaredFilters],
	);
	const toolbarValues = useMemo(
		() =>
			Object.fromEntries(
				toolbarFilters.map((filter) => {
					const value = filters[filter.field];
					return [filter.field, Array.isArray(value) ? value[0] : undefined];
				}),
			),
		[filters, toolbarFilters],
	);
	const activeDateTimeFilters = useMemo<readonly FilterToolbarActiveFilter[]>(
		() =>
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
							valueLabel: `${formatLocalDateTime(from)} – ${formatLocalDateTime(to)}`,
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
				}),
		[filters, resetQuery, visibleDeclaredFilters],
	);
	const dataTableSort = useMemo<SortState | undefined>(
		() => (sort ? { key: sort, direction } : undefined),
		[direction, sort],
	);
	const handleRowClick = useCallback(
		(row: Row, index: number) => {
			const key = rowKey(row, index);
			setSelectedRow((current) => (current?.key === key ? null : { key, row }));
		},
		[rowKey],
	);
	const renderInlineDetail = useCallback(
		(row: Row) =>
			hasInlineDetail && RowDetail ? (
				<div {...stylex.props(styles.rowDetail)}>
					<RowDetail row={row} />
					{rowDetailAction ? (
						<div {...stylex.props(styles.rowDetailAction)}>
							{rowDetailAction(row)}
						</div>
					) : null}
				</div>
			) : null,
		[RowDetail, hasInlineDetail, rowDetailAction],
	);

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
						exportAction &&
						result.kind === "result" &&
						!resultQuery.isPlaceholderData
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
										? (resourceSingular ?? "result")
										: resource.toLocaleLowerCase()
								}${resultContext.isRefreshing ? " · Refreshing…" : ""}`
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
									setCollapsedGroups(new Set());
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
						errorTitle={errorTitle}
						loadingLabel={loadingLabel}
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
							collapsedGroups={collapsedGroups}
							columns={dataColumns}
							onExpandedChange={setExpandedKey}
							expandedKey={expandedKey}
							fill={fill}
							framed
							getRowKey={rowKey}
							hideHeader={hideHeader}
							minWidth={720}
							numbered={numbered}
							onCollapsedGroupsChange={setCollapsedGroups}
							onSort={sorts.length > 0 ? applySort : undefined}
							sort={dataTableSort}
							onRowClick={DetailPanel || RowAction ? handleRowClick : undefined}
							onRowIntent={onRowIntent ? emitRowIntent : undefined}
							renderExpanded={hasInlineDetail ? renderInlineDetail : undefined}
							rows={items}
							sections={sections}
							selectedKey={
								DetailPanel || RowAction ? (selectedRow?.key ?? null) : null
							}
							sticky
							windowThreshold={
								pagination ? Number.POSITIVE_INFINITY : undefined
							}
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
								isDisabled={
									resultQuery.isPlaceholderData || resultQuery.isFetching
								}
								label={`${resource} pagination`}
								onChange={(nextPage: number) => {
									if (resultQuery.isPlaceholderData || resultQuery.isFetching) {
										return;
									}
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
							aria-label={`${resourceSingular ?? "result"} details`}
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

function isDevelopmentBuild() {
	return (
		(import.meta as ImportMeta & { readonly env?: { readonly DEV?: boolean } })
			.env?.DEV ?? false
	);
}

function isEditableTarget(target: EventTarget | null) {
	return (
		target instanceof Element &&
		target.closest(
			"input, textarea, select, [contenteditable], [role='combobox'], [role='listbox'], [role='menu'], [role='option'], [role='dialog']",
		) !== null
	);
}
