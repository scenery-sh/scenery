Below is a copy-pasteable PRD/implementation prompt for a coding agent.

---

# PRD Prompt: Hidden Binary Generated-Client Transport for onlava

You are working on **onlava**, a local-first Go framework/runtime/codegen tool. onlava currently exposes developer-authored APIs through normal Go functions and `//onlava:api` directives, with JSON/HTTP behavior and generated clients.

Implement a new **hidden binary generated-client transport**.

The product goal is **faster generated-client communication** without requiring onlava developers to know anything about protobuf, proto files, gRPC, Connect, schemas, field numbers, descriptors, or RPC services.

Developers should continue writing normal onlava APIs exactly as they do for JSON.

## Core product principle

onlava should expose **one logical API model** and multiple wire formats.

Developer-facing model:

```go
//onlava:api public method=GET path=/users/:id
func GetUser(ctx context.Context, req GetUserRequest) (GetUserResponse, error) {
    ...
}
```

Generated client model:

```ts
const api = createonlavaClient({
  baseUrl: "http://localhost:4000",
  transport: "auto",
});

const result = await api.users.getUser({ id: "u_123" });
```

Runtime wire formats:

```text
JSON wire:
  human-debuggable
  visible in browser devtools
  curl/browser friendly

Binary wire:
  generated-client optimized
  faster serialization/deserialization
  implemented internally using generated protobuf/Connect-like machinery if appropriate
  never exposed as a concept to normal onlava developers
```

Do **not** create a public “protobuf API” or “RPC API” concept.

Do **not** require users to write, edit, commit, understand, or inspect `.proto` files.

Do **not** add proto/gRPC learning curve.

## Explicit non-goals

Do not implement:

```text
- public proto files as source of truth
- manual proto schema editing
- manual service/rpc definitions
- developer-facing gRPC terminology
- developer-facing Connect terminology
- schema lock files
- breaking-change checks
- reserved field-number tracking
- compatibility migration tooling
- buf breaking checks
- field-number stability across incompatible app versions
```

Breaking binary compatibility is acceptable.

If a generated client and server disagree about binary schema, the generated client should automatically fall back to JSON.

JSON is the compatibility escape hatch.

## Required behavior

onlava should serve JSON and binary at the same time.

Developers should see one endpoint:

```text
users.GetUser
```

Not two APIs.

The dashboard/CLI may show wire availability like this:

```text
users.GetUser
  JSON:   available
  Binary: available
  Client: api.users.getUser({ id })
```

But it must not show primary UI concepts such as:

```text
.proto
protobuf service
gRPC method
Connect handler
field number
descriptor
```

Those may exist internally only.

## Recommended runtime URL shape

Keep existing JSON routes unchanged:

```text
GET /users/:id
POST /users
...
```

Add a generated-client binary route namespace, for example:

```text
POST /_wire/<endpoint-id>
GET  /_wire/capabilities
GET  /_wire/recover/<call-id>
```

The exact path can change, but it must be:

```text
- generated and deterministic
- used only by generated clients
- not presented as the primary user API
- safe to serve alongside normal JSON routes
```

The generated client should know the binary route automatically.

## Generated client behavior

Generated clients should support:

```ts
transport: "auto"   // default: binary when possible, JSON fallback
transport: "json"   // JSON only
transport: "binary" // binary preferred, JSON fallback on wire/decode/schema failure
```

Optional internal/debug mode:

```ts
transport: "binary-strict"
```

`binary-strict` may throw instead of falling back, but it should not be the default.

Default should be:

```ts
transport: "auto"
```

Expected call shape:

```ts
const api = createonlavaClient({
  baseUrl: "http://localhost:4000",
});

const user = await api.users.getUser({ id: "u_123" });
```

The public client should expose plain TypeScript objects, not protobuf message classes.

Good:

```ts
await api.users.getUser({ id: "u_123" });
```

Bad:

```ts
new GetUserRequest({ id: "u_123" });
client.userService.getUser(...)
```

Public generated client files should expose clean types and methods.

Internal generated files may include protobuf/binary implementation details under a clearly private path such as:

```text
api/_wire/*
```

## Binary schema generation

onlava should generate binary schemas automatically from the existing onlava endpoint/type IR.

Source of truth:

```text
Go endpoint functions
Go request/response structs
onlava parser/codegen metadata
```

Not source of truth:

```text
.proto files
manual service definitions
external schema files
schema lock files
```

Pipeline:

```text
Go source
  -> onlava parser
  -> onlava endpoint/type IR
  -> generated JSON adapters
  -> generated binary schema
  -> generated binary server adapters
  -> generated binary client internals
  -> clean public generated client wrapper
```

Generated protobuf/proto-like files may exist internally in:

```text
.onlava/gen/wire/...
```

They should not be placed in the app source tree as files the developer is expected to maintain.

They should be treated as ephemeral/generated artifacts.

## No breaking compatibility support

Do not implement long-term protobuf field-number compatibility.

For now, assign binary field numbers deterministically from the current onlava IR.

Acceptable strategies:

```text
- struct field order
- sorted wire names
- deterministic generated order from parser
```

The only requirement is that server and generated client from the same build agree.

Old binary clients talking to new servers may fail binary decoding. That is acceptable.

When that happens, the generated client should fall back to JSON.

## Schema hash / capability check

Because binary compatibility is not guaranteed, add a generated schema hash.

Server exposes a JSON capability endpoint:

```http
GET /_wire/capabilities
```

Example response:

```json
{
  "schema": "onlava.wire.capabilities/v0",
  "wire_schema_hash": "sha256:abc123",
  "endpoints": {
    "users.GetUser": {
      "binary": true,
      "json": true,
      "binary_path": "/_wire/users.GetUser"
    }
  }
}
```

Generated client embeds its own schema hash:

```ts
const ONLAVA_WIRE_SCHEMA_HASH = "sha256:abc123";
```

Behavior:

```text
1. In auto/binary mode, client checks capabilities lazily.
2. If server hash matches client hash, use binary when endpoint supports it.
3. If server hash differs, use JSON for all calls or for affected endpoints.
4. Cache capabilities for the client lifetime.
5. Provide an option to disable capability preflight only for internal testing.
```

This avoids most binary decode problems before they happen.

## JSON fallback rules

Fallback to JSON is a core feature.

The generated client should fall back to JSON when:

```text
- binary capability endpoint is missing
- binary capability endpoint says endpoint unsupported
- server/client wire schema hash mismatch
- binary request encoding fails locally
- binary request decoding fails on server before handler invocation
- binary protocol negotiation fails
- binary response decoding fails
- binary route returns an onlava wire/decode/protocol error
```

Do not fall back to JSON for normal application errors.

Examples that should **not** trigger fallback:

```text
- user not found
- permission denied
- validation error from user handler
- business logic error
- normal 4xx/5xx mapped from onlava errs
```

Those errors should be returned to the caller identically across JSON and binary transports.

## Avoid duplicate side effects during fallback

Binary response decode failure is tricky: the server may have already executed a mutating handler.

Implement fallback in a way that avoids duplicate side effects.

Required approach:

Each generated-client binary call should send a unique call ID:

```http
X-onlava-Call-ID: <uuid-or-random-id>
```

Runtime should maintain a short-lived in-memory recovery store for generated-client calls.

When a binary call invokes the user handler and obtains a result, before returning the binary response, store the JSON-serializable result under the call ID for a short TTL.

Then, if the generated client cannot decode the binary response, it should call:

```http
GET /_wire/recover/<call-id>
```

Expected recovery response:

```json
{
  "schema": "onlava.wire.recovery/v0",
  "endpoint": "users.GetUser",
  "status": "ok",
  "result": {
    "user": {
      "id": "u_123",
      "email": "a@example.com"
    }
  }
}
```

If recovery succeeds, return that JSON-decoded result to the caller.

If recovery is missing:

```text
- for safe/idempotent endpoints, retry the normal JSON endpoint
- for non-idempotent endpoints, return a clear onlavaWireFallbackError
```

Safe/idempotent defaults:

```text
GET, HEAD = safe to retry
POST, PUT, PATCH, DELETE = not safe unless explicitly marked
```

Optional future directive:

```go
//onlava:api public method=POST path=/search idempotent
```

Do not introduce this directive unless needed for the first implementation.

## Supported Go type subset

Binary transport should support a practical subset first.

Support:

```text
string
bool
int
int32
int64
uint32
uint64
float32
float64
[]byte
time.Time
time.Duration
[]T
map[string]T
struct
*struct
*T for scalar optional fields if practical
```

JSON-only or unsupported for binary:

```text
interface{}
any
map[non-string]T
func
chan
cyclic structs
custom JSON marshalers that cannot be represented cleanly
anonymous embedded fields with ambiguous names
types requiring runtime reflection tricks
```

If an endpoint cannot support binary, it should still work over JSON.

Generated client should route that endpoint to JSON automatically.

Example dashboard/inspect output:

```text
debug.Raw
  JSON:   available
  Binary: unavailable
  Reason: response field metadata uses map[string]any
```

Do not fail the whole app because one endpoint cannot be encoded as binary.

## Error model

Keep one onlava error model.

Developer writes:

```go
return GetUserResponse{}, errs.NotFound("user not found")
```

JSON transport returns an onlava JSON error.

Binary transport returns the equivalent internal wire error.

Generated client maps both back to the same public client error type.

The caller should not care whether the response came from JSON or binary.

Client-side behavior:

```ts
try {
  await api.users.getUser({ id });
} catch (err) {
  if (isonlavaError(err, "not_found")) {
    ...
  }
}
```

This should work identically for JSON and binary.

## Runtime architecture

Add a transport-neutral endpoint model if one does not already exist.

Recommended conceptual shape:

```go
type Endpoint struct {
    ID      string
    Access  Access
    JSON    *JSONRoute
    Binary  *BinaryRoute
    Handler EndpointHandler
    Schema  EndpointSchema
}
```

Both JSON and binary handlers should call the same generated endpoint wrapper.

Flow:

```text
JSON request
  -> decode path/query/body
  -> Go request struct
  -> same endpoint wrapper
  -> Go response struct
  -> encode JSON

Binary request
  -> decode generated binary payload
  -> Go request struct
  -> same endpoint wrapper
  -> Go response struct
  -> encode binary payload
```

Shared behavior:

```text
auth
middleware
tracing
logging
request metadata
testing hooks
mocking hooks
errors
```

Do not create separate business-logic paths for JSON and binary.

## Connect-Go guidance

Prefer Connect-Go internally if it fits the implementation.

However:

```text
- do not expose Connect types to onlava app developers
- do not expose Connect concepts in public CLI/dashboard/docs
- do not require app developers to import connectrpc packages
- do not require app developers to define services
- do not require app developers to define proto files
```

Connect/protobuf can be an implementation detail inside generated server/client code.

Public onlava code should remain ordinary Go.

## CLI requirements

Existing command:

```text
onlava gen client
```

should generate a client that supports both JSON and binary where possible.

Do not require:

```text
onlava gen proto
onlava gen rpc
onlava gen grpc
```

Allowed internal/advanced command names:

```text
onlava inspect wire --json
onlava inspect endpoints --json
```

Avoid public primary commands named:

```text
proto
grpc
rpc
connect
```

Suggested inspect output:

```json
{
  "schema": "onlava.endpoints/v0",
  "wire_schema_hash": "sha256:abc123",
  "endpoints": [
    {
      "id": "users.GetUser",
      "source": {
        "file": "users/api.go",
        "line": 12
      },
      "json": {
        "enabled": true,
        "method": "GET",
        "path": "/users/:id"
      },
      "binary": {
        "enabled": true,
        "path": "/_wire/users.GetUser",
        "reason": null
      },
      "client": {
        "method": "api.users.getUser"
      }
    }
  ]
}
```

## Dashboard requirements

Dashboard should show one endpoint list.

Do not create a separate “RPC Services” page for normal users.

Show wire formats as endpoint details:

```text
Endpoint: users.GetUser
Route: GET /users/:id
Generated client: api.users.getUser({ id })

Wire formats:
  JSON      available
  Binary    available
```

For binary requests captured in logs/traces, decode and display the logical JSON-shaped request/response in the dashboard.

Chrome devtools may show binary as opaque; onlava dashboard should make binary calls understandable.

## File/layout guidance

Generated internal wire artifacts can live under:

```text
.onlava/gen/wire/
```

Generated TypeScript client can look like:

```text
web/src/api/
  index.ts
  client.ts
  types.ts
  errors.ts
  _wire/
    json.ts
    binary.ts
    capabilities.ts
    fallback.ts
```

Public import:

```ts
import { createonlavaClient } from "./api";
```

Private/internal imports may reference:

```ts
./_wire/binary
./_wire/json
```

Developers should not need to touch `_wire`.

## Testing requirements

Add tests for at least the following.

### 1. JSON still works

Given a normal onlava endpoint, the existing JSON route still behaves as before.

### 2. Binary works for supported endpoint

Given:

```go
//onlava:api public method=GET path=/users/:id
func GetUser(ctx context.Context, req GetUserRequest) (GetUserResponse, error)
```

Generated client with `transport: "binary"` returns the same plain object as JSON.

### 3. Auto transport uses binary when schema hash matches

Client calls capabilities, sees matching schema hash, uses binary.

### 4. Auto transport falls back to JSON when schema hash mismatches

Simulate mismatch between generated client hash and runtime hash.

Expected:

```text
- no thrown binary decode error
- JSON route is used
- caller receives desired data
```

### 5. Binary unsupported endpoint uses JSON

Endpoint with unsupported binary type, such as `map[string]any`, should still generate a working client method that uses JSON.

### 6. Request decode failure falls back to JSON

Simulate binary request decode failure before handler invocation.

Expected:

```text
- generated client retries JSON
- handler invoked once
- caller receives desired data
```

### 7. Response decode failure recovers without duplicate side effects

Create a mutating endpoint that increments a counter.

Simulate binary response decode failure.

Expected:

```text
- generated client recovers result through JSON recovery path
- handler invoked once
- counter increments once
- caller receives desired data
```

### 8. Application errors do not trigger fallback

Endpoint returns `errs.NotFound`.

Expected:

```text
- generated client returns onlava not_found error
- no JSON fallback retry caused by normal app error
```

### 9. Generated client exposes no protobuf types

Public generated client should not require imports from protobuf/connect packages.

It should expose plain TypeScript input/output objects.

### 10. Public docs/UI avoid proto terminology

No primary docs/dashboard copy should tell normal users to edit proto files, define services, or learn gRPC.

## Implementation phases

### Phase 1: Endpoint IR and binary eligibility

Implement or extend endpoint/type IR.

For every endpoint, determine:

```text
- endpoint ID
- JSON route
- request type
- response type
- source location
- binary eligibility
- unsupported binary reason, if any
```

Add:

```text
onlava inspect endpoints --json
```

or extend existing inspect/check output.

### Phase 2: Hidden wire schema generation

Generate internal binary schema from endpoint IR.

No public proto files.

No schema lock.

No breaking-change checks.

Generate a deterministic `wire_schema_hash`.

### Phase 3: Runtime binary route support

Serve generated binary routes under a hidden generated-client namespace.

Expose capabilities endpoint:

```text
GET /_wire/capabilities
```

Implement recovery endpoint:

```text
GET /_wire/recover/<call-id>
```

Add short-lived in-memory recovery store.

### Phase 4: Generated client support

Update `onlava gen client`.

Generated client should support:

```ts
transport: "auto" | "json" | "binary"
```

Default:

```ts
transport: "auto"
```

Implement:

```text
- capabilities preflight
- schema hash comparison
- binary preferred path
- JSON fallback path
- response recovery path
- endpoint-level JSON fallback for unsupported binary endpoints
```

### Phase 5: Dashboard/logs/traces polish

Dashboard should show logical endpoints and wire availability.

Logs/traces should identify transport:

```text
transport=json
transport=binary
transport=json_fallback
```

But endpoint identity should remain the same:

```text
users.GetUser
```

## Acceptance criteria

This feature is done when:

```text
1. A developer can write a normal //onlava:api endpoint with Go structs.
2. onlava automatically generates binary wire internals.
3. The developer never writes or edits proto files.
4. The generated client can call the endpoint using binary transport.
5. The same generated client can use JSON transport.
6. Auto mode prefers binary when possible.
7. Auto mode falls back to JSON on schema/decode/wire failures.
8. Normal application errors do not trigger fallback.
9. Unsupported binary types gracefully use JSON.
10. JSON and binary share the same auth/middleware/tracing/error behavior.
11. The dashboard/CLI presents one logical endpoint, not separate REST/RPC APIs.
12. Public generated client types are plain app-shaped types, not protobuf message classes.
13. No schema lock or breaking compatibility system is implemented.
14. Existing JSON API behavior remains intact.
```

## Important product language

Use this language in code comments/docs/UI:

```text
JSON transport
Binary transport
Generated client
Wire format
Wire schema
Fallback
Endpoint
```

Avoid this language in primary user-facing surfaces:

```text
proto
protobuf
gRPC
Connect
RPC service
field number
descriptor
buf
```

Advanced/internal comments may mention implementation details, but the default developer experience must remain onlava-native.

## Design summary

The intended user experience is:

```go
//onlava:api public method=GET path=/users/:id
func GetUser(ctx context.Context, req GetUserRequest) (GetUserResponse, error) {
    ...
}
```

```ts
const api = createonlavaClient({ baseUrl });

const result = await api.users.getUser({ id: "u_123" });
```

The developer should not know or care whether this used JSON or binary.

JSON exists for debugging.

Binary exists for speed.

Fallback exists so binary compatibility does not become the developer’s problem.
