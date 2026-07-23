import React, { Profiler, type ProfilerOnRenderCallback } from "react";
import {
	act,
	create,
	type ReactTestRenderer,
} from "react-test-renderer";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean })
	.IS_REACT_ACT_ENVIRONMENT = true;

const { DataTable } = await import("../../../ui/components/DataTable.js");

type Row = { readonly id: string; readonly value: string };

const columns = [
	{ key: "id", header: "ID", render: (row: Row) => row.id },
	{ key: "value", header: "Value", render: (row: Row) => row.value },
] as const;
const getRowKey = (row: Row) => row.id;

type Commit = {
	readonly phase: "mount" | "update" | "nested-update";
	readonly actualDuration: number;
	readonly baseDuration: number;
};

async function profile(rowCount: number, windowThreshold: number) {
	const rows = Array.from({ length: rowCount }, (_, index) => ({
		id: `row-${index}`,
		value: `Value ${index}`,
	}));
	const commits: Commit[] = [];
	const onRender: ProfilerOnRenderCallback = (
		_id,
		phase,
		actualDuration,
		baseDuration,
	) => {
		commits.push({ phase, actualDuration, baseDuration });
	};
	let renderer: ReactTestRenderer | undefined;
	await act(async () => {
		renderer = create(
			<Profiler id="DataTable" onRender={onRender}>
				<DataTable
					columns={columns}
					getRowKey={getRowKey}
					rows={rows}
					selectedKey={null}
					windowThreshold={windowThreshold}
				/>
			</Profiler>,
		);
	});
	await act(async () => {
		renderer?.update(
			<Profiler id="DataTable" onRender={onRender}>
				<DataTable
					columns={columns}
					getRowKey={getRowKey}
					rows={rows}
					selectedKey={`row-${Math.floor(rowCount / 2)}`}
					windowThreshold={windowThreshold}
				/>
			</Profiler>,
		);
	});
	await act(async () => renderer?.unmount());
	return {
		mount_ms: commits.find((commit) => commit.phase === "mount")
			?.actualDuration,
		update_ms: commits.find((commit) => commit.phase === "update")
			?.actualDuration,
	};
}

const measurements = [];
for (const rows of [1_000, 5_000, 10_000]) {
	measurements.push({
		rows,
		full: await profile(rows, Number.POSITIVE_INFINITY),
		windowed: await profile(rows, 200),
	});
}
console.log(JSON.stringify({ kind: "scenery.query-table.react-profiler", measurements }, null, 2));
