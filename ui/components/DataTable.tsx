import { Badge } from "@astryxdesign/core/Badge";
import { EmptyState } from "@astryxdesign/core/EmptyState";
import { Icon } from "@astryxdesign/core/Icon";
import { IconButton } from "@astryxdesign/core/IconButton";
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
	createContext,
	memo,
	type FocusEvent as ReactFocusEvent,
	type KeyboardEvent as ReactKeyboardEvent,
	type MouseEvent as ReactMouseEvent,
	type ReactNode,
	type UIEvent as ReactUIEvent,
	useCallback,
	useContext,
	useEffect,
	useMemo,
	useLayoutEffect,
	useRef,
	useState,
} from "react";
import { cellText } from "./query-table-values.js";
import {
	computeTableWindow,
	defaultTableWindowThreshold,
	precedingGroupHeaderIndex,
	groupHeaderIndex,
	tableWindowRowHeight,
	tableRowAtOffset,
	tableRowOffset,
	type ExpandedTableRow,
	type TableGroupRows,
} from "./table-window.js";

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

type SpacerRowRecord = Record<string, unknown> & {
	kind: "spacer";
	rowKey: string;
	groupKey: string;
	height: number;
};

type InternalRow<T> =
	| TableRowRecord<T>
	| ExpandedRowRecord<T>
	| SpacerRowRecord;

export interface DataTableProps<T> {
	columns: readonly Column<T>[];
	rows: readonly T[];
	sections?: readonly DataTableSection<T>[];
	collapsedGroups?: ReadonlySet<string>;
	onCollapsedGroupsChange?: (groups: ReadonlySet<string>) => void;
	getRowKey: (row: T, index: number) => string;
	minWidth?: number;
	sticky?: boolean;
	framed?: boolean;
	fill?: boolean;
	hideHeader?: boolean;
	layout?: "auto" | "fixed";
	onRowClick?: (row: T, index: number) => void;
	onRowIntent?: (row: T, index: number) => void;
	rowLabel?: (row: T) => string;
	sort?: SortState;
	onSort?: (sortKey: string) => void;
	expandedKey?: string | null;
	onExpandedChange?: (key: string | null) => void;
	renderExpanded?: (row: T) => ReactNode;
	selectedKey?: string | null;
	numbered?: boolean;
	empty?: string;
	/** Window rows above this count. Set to Infinity to disable. */
	windowThreshold?: number;
}

function DataTableInner<T>({
	columns,
	rows,
	sections,
	collapsedGroups: controlledCollapsedGroups,
	onCollapsedGroupsChange,
	getRowKey,
	minWidth,
	sticky,
	framed,
	fill,
	hideHeader,
	layout = "auto",
	onRowClick,
	onRowIntent,
	rowLabel,
	sort,
	onSort,
	expandedKey,
	onExpandedChange,
	renderExpanded,
	selectedKey,
	numbered,
	empty = "No results",
	windowThreshold = defaultTableWindowThreshold,
}: DataTableProps<T>) {
	const [internalCollapsedGroups, setInternalCollapsedGroups] = useState<
		Set<string>
	>(() => new Set());
	const collapsedGroups = controlledCollapsedGroups ?? internalCollapsedGroups;
	const mutableCollapsedGroups = useMemo(
		() => new Set(collapsedGroups),
		[collapsedGroups],
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
	const toggleGroup = useCallback(
		(groupKey: string) => {
			const update = (current: ReadonlySet<string>) => {
				const next = new Set(current);
				if (next.has(groupKey)) next.delete(groupKey);
				else next.add(groupKey);
				return next;
			};
			if (controlledCollapsedGroups) {
				onCollapsedGroupsChange?.(update(controlledCollapsedGroups));
			} else {
				setInternalCollapsedGroups(update);
			}
		},
		[controlledCollapsedGroups, onCollapsedGroupsChange],
	);
	const renderGroupHeader = useCallback(
		(groupKey: string, count: number) => (
			<span {...stylex.props(styles.groupLabel)}>
				<span>{sectionLabels.get(groupKey) ?? groupKey}</span>
				<Badge label={count} variant="neutral" />
			</span>
		),
		[sectionLabels],
	);
	const groupBy = useCallback((item: InternalRow<T>) => item.groupKey, []);
	const groupOrder = useMemo(
		() => sections?.map((section) => section.key),
		[sections],
	);
	const grouped = useTableGroupedRows<InternalRow<T>>({
		data: sourceRows,
		groupBy,
		collapsedGroups: mutableCollapsedGroups,
		onToggleGroup: toggleGroup,
		renderGroupHeader,
		getRowKey: internalRowKey,
		groupOrder,
	});
	const groupedRows = useMemo<InternalRow<T>[]>(
		() => (sections ? (grouped.data as InternalRow<T>[]) : sourceRows),
		[grouped.data, sections, sourceRows],
	);
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
	const expansionContext = useMemo<ExpansionContextValue>(
		() => ({ expandedKey: expandedKey ?? null, onChange: onExpandedChange }),
		[expandedKey, onExpandedChange],
	);
	const expandable = Boolean(renderExpanded && onExpandedChange);
	const scrollContainerRef = useRef<HTMLDivElement | null>(null);
	const [scrollTop, setScrollTop] = useState(0);
	const [viewportHeight, setViewportHeight] = useState(600);
	const [rowHeights, setRowHeights] = useState({row: tableWindowRowHeight, group: tableWindowRowHeight});
	const rowHeight = rowHeights.row;
	const groupHeaders = useMemo(() => sections ? groupHeaderIndex(tableRows, isGroupHeaderRow) : undefined, [sections, tableRows]);
	const groupRows = useMemo<TableGroupRows | undefined>(() => groupHeaders ? {indices: groupHeaders, height: rowHeights.group} : undefined, [groupHeaders, rowHeights.group]);
	const [expandedMeasurement, setExpandedMeasurement] = useState<{key: string; height: number}>();
	const expandedObserver = useRef<ResizeObserver | null>(null);
	const measureExpandedRow = useCallback((node: HTMLTableRowElement | null) => {
		expandedObserver.current?.disconnect();
		expandedObserver.current = null;
		if (!node || expandedKey == null) return;
		const measure = () => {
			const height = node.getBoundingClientRect().height;
			if (height <= 0) return;
			setExpandedMeasurement(previous => previous?.key === expandedKey && previous.height === height ? previous : {key: expandedKey, height});
		};
		measure();
		if (typeof ResizeObserver !== "undefined") {
			const observer = new ResizeObserver(measure);
			observer.observe(node);
			expandedObserver.current = observer;
		}
	}, [expandedKey]);
	useEffect(() => () => expandedObserver.current?.disconnect(), []);
	const expandedRow = useMemo<ExpandedTableRow | undefined>(() => {
		const index = tableRows.findIndex(item => item.kind === "expanded");
		return index < 0 ? undefined : {index, height: expandedMeasurement && expandedMeasurement.key === expandedKey ? expandedMeasurement.height : rowHeight};
	}, [expandedKey, expandedMeasurement, rowHeight, tableRows]);
	const windowed = tableRows.length > Math.max(0, windowThreshold);
	const window = useMemo(
		() =>
			windowed
				? computeTableWindow(tableRows.length, scrollTop, viewportHeight, rowHeight, undefined, expandedRow, groupRows)
				: {
						start: 0,
						end: tableRows.length,
						topHeight: 0,
						bottomHeight: 0,
					},
		[scrollTop, tableRows.length, viewportHeight, windowed, expandedRow, rowHeight, groupRows],
	);
	const renderedRows = useMemo<InternalRow<T>[]>(() => {
		if (!windowed) return tableRows;
		const visible = tableRows.slice(window.start, window.end);
		// If a grouped window starts inside a section, retain that section's
		// preceding header so the visible rows never lose their group context.
		const pinnedHeaderIndex = groupHeaders
			? precedingGroupHeaderIndex(groupHeaders, window.start)
			: undefined;
		const pinnedHeader =
			pinnedHeaderIndex === undefined
				? undefined
				: tableRows[pinnedHeaderIndex];
		const topHeight = Math.max(
			0,
			window.topHeight - (pinnedHeader ? rowHeights.group : 0),
		);
		return [
			...(topHeight > 0
				? [
						{
							kind: "spacer",
							rowKey: "__window_top",
							groupKey: "",
							height: topHeight,
						} as SpacerRowRecord,
					]
				: []),
			...(pinnedHeader ? [pinnedHeader] : []),
			...visible,
			...(window.bottomHeight > 0
				? [
						{
							kind: "spacer",
							rowKey: "__window_bottom",
							groupKey: "",
							height: window.bottomHeight,
						} as SpacerRowRecord,
					]
				: []),
		];
	}, [groupHeaders, rowHeights.group, tableRows, window, windowed]);
	// One representative row of each fixed-height kind establishes geometry.
 // Reattach when windowing replaces the observed DOM rows; size changes also
 // update offsets without requiring a scroll event.
 useLayoutEffect(() => {
  if (!windowed) return;
  const container = scrollContainerRef.current;
  const data = container?.querySelector<HTMLTableRowElement>("tr[data-scenery-row-key]");
  const header = container?.querySelector<HTMLTableRowElement>("tr[data-scenery-group-header]");
  const measure = () => {
   const row = data?.getBoundingClientRect().height;
   const group = header?.getBoundingClientRect().height;
   setRowHeights(previous => {
    const next = {row: row && row > 0 ? row : previous.row, group: group && group > 0 ? group : previous.group};
    return next.row === previous.row && next.group === previous.group ? previous : next;
   });
  };
  measure();
  if (typeof ResizeObserver === "undefined") return;
  const observer = new ResizeObserver(measure);
  if (data) observer.observe(data);
  if (header) observer.observe(header);
  return () => observer.disconnect();
 }, [renderedRows, windowed]);
	const setScrollContainer = useCallback((node: HTMLDivElement | null) => {
		scrollContainerRef.current = node;
		if (node) setViewportHeight(node.clientHeight || 600);
	}, []);
	useEffect(() => {
		const node = scrollContainerRef.current;
		if (!node || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver(() => {
			setViewportHeight(node.clientHeight || 600);
		});
		observer.observe(node);
		return () => observer.disconnect();
	}, []);
	const handleScroll = useCallback((event: ReactUIEvent<HTMLDivElement>) => {
		setScrollTop(event.currentTarget.scrollTop);
		setViewportHeight(event.currentTarget.clientHeight || 600);
	}, []);
	useEffect(() => {
		if (!windowed || selectedKey == null) return;
		const selectedIndex = tableRows.findIndex(
			(item) => item.kind === "row" && item.rowKey === selectedKey,
		);
		const container = scrollContainerRef.current;
		if (selectedIndex < 0 || !container) return;
		const top = tableRowOffset(selectedIndex, expandedRow, rowHeight, groupRows);
		if (top >= container.scrollTop && top + rowHeight <= container.scrollTop + container.clientHeight) return;
		container.scrollTop = Math.max(
			0,
			tableRowOffset(selectedIndex, expandedRow, rowHeight, groupRows) - container.clientHeight / 2,
		);
		setScrollTop(container.scrollTop);
	}, [selectedKey, tableRows, windowed, expandedRow, rowHeight, groupRows]);
	const previousGeometry = useRef<{groupedRows: InternalRow<T>[]; rows: InternalRow<T>[]; expanded?: ExpandedTableRow; rowHeight: number; groups?: TableGroupRows; scrollTop: number} | undefined>(undefined);
	useLayoutEffect(() => {
		const previous = previousGeometry.current;
		const container = scrollContainerRef.current;
		if (windowed && container && previous?.groupedRows === groupedRows && (previous.expanded?.index !== expandedRow?.index || previous.expanded?.height !== expandedRow?.height || previous.rowHeight !== rowHeight || previous.groups?.height !== groupRows?.height)) {
			let index = Math.min(previous.rows.length - 1, tableRowAtOffset(previous.scrollTop, previous.expanded, previous.rowHeight, previous.groups));
			if (previous.rows[index]?.kind === "expanded" && !expandedRow) index = Math.max(0, index - 1);
			const anchor = previous.rows[index];
			const nextIndex = tableRows.findIndex(item => item.rowKey === anchor?.rowKey);
			if (nextIndex >= 0) {
				const relative = Math.max(0, previous.scrollTop - tableRowOffset(index, previous.expanded, previous.rowHeight, previous.groups));
				const offset = tableRowOffset(nextIndex, expandedRow, rowHeight, groupRows) + Math.min(relative, anchor?.kind === "expanded" ? (expandedRow?.height ?? rowHeight) : isGroupHeaderRow(anchor) ? rowHeights.group : rowHeight);
				container.scrollTop = Math.min(Math.max(0, offset), Math.max(0, container.scrollHeight - container.clientHeight));
				setScrollTop(container.scrollTop);
			}
		}
		previousGeometry.current = {groupedRows, rows: tableRows, expanded: expandedRow, rowHeight, groups: groupRows, scrollTop: container?.scrollTop ?? scrollTop};
	}, [expandedRow, groupedRows, scrollTop, tableRows, windowed, rowHeight, groupRows, rowHeights.group]);

	const tableColumns = useMemo<TableColumn<InternalRow<T>>[]>(() => {
		const result: TableColumn<InternalRow<T>>[] = columns.map((column) => ({
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
						: cellText((item.row as Record<string, unknown>)[column.key])
					: null,
		}));
		if (expandable) {
			result.unshift({
				key: "__expand",
				header: "",
				width: pixel(40),
				renderCell: (item: InternalRow<T>) =>
					item.kind === "row" ? <ExpansionCell rowKey={item.rowKey} /> : null,
			});
		}
		return result;
	}, [columns, expandable, minWidth, onSort]);
	const rowIndexPlugin = useTableRowIndex<InternalRow<T>>({
		data: sourceRows,
		getRowKey: internalRowKey,
	});
	const astryxSort = useMemo(
		() =>
			sort
				? [
						{
							sortKey: sort.key,
							direction:
								sort.direction === "asc"
									? ("ascending" as const)
									: ("descending" as const),
						},
					]
				: [],
		[sort],
	);
	const handleSortChange = useCallback(
		(next: readonly { sortKey: string }[]) => {
			if (next[0]) onSort?.(next[0].sortKey);
		},
		[onSort],
	);
	const sortPlugin = useTableSortable<InternalRow<T>>({
		sort: astryxSort,
		onSortChange: handleSortChange,
		allowUnsortedState: false,
	});
	const selectedKeyRef = useRef(selectedKey);
	useEffect(() => {
		selectedKeyRef.current = selectedKey;
	}, [selectedKey]);
	// biome-ignore lint/correctness/useExhaustiveDependencies: windowing replaces DOM rows without changing selection.
	useEffect(() => {
		const container = scrollContainerRef.current;
		if (!container) return;
		for (const row of container.querySelectorAll<HTMLTableRowElement>(
			"tr[data-scenery-row-key]",
		)) {
			const selected = row.dataset.sceneryRowKey === selectedKey;
			if (selected) row.setAttribute("aria-selected", "true");
			else row.removeAttribute("aria-selected");
		}
	}, [renderedRows, selectedKey]);
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
				_columnIndex: number,
			) => {
				if (item.kind !== "row") return props;
				const source = columnsByKey.get(column.key);
				return {
					...props,
					xstyle: [
						...props.xstyle,
						source?.mono && styles.mono,
						source?.nowrap && styles.nowrap,
					].filter(Boolean),
				};
			},
			transformBodyRow: (props: BodyRowRenderProps, item: InternalRow<T>) => {
				if (isGroupHeaderRow(item)) return {...props, htmlProps: {...props.htmlProps, "data-scenery-group-header": true}};
				if (item.kind === "spacer") {
					return {
						htmlProps: { "aria-hidden": true },
						xstyle: [],
						children: (
							<TableCell
								colSpan={999}
								xstyle={styles.windowSpacer}
								style={{ height: item.height }}
							/>
						),
					};
				}
				if (item.kind === "expanded") {
					return {
						htmlProps: { ref: measureExpandedRow, "data-scenery-expanded-row": item.rowKey },
						xstyle: [],
						children: (
							<TableCell colSpan={999} xstyle={styles.expandedCell}>
								{renderExpanded?.(item.row)}
							</TableCell>
						),
					};
				}
				if (item.kind !== "row") return props;
				return {
					...props,
					htmlProps: {
						...props.htmlProps,
						"aria-selected":
							selectedKeyRef.current === item.rowKey ? true : undefined,
						"data-scenery-row-key": item.rowKey,
						"aria-label": rowLabel?.(item.row),
						onClick: onRowClick
							? (event: ReactMouseEvent<HTMLTableRowElement>) => {
									if (interactiveClick(event)) return;
									onRowClick(item.row, item.rowIndex);
								}
							: undefined,
						onFocus: onRowIntent
							? (event: ReactFocusEvent<HTMLTableRowElement>) => {
									if (event.target === event.currentTarget) {
										onRowIntent(item.row, item.rowIndex);
									}
								}
							: undefined,
						onKeyDown: (event: ReactKeyboardEvent<HTMLTableRowElement>) => {
							if (!onRowClick) return;
							if (event.target !== event.currentTarget) return;
							if (event.key !== "Enter" && event.key !== " ") return;
							event.preventDefault();
							onRowClick(item.row, item.rowIndex);
						},
						onPointerEnter: onRowIntent
							? () => onRowIntent(item.row, item.rowIndex)
							: undefined,
						tabIndex: onRowClick || onRowIntent ? 0 : undefined,
					},
					xstyle: [
						...props.xstyle,
						styles.dataRow,
						(onRowClick || onRowIntent) && styles.clickableRow,
					].filter(Boolean),
				};
			},
			transformScrollWrapper: (props: ScrollWrapperRenderProps) => ({
				...props,
				htmlProps: {
					...props.htmlProps,
					ref: setScrollContainer,
					onScroll: handleScroll,
					"data-scenery-windowed": windowed ? "true" : undefined,
				},
				xstyle: [
					...props.xstyle,
					styles.tableScroller,
					fill && styles.tableScrollerFill,
					windowed && styles.tableScrollerWindowed,
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
		onRowIntent,
		renderExpanded,
		rowLabel,
		setScrollContainer,
		measureExpandedRow,
		sticky,
		handleScroll,
		windowed,
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
			<ExpansionContext.Provider value={expansionContext}>
				<Table
					columns={tableColumns}
					data={renderedRows}
					density="balanced"
					dividers="rows"
					emptyState={<EmptyState isCompact title={empty} />}
					hasHover={Boolean(onRowClick)}
					idKey={sections ? grouped.idKey : internalRowKey}
					plugins={plugins}
					textOverflow="wrap"
				/>
			</ExpansionContext.Provider>
		</div>
	);
}

type ExpansionContextValue = {
	readonly expandedKey: string | null;
	readonly onChange?: (key: string | null) => void;
};

const ExpansionContext = createContext<ExpansionContextValue>({
	expandedKey: null,
});

function ExpansionCell({ rowKey }: { readonly rowKey: string }) {
	const { expandedKey, onChange } = useContext(ExpansionContext);
	const expanded = expandedKey === rowKey;
	return (
		<IconButton
			icon={<Icon icon={expanded ? "chevronDown" : "chevronRight"} size="sm" />}
			label={expanded ? "Collapse row" : "Expand row"}
			onClick={(event: ReactMouseEvent<HTMLButtonElement>) => {
				event.stopPropagation();
				onChange?.(expanded ? null : rowKey);
			}}
			variant="ghost"
		/>
	);
}

function interactiveTarget(target: EventTarget | null | undefined) {
	return (
		target instanceof Element &&
		target.closest("a, button, input, select, textarea, [role='button']") !=
			null
	);
}

function interactiveClick(event: ReactMouseEvent<HTMLTableRowElement>) {
	if (interactiveTarget(event.target)) return true;
	return event.nativeEvent.composedPath().some(interactiveTarget);
}

function isGroupHeaderRow(item: unknown) {
	if (typeof item !== "object" || item == null) return false;
	const kind = (item as { kind?: unknown }).kind;
	return kind !== "row" && kind !== "expanded" && kind !== "spacer";
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
		overflowAnchor: "none",
	},
	tableScrollerFill: { flex: 1, minHeight: 0, overflow: "auto" },
	tableScrollerWindowed: {
		maxHeight: "min(70vh, 720px)",
		overflowY: "auto",
		position: "relative",
	},
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
	dataRow: {
		backgroundColor: {
			default: null,
			':is([aria-selected="true"])': colorVars["--color-accent-muted"],
		},
		boxShadow: {
			default: null,
			':is([aria-selected="true"])': `inset 2px 0 0 ${colorVars["--color-accent"]}`,
		},
	},
	clickableRow: { cursor: "pointer" },
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
	windowSpacer: { padding: 0, border: 0 },
});

export const DataTable = memo(DataTableInner) as typeof DataTableInner;
