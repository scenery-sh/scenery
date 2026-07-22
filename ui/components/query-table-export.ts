import type { TablePageColumn } from "./query-table-contract.js";
import { cellText } from "./query-table-values.js";

export function exportRows<Row extends object>(
	fileName: string,
	columns: readonly TablePageColumn<Row>[],
	rows: readonly Row[],
) {
	const csv = serializeCsv(columns, rows);
	const href = URL.createObjectURL(csvBlob(csv));
	const link = document.createElement("a");
	link.href = href;
	link.download = fileName.replaceAll("{date}", localDateToken());
	document.body.append(link);
	link.click();
	link.remove();
	setTimeout(() => URL.revokeObjectURL(href), 0);
}

export function serializeCsv<Row extends object>(
	columns: readonly TablePageColumn<Row>[],
	rows: readonly Row[],
) {
	return [
		columns
			.map((column) => csvCell(column.exportHeader ?? column.label))
			.join(","),
		...rows.map((row) =>
			columns
				.map((column) => csvCell(exportCellText(column, row[column.field])))
				.join(","),
		),
	].join("\r\n");
}

export function csvBlob(csv: string) {
	return new Blob(["\uFEFF", csv], { type: "text/csv;charset=utf-8" });
}

export function exportCellText<Row extends object>(
	column: TablePageColumn<Row>,
	value: unknown,
): string {
	const empty = column.exportEmpty ?? "";
	if (column.exportZeroEmpty && value === 0) return empty;
	if (value === null || value === undefined || value === "") return empty;
	if (column.exportFormat === "raw") {
		return typeof value === "object" ? JSON.stringify(value) : String(value);
	}
	if (column.exportFormat === "date") {
		return typeof value === "string" ? value.slice(0, 10) : String(value);
	}
	return cellText(value, column.statusMap);
}

export function csvCell(value: string) {
	const safe = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
	return `"${safe.replaceAll('"', '""')}"`;
}

export function localDateToken(date = new Date()) {
	const year = String(date.getFullYear()).padStart(4, "0");
	const month = String(date.getMonth() + 1).padStart(2, "0");
	const day = String(date.getDate()).padStart(2, "0");
	return `${year}-${month}-${day}`;
}
