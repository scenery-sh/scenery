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
	test(`windows ${count.toLocaleString()} rows to a bounded mathematical range`, () => {
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


// The renderer executes the real component and plugin handlers. Astryx/style
// mocks exclude browser layout/paint; those are measured in a native browser.
await import("../../../tools/typescript/perf/query-table-profiler-mocks.tsx");
const React = await import("../../../tools/typescript/node_modules/react/index.js");
const {act,create} = await import("../../../tools/typescript/node_modules/react-test-renderer/index.js");
const {DataTable} = await import("../../../ui/components/DataTable.js");
(globalThis as typeof globalThis & {IS_REACT_ACT_ENVIRONMENT?:boolean}).IS_REACT_ACT_ENVIRONMENT=true;
const rows=Array.from({length:10_000},(_,id)=>({id:String(id)}));
const columns=[{key:"id",header:"ID",render:(row:{id:string})=>row.id}];
const key=(row:{id:string})=>row.id;

for (const count of [1_000,5_000,10_000]) {
    test(`the component renders bounded work for ${count} rows`,async()=>{
        let renderer:ReturnType<typeof create>;
        await act(async()=>{renderer=create(React.createElement(DataTable,{rows:rows.slice(0,count),columns,getRowKey:key}))});
        try { expect(renderer!.root.findAllByType("tr").length).toBeLessThanOrEqual(33); }
        finally {await act(async()=>renderer!.unmount())}
    });
}

test("interactive cell and composed-path controls do not activate their row",async()=>{
    const original=globalThis.Element;
    class Target {constructor(readonly control:boolean){} closest(){return this.control?this:null}}
    globalThis.Element=Target as unknown as typeof Element;
    let renderer:ReturnType<typeof create>;
    let activations=0;
    try {
        await act(async()=>{renderer=create(React.createElement(DataTable,{rows:rows.slice(0,1),columns,getRowKey:key,onRowClick:()=>activations++}))});
        const row=renderer!.root.findByType("tr");
        const plain=new Target(false),control=new Target(true);
        row.props.onClick({target:control,nativeEvent:{composedPath:()=>[plain]}});
        row.props.onClick({target:plain,nativeEvent:{composedPath:()=>[control,plain]}});
        expect(activations).toBe(0);
        row.props.onClick({target:plain,nativeEvent:{composedPath:()=>[plain]}});
        expect(activations).toBe(1);
    } finally {if(renderer!)await act(async()=>renderer.unmount());globalThis.Element=original}
});

test("inline expansion renders its detail once and keeps large-table work bounded",async()=>{
    let renderer:ReturnType<typeof create>;
    let expanded:string|null=null;
    const detail=(row:{id:string})=>React.createElement("span",{"data-detail":row.id},`Detail ${row.id}`);
    const props={rows,columns,getRowKey:key,renderExpanded:detail,onExpandedChange:(key:string|null)=>{expanded=key}};
    await act(async()=>{renderer=create(React.createElement(DataTable,{...props,expandedKey:expanded}))});
    try {
        const button=renderer!.root.findAllByType("button")[0];
        button.props.onClick({stopPropagation(){}});
        expect(expanded).toBe("0");
        await act(async()=>renderer!.update(React.createElement(DataTable,{...props,expandedKey:expanded})));
        expect(renderer!.root.findAll(node=>node.props["data-detail"]==="0").length).toBe(1);
        expect(renderer!.root.findAllByType("tr").length).toBeLessThanOrEqual(34);
        renderer!.root.findAllByType("button")[0].props.onClick({stopPropagation(){}});
        expect(expanded).toBe(null);
        await act(async()=>renderer!.update(React.createElement(DataTable,{...props,expandedKey:expanded})));
        expect(renderer!.root.findAll(node=>node.props["data-detail"]==="0").length).toBe(0);
    } finally {await act(async()=>renderer!.unmount())}
});

test("all table observers disconnect after unmount",async()=>{
    const original=globalThis.ResizeObserver;
    const observers:{disconnected:boolean}[]=[];
    class Observer {
        disconnected=false;
        constructor(){observers.push(this)}
        observe(){} unobserve(){} disconnect(){this.disconnected=true}
    }
    globalThis.ResizeObserver=Observer as unknown as typeof ResizeObserver;
    const rowNode={getBoundingClientRect:()=>({height:44})};
    const scroller={clientHeight:600,scrollTop:0,scrollHeight:440_000,querySelector:()=>rowNode,querySelectorAll:()=>[]};
    let renderer:ReturnType<typeof create>;
    try {
        await act(async()=>{renderer=create(React.createElement(DataTable,{rows,columns,getRowKey:key}),{createNodeMock:(element)=>element.type==="div"?scroller:rowNode})});
        expect(observers.length).toBeGreaterThanOrEqual(2);
        await act(async()=>renderer!.unmount());
        expect(observers.every(observer=>observer.disconnected)).toBe(true);
    } finally {globalThis.ResizeObserver=original}
});
