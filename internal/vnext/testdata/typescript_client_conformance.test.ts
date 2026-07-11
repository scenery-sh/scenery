import { describe, expect, test } from "bun:test";

import { PublicApiClient } from "./native/clients/generated/public_api/client.ts";
import type { URLString } from "./native/clients/generated/public_api/types.ts";

import {
	appendHeader,
	appendQuery,
	decodeResponseCookie,
	decodeResponseHeader,
	decodeResponseBody,
  encodeJSON,
	encodeMultipartRequestBody,
  encodeTypedJSON,
	jsonNumber,
	mergeResponseValue,
  parseExactJSON,
  SceneryClientError,
  type TypeDescriptor,
  type TypeRegistry,
} from "./house/clients/generated/public_api/runtime.ts";

const registry: TypeRegistry = Object.freeze({
  "test/record/value": {
    kind: "record",
    fields: [
      { property: "count", wire: "count", value: { kind: "primitive", name: "int64" }, optional: false },
      {
        property: "note",
        wire: "note",
        value: { kind: "nullable", value: { kind: "primitive", name: "string" } },
        optional: true,
        constraints: { min_length: 2, max_length: 8, pattern: "^[a-z]+$" },
      },
    ],
    preserveUnknown: false,
    validations: [
      {
        name: "positive_count",
        source: "value.count <= 0",
        expression: {
          kind: "binary",
          operator: "<=",
          left: { kind: "attribute", source: { kind: "value" }, name: "count" },
          right: { kind: "literal", type: "number", value: "0" },
        },
        code: "TEST_NON_POSITIVE_COUNT",
        message: "count must be positive",
        path: "record.value.count",
      },
    ],
  },
});

const valueDescriptor: TypeDescriptor = { kind: "named", name: "test/record/value" };

describe("Scenery TypeScript client exact codecs", () => {
  test("round-trips arbitrary precision JSON numbers without Number", () => {
    const parsed = parseExactJSON('{"large":9007199254740993123.4500,"tiny":1e-100}');
    expect(encodeJSON(parsed)).toBe('{"large":9007199254740993123.45,"tiny":0.' + "0".repeat(99) + "1}");
    expect(encodeJSON(jsonNumber("1234500", 4))).toBe("123.45");
  });

  test("rejects duplicate and prototype-polluting members", () => {
    for (const source of ['{"a":1,"a":2}', '{"__proto__":1}']) {
      expect(() => parseExactJSON(source)).toThrow(SceneryClientError);
    }
  });

	test("preserves optional, nullable, and lossless bigint forms", () => {
    expect(encodeTypedJSON({ count: 9223372036854775807n }, valueDescriptor, registry)).toBe(
      '{"count":"9223372036854775807"}',
    );
    expect(encodeTypedJSON({ count: 1n, note: null }, valueDescriptor, registry)).toBe('{"count":"1","note":null}');
    expect(() => encodeTypedJSON({ count: 1n, note: undefined, extra: true }, valueDescriptor, registry)).toThrow(
      SceneryClientError,
    );
    expect(() => encodeTypedJSON({ count: 1n, note: "X" }, valueDescriptor, registry)).toThrow(SceneryClientError);
    expect(() => encodeTypedJSON({ count: 0n }, valueDescriptor, registry)).toThrow(
      expect.objectContaining({ code: "TEST_NON_POSITIVE_COUNT", message: "record.value.count: count must be positive" }),
    );
  });

  test("orders sets by canonical JSON UTF-8 bytes rather than UTF-16 code units", async () => {
    const descriptor: TypeDescriptor = { kind: "set", value: { kind: "primitive", name: "string" } };
    expect(encodeTypedJSON(["\u{10000}", "\uE000"], descriptor, registry)).toBe('["\uE000","\u{10000}"]');
    await expect(
      decodeResponseBody(
        new Response('["\uE000","\u{10000}"]', { headers: { "content-type": "application/json" } }),
        "json",
        ["application/json"],
        descriptor,
        registry,
        "test/binding/set",
        1024,
      ),
    ).resolves.toEqual(["\uE000", "\u{10000}"]);
    await expect(
      decodeResponseBody(
        new Response('["\u{10000}","\uE000"]', { headers: { "content-type": "application/json" } }),
        "json",
        ["application/json"],
        descriptor,
        registry,
        "test/binding/set",
        1024,
      ),
    ).rejects.toMatchObject({ code: "contract_violation" });
  });

	test("orders and duplicate-checks set query and header values", () => {
		const descriptor: TypeDescriptor = { kind: "set", value: { kind: "primitive", name: "string" } };
		const query: string[] = [];
		appendQuery(query, "tag", ["b", "a"], "repeated", descriptor, registry);
		expect(query).toEqual(["tag=a", "tag=b"]);
		expect(() => appendHeader(new Headers(), "x-tag", ["b", "a"], "repeated", descriptor, registry)).toThrow(
			expect.objectContaining({ code: "unsupported_runtime" }),
		);
		expect(() => appendQuery([], "tag", ["a", "a"], "repeated", descriptor, registry)).toThrow(
			expect.objectContaining({ code: "invalid_input" }),
		);
	});

	test("encodes declared multipart parts with exact metadata and limits", () => {
		const fileDescriptor: TypeDescriptor = {
			kind: "record",
			fields: [
				{ property: "bytes", wire: "bytes", value: { kind: "primitive", name: "bytes" }, optional: false },
				{ property: "filename", wire: "filename", value: { kind: "primitive", name: "string" }, optional: false },
				{ property: "mediaType", wire: "media_type", value: { kind: "primitive", name: "string" }, optional: false },
			],
			preserveUnknown: false,
		};
		const bodyValue: TypeDescriptor = {
			kind: "record",
			fields: [
				{ property: "note", wire: "note", value: { kind: "primitive", name: "string" }, optional: false },
				{ property: "payload", wire: "payload", value: { kind: "primitive", name: "bytes" }, optional: false },
				{ property: "upload", wire: "upload", value: fileDescriptor, optional: false },
			],
			preserveUnknown: false,
		};
		const descriptor = {
			value: bodyValue,
			parts: [
				{ name: "note-field", property: "note", kind: "text", mediaTypes: [], maxBytes: 8, multiple: false, optional: false, retainFilename: false, value: { kind: "primitive", name: "string" } },
				{ name: "raw-bytes", property: "payload", kind: "bytes", mediaTypes: [], maxBytes: 4, multiple: false, optional: false, retainFilename: false, value: { kind: "primitive", name: "bytes" } },
				{ name: "asset", property: "upload", kind: "file", mediaTypes: ["image/png"], maxBytes: 4, multiple: false, optional: false, retainFilename: true, value: fileDescriptor, fileProperties: { bytes: "bytes", filename: "filename", mediaType: "mediaType" } },
			],
			maxParts: 3,
			maxBytes: 1024,
		} as const;
		const encoded = encodeMultipartRequestBody(
			{ note: "ok", payload: new Uint8Array([0, 1]), upload: { bytes: new Uint8Array([2, 3]), filename: "roof.png", mediaType: "image/png" } },
			descriptor,
			registry,
		);
		expect(encoded.contentType).toStartWith("multipart/form-data; boundary=scenery-vnext-boundary");
		const source = new TextDecoder().decode(encoded.body);
		expect(source).toContain('name="note-field"');
		expect(source).toContain('name="raw-bytes"\r\nContent-Type: application/octet-stream');
		expect(source).toContain('name="asset"; filename="roof.png"\r\nContent-Type: image/png');
		expect(() => encodeMultipartRequestBody(
			{ note: "ok", payload: new Uint8Array([0, 1]), upload: { bytes: new Uint8Array(5), filename: "roof.png", mediaType: "image/png" } },
			descriptor,
			registry,
		)).toThrow(expect.objectContaining({ code: "invalid_input" }));
		expect(() => encodeMultipartRequestBody(
			{ note: "ok", payload: new Uint8Array([0, 1]), upload: { bytes: new Uint8Array([2]), filename: "roof.txt", mediaType: "text/plain" } },
			descriptor,
			registry,
		)).toThrow(expect.objectContaining({ code: "invalid_input" }));
	});

	test("validates response media and decodes exact values", async () => {
    const response = new Response('{"count":"42","note":null}', {
      status: 200,
      headers: { "content-type": "application/json; charset=utf-8" },
    });
    await expect(decodeResponseBody(response, "json", ["application/json"], valueDescriptor, registry, "test/binding/get", 1024)).resolves.toEqual({
      count: 42n,
      note: null,
    });

    const contradictory = new Response("{}", { status: 200, headers: { "content-type": "text/plain" } });
    await expect(decodeResponseBody(contradictory, "json", ["application/json"], valueDescriptor, registry, "test/binding/get", 1024)).rejects.toMatchObject({
      code: "contract_violation",
      bindingAddress: "test/binding/get",
    });

    const oversized = new Response('"too large"', { headers: { "content-type": "application/json", "content-length": "11" } });
    await expect(decodeResponseBody(oversized, "json", ["application/json"], valueDescriptor, registry, "test/binding/get", 4)).rejects.toMatchObject({ code: "contract_violation" });
	});

	test("uses one exact empty-object representation for unit", async () => {
		const unit: TypeDescriptor = { kind: "primitive", name: "unit" };
		expect(encodeTypedJSON({}, unit, registry)).toBe("{}");
		expect(() => encodeTypedJSON({ extra: true }, unit, registry)).toThrow(SceneryClientError);
		await expect(decodeResponseBody(new Response("{}", { headers: { "content-type": "application/json" } }), "json", ["application/json"], unit, registry, "test/binding/unit", 16)).resolves.toEqual({});
	});

	test("validates canonical RFC 3986 URLs including IPv6 and userinfo", () => {
		const descriptor: TypeDescriptor = { kind: "primitive", name: "url" };
		for (const value of [
			"https://[2001:db8::1]/a",
			"https://user:p%2F@xn--bcher-kva.example/b?q=%E2%9C%93#frag/%2F",
		]) {
			expect(encodeTypedJSON(value, descriptor, registry)).toBe(JSON.stringify(value));
		}
		for (const value of ["https://Example.com/a", "https://example.com:443/a", "https://[fe80::1%25en0]/", "https://[::::]/"]) {
			expect(() => encodeTypedJSON(value, descriptor, registry)).toThrow(SceneryClientError);
		}
	});

	test("accepts every RFC-compatible UUID version with the canonical variant", () => {
		const descriptor: TypeDescriptor = { kind: "primitive", name: "uuid" };
		expect(encodeTypedJSON("018f47a2-6f45-0c4a-8b31-4cbbe3c99a22", descriptor, registry)).toBe(
			'"018f47a2-6f45-0c4a-8b31-4cbbe3c99a22"',
		);
		expect(() => encodeTypedJSON("018f47a2-6f45-7c4a-7b31-4cbbe3c99a22", descriptor, registry)).toThrow(SceneryClientError);
	});

	test("decodes repeated response metadata and reconstructs split payloads", () => {
		const response = new Response(null, { headers: { "x-request-id": "request-1", "set-cookie": "session=hello%20world; Path=/; Secure" } });
		Object.defineProperty(response.headers, "getAll", {
			value: (name: string) => name.toLowerCase() === "x-request-id" ? ["request-1"] : [],
		});
		Object.defineProperty(response.headers, "getSetCookie", {
			value: () => ["session=hello%20world; Path=/; Secure"],
		});
		let payload: unknown = undefined;
		payload = mergeResponseValue(payload, ["requestId"], decodeResponseHeader(response, "x-request-id", "repeated", { kind: "primitive", name: "string" }, registry, "test/binding/metadata"), "test/binding/metadata");
		payload = mergeResponseValue(payload, ["sessionToken"], decodeResponseCookie(response, "session", { kind: "primitive", name: "string" }, registry, "test/binding/metadata"), "test/binding/metadata");
		expect(payload).toEqual({ requestId: "request-1", sessionToken: "hello world" });
	});

	test("rejects repetition-dependent metadata on a fetch runtime that collapses headers", () => {
		const response = new Response(null, { headers: { "x-value": "one, two" } });
		Object.defineProperty(response.headers, "getAll", { value: undefined });
		expect(() => decodeResponseHeader(response, "x-value", "repeated", { kind: "list", value: { kind: "primitive", name: "string" } }, registry, "test/binding/metadata")).toThrow(
			expect.objectContaining({ code: "unsupported_runtime" }),
		);
	});

	test("returns declared transport failures and does not retry by default", async () => {
		let requests = 0;
		const client = new PublicApiClient({
			baseUrl: "https://example.test" as URLString,
			fetch: async () => {
				requests++;
				return new Response(JSON.stringify({ code: "transport.invalid_request", message: "invalid" }), {
					status: 400,
					headers: { "content-type": "application/problem+json" },
				});
			},
		});
		await expect(client.processScene({ sceneId: "scene-1" })).resolves.toEqual({
			kind: "failure",
			name: "invalid_request",
			problem: { code: "transport.invalid_request", message: "invalid" },
		});
		expect(requests).toBe(1);
	});

	test("performs no fetch for an already-cancelled call", async () => {
		let requested = false;
		const controller = new AbortController();
		controller.abort();
		const client = new PublicApiClient({
			baseUrl: "https://example.test" as URLString,
			fetch: async () => {
				requested = true;
				return new Response();
			},
		});
		await expect(client.processScene({ sceneId: "scene-1" }, { signal: controller.signal })).rejects.toMatchObject({ code: "cancelled" });
		expect(requested).toBe(false);
	});
});
