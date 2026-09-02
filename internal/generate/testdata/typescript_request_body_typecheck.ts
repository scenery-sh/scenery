import {
	encodeMultipartRequestBody,
	encodeRequestBody,
	type MultipartBodyDescriptor,
	type TypeDescriptor,
	type TypeRegistry,
} from "../../compiler/testdata/house/clients/generated/public_api/runtime.js";

const bytesDescriptor = { kind: "primitive", name: "bytes" } as const satisfies TypeDescriptor;
const bodyDescriptor = {
	kind: "record",
	fields: [
		{
			property: "payload",
			wire: "payload",
			value: bytesDescriptor,
			optional: false,
		},
	],
	preserveUnknown: false,
} as const satisfies TypeDescriptor;
const multipartDescriptor = {
	value: bodyDescriptor,
	parts: [
		{
			name: "payload",
			property: "payload",
			kind: "bytes",
			mediaTypes: ["application/octet-stream"],
			maxBytes: 16,
			multiple: false,
			optional: false,
			retainFilename: false,
			value: bytesDescriptor,
		},
	],
	maxParts: 1,
	maxBytes: 128,
} as const satisfies MultipartBodyDescriptor;
const registry = {} satisfies TypeRegistry;

const multipartBody = encodeMultipartRequestBody(
	{ payload: new Uint8Array([1, 2, 3]) },
	multipartDescriptor,
	registry,
);
const multipartRequest: RequestInit = { method: "POST", body: multipartBody.body };
const bytesRequest: RequestInit = {
	method: "POST",
	body: encodeRequestBody(new Uint8Array([1, 2, 3]), "bytes", bytesDescriptor, registry),
};

void multipartRequest;
void bytesRequest;
