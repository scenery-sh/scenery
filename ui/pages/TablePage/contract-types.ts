import type { ComponentType, ReactNode } from "react";
import type { Problem, RequestState } from "../../components/request-state.js";

export type TablePageAppearance = "auto" | "text" | "number" | "datetime" | "badge";
export type TablePageDirection = "asc" | "desc";

export type TablePageProblem = Problem;
export type TablePageResult<Row> = RequestState<{
  readonly items: readonly Row[];
  readonly nextCursor?: string;
}>;

export interface TablePageQuery {
  readonly filters: Readonly<Record<string, string | readonly string[] | undefined>>;
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

export interface TablePageToolbarProps {
  readonly loading: boolean;
  readonly reload: () => void;
}

export interface TablePageEmptyProps {
  readonly filtered: boolean;
}

export type TablePageColumn<Row> = {
  readonly [Key in keyof Row]: {
    readonly field: Key;
    readonly label: string;
    readonly appearance: TablePageAppearance;
    readonly component?: ComponentType<TablePageCellProps<Row, Row[Key]>>;
  }
}[keyof Row];

export type TablePageFilter =
  | { readonly field: string; readonly label: string; readonly kind: "enum"; readonly options: readonly string[]; readonly component?: ComponentType<TablePageFilterProps<string>> }
  | { readonly field: string; readonly label: string; readonly kind: "datetime"; readonly component?: ComponentType<TablePageFilterProps<TablePageDateTimeRange>> };

export interface TablePageSort {
  readonly field: string;
  readonly label: string;
  readonly default?: TablePageDirection;
}

export interface TablePageSlots<Row, CellKey extends keyof Row = never, FilterKey extends string = never> {
  readonly cells?: { readonly [Key in CellKey]?: ComponentType<TablePageCellProps<Row, Row[Key]>> };
  readonly filters?: { readonly [Key in FilterKey]?: ComponentType<TablePageFilterProps<string>> };
  readonly toolbar?: ComponentType<TablePageToolbarProps>;
  readonly empty?: ComponentType<TablePageEmptyProps>;
}

type Exact<Shape, Actual extends Shape> = Actual & Record<Exclude<keyof Actual, keyof Shape>, never>;

export function defineTablePageSlots<Row, CellKey extends keyof Row = never, FilterKey extends string = never>() {
  return <Actual extends TablePageSlots<Row, CellKey, FilterKey>>(slots: Exact<TablePageSlots<Row, CellKey, FilterKey>, Actual>): Actual => slots;
}

export interface TablePageProps<Row extends object> {
  readonly title: string;
  readonly description?: string;
  readonly columns: readonly TablePageColumn<Row>[];
  readonly filters: readonly TablePageFilter[];
  readonly sorts: readonly TablePageSort[];
  readonly rowLink?: (row: Row) => string;
  readonly pageSize: number;
  readonly load: (query: TablePageQuery) => Promise<TablePageResult<Row>>;
  readonly slots?: TablePageSlots<Row, keyof Row, string>;
  readonly children?: ReactNode;
}
