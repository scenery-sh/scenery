import { afterEach, beforeEach, describe, expect, jest, test } from "bun:test";
import { createServer, type AddressInfo } from "node:net";

import {
	DEV_RUNTIME_MAX_REQUEST_BYTES,
	DevRuntimeClient,
	DevRuntimeError,
	storageTarget,
	type DevRuntimeClientOptions,
} from "../../compiler/testdata/house/clients/generated/public_api/dev-runtime.ts";

interface SentFrame {
	readonly id: number;
	readonly method: string;
}

// The generated client reaches the runtime only through the WebSocket and
// fetch globals, so these fakes observe every frame that would be transmitted.
class FakeSocket {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSING = 2;
	static readonly CLOSED = 3;
	static created: FakeSocket[] = [];

	readyState = FakeSocket.CONNECTING;
	readonly sent: SentFrame[] = [];
	/** UTF-8 size of each sent frame, as a WebSocket transmits it. */
	readonly sentBytes: number[] = [];
	readonly #listeners = new Map<string, ((event: unknown) => void)[]>();

	constructor(readonly url: string) {
		FakeSocket.created.push(this);
	}

	addEventListener(type: string, listener: (event: unknown) => void): void {
		this.#listeners.set(type, [...(this.#listeners.get(type) ?? []), listener]);
	}

	send(frame: string): void {
		if (this.readyState !== FakeSocket.OPEN) throw new Error("send on a socket that is not open");
		this.sent.push(JSON.parse(frame) as SentFrame);
		this.sentBytes.push(new TextEncoder().encode(frame).byteLength);
	}

	close(): void {
		// Like a browser, the close event arrives later; tests deliver it with drop().
		if (this.readyState < FakeSocket.CLOSING) this.readyState = FakeSocket.CLOSING;
	}

	open(): void {
		this.readyState = FakeSocket.OPEN;
		this.#dispatch("open", {});
	}

	receive(data: unknown): void {
		this.#dispatch("message", { data });
	}

	reply(frame: SentFrame | undefined, result: unknown): void {
		this.receive(JSON.stringify({ jsonrpc: "2.0", id: frame?.id, result }));
	}

	drop(): void {
		this.readyState = FakeSocket.CLOSED;
		this.#dispatch("close", {});
	}

	#dispatch(type: string, event: unknown): void {
		for (const listener of this.#listeners.get(type) ?? []) listener(event);
	}
}

const realWebSocket = globalThis.WebSocket;
const realFetch = globalThis.fetch;
const deleteNotes = { query: "DELETE FROM notes" };
const target = storageTarget("app", { app_id: "app", app_root: "/app", worktree_key: "wt", incarnation: "i1", generation: "g1" }, "files");

beforeEach(() => {
	FakeSocket.created = [];
	globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
});

afterEach(() => {
	jest.useRealTimers();
	globalThis.WebSocket = realWebSocket;
	globalThis.fetch = realFetch;
});

function runtimeClient(options: DevRuntimeClientOptions = {}): DevRuntimeClient {
	return new DevRuntimeClient({ url: "ws://runtime.test/runtime", storageUrl: "http://runtime.test/runtime/storage", ...options });
}

function socket(index: number): FakeSocket {
	const created = FakeSocket.created[index];
	if (!created) throw new Error(`socket ${index} was not created`);
	return created;
}

function sentMethods(): string[] {
	return FakeSocket.created.flatMap((created) => created.sent.map((frame) => frame.method));
}

function stubFetch(answer: (init: RequestInit) => Promise<Response>): RequestInit[] {
	const requests: RequestInit[] = [];
	globalThis.fetch = ((_: unknown, init: RequestInit) => {
		requests.push(init);
		return answer(init);
	}) as unknown as typeof fetch;
	return requests;
}

/** Attach at once so a rejection is never unhandled, then read its error. */
async function failure(promise: Promise<unknown>): Promise<DevRuntimeError> {
	try {
		await promise;
	} catch (error) {
		expect(error).toBeInstanceOf(DevRuntimeError);
		return error as DevRuntimeError;
	}
	throw new Error("the call succeeded");
}

describe("DevRuntimeClient connection lifecycle", () => {
	test("close drops queued calls, so a later connection never sends them", async () => {
		const client = runtimeClient();
		const deleted = failure(client.query("app", deleteNotes));
		client.close();
		expect((await deleted).code).toBe("closed");

		const tables = client.postgresTables("app");
		// A replaced socket that opens anyway is closed, never used.
		socket(0).open();
		expect(socket(0).readyState).toBe(FakeSocket.CLOSING);
		socket(1).open();
		socket(1).reply(socket(1).sent[0], []);
		expect(await tables).toEqual([]);
		expect(sentMethods()).toEqual(["postgres/tables"]);
	});

	test("dispose during the reconnect delay never reconnects or sends", async () => {
		jest.useFakeTimers();
		const client = runtimeClient({ reconnectDelayMs: 1000 });
		const first = failure(client.status());
		socket(0).open();
		socket(0).drop();
		expect((await first).code).toBe("closed");

		const deleted = failure(client.query("app", deleteNotes));
		expect(jest.getTimerCount()).toBe(1);
		client.dispose();
		expect((await deleted).code).toBe("closed");
		expect(jest.getTimerCount()).toBe(0);
		jest.advanceTimersByTime(5000);
		FakeSocket.created[1]?.open();
		expect(FakeSocket.created).toHaveLength(1);
		expect(sentMethods()).toEqual(["status"]);
		expect((await failure(client.status())).code).toBe("closed");
	});

	test("calls queued during the reconnect delay share one timer and keep their order", async () => {
		jest.useFakeTimers();
		const client = runtimeClient({ reconnectDelayMs: 1000 });
		const refused = failure(client.status());
		socket(0).drop();
		expect((await refused).code).toBe("closed");

		const tables = client.postgresTables("app");
		const columns = client.postgresSchema("app", { table: "notes" });
		const rows = client.postgresRows("app", { table: "notes" });
		expect(jest.getTimerCount()).toBe(1);
		jest.advanceTimersByTime(1000);
		expect(FakeSocket.created).toHaveLength(2);
		socket(1).open();
		expect(socket(1).sent.map((frame) => frame.method)).toEqual(["postgres/tables", "postgres/schema", "postgres/rows"]);
		const [tablesFrame, columnsFrame, rowsFrame] = socket(1).sent;
		socket(1).reply(rowsFrame, { columns: [], rows: [], limit: 100, offset: 0 });
		socket(1).reply(columnsFrame, []);
		socket(1).reply(tablesFrame, []);
		expect(await Promise.all([tables, columns, rows])).toEqual([[], [], { columns: [], rows: [], limit: 100, offset: 0 }]);
	});

	test("events of a replaced socket are ignored and explicit close reports disconnection", async () => {
		const client = runtimeClient();
		const changes: boolean[] = [];
		client.onConnectionChange((connected) => changes.push(connected));
		const first = failure(client.status());
		socket(0).open();
		client.close();
		expect((await first).code).toBe("closed");
		expect(changes).toEqual([true, false]);

		const tables = client.postgresTables("app");
		socket(0).reply(socket(0).sent[0], {});
		socket(0).drop();
		socket(1).open();
		socket(1).reply(socket(1).sent[0], []);
		expect(await tables).toEqual([]);
		expect(changes).toEqual([true, false, true]);
		expect(client.connected).toBe(true);
	});

	test("a throwing connection listener cannot stall sending or rejection", async () => {
		const reported: unknown[] = [];
		const realReportError = globalThis.reportError;
		globalThis.reportError = (error: unknown) => reported.push(error);
		try {
			const client = runtimeClient();
			const heard: boolean[] = [];
			client.onConnectionChange(() => {
				throw new Error("listener failed");
			});
			client.onConnectionChange((connected) => heard.push(connected));
			const tables = failure(client.postgresTables("app"));
			socket(0).open();
			expect(sentMethods()).toEqual(["postgres/tables"]);
			socket(0).drop();
			expect((await tables).code).toBe("closed");
			expect(heard).toEqual([true, false]);
			expect(reported).toHaveLength(2);
		} finally {
			globalThis.reportError = realReportError;
		}
	});

	test("answers settle their own calls in any order", async () => {
		const client = runtimeClient();
		const tables = client.postgresTables("app");
		const columns = client.postgresSchema("app", { table: "notes" });
		socket(0).open();
		const [tablesFrame, columnsFrame] = socket(0).sent;
		socket(0).reply(columnsFrame, [{ name: "id", type: "uuid", not_null: true, primary_key: true }]);
		socket(0).reply(tablesFrame, [{ schema: "scenery", name: "scenery.notes", type: "table" }]);
		expect(await columns).toEqual([{ name: "id", type: "uuid", not_null: true, primary_key: true }]);
		expect(await tables).toEqual([{ schema: "scenery", name: "scenery.notes", type: "table" }]);
	});

	test("a socket that cannot be created fails its calls and keeps none queued", async () => {
		globalThis.WebSocket = class {
			constructor() {
				throw new DOMException("blocked port", "SecurityError");
			}
		} as unknown as typeof WebSocket;
		const client = runtimeClient({ reconnectDelayMs: 0 });
		const refused = await failure(client.query("app", deleteNotes));
		expect(refused.code).toBe("unavailable");
		expect(refused.cause).toBeInstanceOf(DOMException);

		globalThis.WebSocket = FakeSocket as unknown as typeof WebSocket;
		const tables = client.postgresTables("app");
		socket(0).open();
		socket(0).reply(socket(0).sent[0], []);
		expect(await tables).toEqual([]);
		expect(sentMethods()).toEqual(["postgres/tables"]);
	});

	test("a malformed frame fails the connection's calls as a protocol error", async () => {
		for (const frame of ["not json", JSON.stringify({ jsonrpc: "2.0", result: [] }), JSON.stringify({ jsonrpc: "2.0", id: 1, error: "boom" })]) {
			FakeSocket.created = [];
			const client = runtimeClient({ reconnectDelayMs: 0 });
			const tables = failure(client.postgresTables("app"));
			socket(0).open();
			socket(0).receive(frame);
			expect((await tables).code).toBe("protocol");
			expect(socket(0).readyState).toBe(FakeSocket.CLOSING);
			void client.postgresTables("app").catch(() => undefined);
			expect(FakeSocket.created).toHaveLength(2);
			client.dispose();
		}
	});
});

describe("DevRuntimeClient cancellation", () => {
	test("a pre-aborted call neither connects nor sends", async () => {
		const client = runtimeClient();
		expect((await failure(client.query("app", deleteNotes, AbortSignal.abort()))).code).toBe("aborted");
		expect(FakeSocket.created).toHaveLength(0);
	});

	test("aborting a queued call drops it unsent and keeps the other calls", async () => {
		const client = runtimeClient();
		const controller = new AbortController();
		const deleted = failure(client.query("app", deleteNotes, controller.signal));
		const tables = client.postgresTables("app");
		controller.abort();
		expect((await deleted).code).toBe("aborted");
		socket(0).open();
		expect(sentMethods()).toEqual(["postgres/tables"]);
		socket(0).reply(socket(0).sent[0], []);
		expect(await tables).toEqual([]);
	});

	test("aborting a sent call ignores its late answer", async () => {
		const client = runtimeClient();
		const warmup = client.postgresTables("app");
		socket(0).open();
		socket(0).reply(socket(0).sent[0], []);
		await warmup;
		const controller = new AbortController();
		const query = failure(client.query("app", deleteNotes, controller.signal));
		controller.abort();
		expect((await query).code).toBe("aborted");
		// The runtime may still have run the sent statement; its answer is ignored.
		socket(0).reply(socket(0).sent[1], { columns: [], rows: [] });
		const tables = client.postgresTables("app");
		socket(0).reply(socket(0).sent[2], []);
		expect(await tables).toEqual([]);
		expect(sentMethods()).toEqual(["postgres/tables", "db/query", "postgres/tables"]);
	});

	test("aborting the only call queued during the reconnect delay cancels the reconnect", async () => {
		jest.useFakeTimers();
		const client = runtimeClient({ reconnectDelayMs: 1000 });
		const refused = failure(client.status());
		socket(0).drop();
		expect((await refused).code).toBe("closed");

		const controller = new AbortController();
		const deleted = failure(client.query("app", deleteNotes, controller.signal));
		expect(jest.getTimerCount()).toBe(1);
		controller.abort();
		expect((await deleted).code).toBe("aborted");
		expect(jest.getTimerCount()).toBe(0);
		jest.advanceTimersByTime(5000);
		expect(FakeSocket.created).toHaveLength(1);
	});
});

/**
 * A statement whose db/query request with this id is exactly `bytes` long in
 * UTF-8. "é" takes two UTF-8 bytes but one UTF-16 code unit, so the frame's
 * string length stays far below the byte size the runtime counts.
 */
function statementForRequestBytes(id: number, bytes: number): string {
	const frame = JSON.stringify({ jsonrpc: "2.0", id, method: "db/query", params: { app_id: "app", query: "", params: [] } });
	const room = bytes - new TextEncoder().encode(frame).byteLength;
	return "é".repeat(Math.floor(room / 2)) + "x".repeat(room % 2);
}

describe("DevRuntimeClient request limit", () => {
	test("a call over the limit fails unsent while the connection keeps its other calls", async () => {
		const client = runtimeClient();
		const tables = client.postgresTables("app");
		socket(0).open();
		const atLimit = client.query("app", { query: statementForRequestBytes(2, DEV_RUNTIME_MAX_REQUEST_BYTES) });
		const oversized = failure(client.query("app", { query: statementForRequestBytes(3, DEV_RUNTIME_MAX_REQUEST_BYTES + 1) }));
		// An open socket sends at once, so the request of exactly the limit is
		// already out and the larger one never goes.
		expect(sentMethods()).toEqual(["postgres/tables", "db/query"]);
		expect(socket(0).sentBytes[1]).toBe(DEV_RUNTIME_MAX_REQUEST_BYTES);

		const refused = await oversized;
		expect(refused.code).toBe("request_too_large");
		expect(refused.message).toBe(`db/query request is ${DEV_RUNTIME_MAX_REQUEST_BYTES + 1} bytes, over the development runtime's 1 MiB request limit; it was not sent`);
		expect(refused.details).toEqual({ max_bytes: DEV_RUNTIME_MAX_REQUEST_BYTES, request_bytes: DEV_RUNTIME_MAX_REQUEST_BYTES + 1 });
		socket(0).reply(socket(0).sent[1], { columns: ["length"], rows: [[DEV_RUNTIME_MAX_REQUEST_BYTES]] });
		socket(0).reply(socket(0).sent[0], []);
		expect(await atLimit).toEqual({ columns: ["length"], rows: [[DEV_RUNTIME_MAX_REQUEST_BYTES]] });
		expect(await tables).toEqual([]);
		expect(client.connected).toBe(true);
	});

	test("a call over the limit neither connects nor sends", async () => {
		const client = runtimeClient();
		const oversized = failure(client.query("app", { query: "x".repeat(DEV_RUNTIME_MAX_REQUEST_BYTES) }));
		expect(FakeSocket.created).toHaveLength(0);
		expect((await oversized).code).toBe("request_too_large");
	});
});

describe("DevRuntimeClient storage transfers", () => {
	test("a disposed client refuses calls and transfers alike without contacting the runtime", async () => {
		const requests = stubFetch(async () => Response.json({ data: { object: { key: "a.txt" } } }));
		const client = runtimeClient();
		client.dispose();
		for (const attempt of [
			client.status(),
			client.uploadStorageObject(target, "a.txt", new Blob(["x"])),
			client.downloadStorageObject(target, { key: "a.txt", etag: "e1" }),
		]) {
			expect((await failure(attempt)).code).toBe("closed");
		}
		expect(requests).toHaveLength(0);
		expect(FakeSocket.created).toHaveLength(0);
	});

	test("dispose cancels an in-flight transfer", async () => {
		const requests = stubFetch(
			(init) =>
				new Promise((_, reject) => {
					init.signal?.addEventListener("abort", () => reject(init.signal?.reason));
				}),
		);
		const client = runtimeClient();
		const upload = failure(client.uploadStorageObject(target, "a.txt", new Blob(["x"])));
		await Promise.resolve();
		client.dispose();
		expect((await upload).code).toBe("closed");
		expect(requests).toHaveLength(1);
		expect(requests[0]?.signal?.aborted).toBe(true);
	});

	test("an unreadable successful upload answer is a protocol error", async () => {
		const client = runtimeClient();
		stubFetch(async () => new Response("not json", { status: 200 }));
		const invalid = await failure(client.uploadStorageObject(target, "a.txt", new Blob(["x"])));
		expect(invalid.code).toBe("protocol");
		expect(invalid.cause).toBeInstanceOf(SyntaxError);
		stubFetch(async () => Response.json({ data: {} }));
		expect((await failure(client.uploadStorageObject(target, "a.txt", new Blob(["x"])))).code).toBe("protocol");
	});

	test("a runtime transfer failure keeps its diagnostic and report token", async () => {
		const client = runtimeClient();
		stubFetch(async () => Response.json({ code: "precondition_failed", diagnostic: "SCN8201", message: "object changed", report_token: "r1" }, { status: 412 }));
		const changed = await failure(client.downloadStorageObject(target, { key: "a.txt", etag: "e1" }));
		expect([changed.code, changed.diagnostic, changed.reportToken, changed.status, changed.message]).toEqual(["transfer", "SCN8201", "r1", 412, "SCN8201: object changed"]);
	});

	test("cancellation before or during the response body rejects as aborted", async () => {
		const server = Bun.serve({
			port: 0,
			fetch(request) {
				if (new URL(request.url).searchParams.get("key") === "no-headers") return new Promise<Response>(() => undefined);
				// Headers and one chunk, then a body that never ends.
				return new Response(new ReadableStream({ start: (body) => body.enqueue(new Uint8Array([1, 2, 3])) }));
			},
		});
		try {
			const client = runtimeClient({ storageUrl: `http://127.0.0.1:${server.port}/runtime/storage` });
			const beforeHeaders = new AbortController();
			const waiting = failure(client.downloadStorageObject(target, { key: "no-headers", etag: "e1" }, beforeHeaders.signal));
			setTimeout(() => beforeHeaders.abort(), 20);
			expect((await waiting).code).toBe("aborted");

			let headersArrived!: () => void;
			const arrived = new Promise<void>((resolve) => {
				headersArrived = resolve;
			});
			globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
				const response = await realFetch(input, init);
				headersArrived();
				return response;
			}) as unknown as typeof fetch;
			const duringBody = new AbortController();
			const streaming = failure(client.downloadStorageObject(target, { key: "streaming", etag: "e1" }, duringBody.signal));
			await arrived;
			duringBody.abort();
			expect((await streaming).code).toBe("aborted");
		} finally {
			await server.stop(true);
		}
	});

	test("a body cut short rejects as unavailable", async () => {
		let cut = () => {};
		const server = createServer((connection) => {
			connection.once("data", () => {
				connection.write("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nContent-Length: 1024\r\n\r\npartial");
				cut = () => connection.end();
			});
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		try {
			const { port } = server.address() as AddressInfo;
			const client = runtimeClient({ storageUrl: `http://127.0.0.1:${port}/runtime/storage` });
			// End the connection only once the client holds the response headers.
			globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
				const response = await realFetch(input, init);
				cut();
				return response;
			}) as unknown as typeof fetch;
			const interrupted = await failure(client.downloadStorageObject(target, { key: "a.txt", etag: "e1" }));
			expect(interrupted.code).toBe("unavailable");
			expect(interrupted.message).toStartWith("storage transfer interrupted");
		} finally {
			await new Promise<void>((resolve) => server.close(() => resolve()));
		}
	});
});

describe("DevRuntimeClient runtime failures", () => {
	test("a refused call rejects alone with the runtime's failure object and the connection keeps serving", async () => {
		const client = runtimeClient();
		const refused = failure(client.query("app", { query: "select pg_sleep(20)" }));
		const tooLarge = failure(client.postgresRows("app", { table: "scenery.events", limit: 500 }));
		const tables = client.postgresTables("app");
		socket(0).open();
		const [queryFrame, rowsFrame, tablesFrame] = socket(0).sent;
		expect(sentMethods()).toEqual(["db/query", "postgres/rows", "postgres/tables"]);

		const capacity = {
			code: "capacity_exhausted",
			diagnostic: "SCN8011",
			message: "the development runtime is already running 6 database or storage calls on this connection; retry after one completes",
			details: { class: "work", scope: "connection", limit: 6 },
		};
		socket(0).receive(JSON.stringify({ jsonrpc: "2.0", id: queryFrame?.id, error: { code: -32000, message: `SCN8011: ${capacity.message}`, data: capacity } }));
		const budget = {
			code: "result_too_large",
			diagnostic: "SCN8013",
			message: "the result exceeds 500 rows or 4 MiB; request fewer rows",
			details: { max_rows: 500, max_bytes: 4194304, rows_within_budget: 40 },
		};
		socket(0).receive(JSON.stringify({ jsonrpc: "2.0", id: rowsFrame?.id, error: { code: -32000, message: `SCN8013: ${budget.message}`, data: budget } }));
		socket(0).reply(tablesFrame, [{ schema: "scenery", name: "scenery.events", type: "table" }]);

		expect(await refused).toMatchObject({ code: "rpc", diagnostic: "SCN8011", message: `SCN8011: ${capacity.message}`, details: capacity.details });
		expect(await tooLarge).toMatchObject({ code: "rpc", diagnostic: "SCN8013", details: { rows_within_budget: 40 } });
		expect(await tables).toEqual([{ schema: "scenery", name: "scenery.events", type: "table" }]);
		expect(client.connected).toBe(true);
		client.dispose();
	});
});
