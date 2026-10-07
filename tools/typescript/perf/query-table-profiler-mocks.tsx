import { mock } from "bun:test";
import React from "react";
import * as ReactModule from "react";
import * as ReactJSXRuntime from "react/jsx-runtime";
import * as ReactJSXDevRuntime from "react/jsx-dev-runtime";

const tokenVars = new Proxy(
	{},
	{ get: (_target, property) => `var(--${String(property)})` },
);

const toolingRoot = new URL("../",import.meta.url).pathname;
function mockFromUI(name: string, factory: () => object) {
	mock.module(name, factory);
	const path = Bun.resolveSync(name,toolingRoot);
	mock.module(path,factory);
}

mockFromUI("react", () => ReactModule);
mockFromUI("react/jsx-runtime", () => ReactJSXRuntime);
mockFromUI("react/jsx-dev-runtime", () => ReactJSXDevRuntime);
mockFromUI("@stylexjs/stylex", () => ({
	create: <T,>(styles: T) => styles,
	props: () => ({}),
}));
mockFromUI("@astryxdesign/core/theme/tokens.stylex", () => ({
	borderVars: tokenVars,
	colorVars: tokenVars,
	radiusVars: tokenVars,
	spacingVars: tokenVars,
}));
mockFromUI("@astryxdesign/core/Badge", () => ({
	Badge: ({ label }: { label: React.ReactNode }) => <span>{label}</span>,
}));
mockFromUI("@astryxdesign/core/EmptyState", () => ({
	EmptyState: ({ title }: { title: React.ReactNode }) => <span>{title}</span>,
}));
mockFromUI("@astryxdesign/core/Icon", () => ({
	Icon: () => null,
}));
mockFromUI("@astryxdesign/core/IconButton", () => ({
	IconButton: ({label,onClick}: {label:string;onClick:React.MouseEventHandler<HTMLButtonElement>}) => <button aria-label={label} onClick={onClick}>{label}</button>,
}));
mockFromUI("@astryxdesign/core/Table", () => ({
	pixel: (value: number) => value,
	proportional: (value: number) => value,
	TableCell: ({ children }: { children?: React.ReactNode }) => (
		<td>{children}</td>
	),
    Table: ({columns,data,idKey,plugins}: {
        columns:readonly {key:string;renderCell?:(row:unknown)=>React.ReactNode}[];
        data:readonly unknown[];idKey:(row:unknown)=>string;
        plugins?:{behavior?:{
            transformScrollWrapper?:(props:{htmlProps:React.HTMLAttributes<HTMLDivElement>;xstyle:unknown[]})=>{htmlProps:React.HTMLAttributes<HTMLDivElement>};
            transformBodyRow?:(props:{htmlProps:React.HTMLAttributes<HTMLTableRowElement>;xstyle:unknown[];children:React.ReactNode},row:unknown)=>{htmlProps:React.HTMLAttributes<HTMLTableRowElement>;children:React.ReactNode};
        }};
    }) => {
        const wrapper=plugins?.behavior?.transformScrollWrapper?.({htmlProps:{},xstyle:[]});
        return <div {...wrapper?.htmlProps}><table><tbody>{data.map(row=>{
            const children=columns.map(column=><td key={column.key}>{column.renderCell?.(row)}</td>);
            const props=plugins?.behavior?.transformBodyRow?.({htmlProps:{},xstyle:[],children},row);
            return <tr key={idKey(row)} {...props?.htmlProps}>{props?.children??children}</tr>;
        })}</tbody></table></div>;
    },
	useTableGroupedRows: ({
		data,
		getRowKey,
	}: {
		data: readonly unknown[];
		getRowKey: (row: unknown) => string;
	}) => ({ data, idKey: getRowKey, plugin: {} }),
	useTableRowIndex: () => ({}),
	useTableSortable: () => ({}),
}));
