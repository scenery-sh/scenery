import { timingSafeEqual } from "node:crypto";
import { once } from "node:events";
import { closeSync, createReadStream, readFileSync } from "node:fs";
import { createServer } from "node:http2";
import { pipeline } from "node:stream/promises";
import { Host, MAX_METADATA, readMetadata } from "./host.mjs";

export { CapabilityError } from "./host.mjs";
export { outcomeCodec } from "./wire.mjs";

export function readLaunch() {
  if (process.argv.length !== 3) throw new Error("expected one worker launch file");
  const launch = JSON.parse(readFileSync(process.argv[2], "utf8"));
  if (launch.identity?.abi !== 1 || typeof launch.token !== "string" || launch.token.length < 32) {
    throw new Error("invalid worker launch");
  }
  return launch;
}

export function startupInputs(parse) {
  const fd = Number(process.env.SCENERY_CONFIG_SNAPSHOT_FD);
  delete process.env.SCENERY_CONFIG_SNAPSHOT_FD;
  if (!Number.isSafeInteger(fd) || fd < 3) throw new Error("missing private worker inputs");
  try {
    const { config, databases } = parse(readFileSync(fd, "utf8"));
    return { config, databases: Object.freeze(databases) };
  } finally {
    closeSync(fd);
  }
}

const streamMarker = Symbol("worker stream");
export function stream(outcome, body, size) {
  if (!(typeof size === "bigint" || Number.isSafeInteger(size)) ||
      size < 0 || BigInt(size) > (1n << 63n) - 1n) {
    throw new RangeError("invalid worker stream size");
  }
  return { outcome, body, size: BigInt(size), [streamMarker]: true };
}

export function unpack(output) {
  return output?.[streamMarker] === true ? output : { outcome: output };
}

const eventStreamMarker = Symbol("worker event stream");
const sseEventMarker = Symbol("worker sse event");
export function sseEvent(data, meta = undefined) {
  return { data, id: meta?.id, event: meta?.event, [sseEventMarker]: true };
}

export function eventStream(outcome, events) {
  return { outcome, events, [eventStreamMarker]: true };
}

export function unpackEvents(output) {
  return output?.[eventStreamMarker] === true ? output : { outcome: output };
}

export function isSSEEvent(value) {
  return value?.[sseEventMarker] === true;
}

export async function serve(launch, parse, dispatch) {
  const host = new Host(launch);
  const server = createServer();
  const sessions = new Set();
  const calls = new Set();
  let draining = false;
  const authorization = Buffer.from("Bearer " + launch.token);
  const attestation = {
    "x-worker-contract": launch.identity.contract_revision,
    "x-worker-implementation": launch.identity.implementation_revision,
  };
  server.on("session", (session) => {
    sessions.add(session);
    session.on("error", () => session.destroy());
    session.once("close", () => sessions.delete(session));
    if (draining) session.close();
  });
  server.on("stream", (request, headers) => {
    request.on("error", () => request.destroy());
    const call = handle(request, headers);
    calls.add(call);
    void call.finally(() => calls.delete(call)).catch(() => request.destroy());
  });

  async function handle(request, headers) {
    const token = Buffer.from(typeof headers.authorization === "string" ? headers.authorization : "");
    if (token.length !== authorization.length || !timingSafeEqual(token, authorization)) {
      request.respond({ ":status": 401 });
      request.end();
      return;
    }
    const respond = (status, data) => {
      request.respond({ ...attestation, ":status": status, "content-type": "application/json" });
      request.end(data);
    };
    const fault = (status) => respond(status, '{"error":"worker request failed"}');
    if (draining) return fault(503);
    switch (headers[":path"]) {
      case "/worker/v1/ready":
        if (headers[":method"] !== "GET") return fault(405);
        return respond(200, JSON.stringify(launch.identity));
      case "/worker/v1/invoke":
        if (headers[":method"] !== "POST") return fault(405);
        break;
      default:
        return fault(404);
    }

    const canceled = new AbortController();
    request.once("close", () => canceled.abort());
    let envelope;
    let input;
    try {
      const raw = await readMetadata(request);
      envelope = JSON.parse(raw);
      const wire = parse(raw);
      input = wire.input;
      if (wire.context?.metadata !== undefined) envelope.context.metadata = wire.context.metadata;
      if (!envelope?.context || typeof envelope.context.scope !== "string" ||
          typeof envelope.operation !== "string" || input === undefined) {
        throw new TypeError("invalid worker invocation");
      }
    } catch {
      if (!request.destroyed) fault(400);
      return;
    }
    if (envelope.service !== launch.identity.service ||
        envelope.context.contract_revision !== launch.identity.contract_revision) return fault(409);
    if (envelope.mode !== "direct" && envelope.mode !== "durable") return fault(400);

    const deadline = Date.parse(envelope.context.deadline);
    const timer = Number.isFinite(deadline) ? setTimeout(
      () => canceled.abort(new DOMException("Deadline exceeded", "TimeoutError")),
      Math.max(0, Math.min(deadline - Date.now(), 2147483647)),
    ) : undefined;
    try {
      if (Number.isFinite(deadline) && deadline <= Date.now()) canceled.abort();
      if (canceled.signal.aborted) return;
      const context = host.context(envelope, canceled.signal, parse);
      const output = await dispatch(envelope.operation, input, context);
      if (canceled.signal.aborted || request.destroyed) return;
      const metadata = Buffer.from('{"outcome":' + output.outcome + '}');
      if (metadata.length > MAX_METADATA) return fault(500);
      if (output.events !== undefined) {
        const prefix = Buffer.alloc(4);
        prefix.writeUInt32BE(metadata.length);
        request.respond({
          ...attestation, ":status": 200,
          "content-type": "application/vnd.worker.events",
        });
        await writeChunk(request, Buffer.concat([prefix, metadata]), canceled.signal);
        for await (const line of output.events) {
          if (canceled.signal.aborted || request.destroyed) return;
          if (typeof line !== "string") throw new TypeError("worker event streams must yield encoded JSON lines");
          await writeChunk(request, Buffer.from(line + "\n"), canceled.signal);
        }
        if (!canceled.signal.aborted && !request.destroyed) request.end();
        return;
      }
      if (output.body === undefined) return respond(200, metadata);
      const prefix = Buffer.alloc(4);
      prefix.writeUInt32BE(metadata.length);
      request.respond({
        ...attestation, ":status": 200,
        "content-type": "application/vnd.worker.stream",
        "x-worker-stream-size": output.size.toString(),
      });
      await pipeline(framed(prefix, metadata, output), request, { signal: canceled.signal });
    } catch {
      if (request.destroyed || canceled.signal.aborted) return;
      if (request.headersSent) request.destroy();
      else fault(500);
    } finally {
      clearTimeout(timer);
    }
  }

  const stop = () => {
    draining = true;
    server.close();
    for (const session of sessions) session.close();
  };
  const parentPipe = Number.isInteger(launch.parent_death_fd) && launch.parent_death_fd >= 3
    ? createReadStream(null, { fd: launch.parent_death_fd, autoClose: true })
    : undefined;
  if (parentPipe) {
    parentPipe.on("end", stop);
    parentPipe.on("error", stop);
    parentPipe.resume();
  }
  process.once("SIGTERM", stop);
  process.once("SIGINT", stop);
  const closed = once(server, "close");
  try {
    server.listen(launch.worker_socket);
    await closed;
    await Promise.allSettled(calls);
  } finally {
    process.off("SIGTERM", stop);
    process.off("SIGINT", stop);
    if (parentPipe) {
      parentPipe.off("end", stop);
      parentPipe.off("error", stop);
      parentPipe.destroy();
    }
    host.close();
  }
}

async function writeChunk(stream, chunk, signal) {
  if (signal.aborted || stream.destroyed) return;
  if (!stream.write(chunk)) {
    await once(stream, "drain", { signal });
  }
}

async function* framed(prefix, metadata, output) {
  yield prefix;
  yield metadata;
  let remaining = output.size;
  for await (const bytes of output.body) {
    if (!(bytes instanceof Uint8Array)) throw new TypeError("worker streams must yield bytes");
    remaining -= BigInt(bytes.byteLength);
    if (remaining < 0n) throw new Error("worker stream exceeds declared size");
    yield bytes;
  }
  if (remaining !== 0n) throw new Error("worker stream ended before declared size");
}
