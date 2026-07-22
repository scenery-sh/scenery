import type { ReactNode } from "react";
import type { DataTableSection } from "./DataTable.js";
import type {
	TablePageColumn,
	TablePageGroup,
} from "./query-table-contract.js";
import { cellText, dateValue, orderedGroupKeys } from "./query-table-values.js";
import { StatusBadge } from "./StatusBadge.js";

export { cellText } from "./query-table-values.js";

export function renderTableCell<Row extends object>(
	column: TablePageColumn<Row>,
	row: Row,
): ReactNode {
	const value = row[column.field];
	if (column.component) {
		const Component = column.component;
		return <Component row={row} value={value} />;
	}
	if (column.appearance === "datetime") {
		const date = dateValue(value);
		if (date) {
			return <time dateTime={date.toISOString()}>{date.toLocaleString()}</time>;
		}
		if (value === null || value === undefined || value === "") return "—";
		return cellText(value);
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

export function normalizeFilterOption(
	option: string | { readonly value: string; readonly label: string },
) {
	return typeof option === "string"
		? { label: option, value: option }
		: { label: option.label, value: option.value };
}

export function groupTableRows<Row extends object>(
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

	const ordered = orderedGroupKeys([...buckets.keys()], group.order);

	const column = columns.find(
		(candidate) => String(candidate.field) === group.field,
	);
	return ordered.map((key) => ({
		key,
		label: cellText(key, column?.statusMap),
		rows: buckets.get(key) ?? [],
	}));
}

export function firstColumnText<Row extends object>(
	row: Row,
	columns: readonly TablePageColumn<Row>[],
): string | null {
	const column = columns[0];
	if (!column) return null;
	return cellText(row[column.field], column.statusMap);
}
