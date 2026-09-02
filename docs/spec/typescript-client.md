# Scenery TypeScript Client

Normative companion specification

Umbrella specification: [SPEC.md](SPEC.md)

## 1. Purpose

This document defines deterministic TypeScript client generation for Scenery public bindings. It specifies generated API shape, type mapping, codecs, outcomes, cancellation, metadata, revisions, and compatibility behavior.

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

## 2. Dependencies and scope

The generator depends on:

- the exact current specification and evolution-rule revisions;
- each binding codec represented by a client;
- canonical expanded graph and artifact-projection revisions.

The current contract generates public unary HTTP clients for `scenery.http-codec`.
Bindings using terminal path tails follow
[http-path-tail.md](http-path-tail.md). Enqueue receipts
are included for enqueue delivery.

An assistant conversation client is a dedicated provider-neutral projection of
the assistant public protocol. It is not generic HTTP streaming: the generator
emits the fixed assistant event stream and its cursor/reconnect semantics only
when an assistant is reachable from the selected target. The projection never
imports or names an implementation adapter, provider URL, token, or private
control field.

It does not generate:

- server-side internal binding clients;
- raw or transport-coupled handlers;
- implementation-declared or opaque wire facets as verified APIs;
- WebSocket, streaming, event-consumer, or arbitrary CLI clients;
- a client for an unexported binding.

## 3. Client target

A client target selects an exact public surface:

~~~hcl
typescript_client "public_api" {
  gateways    = [http_gateway.public_api]
  package     = "@onlv/scenery-client"
  module      = "esm"
  runtime     = "fetch"
  output_root = relative_path("clients/generated/public_api")
}
~~~

The target schema declares:

- gateway/export scope;
- package name;
- ESM module format;
- minimum TypeScript and JavaScript runtime versions;
- fetch implementation identity;
- output root;
- optional operation inclusion filters over exported verified resources.

Filters cannot make a required referenced type or outcome disappear. Target configuration is part of `typescript_client_revision[target]`.

## 4. Artifact identity

Every generated client has a detached `scenery.typescript-client-generated` descriptor carrying its exact `schema_revision` and containing:

- target address and package name;
- generator identity/version;
- generator digest;
- codec identities/digests;
- whole application `contract_revision` for provenance;
- `typescript_client_revision[target]` for cache and compatibility identity;
- covered gateways, bindings, operations, and type addresses;
- compatibility catalog identity;
- normalized generated-file content digest.

The projection includes only target configuration and reachable public wire/API shape. Unrelated internal policy or implementation changes do not change the client revision unless they alter exposed client metadata promised by this contract.

## 5. Files and exports

Generation emits deterministic ESM source and declarations. The logical modules are:

~~~text
index.ts
client.ts
types.ts
runtime.ts
metadata.ts
~~~

A generator MAY combine physical files only when exports and artifact bytes remain deterministic for that configured layout. Generated files contain ownership markers and are replaced atomically as one descriptor-covered set.

The generated HTTP client stores per-binding request and response mappings in a descriptor table and executes them through one shared runtime helper. Methods remain one typed async function per covered binding. The generated runtime includes query, binding header/cookie, multipart, and retry sections only when at least one covered descriptor or the target retry configuration requires that capability. The helper is not part of the public `index.ts` export surface.

`index.ts` exports the client class/factory, public contract types, runtime error types, metadata, and scalar helper types. It does not export internal implementation helpers.

## 6. Naming

Semantic resource and field names determine TypeScript identifiers; wire names remain exact quoted property keys in encoders/decoders.

- records, enums, unions, operation inputs/outcomes, and client classes use deterministic `UpperCamelCase`;
- methods and ordinary semantic fields use deterministic `lowerCamelCase`;
- declared outcome discriminants and wire keys retain their exact semantic or wire string;
- initialisms do not use environment-specific dictionaries;
- sibling declarations sort by canonical semantic name/address.

The conversion algorithm splits ASCII lower-snake and lower-kebab identifiers at separators, capitalizes the first ASCII letter of each component, and otherwise preserves component bytes. Empty components are invalid. A generated reserved-word or same-scope collision is a compile error; numeric suffixes are not invented.

An explicit generated-name override, if a future schema permits one, is contract surface and participates in the client revision.

## 7. Scalar type mapping

| Scenery | TypeScript API type | JSON representation |
|---|---|---|
| `bool` | `boolean` | boolean |
| `int` | `bigint` | canonical decimal string |
| `int32`, `uint32` | `number` | JSON number |
| `int64`, `uint64` | `bigint` | canonical decimal string unless the contract explicitly selects safe JSON number |
| `decimal` | `DecimalString` | canonical decimal string |
| `float32`, `float64` | `number` | finite JSON number |
| `string` | `string` | exact string |
| `bytes` | `Uint8Array` | padded RFC 4648 base64 in JSON |
| `uuid` | `UUIDString` | canonical UUID string |
| `date` | `DateString` | `YYYY-MM-DD` string |
| `datetime` | `DateTimeString` | normalized RFC 3339 string |
| `duration` | `DurationString` | codec-defined duration string |
| `size` | `bigint` | canonical integer string or codec-defined form |
| `url` | `URLString` | canonical absolute URL string |
| `relative_path` | `RelativePathString` | normalized relative path string |
| `json` | `JsonValue` | exact canonical JSON value model |

Branded strings are structurally strings plus an unexported unique-symbol brand. Runtime constructors/decoders validate them. Callers cannot pass an unvalidated arbitrary string without an explicit assertion.

`JsonValue` is:

~~~ts
type JsonValue =
  | null
  | boolean
  | string
  | JsonNumber
  | readonly JsonValue[]
  | { readonly [key: string]: JsonValue };
~~~

`JsonNumber` is a runtime-owned exact numeric wrapper preserving Scenery's JSON numeric domain. It is not an unconstrained JavaScript `number`.

Every numeric encoder performs bounds, integrality, finite-value, negative-zero, and constraint checks before sending. Decoders reject lossy or out-of-range values.

## 8. Composite type mapping

| Scenery | TypeScript |
|---|---|
| `list(T)` | `readonly T[]` |
| `set(T)` | `readonly T[]` in canonical semantic order |
| `map(T)` | `Readonly<Record<string, T>>` |
| `tuple(T...)` | readonly tuple |
| `optional(T)` field | optional property `field?: T` |
| `nullable(T)` | `T \| null` |
| `optional(nullable(T))` field | `field?: T \| null` |

Optional absence is represented only by an absent property or `undefined` at the API boundary. Encoders omit it. `null` is accepted and emitted only for nullable values. Decoders MUST NOT collapse absent and null.

Mutable caller arrays, byte arrays, and objects are defensively copied or consumed immutably at the request boundary. Returned values are typed readonly; runtime metadata and unknown payloads cannot be mutated through shared internal state.

## 9. Records and unknown fields

A record becomes a generated readonly interface. Semantic property order is canonical; JSON encoding uses declared wire names.

For `unknown_fields = "preserve"`, the API type includes:

~~~ts
readonly unknownFields: Readonly<Record<string, JsonValue>>;
~~~

The field is generated metadata, not a wire member named `unknownFields`. Decoding stores unknown wire members there. Encoding merges them after rejecting collisions with effective declared wire names.

Closed-record decoders reject unknown members exactly as the codec requires.

## 10. Enums

A closed enum becomes a union of exact string literals plus an exported namespace-like constants object:

~~~ts
export type ProcessMode = "all" | "roof_only";
export const ProcessMode = {
  All: "all",
  RoofOnly: "roof_only",
} as const;
~~~

An open enum preserves unknown strings using a brand:

~~~ts
export type ProcessMode =
  | "all"
  | "roof_only"
  | (string & { readonly __processModeUnknown: unique symbol });
~~~

Decoders retain the exact unknown wire string; encoders reproduce it. Helpers distinguish known and unknown without changing the value.

## 11. Tagged unions

Known variants become a discriminated union with exact declared tags:

~~~ts
export type Detection =
  | { readonly kind: "roof"; readonly value: RoofDetection }
  | { readonly kind: "ground"; readonly value: GroundDetection };
~~~

An open union adds:

~~~ts
| {
    readonly kind: string;
    readonly value: JsonValue;
    readonly unknown: true;
  }
~~~

The decoder preserves the exact unknown tag and canonical JSON payload. Known variants never contain `unknown: true`. A closed union rejects unknown tags.

## 12. Operation outcomes

Each operation generates one closed outcome union. Business results and declared business errors resolve normally; transport, admission, dispatch, codec, network, and undeclared system failures use typed failure variants or exceptions as specified here.

For a call binding:

~~~ts
export type ProcessSceneOutcome =
  | { readonly kind: "result"; readonly name: "processed"; readonly value: ProcessSceneResult }
  | { readonly kind: "error"; readonly name: "invalid_input"; readonly problem: Problem };
~~~

A declared mapped admission or transport outcome that the contract exposes to callers is a typed `failure` variant. A network failure, cancellation, malformed server response, unsupported runtime, or server response contradicting the manifest rejects the promise with `SceneryClientError`, whose `code`, `bindingAddress`, safe metadata, and optional cause are stable.

Generated methods never infer a business error from HTTP status alone; they use the binding's typed response mapping. Unknown status/body combinations are contract violations.

For enqueue delivery, the accepted result contains a typed `EnqueueReceipt` with durable identity, execution ID, accepted revision, and contract-authorized status URL or polling capability. It does not pretend the operation completed.

## 13. Client API

For each target, generation emits one target client:

~~~ts
export interface PublicApiClientOptions {
  readonly baseUrl: URLString;
  readonly fetch?: typeof globalThis.fetch;
  readonly defaultHeaders?: Readonly<Record<string, string>>;
}

export interface CallOptions {
  readonly signal?: AbortSignal;
  readonly headers?: Readonly<Record<string, string>>;
}

export class PublicApiClient {
  constructor(options: PublicApiClientOptions);

  processScene(
    input: ProcessSceneInput,
    options?: CallOptions,
  ): Promise<ProcessSceneOutcome>;
}
~~~

There is exactly one method per covered binding operation unless multiple covered bindings for one operation require distinct transport surfaces; then method names are deterministically binding-qualified and collisions are compile errors.

Generated methods dispatch through the shared table-driven runtime helper. Capability projection removes unused query, binding header/cookie, multipart, and retry branches from `runtime.ts`; it does not create per-method runtime copies. Generated methods do not inline a second copy of the request/response machine and they do not generate React hooks.

Authentication material is supplied only through an explicitly generated authentication capability or request option authorized by the target. The generic `headers` option cannot override framework-owned content, host, forwarding, trace, or credential headers.

The client joins base URL and gateway/binding paths according to the HTTP codec. It does not use platform URL behavior where that would change canonical encoding.

### 13.1 Path-tail URL construction

For a binding with `path_tail`, the client reads the semantic `string` or
`RelativePathString` field rather than accepting a pre-encoded fragment. For a
non-empty value it MUST split on `/`, reject leading, trailing, empty, dot,
dot-dot, or backslash segments, UTF-8 and RFC 3986 percent-encode each segment
independently, and join the encoded segments with literal `/` separators. A
literal percent is data and therefore encodes as `%25`.

Encoding the entire tail as one component is invalid because it would encode
structural slashes as `%2F`. An empty `string` or absent
`optional(relative_path)` emits the fixed binding prefix without a trailing
slash. A non-optional `relative_path` cannot be empty. Generated metadata
records both the base codec and path-tail schema revisions, and the path tail
participates in `typescriptClientRevision`.

### 13.2 Assistant conversation client

For each reachable `assistant` resource, the target emits a namespaced client
whose methods are provider-neutral and generated from the assistant public
schemas:

~~~ts
client.assistants.support.createConversation(input, options?)
client.assistants.support.sendTurn(conversationId, input, options?)
client.assistants.support.streamEvents(conversationId, options?)
client.assistants.support.resolveApproval(conversationId, approvalId, decision, options?)
client.assistants.support.cancelRun(conversationId, runId, options?)
~~~

`createConversation` and `sendTurn` return typed Scenery conversation/run
handles. `streamEvents` returns an `AsyncIterable` of the closed
`scenery.assistant.public-event` union, consumes
`application/x-ndjson`, and accepts an exclusive `after` cursor for
reconnection. It MUST reject malformed sequences, suppress a repeated sequence
when reconnecting, and surface closed public error codes rather than helper
vocabulary. Approval decisions are `approve` or `deny`; cancellation is
idempotent for a known run. `AbortSignal` remains the caller cancellation
mechanism.

The generated assistant surface records the assistant public-protocol and
client projection revisions in metadata. It contains no adapter package name,
private listener address, control token, provider event label, or provider
signature. Implementation-only changes invalidate the assistant build/runtime
projection but MUST NOT alter these public client types unless the declared
assistant surface or public schemas change.

### 13.3 Generated route access

A React target emits one merged route catalog. `createSceneryApp` MUST return
`{router, App, routes}`, and `routes` MUST contain generated and authored
descriptors with optional exact `applicationKey` and `accessKey` metadata.
`matchSceneryRoute(routes, path)` MUST ignore query/hash suffixes, normalize a
non-root trailing slash, match `$parameter` as one nonempty segment, and prefer
an exact static route over a parameterized route.

The access contract is synchronous:

~~~ts
type SceneryAccessResult =
  | { readonly status: "allowed" }
  | { readonly status: "pending" }
  | { readonly status: "denied"; readonly reason?: string; readonly redirectTo?: string };

type SceneryAccessTarget =
  | { readonly kind: "route"; readonly route: SceneryRouteDescriptor }
  | {
      readonly kind: "workspace-tab";
      readonly route: SceneryRouteDescriptor;
      readonly name: string;
      readonly label: string;
      readonly applicationKey?: string;
      readonly accessKey?: string;
    };
~~~

One optional `resolveAccess(target, currentPath)` callback MUST control generated
navigation inclusion, direct-route rendering, and workspace-tab inclusion. A
pending or denied route MUST NOT invoke its page component. A denial with
`redirectTo` uses replace navigation; otherwise the target renders the
application denial slot or the semantic generated fallback. Pending renders the
pending slot or `null`. Denied and pending tabs are omitted; a selected omitted
tab is replaced by the first allowed tab, while zero allowed tabs render the
pending or denial state. Operational tab availability remains a distinct
contract. The resolver MUST NOT fetch, mutate, cache, or authorize backend
records. `navigationFilter(route, currentRoute, currentPath)` runs only after
access gating, and `contentGroup(currentRoute, currentPath)` receives the same
matched descriptor.

## 14. Cancellation, time, and retries

`AbortSignal` is the only v1 caller cancellation mechanism. An already-aborted signal performs no request. Cancellation rejects with `SceneryClientError` code `cancelled` and retains the platform cause without exposing secret data.

The generated client does not retry by default. A target may enable only a versioned retry policy that respects operation idempotency, request replay safety, `Retry-After`, deadline, and cancellation. `RetryRuntime`, its `index.ts` type export, and retry implementation are emitted only for a target that enables retry. Retry configuration participates in the client revision.

The client never reads ambient clock, random, locale, or environment state for contract decisions. Runtime request IDs or retry jitter use injected runtime capabilities and do not affect generated artifacts.

## 15. Metadata

The package exports immutable `sceneryClientMetadata` containing:

- target, gateway, binding, operation, and reachable type addresses;
- whole `contractRevision`;
- `typescriptClientRevision`;
- generator and codec identities/digests;
- compatibility catalog identity;
- generated operation-to-method mapping;
- guarantee classifications;
- source specification revision and generator identity.

It contains no absolute source paths, secret values, deployment credentials, or private resource details outside the client projection.

Metadata keys and arrays use canonical semantic ordering.

## 16. Compatibility

Compatibility uses the current evolution rules from the previous published client projection to the target projection.

- A generated API or wire breaking/unknown change requires coordinated consumer migration.
- Compatible operation, optional-input, or preserving-output additions do not require coordinated consumer migration.
- Implementation-only, documentation-only, or unrelated graph changes do not change the client revision.
- Generator byte changes with identical promised TypeScript API/wire semantics create a new artifact digest but may retain semantic compatibility.

The descriptor records the exact client projection revision and compatibility catalog identity. It never chooses a release number or generates legacy forwarding methods. A method rename changes the generated API and is classified by the evolution rules.

## 17. Generation workflow

`scenery check` builds the expected client in an overlay and reports missing/stale selected committed artifacts without modifying files. `scenery generate --target typescript_client.public_api` atomically replaces the descriptor-covered output. `scenery generate --check` verifies byte identity and is suitable for CI.

Unknown files under a generated client root are not adopted or deleted unless the root's ownership policy explicitly makes the entire root generator-owned.

Given identical graph, target, catalogs, codec revisions, and generator version, output bytes MUST be identical across hosts.

## 18. Security

Generated decoders validate all declared bounds, variants, mappings, media types, and outcome coverage. They reject prototype-polluting object keys where object construction could interpret them specially and use null-prototype maps or safe own-property checks.

Secrets, authentication credentials, sensitive values, and raw response bodies are redacted from error messages and metadata. Error causes are not serialized by default.

The client does not weaken an operation's binding requirements. It cannot generate an anonymous convenience method for an authenticated binding or expose an internal binding publicly.

## 19. Conformance requirements

A conforming generator/runtime passes fixtures for:

- deterministic files, exports, names, ordering, and collision errors;
- every scalar mapping and exact JSON representation;
- finite floats, integer bounds, and lossless bigint handling;
- optional, nullable, and optional-nullable distinction;
- lists, canonical sets, maps, tuples, and defensive copying;
- closed and preserving records;
- closed/open enums and tagged unions with unknown round trips;
- typed result, error, failure, and enqueue outcomes;
- response-map decoding and contradictory-response rejection;
- request cancellation before and during fetch;
- retry disabled by default and idempotency-gated retry when selected;
- immutable metadata and redaction;
- artifact projection and descriptor revisions;
- compatibility classifications from the current evolution rules;
- public-only and framework-enforced filtering;
- assistant client method names, handle validation, NDJSON event decoding,
  exclusive-cursor reconnects, approval/cancel decisions, redaction, and
  provider-identity absence;
- for path-tail bindings, independent tail-segment encoding, structural slash preservation, and empty-tail prefix behavior;
- overlay check, atomic generation, stale detection, and clean-tree CI;
- byte-identical output from at least two supported host platforms.

## Appendix A: Deliberate exclusions

The current TypeScript client contract does not define React hooks, framework-specific caches, Node-only internal clients, generic streaming, WebSockets, raw responses, automatic credential storage, implicit retries, CommonJS output, or publishing to a package registry. The shared request/response helper is not a React hook and is not exported from `index.ts`. The dedicated assistant event stream in Section 13.2 is the sole streaming-shaped projection and does not make arbitrary binding streaming available.
