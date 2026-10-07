import { arch, cpus, hostname, platform } from "node:os";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import reactPackage from "../node_modules/react/package.json" with { type: "json" };
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
type Observation = { readonly mount_ms: number; readonly update_ms: number };
type Measurement = {
	readonly rows: number;
	readonly full: Observation[];
	readonly windowed: Observation[];
};

const samples = 20;
const warmups = 2;
const measurements: Measurement[] = [];
const inputPaths = [
	"ui/components/DataTable.tsx",
	"ui/components/table-window.ts",
	"tools/typescript/perf/query-table-profiler.bench.tsx",
	"tools/typescript/perf/query-table-profiler-mocks.tsx",
	"tools/typescript/package.json",
	"tools/typescript/bun.lock",
	"tools/typescript/tsconfig.runtime.json",
];
async function inputRevision() {
	const hash = new Bun.CryptoHasher("sha256");
	for (const path of inputPaths) {
		hash.update(path);
		hash.update(await Bun.file(path).arrayBuffer());
	}
	return `sha256:${hash.digest("hex")}`;
}
const beforeInput = await inputRevision();
const started = performance.now();
for (const rows of [1_000, 5_000, 10_000]) {
	const result: Measurement = { rows, full: [], windowed: [] };
	for (let sample = -warmups; sample < samples; sample++) {
		const order = sample % 2 === 0
			? ["full", "windowed"] as const
			: ["windowed", "full"] as const;
		for (const mode of order) {
			const observation = await profile(rows, mode === "full" ? Infinity : 200);
			if (observation.mount_ms === undefined || observation.update_ms === undefined) {
				throw new Error("Missing profiler commit");
			}
			if (sample >= 0) {
				result[mode].push({ mount_ms: observation.mount_ms, update_ms: observation.update_ms });
			}
		}
	}
	measurements.push(result);
}
function distribution(values: number[]) {
	const sorted = values.toSorted((a, b) => a - b);
	return {
		count: sorted.length,
		median_ms: sorted[Math.floor(sorted.length / 2)],
		p95_ms: sorted[Math.ceil(sorted.length * 0.95) - 1],
	};
}
const commit = Bun.spawnSync(["git", "rev-parse", "HEAD"]).stdout.toString().trim();
const finalInput = await inputRevision();
if (beforeInput !== finalInput) throw new Error("Profiler workload changed during measurement");
const id = new Date().toISOString().replaceAll(/[-:.]/g, "");
const report = {
	kind: "scenery.query-table.react-profiler",
	run_id: id,
	parent_run_id: null,
	source_commit: commit,
	input_revision: beforeInput,
	final_input_revision: finalInput,
	inputs_stable: true,
	producer: { name: "Scenery query-table profiler", runner: "bun", version: Bun.version },
	context: {
		purpose: "benchmark", runner: "bun", runner_version: Bun.version,
		react_version: reactPackage.version, host: hostname(), os: platform(),
		architecture: arch(), cpus: cpus().length, concurrency: 1,
		cache: "module/dependency warm; new renderer per sample",
	},
	boundary: "React test renderer commits with Astryx/style mocks; excludes browser layout, paint and consuming-application cost",
	sample_count: samples,
	warmups,
	order: "alternating full/windowed per pair",
	wall_ms: performance.now() - started,
	correctness: "complete mount/update observations",
	performance: "observational; no elapsed-time gate",
	measurements: measurements.map(entry => ({
		...entry,
		summary: {
			full: {
				mount: distribution(entry.full.map(x => x.mount_ms)),
				update: distribution(entry.full.map(x => x.update_ms)),
			},
			windowed: {
				mount: distribution(entry.windowed.map(x => x.mount_ms)),
				update: distribution(entry.windowed.map(x => x.update_ms)),
			},
		},
	})),
};
const directory = resolve(".scenery/harness/query-table-profiler/runs", id);
await mkdir(directory, { recursive: true });
await writeFile(resolve(directory, "result.json"), JSON.stringify(report, null, 2) + "\n", { flag: "wx" });
console.log(JSON.stringify({ ...report, artifact: resolve(directory, "result.json") }, null, 2));
