import type { ComponentType, ReactNode } from "react";
import type { Problem, RequestState } from "./request-state.js";
import type { StatusMap } from "./StatusBadge.js";

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

// Built-in enum controls are intentionally single-select. The wire query
// retains arrays because generated list inputs use list(string).
export type TablePageFilterValue = string | undefined;

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
	/** True while retained rows are shown for a different query in flight. */
	readonly isPlaceholderData: boolean;
	/** True while any request refreshes already delivered rows. */
	readonly isRefreshing: boolean;
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

export interface TablePageDateTimePreset {
	readonly label: string;
	readonly range: "today" | "last_7_days" | "month_to_date";
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
export type TablePageRowIntent<Row> = (row: Row) => void | Promise<void>;

export type TablePageExportFormat = "display" | "raw" | "date";

export type TablePageColumn<Row> = {
	readonly [Key in keyof Row]: {
		readonly field: Key;
		readonly label: string;
		readonly appearance: TablePageAppearance;
		readonly component?: ComponentType<TablePageCellProps<Row, Row[Key]>>;
		readonly statusMap?: StatusMap;
		readonly hidden?: boolean;
		readonly export?: boolean;
		readonly exportHeader?: string;
		readonly exportFormat?: TablePageExportFormat;
		readonly exportEmpty?: string;
		readonly exportZeroEmpty?: boolean;
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
			readonly presets?: readonly TablePageDateTimePreset[];
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
	readonly rowIntent?: TablePageRowIntent<Row>;
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
	readonly resourceSingular?: string;
	readonly description?: string;
	readonly loadingLabel?: ReactNode;
	readonly errorTitle?: string;
	readonly columns: readonly TablePageColumn<Row>[];
	readonly filters: readonly TablePageFilter<Row, Metadata>[];
	readonly sorts: readonly TablePageSort[];
	readonly searchable?: boolean;
	readonly hideSearch?: boolean;
	readonly rowLink?: (row: Row) => string;
	readonly rowDetail?: ComponentType<TablePageRowDetailProps<Row>>;
	readonly detailPanel?: ComponentType<TablePageDetailPanelProps<Row>>;
	readonly rowAction?: ComponentType<TablePageRowActionProps<Row>>;
	readonly onRowIntent?: TablePageRowIntent<Row>;
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
