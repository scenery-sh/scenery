import { once } from "node:events";
import { connect as connectHTTP2 } from "node:http2";
import { connect as connectUnix } from "node:net";
import { pipeline } from "node:stream/promises";

export const MAX_METADATA = 32 << 20;

export async function readMetadata(stream) {
  const chunks = [];
  let size = 0;
  for await (const chunk of stream) {
    size += chunk.length;
    if (size > MAX_METADATA) throw new Error("worker metadata limit exceeded");
    chunks.push(chunk);
  }
  return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(Buffer.concat(chunks, size));
}

export class CapabilityError extends Error {
  constructor(message, delivery, details) {
    super(message);
    this.name = "CapabilityError";
    this.delivery = delivery;
    this.details = details;
  }
}

export function standaloneContext(options = {}) {
  const mode = options.mode ?? "direct";
  const executionId = options.executionId ?? (mode === "durable" ? "standalone" : undefined);
  const invocation = Object.freeze({
    invocation_id: options.invocationId ?? "standalone",
    generation: 1,
    contract_revision: "",
    ...(options.principal !== undefined ? { principal: options.principal } : {}),
    ...(options.tenantId !== undefined ? { tenant_id: options.tenantId } : {}),
    ...(executionId !== undefined ? { execution_id: executionId } : {}),
  });
  const signal = options.signal ?? new AbortController().signal;
  const unavailable = async () => {
    signal.throwIfAborted();
    throw new CapabilityError("host capability unavailable", "not_sent");
  };
  return Object.freeze({
    invocation,
    signal,
    mode,
    binding: options.binding,
    bindingRequest: unavailable,
    eventEmit: unavailable,
    configSnapshot: unavailable,
    connectionToken: unavailable,
    telemetry: unavailable,
    storageRead: unavailable,
    storageHead: unavailable,
    storageWrite: unavailable,
    storageDelete: unavailable,
    storageDeletePrefix: unavailable,
    storageList: unavailable,
  });
}

export class Host {
  #launch;
  #session;
  #connecting;

  constructor(launch) {
    this.#launch = launch;
  }

  async #connect() {
    const socket = connectUnix(this.#launch.host_socket);
    await once(socket, "connect");
    const session = connectHTTP2("http://host", { createConnection: () => socket });
    session.on("error", () => session.destroy());
    this.#session = session;
    return session;
  }

  #connection() {
    if (this.#session && !this.#session.destroyed && !this.#session.closed) return this.#session;
    this.#connecting ??= this.#connect().finally(() => { this.#connecting = undefined; });
    return this.#connecting;
  }

  close() {
    this.#session?.destroy();
  }

  async #request(scope, signal, method, path, expected, body, headers = {}) {
    signal.throwIfAborted();
    let session;
    let request;
    try {
      session = await this.#connection();
      signal.throwIfAborted();
      request = session.request({
        ...headers, ":method": method, ":path": "/host/v1/" + path,
        authorization: "Bearer " + this.#launch.token, "x-worker-scope": scope,
      }, { signal, endStream: body === undefined });
    } catch {
      signal.throwIfAborted();
      throw new CapabilityError("host capability unavailable", "not_sent");
    }
    request.on("error", () => request.destroy());
    const response = once(request, "response", { signal });
    const prematureClose = () => request.emit("error", new Error("host closed capability stream"));
    request.once("close", prematureClose);
    try {
      if (typeof body === "string") request.end(body);
      else if (body !== undefined) {
        // Keep the readable side open for the Host's reply.
        void pipeline(body, request, { signal }).catch(() => request.destroy());
      }
      const [received] = await response;
      if (received[":status"] !== expected) {
        request.off("close", prematureClose);
        const status = received[":status"];
        const details = await readCapabilityError(request, status);
        throw new CapabilityError(
          details.message ?? "host rejected capability",
          status >= 400 && status < 500 ? "not_sent" : "unknown",
          details,
        );
      }
      return { headers: received, body: request };
    } catch (error) {
      signal.throwIfAborted();
      if (error instanceof CapabilityError) throw error;
      throw new CapabilityError("host capability interrupted", "unknown");
    } finally {
      request.off("close", prematureClose);
    }
  }

  context(envelope, signal, parse) {
    const { scope, ...invocationContext } = envelope.context;
    const invocation = Object.freeze(invocationContext);
    const request = (method, path, status, body, headers) =>
      this.#request(scope, signal, method, path, status, body, headers);
    const read = async (...args) => {
      const response = await request(...args);
      try {
        return await readMetadata(response.body);
      } catch {
        signal.throwIfAborted();
        throw new CapabilityError("invalid or interrupted host capability response", "unknown");
      }
    };
    const json = async (...args) => JSON.parse(await read(...args));
    const media = { "content-type": "application/json" };
    const post = (path, value) => json("POST", path, 200, JSON.stringify(value), media);
    const storage = (operation, store, params) => {
      const query = new URLSearchParams({ store });
      for (const [key, value] of Object.entries(params ?? {})) {
        if (value !== undefined && key !== "store") query.set(key, String(value));
      }
      return "storage/" + operation + "?" + query;
    };
    const conditions = (options, allowIfNoneMatch = false) => {
      const headers = {};
      if (options?.ifMatch !== undefined) headers["if-match"] = options.ifMatch;
      if (options?.ifNoneMatch !== undefined && options?.ifNoneMatch !== false) {
        if (!allowIfNoneMatch || (options.ifNoneMatch !== true && options.ifNoneMatch !== "*")) {
          throw new RangeError("invalid storage ifNoneMatch condition");
        }
        headers["if-none-match"] = "*";
      }
      return headers;
    };
    return Object.freeze({
      invocation, signal, mode: envelope.mode, binding: envelope.binding,
      async bindingRequest(client, input, enqueue = false) {
        const payload = '{"client":' + JSON.stringify(client) + ',"input":' + input + '}';
        if (Buffer.byteLength(payload) > MAX_METADATA) {
          throw new CapabilityError("binding metadata exceeds the limit", "not_sent");
        }
        const raw = await read("POST", enqueue ? "durable/enqueue" : "binding/invoke", 200, payload, media);
        let reply;
        try {
          reply = parse(raw);
        } catch {
          throw new CapabilityError("invalid host binding reply", "unknown");
        }
        if (reply?.error && !Object.hasOwn(reply, "output")) {
          const error = JSON.parse(raw).error;
          let delivery;
          for (let detail = error; detail; detail = detail.cause) {
            const value = detail.meta?.delivery ?? detail.outcome;
            if (value === "not_sent" || value === "unknown") {
              delivery = value;
              break;
            }
          }
          throw new CapabilityError(error.message, delivery, error);
        }
        if (!reply || !Object.hasOwn(reply, "output") || Object.keys(reply).length !== 1) {
          throw new CapabilityError("invalid host binding reply", "unknown");
        }
        return reply.output;
      },
      async eventEmit(publisher, payload) {
        const body = '{"publisher":' + JSON.stringify(publisher) + ',"payload":' + payload + '}';
        if (Buffer.byteLength(body) > MAX_METADATA) {
          throw new CapabilityError("event metadata exceeds the limit", "not_sent");
        }
        const raw = await read("POST", "events/emit", 200, body, media);
        let reply;
        try {
          reply = JSON.parse(raw);
        } catch {
          throw new CapabilityError("invalid host event reply", "unknown");
        }
        if (reply?.error && !Object.hasOwn(reply, "output")) {
          throw new CapabilityError(reply.error.message, undefined, reply.error);
        }
        const receipt = reply?.output;
        if (!reply || Object.keys(reply).length !== 1 || typeof receipt?.event_id !== "string" || typeof receipt.event !== "string" ||
            !Number.isInteger(receipt.deliveries) || receipt.deliveries < 0 ||
            receipt.deliveries > 0xffffffff || typeof receipt.deduplicated !== "boolean") {
          throw new CapabilityError("invalid host event receipt", "unknown");
        }
        return receipt;
      },
      configSnapshot: async () => parse(await read("GET", "config/snapshot", 200)),
      async connectionToken(provider, scopes) {
        return (await post("connections/token", { provider, scopes })).token;
      },
      async telemetry(records) {
        if (records.length > 64) throw new RangeError("telemetry batch exceeds 64 records");
        await post("telemetry", records);
      },
      storageRead: (store, key, options) => request("GET", storage("read", store, { key }), 200, undefined, conditions(options)),
      async storageHead(store, key) {
        return (await request("HEAD", storage("read", store, { key }), 200)).headers;
      },
      storageWrite: (store, key, body, options) => json("PUT", storage("write", store, { key }), 201, body, {
        ...conditions(options, true),
        ...(options?.contentType === undefined ? {} : { "content-type": options.contentType }),
      }),
      async storageDelete(store, key, options) {
        await request("DELETE", storage("delete", store, { key }), 204, undefined, conditions(options));
      },
      async storageDeletePrefix(store, prefix = "") {
        await request("DELETE", storage("delete", store, { ...(prefix === "" ? {} : { key: prefix }), recursive: "1" }), 204);
      },
      storageList: (store, options) => json("GET", storage("list", store, options), 200),
    });
  }
}

async function readCapabilityError(stream, status) {
  const details = { status };
  const chunks = [];
  let size = 0;
  try {
    for await (const chunk of stream) {
      size += chunk.length;
      if (size > 64 << 10) {
        stream.destroy();
        return details;
      }
      chunks.push(chunk);
    }
    const parsed = JSON.parse(Buffer.concat(chunks, size).toString("utf8"));
    if (typeof parsed?.code === "string") details.code = parsed.code;
    if (typeof parsed?.message === "string") details.message = parsed.message;
  } catch {
    // Ignore unreadable or non-JSON error bodies and keep HTTP status details.
  }
  return details;
}
