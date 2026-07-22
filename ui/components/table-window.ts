export interface TableWindow {
	start: number;
	end: number;
	topHeight: number;
	bottomHeight: number;
}

export const defaultTableWindowThreshold = 200;
export const tableWindowRowHeight = 44;
export const tableWindowOverscan = 8;

/** Pure window calculation shared by the table and its deterministic perf test. */
export function computeTableWindow(
	count: number,
	scrollTop: number,
	viewportHeight: number,
	rowHeight = tableWindowRowHeight,
	overscan = tableWindowOverscan,
): TableWindow {
	const visible = Math.max(1, Math.ceil(viewportHeight / rowHeight));
	const start = Math.min(
		Math.max(0, count - 1),
		Math.max(0, Math.floor(scrollTop / rowHeight) - overscan),
	);
	const end = Math.min(count, start + visible + overscan * 2);
	return {
		start,
		end,
		topHeight: start * rowHeight,
		bottomHeight: Math.max(0, (count - end) * rowHeight),
	};
}

/** Locate the nearest preceding group header for a window starting in a group. */
export function precedingGroupHeaderIndex<T>(
	items: readonly T[],
	start: number,
	isHeader: (item: T) => boolean,
): number | undefined {
	if (start <= 0 || isHeader(items[start])) return undefined;
	for (let index = start - 1; index >= 0; index--) {
		if (isHeader(items[index])) return index;
	}
	return undefined;
}
