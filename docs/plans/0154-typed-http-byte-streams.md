# Typed HTTP Byte Streams

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery's `bytes` response codec currently requires an operation handler to
return a complete `[]byte`. The generated direct HTTP adapter then validates
and defensively clones the typed outcome through its canonical JSON wire form,
which temporarily base64-encodes the bytes. A 17.9 MB Apple Flyover GLB in
ONLV therefore creates several full-size copies before the HTTP response is
written even though the external response is raw octets.

This plan implements the reserved `delivery = "stream"` HTTP mode for direct,
typed byte responses. A Go handler returns its ordinary typed outcome plus a
`scenery.ByteStream`; the stream supplies the response-mapped `bytes` field
without entering the outcome's JSON clone. The runtime enforces the declared
response limit before committing headers, negotiates media type and streaming
compression, copies incrementally, closes the reader on every path, and never
exposes `http.ResponseWriter` or `http.Request` to application code.

ONLV Drive is the first real consumer. Its public `/drive/{path...}` route and
generated client result remain unchanged while `io.ReadAll`, transient base64,
and multiple whole-object memory copies disappear from the server path.

## Progress

- [x] (2026-08-27) Traced ONLV Drive, generated outcome cloning, HTTP response
  encoding, runtime writing, and the reserved stream-delivery validation.
- [x] (2026-08-27) Chose a direct-only typed handler ABI that keeps transport
  handles out of application code and preserves the existing HTTP contract.
- [x] (2026-08-27 22:05Z) Implemented compiler validation, Go verification,
  generation, direct HTTP streaming, incremental gzip, and reader ownership.
- [x] (2026-08-27 22:12Z) Migrated ONLV Drive and regenerated affected client
  descriptors without changing its route or TypeScript result shape.
- [x] (2026-08-27 22:30Z) Passed focused and full Go suites, fixture
  regeneration, ONLV check/generate/repository harness, exact HTTP byte/hash,
  structural GLB, and unmocked authenticated viewer acceptance.
- [x] (2026-08-27 22:32Z) Passed Scenery's 22-step full self-harness and
  completed plan bookkeeping.

## Surprises & Discoveries

- Observation: `delivery = "stream"` and `server_sent_events` are already
  reserved separately, but the compiler rejects both with SCN7008.
  Evidence: `internal/compiler/http.go` and `docs/spec/http.md`.

- Observation: the browser never receives base64. The expensive conversion is
  caused by generated `Clone<Operation>Outcome`, which is implemented as a
  canonical marshal/unmarshal round trip before the response codec writes raw
  bytes.
  Evidence: `internal/generate/generate_go_contract_api.go` and
  `internal/generate/generate_application_http.go`.

- Observation: response compression defaults to gzip, so a correct streaming
  implementation must either stream gzip or change an existing effective
  HTTP default. Streaming gzip preserves the current contract without an ONLV
  exception.
  Evidence: `internal/compiler/http_effective.go`.

- Observation: ONLV exposes the same download operation to MCP, whose JSON
  result cannot carry a live reader.
  Evidence: the generated MCP adapter already owns a 16 MiB result limit and
  canonical outcome codec, while the direct HTTP binding owns a 200 MiB raw
  response limit.

- Observation: the exact Flyover object works through streaming gzip as well
  as identity, and Three.js does not need a `Content-Length` header to render
  the chunked compressed response.
  Evidence: both curl variants decompress to SHA-256
  `5250feb64f9510742781e1350d2d64b24e2b001792cf8a8e18e0bfe03384f71e`;
  the live viewer reached renderer status `Ready` over chunked gzip.

## Decision Log

- Decision: Add a `scenery.ByteStream` second success channel to native
  handlers selected by HTTP `delivery = "stream"`, rather than adding a
  serializable stream scalar or exposing raw Go HTTP objects.
  Rationale: the operation result remains the singular typed contract, while
  the non-serializable reader remains an explicit implementation ABI owned by
  the one direct HTTP binding.
  Date/Author: 2026-08-27 / Codex.

- Decision: Restrict the first implementation to direct HTTP bindings whose
  result responses map `codec = "bytes"`; errors and standard transport
  outcomes remain ordinary buffered problem responses.
  Rationale: this solves large object delivery without pretending arbitrary
  request streams, SSE, durable streams, or internal-client streams exist.
  Date/Author: 2026-08-27 / Codex.

- Decision: Require a known non-negative stream size and enforce the effective
  response limit before headers are written.
  Rationale: object storage already provides exact size metadata, and early
  rejection preserves the existing response-limit contract.
  Date/Author: 2026-08-27 / Codex.

- Decision: Permit MCP as the only coexisting non-stream binding and make its
  generated adapter buffer the stream explicitly under the MCP result limit.
  Rationale: direct HTTP remains genuinely incremental, while the operation's
  existing assistant tool keeps its JSON contract and fails before reading an
  object larger than that transport permits.
  Date/Author: 2026-08-27 / Codex.

## Outcomes & Retrospective

The implementation and consumer migration are complete. Direct typed HTTP
downloads now move bytes from an application-owned `io.ReadCloser` into the
response writer without putting the body through canonical JSON. Limits are
checked from the exact declared size before headers; identity responses carry
that `Content-Length`; gzip is emitted incrementally; `HEAD`, negotiation
failure, short reads, client disconnects, and normal completion all release
the reader.

ONLV Drive keeps `/drive/{path...}`, response headers, the 200 MiB binding
limit, and its generated TypeScript outcome shape. Its handler no longer calls
`io.ReadAll` or closes a successful object itself. The exact 17,900,580-byte
maximum-quality Flyover GLB downloads over identity and gzip with the stored
hash, parses to the expected 100 m scene, and reaches `Ready` in the existing
Three.js viewer. The full Scenery self-harness also passed, including Go tests,
vet, changed-area routing, fixtures, schemas, storage, Postgres, dashboard, and
TypeScript client conformance.

## Context and Orientation

`internal/compiler/http.go` validates HTTP mappings and currently rejects
stream delivery. `internal/compiler/resource_validate.go` checks execution and
delivery compatibility. `internal/generate/verify_go_native.go` verifies the
native handler ABI. `internal/generate/generate_application.go` and
`generate_application_http.go` emit the service interface, invocation, and
response mapping. `runtime/registry.go`, `runtime/contract_stream.go`, and
`runtime/server.go` own the typed response and actual HTTP write.

ONLV Drive is declared in `drive/package.scn`; `drive/get.go` currently opens
the object and calls `io.ReadAll` before returning `DownloadSuccess`.

## Milestones

1. Specify and validate the narrow typed byte-stream contract.
2. Generate the three-result native handler ABI and stream response adapter.
3. Stream and close bodies in the runtime with limits and optional gzip.
4. Migrate ONLV Drive without changing its route or browser-facing result.
5. Prove source, generated artifacts, live bytes, GLB structure, and rendering.

## Plan of Work

Define the stream ownership types in Scenery runtime and alias the application
type from the root `scenery` package. Teach the compiler that stream delivery
is compatible only with direct HTTP execution, that result bodies use the
`bytes` codec, and that one operation cannot mix stream and non-stream handler
ABIs. Update native verification and generated composition so streaming
handlers return `(Outcome, scenery.ByteStream, error)`. The adapter clones only
the ordinary outcome metadata, keeps the stream separate, and transfers reader
ownership to a `ContractHTTPResponse` stream encoder.

Update the server writer to copy the declared number of bytes incrementally,
optionally through a gzip writer, and close on success, negotiation failure,
handler failure, HEAD, disconnect, or short read. Then set Drive's binding to
stream delivery and return the open object body plus size instead of buffering.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/compiler ./internal/generate ./runtime
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go test ./...
    .scenery/harness/bin/scenery harness self --summary --write

From `/Users/petrbrazdil/Repos/onlv` using the source-local Scenery binary:

    go test ./drive ./jobs ./maps
    /Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery check --app-root /Users/petrbrazdil/Repos/onlv -o json
    /Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery generate --check --app-root /Users/petrbrazdil/Repos/onlv -o json
    just repo-harness

The live proof requests the existing 17,900,580-byte Flyover GLB, checks its
status, content length, ETag/SHA-256, and GLB header, parses its scenes,
primitives, textures, triangles, and bounds, then loads the existing scene URL
in the browser and confirms the failure panel is absent.

## Validation and Acceptance

Acceptance requires all of the following:

- stream delivery compiles only for direct HTTP byte-result bindings;
- the generated native signature is `(Outcome, scenery.ByteStream, error)`;
- the generated adapter never marshals the stream body into the outcome clone;
- response limits reject an oversized declared stream before reading it;
- media negotiation, identity responses, streaming gzip, HEAD, disconnect,
  short reads, and reader closure have focused runtime coverage;
- ONLV Drive contains no `io.ReadAll` download path and keeps its public route,
  headers, response status, and generated TypeScript outcome shape;
- Scenery focused tests, fixture regeneration, `go test ./...`, and full
  self-harness pass;
- ONLV focused Go tests, source-local check/generate, and repository harness
  pass;
- the exact maximum-quality Flyover GLB downloads, parses, and renders.

## Idempotence and Recovery

Generation and validation commands are repeatable. Generated fixture and ONLV
client changes are accepted only when their descriptors match the new source
contract. A failed live request cannot mutate the stored object. The runtime
owns and closes a returned stream exactly once; the application must not close
it after a successful return.

## Artifacts and Notes

- `go test ./runtime ./internal/compiler ./internal/generate ./internal/spec`:
  passed.
- Both committed TypeScript fixture regeneration commands: `changed: []`.
- `go test ./...`: passed across the complete Scenery module.
- `.scenery/harness/bin/scenery harness self --summary --write`: all 22 steps
  passed.
- ONLV `go test ./drive ./jobs ./maps`: passed.
- Source-local ONLV `scenery check -o json`: contract and implementation valid,
  no diagnostics.
- Source-local ONLV `scenery generate --check -o json`: `changed: []`, no
  diagnostics.
- ONLV `just repo-harness`: passed.
- Routed identity and gzip GETs both hash to
  `5250feb64f9510742781e1350d2d64b24e2b001792cf8a8e18e0bfe03384f71e`;
  identity and `HEAD` report `Content-Length: 17900580`, while gzip is chunked.
- GLTF Transform reports one scene/node/mesh, 52 primitives/materials/PNG
  textures, 15,386 vertices, 14,142 triangles, and bounds
  `[-50,-50,0]` to `[50,50,26.720624923706055]`.
- An authenticated headless viewer fetched the unmocked routed asset over
  chunked gzip, cleared its loading overlay, exposed renderer status `Ready`
  and its active top-down control, and showed no failure panel.
- Machine-local build, harness, and browser artifacts remain uncommitted.

## Interfaces and Dependencies

The implementation uses only the Go standard library. The public application
surface is `scenery.ByteStream`; the runtime transport surface is a streamed
`ContractHTTPResponse`. No environment variable, external dependency, raw
handler, alternate route, or compatibility path is added.
