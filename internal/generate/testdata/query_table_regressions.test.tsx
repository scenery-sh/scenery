import { expect, test } from "bun:test";
import type { TablePageColumn } from "../../../ui/components/query-table-contract.js";
import {
	exactDateTime,
	localDateTime,
} from "../../../ui/components/query-table-datetime.js";
import {
	csvBlob,
	csvCell,
	exportCellText,
	localDateToken,
	serializeCsv,
} from "../../../ui/components/query-table-export.js";
import {
	cellText,
	dateValue,
	orderedGroupKeys,
} from "../../../ui/components/query-table-values.js";

type Row = {
	readonly value: string | null;
	readonly group: string;
	readonly created: string | number | Date;
};

const valueColumn: TablePageColumn<Row> = {
	field: "value",
	label: "Value",
	appearance: "text",
};

test("CSV output hardens formulas and preserves empty data cells", () => {
	expect(csvCell("=1+1")).toBe('"\'=1+1"');
	expect(csvCell("+SUM(A1:A2)")).toBe('"\'+SUM(A1:A2)"');
	expect(csvCell("-3")).toBe('"\'-3"');
	expect(csvCell("@name")).toBe('"\'@name"');
	expect(csvCell("\tformula")).toBe('"\'\tformula"');
	expect(csvCell('safe "value"')).toBe('"safe ""value"""');
	expect(exportCellText(valueColumn, null)).toBe("");
});

test("CSV uses RFC 4180 rows and a UTF-8 Excel-compatible blob", async () => {
	const csv = serializeCsv(
		[valueColumn],
		[
			{ value: "first", group: "1", created: "" },
			{ value: "second", group: "2", created: "" },
		],
	);
	expect(csv).toBe('"Value"\r\n"first"\r\n"second"');
	const blob = csvBlob(csv);
	expect(blob.type).toBe("text/csv;charset=utf-8");
	expect([...new Uint8Array(await blob.arrayBuffer()).slice(0, 3)]).toEqual([
		0xef, 0xbb, 0xbf,
	]);
});

test("dated export names use local calendar fields", () => {
	const local = new Date(2026, 0, 2, 23, 30);
	expect(localDateToken(local)).toBe("2026-01-02");
});

test("datetime editing preserves hidden precision and rejects invalid input safely", () => {
	const original = "2026-07-21T21:59:59.999Z";
	expect(exactDateTime(localDateTime(original), original)).toBe(original);
	expect(exactDateTime("not-a-date" as never, original)).toBe(original);
	expect(exactDateTime("not-a-date" as never)).toBeUndefined();
});

test("datetime values accept epochs and Date objects with invalid fallback", () => {
	const epoch = 1_753_142_400_000;
	expect(dateValue(epoch)?.toISOString()).toBe(new Date(epoch).toISOString());
	expect(dateValue(new Date(epoch))?.toISOString()).toBe(
		new Date(epoch).toISOString(),
	);
	expect(dateValue("not-a-date")).toBeUndefined();
});

test("group labels sort numerically and fallback values serialize objects", () => {
	expect(orderedGroupKeys(["10", "2"])).toEqual(["2", "10"]);
	expect(cellText({ nested: true })).toBe('{"nested":true}');
});

test("StatTile renders a zero secondary metric", async () => {
	const source = Bun.file("ui/components/StatTile.tsx");
	expect(await source.text()).toContain("sub !== null && sub !== undefined");
});

test("download cleanup is deferred until after the attached link click", async () => {
	const source = await Bun.file("ui/components/query-table-export.ts").text();
	expect(source).toContain("document.body.append(link)");
	expect(source).toContain("link.remove()");
	expect(source).toContain("setTimeout(() => URL.revokeObjectURL(href), 0)");
});
