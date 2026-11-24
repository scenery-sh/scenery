export { outcomeCodec } from "./wire.mjs";

export interface Invocation {
  readonly invocation_id: string;
  readonly generation: number;
  readonly contract_revision: string;
  readonly principal?: string;
  readonly tenant_id?: string;
  readonly traceparent?: string;
  readonly deadline?: string;
  readonly locale?: string;
  readonly execution_id?: string;
  readonly metadata?: Readonly<Record<string, unknown>>;
}

export interface ConfigSnapshot {
  readonly revision: string;
  readonly values: Readonly<Record<string, unknown>>;
  /** Base64-encoded secret bytes in the deployment snapshot. */
  readonly secrets: Readonly<Record<string, string>>;
}

export interface StorageConditions {
  readonly ifMatch?: string;
  readonly ifNoneMatch?: string;
}
export type StreamBody = Iterable<Uint8Array> | AsyncIterable<Uint8Array>;
export type Headers = Readonly<Record<string, string | number | readonly string[] | undefined>>;

export interface TelemetryRecord {
  readonly kind: "span" | "log";
  readonly name: string;
  readonly message?: string;
  readonly elapsed_ns?: number;
  readonly attributes?: Readonly<Record<string, unknown>>;
}

export interface Context {
  readonly invocation: Invocation;
  readonly signal: AbortSignal;
  readonly mode: "direct" | "durable";
  readonly binding?: string;
  bindingRequest(client: string, input: string, enqueue?: boolean): Promise<unknown>;
  configSnapshot(): Promise<ConfigSnapshot>;
  connectionToken(provider: string, scopes: readonly string[]): Promise<string>;
  telemetry(records: readonly TelemetryRecord[]): Promise<void>;
  storageRead(store: string, key: string, options?: StorageConditions): Promise<{ headers: Headers; body: AsyncIterable<Uint8Array> }>;
  storageHead(store: string, key: string): Promise<Headers>;
  storageWrite(store: string, key: string, body: StreamBody, options?: StorageConditions & { contentType?: string }): Promise<unknown>;
  storageDelete(store: string, key: string, options?: StorageConditions): Promise<void>;
  storageList(store: string, options?: { prefix?: string; cursor?: string; limit?: number }): Promise<unknown>;
}

export class CapabilityError extends Error {
  readonly delivery: "not_sent" | "unknown" | undefined;
  readonly details: unknown;
}

declare const streamMarker: unique symbol;
export interface Stream<T> {
  readonly [streamMarker]: true;
  readonly outcome: T;
  readonly body: StreamBody;
  readonly size: bigint;
}
export function stream<T>(outcome: T, body: StreamBody, size: bigint | number): Stream<T>;
export function unpack<T>(output: T | Stream<T>): { outcome: T; body?: StreamBody; size?: bigint };

interface Launch {
  readonly worker_socket: string;
  readonly host_socket: string;
  readonly token: string;
  readonly identity: { abi: 1; service: string; contract_revision: string; implementation_revision: string };
}
export function readLaunch(): Launch;
export function startupInputs(parse: (source: string) => unknown): { config: ConfigSnapshot | undefined; databases: Readonly<Record<string, string>> };
export function serve(
  launch: Launch,
  parse: (source: string) => unknown,
  dispatch: (operation: string, input: unknown, context: Context) => Promise<{ outcome: string; body?: StreamBody; size?: bigint }>,
): Promise<void>;
