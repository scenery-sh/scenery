# Worker ABI v1

The manifest-driven Host and its Go, Rust, JavaScript/TypeScript, and Python
workers use HTTP/2 with prior knowledge over private Unix domain sockets. They use distinct listeners and pool connections across
invocations. The supervisor provides the two socket paths and a bearer token
unique to the worker instance through its private launch configuration. Each
request carries the token in `Authorization`. The Host authenticates instance
identity before consulting any application data. Worker protocol paths and
headers stay on these listeners.

The Rust supervisor starts workers in separate process groups. It verifies
readiness for an entire candidate before asking the gateway to publish it
as the next generation. In development the CLI supplies a launch plan and
validates the source snapshot between preparation and publication. A rejected
candidate leaves the serving generation active. The gateway retains admitted
work through retirement, while the supervisor owns worker lifetimes and
restarts exited workers with fresh sockets and tokens.

The source snapshot covers the files under the application root that neither
the built-in exclusions nor a `.gitignore` leaves out. The CLI and the Host
select and hash the same files, so their snapshot digests agree; the Host
reads a file again only when its `lstat` identity changed.

With a Vite frontend at the application root, the Host also serves backend
routes under the browser's `/api` mount. Directly declared paths keep priority
over mounted application bindings. Browser authentication uses the mount in
its cookie paths and callback URLs.

## Host to worker

- `GET /worker/v1/ready` returns a JSON object with `abi: 1`, `service`,
  `contract_revision`, and `implementation_revision`. It succeeds after the
  service constructor and startup have completed. The Host checks the response
  against the launch descriptor before publishing the worker in a generation.
- `POST /worker/v1/invoke` receives the following JSON envelope.

```json
{
  "service": "pricing/service/pricing",
  "operation": "pricing/operation/quote",
  "binding": "pricing/binding/quote_http",
  "mode": "direct",
  "context": {
    "invocation_id": "01...",
    "scope": "opaque-host-capability-scope",
    "generation": 2,
    "contract_revision": "sha256:...",
    "principal": "user-1",
    "tenant_id": "tenant-1",
    "traceparent": "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
    "deadline": "2026-11-24T21:30:00Z",
    "locale": "en",
    "metadata": {}
  },
  "input": {"item": "sample"}
}
```

`binding` may be omitted for an unbound durable execution. Durable requests
have `mode: "durable"` and `execution_id` in `context`. Principal, tenant,
traceparent, deadline, locale, execution ID and metadata are optional. The
Host constructs this context from its own authenticated invocation. A worker
verifies the addressed service and contract revision before dispatch. A service or contract revision mismatch returns HTTP 409.
The Host may include request type, admission time, caller binding, deployment,
trace identity, and authenticated identity data in metadata. For an invocation
admitted over HTTP, metadata also carries the request `method`, `path`,
`path_params` as `{"name","value"}` pairs in route order, and `headers` as
lists of values by case-insensitive name. The `Authorization`,
`Proxy-Authorization`, and `Cookie` headers stay at the Host. Go,
JavaScript/TypeScript, and Python workers receive database connection
information at startup and connect directly to PostgreSQL.

A successful JSON response contains `outcome`, the contract outcome envelope
with `kind`, `name` and `value` for a declared result or `problem` for a
declared error. An unexpected handler error or panic uses a neutral fault
response. The Host validates worker outcomes using the manifest before
applying public response mappings. An attested implementation failure is
distinct from an interrupted call whose delivery remains unknown.

A streaming result uses `Content-Type: application/vnd.worker.stream`.
The response body starts with an unsigned 32-bit big-endian length followed
by that many bytes of the same JSON outcome response. The remaining body is
the byte stream. The metadata has a 32 MiB limit. Both peers stream the
remaining body with bounded buffering and HTTP/2 flow control.
`X-Worker-Stream-Size` carries the exact decimal byte length of the stream.

A typed event-stream result (`delivery = "stream_out"`) uses
`Content-Type: application/vnd.worker.events` without `X-Worker-Stream-Size`.
The response body begins with the same 4-byte unsigned big-endian length and
outcome JSON metadata (`{"outcome":{"kind":"result","name":"<variant>","value":null}}`),
followed by newline-delimited JSON event frames
(`{"data":<canonical JSON>,"id":"optional","event":"optional"}\n`) flushed per
event. The Host validates and canonicalizes each `data` value against the
declared result type before writing `text/event-stream` SSE frames; pre-stream
declared errors use ordinary `Content-Type: application/json`. Client
disconnect, deadline expiry, or Host shutdown cancels the HTTP/2 stream and
the worker's invocation context.

Workers send `X-Worker-Contract` and `X-Worker-Implementation` on every
authenticated response. The Host rejects mismatched attestations. The Host
drains a retiring generation by withholding new invocations, retaining its
bindings for admitted calls, then terminating obsolete workers once their
admitted calls finish or the drain deadline expires. The deadline ends only
the calls of workers the next generation replaced: a worker it carried over
keeps serving the calls it already admitted.

## Worker to Host

Capabilities use `/host/v1/` on the Host's private listener. In addition to
the worker's bearer token, each call sends `X-Worker-Scope` with the opaque
scope from an active invocation. The Host checks that the scope belongs to
the requesting worker and resolves the generation and principal from the
scope. Workers have no API for supplying their own principal, tenant or
generation. The Host bounds these calls by the invocation's cancellation and
deadline.

| Capability | Request |
| --- | --- |
| `binding/invoke` | POST `{"client":"<local client name>","input":<canonical JSON>}`. An answer contains `output` with the typed outcome or an `error`. |
| `durable/enqueue` | POST the same envelope with a declared enqueue client. The Host derives dispatch keys and returns the durable receipt. |
| `events/emit` | POST `{"publisher":"<local publisher name>","payload":<canonical JSON>}`. The Host returns `output` containing an event receipt. |
| `storage/read`, `storage/write`, `storage/delete`, `storage/list` | Use `store` for the declared dependency name and `key` for object operations (or `recursive=1` with optional prefix `key` on `storage/delete`). GET reads and lists, HEAD reads object metadata, PUT writes, and DELETE removes one object or a prefix. Reads and writes stream bytes. Reads, writes, and single-object deletes accept `If-Match`; writes also accept `If-None-Match: *`. |
| `connections/token` | POST `{"provider":"<configured provider>","scopes":["..."]}`. The Host resolves the owner from invocation scope. |
| `config/snapshot` | GET the revision-bound service configuration visible to this worker. |
| `telemetry` | POST a batch of up to 64 span, log, and metric records. The Host supplies the correlation identities from the invocation. |

The Host chooses each store's backend from its configuration: a local
directory, the development storage proxy, or an S3-compatible bucket. A
worker observes the same requests, records, and failures from each.

`events/emit` resolves the service's declared publisher from the invocation
scope. An undeclared publisher or a denied subscription returns HTTP 403.
An invalid payload returns HTTP 400. The Host evaluates all subscription
policies before writing to PostgreSQL, including on a retry.

The receipt contains `event_id`, `event` (the external name), `deliveries`
(the number of jobs the transaction created), and `deduplicated`. A keyed
event with the same external name and canonical key returns the original
receipt with `deduplicated: true` and creates no jobs. Job deduplication
follows the selected durable execution, so a new event may reuse an existing
job and return fewer deliveries than subscriptions. Events without subscribers
are persisted with zero deliveries. The Host keeps event keys with their
event rows.

Jobs inherit the emitter's trace and invocation identity and record the
subscription as their caller binding. Each event fan-out advances the
cascade depth. The Host returns HTTP 409 (`failed_precondition`) when an
invocation at depth 16 tries to emit another event.

A metric record names a gauge and may include a finite numeric
`attributes.value` and an `attributes.unit`. With no value the Host
reports `elapsed_ns` in seconds. The Host attaches the invocation's
trace, service, runtime, generation, and operation to the sample.

Telemetry batches are sent explicitly. Await the acknowledgement before
returning the outcome or closing the stream so that the invocation scope
is still active. The Host records worker dispatch failures independently of
worker-supplied telemetry. An abrupt process exit can lose telemetry that
the Host has not acknowledged.

The Host and SDK validate the applicable contract type before dispatch and
after receipt. Canonical wire bytes preserve exact numeric values, tagged
outcomes and the distinction between missing and null fields. The shared
wire conformance corpus covers integer bounds, exact scalars, canonical
floats, presence, closed and unknown-preserving records, enums, and outcomes
in Go, the Rust wire crate and SDK, JavaScript/TypeScript, and Python. The
generator embeds the wire crate inside the generated Rust SDK so the Rust paths
are local to the application workspace.

## Delivery and cancellation

The Host reports `not_sent` only when it can prove non-delivery, such as a
failed dial before a stream is opened or an attested HTTP 409 identity refusal before dispatch.
An incomplete or unattested reply after send is `unknown`. The transport must
not replay an ambiguous invocation. Declared idempotency and durable execution
policy control application-level retries.

An expired deadline or canceled caller cancels the HTTP/2 stream and the
worker invocation context. A handler may finish after cancellation. Its
result is discarded, its scope is revoked, and its side effects are governed
by the operation's existing idempotency contract.

A stopping Host claims no further durable work. A running durable execution
keeps its lease for the drain window; one that has not finished by then is
handed back with its attempt, so a deployment neither spends an attempt nor
leaves the job waiting for its lease to expire. At startup the Host refuses
to register a durable revision while queued or running jobs still belong to
another one (`migration_required`).

## Tools and assistant ingress

The [worker bundle manifest](../schemas/scenery.worker-bundle.schema.json)
projects MCP capabilities and assistant surfaces from the compiled graph. Each local capability names its worker binding, including its
input and output schemas, execution address, size limits, and approval policy.
MCP requests cross the same ABI as HTTP and internal bindings. The host chooses
the worker generation and supplies the principal and tenant from the
authenticated assistant run.

An assistant exposes the declared conversation, turn, event, approval, and
cancel routes. The host seals public conversation and approval handles, checks
ownership, and translates private runtime events to public events. A capability
requiring approval can consume one grant bound to the run, tool, and proposed
arguments. Control endpoints and MCP assertions stay on loopback.

Development uses the CLI's managed assistant supervisor. A production host can
use a separate supervisor via the existing private assistant runtime descriptor
and token key settings. The bundle manifest contains the declarations and expected
revisions. Runtime credentials stay in the private descriptor file.

The manifest's `reports` member carries the schema revision and producer
version of `scenery.failure.report`. The Host stamps them on every failure
report it records and attaches sanitized invocation metadata (up to 256 KiB,
scrubbing auth, cookie, and secret headers) so `scenery inspect report <token> -o json`
reads Host reports like the CLI's own and `scenery replay <token> [--emit-test]`
can deterministically reproduce the failure or emit a regression test.
