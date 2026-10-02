import { expect, test } from "bun:test";
import {
	computeTableWindow,
	precedingGroupHeaderIndex,
	groupHeaderIndex,
	tableWindowOverscan,
	tableWindowRowHeight,
	tableRowOffset,
	tableRowAtOffset,
} from "../../../ui/components/table-window.js";

test("keeps a measured inline detail in a bounded window and exact offsets", () => {
	const count = 10_001;
	const expanded = { index: 501, height: 1_200 };
	for (const scroll of [0, tableRowOffset(500,expanded), tableRowOffset(501,expanded)+600, tableRowOffset(8_500,expanded)]) {
		const window = computeTableWindow(count,scroll,600,44,8,expanded);
		expect(window.end-window.start+2).toBeLessThanOrEqual(33);
		expect(window.topHeight+tableRowOffset(window.end,expanded)-tableRowOffset(window.start,expanded)+window.bottomHeight).toBe(tableRowOffset(count,expanded));
		const row = tableRowAtOffset(scroll,expanded);
		expect(row).toBeGreaterThanOrEqual(window.start);
		expect(row).toBeLessThan(window.end);
	}
	expect(tableRowAtOffset(tableRowOffset(501,expanded)+1199,expanded)).toBe(501);
	expect(tableRowAtOffset(tableRowOffset(502,expanded),expanded)).toBe(502);
});

for (const count of [1_000, 5_000, 10_000]) {
	test(`windows ${count.toLocaleString()} rows to a bounded DOM`, () => {
		const window = computeTableWindow(count, 0, 600);
		// One rendered row per item in the window, plus top/bottom spacer rows.
		const rowCount = window.end - window.start + 2;

		expect(rowCount).toBeLessThanOrEqual(
			Math.ceil(600 / tableWindowRowHeight) + tableWindowOverscan * 2 + 2,
		);
		expect(
			window.topHeight +
				(window.end - window.start) * tableWindowRowHeight +
				window.bottomHeight,
		).toBe(count * tableWindowRowHeight);
	});
}

test("keeps absolute offsets while scrolling and keyboard-revealing a row", () => {
	const selectedIndex = 8_500;
	const viewportHeight = 600;
	const scrollTop = selectedIndex * tableWindowRowHeight - viewportHeight / 2;
	const window = computeTableWindow(10_000, scrollTop, viewportHeight);

	expect(window.start).toBeGreaterThan(0);
	expect(selectedIndex).toBeGreaterThanOrEqual(window.start);
	expect(selectedIndex).toBeLessThan(window.end);
	expect(window.topHeight + window.bottomHeight).toBeGreaterThan(0);
});

test("clamps a stale deep scroll position after the result shrinks", () => {
	const window = computeTableWindow(3, 9_000 * tableWindowRowHeight, 600);
	expect(window.start).toBe(2);
	expect(window.end).toBe(3);
});

test("retains the preceding group header when a window starts within a section", () => {
	const rows = ["header:a", "a1", "a2", "header:b", "b1", "b2"];
	const headers = groupHeaderIndex(rows, (row) => row.startsWith("header:"));
	expect(
		precedingGroupHeaderIndex(headers, 5),
	).toBe(3);
	expect(
		precedingGroupHeaderIndex(headers, 3),
	).toBeUndefined();
});

test("uses measured data and group heights with an inline detail", () => {
	const heights = [37, 49, 1_200, 49, 37, 49, 49];
	const expanded = {index: 2, height: 1_200};
	const groups = {indices: [0, 4], height: 37};
	let offset = 0;
	for (let index = 0; index < heights.length; index++) {
		expect(tableRowOffset(index, expanded, 49, groups)).toBe(offset);
		expect(tableRowAtOffset(offset, expanded, 49, groups)).toBe(index);
		expect(tableRowAtOffset(offset + heights[index] - 1, expanded, 49, groups)).toBe(index);
		offset += heights[index];
	}
	expect(tableRowOffset(heights.length, expanded, 49, groups)).toBe(offset);
	const window = computeTableWindow(heights.length, 1_300, 100, 49, 0, expanded, groups);
	expect(window.start).toBe(3);
	expect(window.end).toBe(6);
	expect(window.topHeight + heights.slice(window.start, window.end).reduce((sum, height) => sum + height, 0) + window.bottomHeight).toBe(offset);
});

test("keeps the memo boundary and stable windowed data path", async () => {
	const source = await Bun.file("ui/components/DataTable.tsx").text();
	expect(source).toContain("export const DataTable = memo(DataTableInner)");
	expect(source).toContain("data={renderedRows}");
	expect(source).toContain("expandedKey == null");
	expect(source).toContain("data: sourceRows");
	expect(source).toContain("new ResizeObserver");
	expect(source).toContain("observer.disconnect()");
	expect(source).toContain("<ExpansionCell rowKey={item.rowKey} />");
	expect(source).not.toContain("selectedKey,\n    sticky,");
});

test("inline expansion does not participate in QueryTable column memoization", async () => {
	const source = await Bun.file("ui/components/QueryTable.tsx").text();
	const columnMemo = source.slice(
		source.indexOf("const dataColumns = useMemo"),
		source.indexOf("const toolbarFilters = useMemo"),
	);
	expect(columnMemo).not.toContain("expandedKey");
	expect(columnMemo).not.toContain("__expand");
});

test("interactive cell controls never activate their table row", async () => {
	const source = await Bun.file("ui/components/DataTable.tsx").text();
	expect(source).toContain(
		"event.nativeEvent.composedPath().some(interactiveTarget)",
	);
	expect(source).toContain("if (interactiveClick(event)) return");
});
