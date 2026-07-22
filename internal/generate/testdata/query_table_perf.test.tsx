import { expect, test } from "bun:test";
import {
	computeTableWindow,
	precedingGroupHeaderIndex,
	tableWindowOverscan,
	tableWindowRowHeight,
} from "../../../ui/components/table-window.js";

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
	expect(
		precedingGroupHeaderIndex(rows, 5, (row) => row.startsWith("header:")),
	).toBe(3);
	expect(
		precedingGroupHeaderIndex(rows, 3, (row) => row.startsWith("header:")),
	).toBeUndefined();
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
