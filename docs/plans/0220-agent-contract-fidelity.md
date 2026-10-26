# Agent Contract Fidelity And Verified Change Loop

## Purpose / Big Picture

Agents working on a Scenery application lose time when an interface states one
contract and returns another, when they must poll to learn whether their change
is running, and when a failed call cannot be tied to the code that served it.
This plan implements the developer's eight-point review (2026-10-19, baseline
`6fbc39e5a3dfb363ffc4c15d82f158d8bc3f6371`) in the registered feature
`agent-contract-fidelity` at `/Users/petrbrazdil/Repos/scenery-agent-contract-fidelity`
on branch `feat/agent-contract-fidelity`.

After this work an agent can trust that every MCP tool's advertised output schema
describes the structured result it receives, that a framework failure carries a
stable problem code, that `scenery help ... -o json` matches the real parser
(including application CLI bindings), that read-only agent requests do not
recompile the graph, that documentation excerpts and resource listings are
reachable in bounded pages, that record fields carry meaning and validated
examples, that it can wait for its exact source snapshot to be served or for a
durable execution to finish, and that it can invoke a declared CLI binding inside
the running development runtime with the serving generation and trace identity in
the result.

Terms: the *development runtime RPC* is the JSON-RPC surface at `/runtime`
documented in `docs/local-contract.md`; a *generation* is one published set of
service processes behind the stable process host; the *source snapshot digest*
(`source_snapshot_digest`) identifies the exact source inputs a generation was
built from.

## Progress

- [x] (2026-10-19 13:10Z) Verified every cited finding on the baseline and
  created the feature worktree with `scenery feature create`.
- [x] (2026-10-19 14:20Z) MCP schemas: durable tools advertise
  `{outcome: "accepted", receipt}`; tagged unions project the flattened
  discriminator wire form (with `wire_tag`, closed records and open unions);
  every local tool advertises the framework failure variant; outcome names
  `accepted`/`failed` are reserved for MCP-projected operations. Federated tools
  relay the remote `CallToolResult` natively. Gateway conformance test validates
  received structured content against the advertised schema with a real JSON
  Schema validator (`internal/mcpgateway/conformance_test.go`).
- [x] (2026-10-19 14:20Z) Framework failures keep typed `errs` codes from the
  runtime dispatcher across the process link to the gateway problem code.
- [x] (2026-10-19 14:50Z) `agent serve` reuses the last compiled graph while
  `compiler.SnapshotUnchanged` holds and answers `schema.get` without compiling.
- [x] (2026-10-19 15:30Z) Help drift test runs every standalone parser and
  requires its help descriptor to document exactly its flags; fixed `up`,
  `logs`, `generate`, `inspect`, `db`, `snapshot`, `traces`, `metrics`, `system`.
  Application CLI bindings answer `help -o json` with `application_binding`.
- [x] (2026-10-19 15:55Z) Docs excerpts reach oversized lines and report
  `end_byte`; `resources.list` pages with `limit`, `summary` and bound cursors.
- [x] (2026-10-19 16:20Z) Record fields declare `description` and validated
  `examples` (SCN1234, exact wire form); outcomes declare `description`; MCP and
  OpenAPI publish both.
- [x] (2026-10-19 16:50Z) Development runtime RPC `wait` with its own call class,
  `scenery ps --wait`, generated `waitForSource` and the checked
  `scenery.dev-runtime.wait` schema.
- [x] (2026-10-19 17:00Z) `scenery_execution_wait` framework MCP tool; status,
  cancel and wait advertise output schemas.
- [x] (2026-10-19 17:40Z) `--live` CLI binding invocation through RPC
  `cli/invoke` and the process host, with invocation, trace, generation and
  build identity; trace summaries carry the serving generation; development MCP
  adapter `scenery agent serve --mcp`.
- [x] (2026-10-19 18:10Z) Runtime acceptance on a copy of `testdata/apps/basic`
  with a CLI binding, and the selected landing gates (see Outcomes).

### Resume here

2026-10-19: implementation and validation are complete on
`feat/agent-contract-fidelity`. Next action: the developer decides on landing
(PR or local landing). Nothing is pushed.

## Surprises & Discoveries

- Every CLI flag set silently accepts `-o` because `parseCLIFlags` registers it
  when absent (`cmd/scenery/cli_flags.go`); the drift test ignores that flag.
- `scenery generate` has two parsers: `runContractGenerate` for contracts and
  `parseGenerateArgs` for `generate sqlc`; help documented `--check`/`--target`
  correctly but missed `--non-interactive`, `--quiet` and `--dry-run`.
- The checked help schema only allowed `--` prefixes in required combinations,
  so output-mode relationships (`-o json` requires `--detach`) were not
  expressible; the pattern is now `^-`.
- Sixteen documentation lines exceeded the 2,048-byte excerpt budget, two of them
  table rows that cannot be wrapped, so a code change was required in addition
  to wrapping prose.

## Decision Log

- 2026-10-19: Federated results are relayed as the remote `CallToolResult`
  instead of wrapping them in a Scenery envelope. The remote output schema then
  stays accurate and image/resource blocks stay typed. The `FederatedTools`
  interface is unchanged; its outcome value carries the remote result JSON.
- 2026-10-19: The framework failure is part of every local tool's advertised
  schema rather than an undocumented `isError` side channel, so a client that
  validates structured content never sees an unadvertised shape. This reserves
  the outcome names `accepted` and `failed` for MCP-projected operations.
- 2026-10-19: Help/parser unification uses a test that observes the flag sets
  the real parsers register instead of rewriting 60 parsers onto a new
  declaration table; drift now fails the build either way.
- 2026-10-19: CLI bindings stay out of the service adapter's covered addresses
  (that set feeds the service contract revision); service process plans list
  them separately so the host can route live invocations.
- 2026-10-19: `resources.list` reuses the context-continuation token format and
  TTL so there is one opaque continuation hash domain.

## Outcomes & Retrospective

All eight review points are implemented. Evidence (2026-10-19):

- Live acceptance (isolated agent home, prepared framework executable): `ps
  --wait` reported `served` for generation 1; `echo-msg hi --live` returned
  `echo:hi` with generation 1, invocation and trace IDs; after editing the
  handler body `ps --wait` reported `served` for generation 2 after ~1 s and the
  live call returned `echo-v2:hi`; `traces list` showed the CLI traces with
  generations 1 and 2 and different build digests and the HTTP request trace
  with generation 2; a compile error made `ps --wait` exit 3 with `failed` and
  the compiler message while generation 2 kept serving; `help echo-msg -o json`
  returned `application_binding`; `agent serve --mcp` answered
  `runtime_status`, `binding_invoke` and `runtime_wait` against the runtime.
- `go test ./...`, `golangci-lint run ./...` (0 issues),
  `go run ./scripts/verify --summary --write` (pass with the 27 pre-existing
  knowledge-freshness warnings and the advisory suite-time warning), the
  dev-runtime client Bun test, and the landing gates `scripts/feature-check
  --base 6fbc39e5`, probes `cli-grammar`, `cli-process` and `observability` all
  passed.
- Not exercised end to end: the assistant MCP gateway with a live helper (its
  schemas and results are proven by the gateway conformance tests with a real
  MCP client and JSON Schema validator).

## Plan of Work

Milestones 1–4 are complete (see Progress). Remaining milestones:

5. Field metadata. Add optional `description` and `examples` to the
   `scenery.record.field` source schema (`internal/spec/source_schemas.go`), carry
   them through the compiled graph, validate each example against the field type
   in the compiler, and project them as JSON Schema `description`/`examples` in
   `internal/mcpprojection`.
6. Revision wait. Add a development runtime RPC method that captures the target
   source snapshot when called and waits, bounded by a timeout, until a
   generation built from that snapshot serves, a newer snapshot supersedes it,
   the build fails or blocks, or the timeout elapses. Expose it as
   `scenery up wait` (exact grammar in `docs/local-contract.md`).
7. Durable wait. Add `scenery_execution_wait` beside the status and cancel tools:
   it waits for a terminal state of an owned execution up to a bounded timeout,
   and a timeout never claims completion or cancellation.
8. Live invocation. `scenery <binding command> --live` forwards the decoded input
   to the running runtime through the development runtime RPC and returns the
   ordinary CLI envelope plus `invocation_id`, `trace_id` and the serving
   `generation`/`build_input_digest`.

## Validation and Acceptance

Changed-area classes: CLI process, runtime HTTP/RPC, MCP gateway/projection,
compiler/spec schemas and documentation. This is a source and contract change,
so select full after editing, from the repository root
`/Users/petrbrazdil/Repos/scenery-agent-contract-fidelity`:

```sh
go run ./scripts/verify --summary --write
```

Then read `agent-context-summary.json` and run the exact
`changed_area.recommended_commands` union from `agent-context.json`, plus:

```sh
golangci-lint run ./...
go test ./internal/mcpprojection ./internal/mcpgateway ./internal/mcpfederation ./internal/contractagent ./internal/graph ./internal/machine ./runtime ./cmd/scenery
```

Runtime acceptance: in a copy of a fixture app run
`.scenery/harness/bin/scenery up --detach -o json`, edit a handler body, call
the revision wait and observe `served` with the new source snapshot digest, then
invoke a CLI binding with `--live` and observe the serving generation and trace
identity in the envelope.

## Idempotence and Recovery

All steps are source edits, tests and local runtime runs in the feature worktree.
Re-running the verifier or tests is safe. Stop any runtime started for acceptance
with `.scenery/harness/bin/scenery down --app-root <copy>`; do not delete data of
runtimes this plan did not start. If a milestone is abandoned, revert its commit
on the feature branch; earlier milestones are independent commits.
