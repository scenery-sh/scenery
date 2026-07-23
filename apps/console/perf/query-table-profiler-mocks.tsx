import { mock } from "bun:test";
import React from "react";
import * as ReactModule from "react";
import * as ReactJSXRuntime from "react/jsx-runtime";

const tokenVars = new Proxy(
	{},
	{ get: (_target, property) => `var(--${String(property)})` },
);

const uiComponents = new URL("../../../ui/components/", import.meta.url).pathname;
function mockFromUI(name: string, factory: () => object) {
	mock.module(name, factory);
	mock.module(Bun.resolveSync(name, uiComponents), factory);
}

mockFromUI("react", () => ReactModule);
mockFromUI("react/jsx-runtime", () => ReactJSXRuntime);
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
	IconButton: () => null,
}));
mockFromUI("@astryxdesign/core/Table", () => ({
	pixel: (value: number) => value,
	proportional: (value: number) => value,
	TableCell: ({ children }: { children?: React.ReactNode }) => (
		<td>{children}</td>
	),
	Table: ({
		columns,
		data,
		idKey,
	}: {
		columns: readonly {
			key: string;
			renderCell?: (row: unknown) => React.ReactNode;
		}[];
		data: readonly unknown[];
		idKey: (row: unknown) => string;
	}) => (
		<table>
			<tbody>
				{data.map((row) => (
					<tr key={idKey(row)}>
						{columns.map((column) => (
							<td key={column.key}>{column.renderCell?.(row)}</td>
						))}
					</tr>
				))}
			</tbody>
		</table>
	),
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
