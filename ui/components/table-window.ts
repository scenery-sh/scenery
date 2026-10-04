export interface TableWindow {
	start: number;
	end: number;
	topHeight: number;
	bottomHeight: number;
}

export const defaultTableWindowThreshold = 200;
export const tableWindowRowHeight = 44;
export const tableWindowOverscan = 8;

export interface TableGroupRows {
 indices: readonly number[];
 height: number;
}

export interface ExpandedTableRow {
	index: number;
	height: number;
}

/** Pixel offset of a row boundary, including the measured inline detail. */
export function tableRowOffset(index: number, expanded?: ExpandedTableRow, rowHeight = tableWindowRowHeight, groups?: TableGroupRows): number {
	return index * rowHeight + (groups ? lowerBound(groups.indices, index) * (groups.height - rowHeight) : 0) + (expanded && index > expanded.index ? expanded.height - rowHeight : 0);
}

/** Row containing a pixel offset; the detail is one variable-height row. */
export function tableRowAtOffset(offset: number, expanded?: ExpandedTableRow, rowHeight = tableWindowRowHeight, groups?: TableGroupRows): number {
	const pixel = Math.max(0, offset);
	if (groups?.indices.length) {
  let low = 0;
  let high = Math.ceil(pixel / Math.min(rowHeight, groups.height, expanded?.height ?? rowHeight)) + 1;
  while (low + 1 < high) {
   const middle = Math.floor((low + high) / 2);
   if (tableRowOffset(middle, expanded, rowHeight, groups) <= pixel) low = middle;
   else high = middle;
  }
  return low;
 }
	if (!expanded || pixel < expanded.index * rowHeight) return Math.floor(pixel / rowHeight);
	if (pixel < expanded.index * rowHeight + expanded.height) return expanded.index;
	return Math.floor((pixel - expanded.height + rowHeight) / rowHeight);
}

/** Pure window calculation shared by the table and deterministic checks. */
export function computeTableWindow(
	count: number,
	scrollTop: number,
	viewportHeight: number,
	rowHeight = tableWindowRowHeight,
	overscan = tableWindowOverscan,
	expanded?: ExpandedTableRow,
 groups?: TableGroupRows,
): TableWindow {
	const visible = Math.max(1, Math.ceil(viewportHeight / rowHeight));
	const start = Math.min(
		Math.max(0, count - 1),
		Math.max(0, tableRowAtOffset(scrollTop, expanded, rowHeight, groups) - overscan),
	);
	const end = Math.min(count, expanded || groups?.indices.length
		? Math.max(start + 1, tableRowAtOffset(scrollTop + viewportHeight, expanded, rowHeight, groups) + overscan + 1)
		: start + visible + overscan * 2);
	return {
		start,
		end,
		topHeight: tableRowOffset(start, expanded, rowHeight, groups),
		bottomHeight: Math.max(0, tableRowOffset(count, expanded, rowHeight, groups) - tableRowOffset(end, expanded, rowHeight, groups)),
	};
}

/** Index headers once per row set. Its memory scales with groups, not rows. */
export function groupHeaderIndex<T>(items: readonly T[], isHeader: (item: T) => boolean): number[] {
 const headers: number[] = [];
 for (let index = 0; index < items.length; index++) {
  if (isHeader(items[index])) headers.push(index);
 }
 return headers;
}

/** A header at the window boundary already renders and needs no pinned copy. */
export function precedingGroupHeaderIndex(headers: readonly number[], start: number): number | undefined {
 const index = lowerBound(headers, start);
 return headers[index] === start ? undefined : headers[index - 1];
}

function lowerBound(indices: readonly number[], value: number): number {
 let low = 0, high = indices.length;
 while (low < high) {
  const middle = Math.floor((low + high) / 2);
  if (indices[middle] < value) low = middle + 1;
  else high = middle;
 }
 return low;
}
