# MCP-Native Scenery Assistants with a Managed Node/V8 Helper

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

This plan introduces application-facing assistants without exposing the
underlying agent framework to end users. MCP is the permanent capability
boundary. The public conversation protocol, generated clients, routes, errors,
events, identifiers, and browser artifacts are owned by Scenery. The initial
developer-facing runtime adapter uses Eve, but that name is permitted only in
developer and operator implementation surfaces.

The implementation deliberately does not link Node, V8, libnode, or any C++
embedding shim into the Go process. Development runs a managed Node/V8 child
process. Production builds embed a compressed, platform-matched Node runtime
and a compiled assistant capsule as inert bytes in the app binary, extract them
to a content-addressed runtime directory, and execute Node as a supervised
child process.

## Purpose / Big Picture

A Scenery developer should be able to declare an assistant, expose selected
application operations as MCP tools, add ordinary hand-authored capabilities,
and run the complete application with `scenery up`. The developer may edit the
assistant's instructions, skills, tools, models, subagents, channels, and evals
in the provider-native source tree. Scenery prepares the MCP connection,
private control channel, runtime capsule, public API, generated browser client,
process supervision, authentication propagation, authorization, inspection,
build packaging, and deployment wiring.

An end user should see only a Scenery assistant. The user must not see or infer
the provider name from supported public surfaces. In particular, the provider
name must not appear in public route paths, JSON keys, event types, generated
browser packages, public OpenAPI, public JSON schemas, HTTP headers, cookies,
source maps, normal public errors, model output, or public inspection payloads.
Developer-only source, package locks, implementation diagnostics, private
runtime descriptors, and explicit implementation inspection may name the
provider.

The completed vertical slice must make all of the following observable:

1. A package-local Scenery operation can opt into MCP with a canonical
   `binding` whose protocol is `mcp`.
2. A root `mcp_server` can compose those local bindings and declared external
   MCP connections behind one private Scenery MCP endpoint.
3. A root `assistant` can bind that MCP server to an authored assistant source
   tree and a Scenery-owned public conversation surface.
4. `scenery up` starts the Go app and one managed Node/V8 child for each
   declared assistant, waits for revision handshakes, and reports typed health.
5. A browser can create a conversation, stream normalized NDJSON events,
   approve a capability call, submit a follow-up turn, reconnect from a cursor,
   and cancel an active run without importing any provider package.
6. The runtime authorizes every MCP tool invocation independently of model
   visibility or approval state.
7. Developers can add capabilities in two supported ways: ordinary Scenery
   operations plus MCP bindings in Go application code, and provider-native
   tools or skills under the authored assistant directory.
8. A production `scenery build` remains a single app binary. When an assistant
   is declared, that binary contains compressed child-runtime assets, extracts
   them safely, and starts them out of process.
9. A public-surface conformance gate proves that the forbidden provider token
   and known provider-specific signatures do not escape.

This plan does not build a generic JavaScript runtime, embed V8 in Go, expose a
public MCP server, implement arbitrary user-code execution, or replace
Scenery's existing local control-plane agent.

## Progress

- [x] 2026-08-03 23:26Z: Agreed that MCP is the sole capability ABI and that
      the end-user conversation protocol remains Scenery-owned.
- [x] 2026-08-03 23:26Z: Agreed to use a managed Node/V8 helper process and to
      reject in-process Node, V8, libnode, and cgo embedding.
- [x] 2026-08-03 23:26Z: Agreed that the provider name is developer-visible but
      forbidden on supported end-user surfaces.
- [x] 2026-08-03 23:26Z: Verified that `0146`, `0147`, and `0148` are already
      allocated and selected permanent plan ID `0149`.
- [x] 2026-08-03 23:26Z: Verified that MCP `2025-11-25` is the current stable
      release and selected official Go SDK `v1.6.1`; the `2026-07-28` line is
      still an RC and is not the initial production baseline.
- [x] 2026-08-04: Added this plan as
      `docs/plans/0149-mcp-native-scenery-assistants.md`, linked it from
      `docs/plans/active.md`, and refreshed `docs/knowledge.json`.
- [x] 2026-08-04: Recorded the activation baseline. The worktree already had
      unrelated edits in `apps/console/package.json` and
      `apps/console/bun.lock`, so the whole-worktree changed-area union was
      `cli-json-contract` plus `dashboard`. The recommended commands were
      `.scenery/harness/bin/scenery harness self --quick --summary --write`,
      `.scenery/harness/bin/scenery harness ui -o json --write`,
      `cd apps/console && bun run lint && bun run typecheck && bun run build`,
      and `go test ./cmd/scenery`.
- [x] 2026-08-04: Executed the activation validation union. The console lint,
      typecheck, and production build passed; `go test ./cmd/scenery` passed;
      and the UI harness returned sanitized `SCN9000` report token
      `rpt_taweu25rl4beor3bpjkkxm7nz4`. The quick self-harness result and its
      intentional current MCP-guardrail conflict are recorded below.
- [x] 2026-08-04: Completed the implementation baseline from repository commit
      `5694410935306e6505ce725fead9cce578b34e0c`.
      `./scripts/build-dashboard-ui-embed.sh` passed without adding a diff,
      `go test ./...` passed, and the full self-harness passed every step
      except the already recorded MCP architecture guardrail. The expanded
      native fixture contained 18 resources and three bindings with protocols
      `http`, `internal`, and `cli`; its workspace revision was
      `sha256:1a1ea54be2d578b212d35a2fa4f2fd71eb875d515440a5434ebbcd810b421e44`
      and contract revision was
      `sha256:b504399530e7736a311ddeac9018da3677c136f193787017e6b5af5c104c2f33`.
- [x] 2026-08-04: Narrowed the obsolete architecture residue guardrail before
      adding the canonical source contract. Bare `MCP` and `Model Context
      Protocol` terminology is now allowed, while the retired pre-MCP agent
      RPC, host-owned MCP transport, experimental agent client, browser-debug
      MCP bridge, and legacy SSE
      signatures remain rejected. The focused harness tests passed, followed by
      `.scenery/harness/bin/scenery harness self --quick --summary --write` with
      all eight quick steps green, including architecture checks, affected
      package tests, and schema validation.
- [x] 2026-08-04: Completed Milestone 1. Canonical source/spec/compiler/graph
      support now covers MCP bindings, connections, servers, and assistants;
      formatter/CST round trips, deterministic provenance, exact revision
      domains, stable SCN2419-SCN2430 diagnostics, and semantic evolution rules
      are tested. Native and dedicated assistant fixtures compile
      deterministically, native `check` is clean, and the two required fixture
      generator commands were each run twice with `changed: []` on the second
      run.
- [x] 2026-08-04: Completed Milestone 2. Added the exact official Go SDK
      `v1.6.1`, provider-neutral manifest/wire contracts, canonical expanded-
      graph projection with a checked-in byte golden, a loopback-only
      Streamable HTTP gateway constrained to MCP `2025-11-25`, and generated
      runtime registration through the existing policy/direct-or-durable
      execution paths. In-process SDK tests cover negotiated version,
      principal-filtered listing, read/write/declared-error calls, durable
      receipt/status/cancel, cancellation, per-request assertion context,
      stale revisions, body/result limits, session-principal swaps, and public
      route absence. Focused tests, MCP/runtime race tests, and `go test ./...`
      pass.
- [x] 2026-08-04: Completed Milestone 3. Added strict provider-neutral public
      and private contracts, sealed owner-bound conversation and approval
      handles, a typed helper lifecycle, a deterministic Go fake, five
      generated public conversation routes, native auth/policy propagation,
      anonymous initiator identity, reconnect/cancel/approval behavior,
      incremental NDJSON normalization, streaming lexical redaction, exact
      public/private JSON schemas, and composition-owned assistant
      registration. Focused package, CLI-schema, generator, runtime, race, and
      repository validation pass.
- [x] 2026-08-04: Completed Milestone 4. Added one Scenery-owned federation
      lifecycle per canonical MCP server using the official Go SDK, exact
      `2025-11-25` negotiation, Streamable HTTP, static no-auth/bearer/header
      auth, provider-backed symbolic secret resolution, deterministic filtered
      namespaces, conservative local policy, required/optional readiness,
      notification and TTL refresh, bounded metadata/pagination/results, and
      private generated composition registration. Focused, race, vet,
      deterministic generation, fixture compilation, and repository tests
      pass.
- [x] 2026-08-04: Added managed Node `24.18.0` to both checked-in
      toolchain manifests for `linux/amd64` and `darwin/arm64`. The official
      clear-signed SHASUMS verified with Node's release keyring as Richard
      Lau's key `C82FA3AE1CBEDC6BE46B9360C43CEC45C17AB93C`; the downloaded
      Darwin archive matched the signed digest. Managed sync/strict verify
      pass, and the managed binaries report Node `v24.18.0` and npm `11.16.0`.
- [x] 2026-08-04: Installed exact `eve@0.29.5` with managed npm in the
      developer-only adapter fixture. The lock digest is
      `sha256:d51e5dbc632e4ce1f5d78d6b6ef4d55b38e07c1ced48bb3229c1d62601810dae`.
      Before adapter implementation, read the installed README, project
      structure, TypeScript API, MCP connection, custom channel, auth, HITL,
      sessions/streaming, client streaming, instrumentation, CLI/build, and
      self-hosted deployment guides.
- [x] 2026-08-04: Completed Milestone 5. Added the developer-only Eve adapter
      with transient reserved-path-safe overlays, strict provider-neutral
      control records, owner-bound short-lived MCP assertions, deterministic
      reconnect cursors, parallel action/request normalization, generated
      loopback channel and Scenery MCP connection, and a managed-Node
      bootstrap. A real Node `v24.18.0` child running Eve `0.29.5` invoked an
      authored `local` tool, discovered and proposed `scenery__echo`, paused
      for approval, resumed from sequence 8, and completed the actual private
      MCP call with `Scenery-Assistant-Assertion` on every request.
- [x] 2026-08-04: Completed Milestone 6. Added strict provider-neutral helper
      clients, app-child private MCP gateways, persisted token keys, atomic
      multi-assistant replacement, bounded late-helper retry, deterministic
      Node/capsule archives, content-addressed extraction, tamper recovery,
      direct production launch, typed status/inspection/doctor surfaces, and
      dev reload/crash supervision. The production helper executes the
      immutable capsule entry from a private writable home rather than using
      the verified capsule as its working directory.
- [x] 2026-08-04: Completed Milestone 7. Generated clients expose the five
      provider-neutral conversation methods plus a headless React hook with
      fatal UTF-8 decoding, bounded reconnect backoff, cursor resume, and
      duplicate suppression. `assistant init` now plans and applies canonical
      source plus exact scaffold files through the workspace transaction,
      supports dry-run, and preserves every existing authored file;
      `assistant sync` validates the exact Eve 0.29.5 lock and reuses a
      content-addressed managed-Node dependency cache without changing the
      manifest or lock. Focused, race, full CLI, TypeScript, conformance,
      schema, leak, and repeated-generation checks pass.
- [x] 2026-08-04: Completed Milestone 8. The deterministic acceptance script
      passed all 24 reported setup and behavioral cases twice, including the
      real Go app, managed Node helper, local/durable/federated/provider-local
      tools, allow and deny approval, owner isolation, reconnect, stale
      revision, helper crash/restart, normalized outage, production extraction,
      a clean-PATH production launch, and the provider-leak gate.
- [x] 2026-08-04: Final validation passed: managed Node sync/strict verify,
      every changed-area package command, the full race lane, both fixture
      regenerations at `changed: []`, dashboard lint/typecheck/build, browser UI
      harness against `testdata/apps/basic`, `go vet ./...`, and the full
      worktree-local self-harness with every executable step green.
- [x] 2026-08-04: Moved the completed plan from `docs/plans/active.md` to
      `docs/plans/completed.md` after all acceptance criteria passed.

## Surprises & Discoveries

- 2026-08-04: The activation quick self-harness passed the knowledge contract,
  changed-area oracle, documentation inspection, contract drift checks, and
  schema validation, but failed architecture checks with 100 errors because
  `cmd/scenery/harness_arch.go` deliberately rejects `MCP` as a removed
  agent transport. The errors are confined to the new plan and its two index
  entries: 94 in this plan, four in `docs/knowledge.json`, and two in
  `docs/plans/active.md`. Milestone 1 must deliberately replace or narrow
  that obsolete guardrail as part of establishing the new canonical MCP
  contract; plan activation does not silently weaken it.
- 2026-08-04: The quick harness executable is a worktree-local built artifact.
  The first full self-harness after changing `cmd/scenery/harness_arch.go`
  refreshed `.scenery/harness/bin/scenery`; a subsequent quick self-harness
  exercised the narrowed scanner and passed. This is why validation must not
  infer current scanner behavior from a stale harness binary.
- 2026-08-04: The first native revision golden exposed that child-schema field
  domains do not automatically control the parent resource field projection.
  `assistant.implementation` initially entered the contract revision and
  `mcp_connection.tools` initially did not. Explicit parent-field overrides now
  make implementation changes implementation-only and tool filters contract
  changes; the native golden proves both boundaries.
- 2026-08-04: The official SDK intentionally negotiates older MCP revisions by
  default and reads a Streamable HTTP POST body before dispatch. The private
  gateway therefore performs an outer exact-version precheck and wraps the
  body with `http.MaxBytesReader`; SDK client tests prove the negotiated result
  is exactly `2025-11-25` and older/missing session revisions fail closed.
- 2026-08-04: Generated operation codecs emit Scenery's internal
  `{kind,name,value|problem}` envelope. The runtime bridge must decode that
  envelope and translate it to the MCP-neutral
  `{outcome,value|problem}` envelope; treating the generated bytes as a generic
  completed value would double-wrap results and lose declared-error semantics.
- 2026-08-04: The public approval vocabulary is `approve`/`deny`, matching the
  browser contract, while the private helper control vocabulary is
  `allow`/`deny`. The gateway performs the one explicit translation so a
  private spelling cannot become a browser compatibility constraint.
- 2026-08-04: A streaming endpoint cannot replace an HTTP status after valid
  bytes have been flushed. The gateway returns a neutral HTTP error when the
  first private event is malformed, but after streaming begins it emits a
  monotonic provider-neutral `assistant.run.failed` event and terminates. A
  pipe-backed test proves the first public event arrives before helper EOF and
  that late malformed bytes never escape.
- 2026-08-04: Milestone 3 deliberately uses one process-random fallback key
  when generated composition has not supplied persistent token keys. It fails
  closed on entropy failure but handles do not survive a process restart. The
  persisted framework/local secret and production-key doctor check remain
  mandatory Milestone 6 work.
- 2026-08-04: Root assistants do not belong to an arbitrary native service
  adapter. Generated composition now contributes a synthetic
  `scenery/assistants` contract registration, so assistant-only applications,
  ownership verification, Seal rollback, and deterministic ordering use the
  same registry transaction as other generated runtime surfaces.
- 2026-08-04: The canonical `mcp_server.connection` source shape has no
  per-remote approval or effect fields. Federation therefore cannot safely
  infer policy from untrusted MCP annotations; every remote tool currently
  receives the fixed local default `approval = always`, `destructive = true`,
  and `open_world = true`. A future authored policy requires an explicit
  source-contract change rather than a private adapter convention.
- 2026-08-04: Streamable HTTP's GET is a long-lived server-event stream, so a
  generic response-body cap breaks tool-change notifications. The transport
  applies the JSON response limit to POST only, bounds DELETE shutdown
  separately, disables ambient proxies, and rejects redirects so credentials
  cannot cross an origin boundary.
- 2026-08-04: `InitializeServices` aborts the whole Go application when an
  initializer returns an error. Required remote MCP outages are instead
  retained as live typed federation readiness while initialization succeeds;
  the background refresh retries unavailable connections and clears readiness
  automatically after recovery.
- 2026-08-04: Official Node homes express bundled npm as a parent-relative
  `bin/npm -> ../lib/node_modules/npm/bin/npm-cli.js` symlink. The existing
  toolchain extractor skipped every link containing `../`, so the first
  managed install had Node but no npm. Extraction now resolves the target and
  accepts it only when it remains beneath the artifact home; a focused test
  proves the bundled shape and rejects an escaping link.
- 2026-08-04: The installed Eve `0.29.5` package has no public model-endpoint
  override. Its supported deterministic test surface is `mockModel()` from
  `eve/evals`, which implements the AI SDK model in process without network
  access. Milestone 5's private real-process proof uses that pinned API rather
  than inventing a provider-specific endpoint option.
- 2026-08-04: Eve's durable event feed remains open after
  `session.waiting`, and its provider event index does not equal Scenery's
  filtered private sequence. The generated channel therefore closes each HTTP
  response at a session boundary, retains the provider cursor plus normalized
  private history per session, and replays only records after the requested
  Scenery sequence. The approval proof resumed after sequence 8 and returned
  sequences 9 through 13 without duplicates.
- 2026-08-04: The first post-M5 repository suite exposed a federation shutdown
  hang during an MCP initialize request. The official SDK detaches its
  connection lifecycle context and may issue a cancellation notification
  after the caller context is cancelled. The Scenery auth transport now
  rejects every request after its connection shutdown signal, explicitly
  cancels the active cloned HTTP request, and keeps that cancellation alive
  until the response body closes. Focused shutdown, package, race, and vet
  tests pass; the original official SDK connection path and standalone SSE
  behavior remain intact.
- 2026-08-04: A private MCP gateway in the `scenery` CLI supervisor can project
  the compiled graph but cannot execute generated local tools or use the live
  federation: those registrations exist only in the generated application
  process. The private gateway therefore starts as an app-child runtime
  service, depends on the canonical federation service, and dispatches through
  the app's real `MCPToolDispatcher`. A signed HTTP test proves that path calls
  a generated-style local registration.
- 2026-08-04: Starting the helper only after the app-owned gateway creates a
  lifecycle inversion if app initialization synchronously waits for helper
  health. Runtime bootstrap now commits an initial neutral unavailable state,
  returns promptly, and owns one bounded background retry loop. This lets the
  public app and private gateway start first while the supervisor brings up the
  helper against stable private control and MCP slots.
- 2026-08-04: The first Go fixture materialization after adding embedded
  assistant composition did not reach a fixed point. The generated MCP
  manifest embedded `workspace_revision`, while the workspace revision walker
  re-hashed generated Go beneath an overlapping `implementation_root` even
  though `internal/scenerygen` was a declared managed generated root. The
  compiler now excludes managed roots from implementation-root revision walks;
  a focused regression and two consecutive native materializations prove the
  stable revision
  `sha256:fcb0641abd841af64e449d2f23bc0cd4b63282e9578d16b9dfdd83d9e609c7ec`
  and `changed: []` on the second run.
- 2026-08-04: Schema identities are logical names, not structural revisions.
  The self-harness exposed fixtures that repeated the logical MCP/control
  protocol identity in `schema_revision`; current manifests and runtime
  descriptors now carry exact content-derived structural digests while keeping
  their stable `kind` and protocol identities separate.
- 2026-08-04: Architecture residue scanning must match identifier tokens, not
  arbitrary substrings. The first provider-leak pass found obsolete names only
  as fragments inside current identifiers; boundary-aware matching preserves
  the intended ban without rejecting unrelated current source.
- 2026-08-04: The durable assistant fixture exposed an escaped formatting
  directive in generated Go: a template emitted `%%T` into source rather than
  `%T`. A generated-source compile regression now fixes and protects the
  declared-error type path.
- 2026-08-04: The Go control client validated assistant, runtime, and capability
  revisions after helper responses, but a raw request to the private helper
  could bypass that check. The generated helper channel now rejects every
  mismatched request revision with the neutral `revision_mismatch` response
  before provider dispatch.
- 2026-08-04: The standalone assistant fixture initially changed after every
  materialization because generated roots were not declared as managed roots
  in its authored workspace. Declaring the generated Go and TypeScript roots
  made two consecutive materializations converge to `changed: []`.
- 2026-08-04: Durable execution requires the app database even when the
  assistant's direct calls are otherwise self-contained. Development proof now
  uses the fixture's declared managed PostgreSQL service; production proof
  resolves the exact app database from `scenery db list` plus managed server
  state and never falls back to the shared administrative database.
- 2026-08-04: Supervisor manifest publication precedes the final API/helper PID
  population. Real-process acceptance therefore keeps resolving the manifest
  until both processes exist and the recorded API process demonstrably owns
  its listener, rather than treating the first readable manifest as ready.
- 2026-08-04: Assistant assets are executable only in the current `artifact`
  build role; the `development` role is supervisor-managed and does not embed
  the production capsule. Milestone 8 production proof therefore builds the
  artifact target and records this implementation/plan wording distinction.
- 2026-08-04: Embedding the complete opaque conversation token inside an
  approval token made valid handles recursively exceed the 1 KiB token limit.
  Approval claims now carry the domain-separated conversation digest and
  compare it in constant time; a regression binds an approval to a valid
  conversation token larger than 1 KiB without weakening ownership.
- 2026-08-04: The first published helper PID is transient while the dev
  supervisor completes replacement. Acceptance now requires three consecutive
  current owned PID samples before crash/restart assertions, preventing a
  manifest publication race from masquerading as helper instability.
- 2026-08-04: Running the production helper with the verified capsule as its
  working directory let the provider create relative state inside an otherwise
  immutable content-addressed tree. The second process correctly rejected the
  mutated tree. Production now keeps the capsule as an immutable import source
  and uses the existing private writable helper home as `cmd.Dir`; the complete
  acceptance suite passes twice against the reused extracted assets.
- 2026-08-04: The machine-installed `scenery v0.3.5` left generated fixture
  `go.work` files that redirected storage probes to the old module and applied
  the old blanket MCP architecture guardrail. Removing those generated files
  and rerunning with `.scenery/harness/bin/scenery` produced the current
  result: all self-harness steps pass.
- 2026-08-03: The next plan number was not `0146`; completed plans already use
  `0146` through `0148`. Evidence: `docs/plans/completed.md` and repository
  search for those IDs. This plan uses `0149`.
- 2026-08-03: MCP `2026-07-28` is still published as a release candidate even
  though its nominal revision date has passed. The stable release remains
  `2025-11-25`, and the official Go SDK's latest stable line is `v1.6.1`.
  Evidence: the official MCP and Go SDK release pages.
- 2026-08-03: Eve custom channels accept caller-chosen continuation tokens and
  expose durable event streams with a start index. This permits Scenery to own
  opaque conversation identity and reconnect semantics without exposing the
  provider's default channel.
- 2026-08-03: The repository's existing operation, execution, binding, policy,
  and generated-client kernel is already the right manual-capability model.
  MCP should be a new binding protocol, not a parallel `mcp_tool` declaration
  that bypasses application semantics.
- 2026-08-03: Public opaque identifiers encoded with base64 could accidentally
  contain the forbidden provider token. Public assistant identifiers and
  sealed handles therefore use a versioned prefix plus hexadecimal encoding.
  Hexadecimal cannot spell the token because its alphabet does not contain
  `v`.

Add later findings here with the command, test output, trace, revision, or
artifact that exposed each finding.

## Decision Log

- **Decision:** MCP is the only capability protocol between the application
  assistant runtime and Scenery.
  **Rationale:** It gives Scenery one provider-neutral tool ABI for local
  operations, external servers, evals, developer tooling, and future runtimes.
  **Date/Author:** 2026-08-03, user and plan author.

- **Decision:** Replace the old blanket MCP-token architecture ban with a
  narrow legacy-transport residue list, and leave provider-specific public
  leakage to the dedicated Milestone 8 gate.
  **Rationale:** MCP is now a canonical Scenery capability ABI, so rejecting
  its name conflicts with the new contract. The narrower list still prevents
  accidental restoration of the retired agent RPC and host-owned transport,
  while the later public-surface scanner has the correct scope for provider
  leakage.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** The browser conversation protocol is not MCP and is not the
  provider's default HTTP channel.
  **Rationale:** Conversation streaming, ownership, approvals, cancellation,
  reconnect cursors, and public compatibility are application concerns. A
  Scenery-owned protocol also prevents provider-specific paths and event shapes
  from becoming public contracts.
  **Date/Author:** 2026-08-03, user and plan author.

- **Decision:** Model local application capabilities as ordinary operations
  with `binding.protocol = "mcp"`.
  **Rationale:** Generated dispatch then preserves authentication,
  authorization, middleware, tracing, idempotency, direct or durable execution,
  and declared outcomes. No new Go registration side channel is required.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** Add concrete `assistant`, `mcp_server`, and `mcp_connection`
  resources rather than a public generic extension/resource mechanism.
  **Rationale:** Scenery's public source language remains singular and
  auditable. These resources express actual application semantics and can be
  evolved with normal graph tooling.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** Reserve SCN2419 through SCN2430 for the canonical MCP and
  assistant compile-time diagnostics.
  **Rationale:** The contiguous range gives invalid binding shape, tool
  identity, projection type, sensitive output, effect metadata, route
  collision, source path, exact package lock, adapter, external transport,
  external auth, and reference-cycle failures stable machine identities.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** External tool filters are optional exact `allow` or `block`
  sets; neither means unfiltered and authored source specifying both is
  rejected.
  **Rationale:** This preserves the plan's exact-filter semantics without
  forcing a redundant allow-all list or adding precedence rules.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Use MCP revision `2025-11-25` and
  `github.com/modelcontextprotocol/go-sdk v1.6.1`.
  **Rationale:** Both are stable as of plan authoring. Do not begin production
  support on the `2026-07-28` RC or a pre-release SDK.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** The private capability manifest has logical identity
  `scenery.mcp-capability-manifest`, an exact content-derived structural schema
  revision, and framework tool names
  `scenery_execution_status` and `scenery_execution_cancel`.
  **Rationale:** The underscore names obey the same portable lower-case MCP
  tool-name contract as application capabilities. The manifest carries only
  provider-neutral addresses, schemas, policies, effects, limits, origins,
  and revisions; connection URLs and credentials are excluded.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Pin Node `24.18.0` and initially scaffold `eve@0.29.5` with an
  exact `package-lock.json`; use the npm bundled with the pinned Node
  distribution.
  **Rationale:** Eve currently requires Node 24 or newer. One managed Node home
  is enough for Node, V8, and npm, avoiding an ambient runtime and a second
  package-manager dependency. Before writing adapter code, install the exact
  package and read `node_modules/eve/docs/README.md` and the relevant bundled
  guides. If `eve@0.29.5` is not present in the configured registry, stop that
  milestone, record the evidence here, and replace it only with an exact
  non-prerelease version; never use `latest`, `^`, `~`, a Git branch, or an
  unrecorded version.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** Interpret the deterministic fake-model requirement through
  Eve `0.29.5`'s public `mockModel()` API, not an HTTP model endpoint.
  **Rationale:** The pinned package exposes no endpoint override, while
  `mockModel()` is deterministic, requires no model credentials or network,
  and exercises the same compiled helper process and channel/tool runtime.
  This changes only the developer-only acceptance fixture.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** The helper bootstrap receives the complete provider-neutral
  `SCENERY_ASSISTANT_CONTROL_ADDR` and derives the loopback listen port only
  after strict URL validation; it does not introduce a separate ambient port
  variable.
  **Rationale:** Supervision owns one private control address. One exact input
  prevents address/port drift and keeps the child environment aligned with the
  provider-neutral runtime contract.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Development and production hand all private helper descriptors
  to the generated app through one mode-0600
  `SCENERY_ASSISTANT_RUNTIME_CONFIG` file; the app child, not the CLI parent,
  owns each private MCP gateway.
  **Rationale:** One multi-assistant descriptor transaction keeps control
  tokens, bridge secrets, loopback slots, and exact revisions consistent while
  placing capability dispatch in the only process that owns generated local
  registrations and live federation state.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Assistant continuation handles use a stable 32-byte
  framework-owned token key from a private local file or the existing
  production secret environment; there is no process-random fallback.
  **Rationale:** Handles must survive helper and app restarts, replicas need one
  shared key, and an invalid or missing key must fail closed without crashing
  unrelated application routes.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** The Node/V8 runtime is always a child process.
  **Rationale:** Process isolation gives a hard crash and cancellation boundary,
  preserves Go's build architecture, and avoids libnode's unstable embedding
  API, cgo, C++ toolchains, and V8 thread-affinity hazards.
  **Date/Author:** 2026-08-03, user and plan author.

- **Decision:** Development executes the managed Node home directly; production
  embeds compressed Node and assistant capsules into the app binary and
  extracts them to a verified content-addressed directory before spawning the
  child.
  **Rationale:** Development keeps rebuilds fast. Production preserves the
  single-binary app contract without running JavaScript in the Go process.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** A production helper executes its absolute capsule entry while
  using its private writable home as the process working directory.
  **Rationale:** Content-addressed capsule trees must remain immutable and
  reusable. Provider-created relative state belongs in private runtime state,
  never beneath the verified extraction root.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Public conversation handles are AEAD-sealed and hex-encoded.
  **Rationale:** A handle can carry the private session ID, private continuation
  token, assistant identity, owner digest, and token version without a new
  conversation mapping database or provider leakage. The hex alphabet also
  makes accidental occurrence of the forbidden provider token impossible.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** Approval claims bind to the domain-separated digest of the
  public conversation handle instead of embedding the complete opaque handle.
  **Rationale:** The binding remains exact and constant-time while avoiding
  recursive token growth beyond the strict public token-size limit.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Approval is a trusted interaction policy, not the authorization
  boundary.
  **Rationale:** The managed helper may ask for approval before an MCP call, but
  the Go gateway must still authenticate and authorize every invocation. Tool
  visibility and approval cannot grant application access.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** External MCP credentials terminate in Scenery.
  **Rationale:** The helper receives one private Scenery MCP connection.
  Scenery federates declared external servers, applies namespaces and filters,
  stores credentials, audits calls, and prevents third-party tokens from
  entering model context or provider state.
  **Date/Author:** 2026-08-03, plan author.

- **Decision:** Generated federation descriptors are private composition data,
  and carry only symbolic secret resource/store/key references; secret values
  are resolved by provider-registered runtime resolvers with no environment
  fallback.
  **Rationale:** Public capability manifests and browser clients remain free of
  URLs, auth metadata, timeouts, and credentials. Provider-backed resolution
  keeps deployment ownership explicit and lets the runtime scrub short-lived
  secret copies after constructing the Scenery-owned transport.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Until the source language defines an authored remote-tool
  policy, every federated tool requires approval and is classified as
  destructive and open-world; remote MCP annotations are ignored.
  **Rationale:** The canonical source has no trustworthy per-connection policy
  input. The conservative fixed policy fails closed without inventing an
  unversioned configuration surface or trusting remote hints.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** A required remote outage makes the shared federation and its
  assistants unready but does not fail Go service initialization; optional
  outages omit their tools and emit one rate-limited safe diagnostic.
  **Rationale:** The app must remain alive so health, diagnostics, and bounded
  recovery can operate. Live readiness still prevents tool use until every
  required connection is healthy.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Public approval decisions are `approve` and `deny`; private
  helper decisions are `allow` and `deny`.
  **Rationale:** Browser code receives the exact Scenery-owned vocabulary in
  the public JSON contract. One gateway translation keeps private helper
  vocabulary out of the public compatibility surface.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** The provider-neutral helper boundary streams strict private
  NDJSON through `io.ReadCloser`, and public forwarding is incremental.
  **Rationale:** Browsers must observe live assistant progress rather than a
  response buffered until helper EOF. Each private line is validated before
  its normalized public event is flushed; post-flush corruption becomes a
  neutral terminal event.
  **Date/Author:** 2026-08-04, implementation.

- **Decision:** Generated root assistant surfaces are owned by a synthetic
  composition registration rather than the first native service adapter.
  **Rationale:** Root resources need deterministic ownership even when an app
  has no native services. ContractRegistry Seal then validates, applies,
  snapshots, and rolls back assistant routes atomically.
  **Date/Author:** 2026-08-04, implementation.

Record every later material choice here before relying on it in code.

## Outcomes & Retrospective

Scenery now has one provider-neutral application-assistant vertical slice. MCP
bindings remain ordinary application bindings, the Go runtime owns all public
conversation, authorization, durable-execution, federation, and error
semantics, and the provider runs only behind a private supervised control
boundary. Development uses managed Node; production preserves the single-app-
binary contract by embedding verified Node and assistant capsule archives and
executing them as an out-of-process child.

The implementation shipped the canonical source/compiler/evolution contract,
official MCP Go SDK integration, local and federated dispatch, sealed owner-
bound handles, generated Go and TypeScript surfaces, assistant init/sync,
doctor/inspect/status integration, deterministic build assets, a realistic
fixture, and an independent provider-leak gate. The real-process suite passed
twice and covers the complete browser-to-helper-to-MCP path plus crash,
revision, isolation, denial, outage, and clean-PATH production cases.

The largest implementation lesson was that every mutable byte must remain
outside verified production capsules, even when the provider writes only
incidental relative state. The largest validation lesson was to distrust stale
installed harness binaries and generated fixture workspaces: final evidence
must come from the freshly built worktree-local binary. No acceptance criterion
or follow-up implementation work remains in this plan.

## Context and Orientation

### Repository boundaries

The canonical application model is `.scn`. `.scenery.json` carries independent
runtime configuration. `internal/scn` parses source, `internal/spec` owns the
current schema and diagnostic catalogue, `internal/graph` owns deterministic
resources and revisions, and `internal/compiler` produces source, effective,
and expanded graphs. Generation and runtime wiring consume those graphs; they
must not rediscover application semantics from Go or TypeScript source.

`internal/generate` produces Go contracts and composition, TypeScript clients,
OpenAPI, and other managed artifacts atomically. `internal/build` owns the
transient build workspace. `runtime` is linked into the generated app binary
and owns the one public app server. `cmd/scenery` owns CLI orchestration,
`scenery up`, child-process supervision, doctor, inspection, logs, and build
commands.

The existing `internal/agent` package is Scenery's local control-plane agent and
session router. It is not the application-facing assistant introduced here.
Do not add assistant semantics to `internal/agent`, and do not rename that
existing subsystem as part of this plan.

The existing `internal/contractagent` package exposes graph/evolution JSON-RPC.
It is also not the application assistant runtime and must remain separate.

### Terms

An **MCP binding** is a canonical Scenery binding that projects one operation
and execution as an MCP tool.

An **MCP server** is a graph resource that composes selected MCP bindings and
optional external MCP connections into one private logical capability surface.

An **assistant** is a graph resource that binds an MCP server, an authored
developer implementation, and a Scenery-owned public conversation surface.

The **helper** is the supervised Node/V8 child process. The helper runs the
developer's agent implementation and the generated provider adapter. It does
not serve public traffic.

The **control protocol** is a private, versioned Scenery protocol between the Go
runtime and the helper. It is separate from MCP.

A **public surface** is anything a normal application user or browser can
receive: route paths, response bodies, response headers, cookies, OpenAPI,
generated browser code, browser source maps, public events, public errors, and
default public inspection payloads.

A **developer implementation surface** includes authored assistant source,
package manifests and locks, generated server-only adapter source, private
runtime descriptors, private logs, and explicit implementation inspection.
Those surfaces may name Eve.

### Target runtime flow

```text
Browser
  |
  | Scenery assistant protocol
  v
Scenery Go app
  |-- verifies public conversation handle and principal ownership
  |-- normalizes events and errors
  |-- owns approval and cancellation endpoints
  |-- applies the public-output provider redactor
  |
  +--> private control HTTP on loopback
  |      |
  |      v
  |    managed Node/V8 helper
  |      |-- generated Scenery custom channel
  |      |-- authored Eve agent, tools, skills, subagents, evals
  |      `-- generated Scenery MCP client connection
  |             |
  +<------------+ private Streamable HTTP MCP on loopback
  |
  |-- generated local operation dispatch
  `-- federated external MCP clients
```

There is still one public server: the Go app. The helper and private MCP
listener bind only to loopback on randomly allocated ports and require
per-process authentication. No router record exposes either private listener.

### Proposed source contract

The first implementation must pin this source shape with parser, formatter,
schema, compiler, graph, provenance, and golden tests. Equivalent changes to
field spelling require a Decision Log entry before implementation; do not add
aliases.

A package-local operation becomes an MCP tool through an ordinary binding:

```hcl
binding "process_scene_mcp" {
  operation = operation.process_scene
  execution = execution.process_scene_direct

  protocol = "mcp"
  delivery = "call"

  exposure       = "application"
  authentication = std.authentication.inherit
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  mcp {
    name        = "process_scene"
    title       = "Process a scene"
    description = "Process one scene and return its declared outcome."

    read_only   = false
    destructive = false
    idempotent  = false
    open_world  = false
  }
}

export "process_scene_mcp" {
  value = binding.process_scene_mcp
}
```

The root composes capabilities:

```hcl
mcp_server "support" {
  capability "process_scene" {
    binding  = module.house.process_scene_mcp
    name     = "house__process_scene"
    approval = "always"
  }

  connection "docs" {
    connection = mcp_connection.docs
    namespace  = "docs"
    required   = false
  }

  max_input_bytes  = 262144
  max_result_bytes = 1048576
}
```

An external server is declared and owned by Scenery:

```hcl
mcp_connection "docs" {
  transport = "streamable_http"
  url       = "https://docs.example.test/mcp"

  auth {
    scheme = "bearer"
    secret = secret.docs_mcp_token
  }

  tools {
    allow = ["search", "fetch"]
  }

  connect_timeout = "5s"
  call_timeout    = "30s"
}
```

The initial external auth surface supports `none`, `bearer`, and one named
header backed by a Scenery secret. OAuth, interactive authorization, SSE-only
servers, and arbitrary header maps are outside this plan. Add them later as
explicit schema additions, not hidden config.

The root assistant is developer-aware and publicly provider-neutral:

```hcl
assistant "support" {
  mcp_server = mcp_server.support

  implementation {
    adapter      = "eve"
    source       = "./assistants/support"
    package      = "./assistants/support/package.json"
    package_lock = "./assistants/support/package-lock.json"
  }

  surface {
    gateway        = http_gateway.public_api
    path           = "/assistants/support"
    authentication = std.authentication.none
    authorization  = std.authorization.public
    pipeline       = std.pipeline.empty
    session_access = "initiator"
    client         = typescript_client.public_api
  }
}
```

`implementation.adapter` is deliberately developer-facing. It may appear in
source/effective/expanded compilation, explicit implementation inspection,
private descriptors, and developer logs. It must not be copied into public
client metadata or public runtime responses.

The first implementation supports only `session_access = "initiator"`.
Additional sharing models require a later plan because they change ownership
and privacy semantics.

### MCP projection rules

The compiler must reject an MCP binding unless:

- `protocol` is exactly `mcp`;
- the binding has exactly one `mcp` child;
- `delivery` is `call`;
- the operation input is `std.type.unit` or a record that can project to an
  object JSON Schema;
- every projected type has an exact contract JSON representation;
- the final tool name is unique within its `mcp_server`;
- declared idempotency hints do not contradict operation/execution semantics;
- a sensitive output is either excluded or explicitly admitted by a dedicated
  source attribute that defaults to false;
- authentication, authorization, and pipeline references resolve normally;
- a durable execution has a generated receipt/status/cancel projection;
- result and input limits are positive and bounded by framework maxima.

Tool names use lower-case semantic names and double-underscore namespaces:
`^[a-z][a-z0-9_]{0,127}$`. The final name is a contract. Renaming it is a
compatibility change.

Each local tool result uses this structured envelope:

```json
{
  "outcome": "processed",
  "value": {
    "status": "ready"
  }
}
```

The MCP result carries the same object as `structuredContent` and a canonical
JSON text item in `content`. `isError` follows Scenery's declared outcome
classification or a runtime execution error; it is not inferred from model
text.

The capability manifest has logical identity
`scenery.mcp-capability-manifest`. It contains stable tool IDs, names, titles,
descriptions, exact input/output schemas, operation and execution addresses,
effect hints, approval policy, limits, authorization references, origin
(`local` or `federated`), and the bound source/contract revisions.

### Public assistant protocol

For an assistant whose surface path is `/assistants/support`, the Go runtime
owns these routes:

```text
POST /assistants/support/v1/conversations
POST /assistants/support/v1/conversations/{conversation_id}/turns
GET  /assistants/support/v1/conversations/{conversation_id}/events?after={sequence}
POST /assistants/support/v1/conversations/{conversation_id}/approvals/{approval_id}
POST /assistants/support/v1/conversations/{conversation_id}/runs/{run_id}/cancel
```

Conversation creation includes the first user message. This lets the helper
return its private session ID before Scenery issues the public conversation
handle.

A create response is provider-neutral:

```json
{
  "conversation_id": "conv1_<hex-sealed-handle>",
  "run_id": "run_<hex-random>",
  "events_url": "/assistants/support/v1/conversations/conv1_<...>/events"
}
```

The sealed handle contains, under AEAD:

- token version;
- canonical assistant address;
- random conversation nonce;
- stable owner digest;
- private helper session ID;
- private helper continuation token;
- issue time.

No plaintext provider identifier is present. The key comes from a
framework-owned assistant token key. Local development persists it under
Scenery-owned state. A production build requires one stable key shared by all
replicas, supplied through the existing secret mechanism. Reuse an existing
Scenery AEAD/token primitive if one already meets these requirements; otherwise
add `internal/assistanttoken` and keep it independent of CLI and runtime
packages.

For unauthenticated surfaces, Scenery issues a signed, HttpOnly, SameSite=Lax
provider-neutral initiator cookie containing a random anonymous principal ID.
The public cookie name must be Scenery-owned. A conversation handle opened by a
different principal returns the same not-found response as an unknown handle.

The event stream is
`application/x-ndjson; charset=utf-8`. Every line validates against the
provider-neutral envelope:

```json
{
  "type": "assistant.message.delta",
  "assistant": "support",
  "conversation_id": "conv1_<...>",
  "run_id": "run_<...>",
  "sequence": 17,
  "occurred_at": "2026-08-04T00:00:00Z",
  "data": {
    "text": "..."
  }
}
```

The initial event catalogue is:

```text
assistant.run.started
assistant.message.delta
assistant.message.completed
assistant.capability.proposed
assistant.approval.required
assistant.capability.started
assistant.capability.completed
assistant.run.completed
assistant.run.cancelled
assistant.run.failed
assistant.runtime.restarting
```

Private event names, private session IDs, private turn IDs, model-provider
payloads, raw stack traces, and provider errors never cross this boundary.

The stream sequence is monotonic within one conversation. `after=N` resumes
strictly after `N`; repeated reads are idempotent. The Go gateway validates and
normalizes every private event before flushing it.

### Provider-name concealment

The exact word `eve`, case-insensitively, is forbidden as a lexical token on
public surfaces. Do not scan for the raw substring because normal words such as
`event` contain those letters. The conformance gate must use token boundaries
and a list of known provider signatures, including:

```text
/eve/
/eve/v1
eve_
eve-
node_modules/eve
from "eve"
from "eve/
@vercel/connect/eve
provider-native default event names and error codes
```

The public output path also applies a streaming lexical redactor. It performs
Unicode normalization, preserves a rolling buffer across NDJSON/text chunks,
detects the forbidden token across chunk boundaries, and replaces it with
`assistant runtime`. It covers model text, tool-provided display text, public
errors, attachment names, and generated summaries. Tests must include `E` in
one chunk and `ve` in the next, mixed case, punctuation boundaries, and the
non-match `event`.

Developer/operator logs may contain the provider name. Public application logs
or APIs may not proxy raw helper stderr.

## Milestones

### Milestone 0: Activate the plan and capture the baseline

Add this file to the repository, link it from the active index, refresh the
knowledge index, read every governing `AGENTS.md`, and record the baseline
commands under Progress. Inspect the current expanded native fixture and
existing binding schemas before editing.

Proof is a clean baseline or a precisely recorded pre-existing failure,
`docs/plans/active.md` linking this plan, and
`.scenery/harness/agent-context.json` containing the initial changed-area
recommendations.

### Milestone 1: Add canonical source, spec, graph, and evolution contracts

Extend `internal/spec`, source schema metadata, parser/formatter tests, graph
resource types, compiler lowering, reference validation, provenance,
inspection, semantic diff, and evolution support for:

- `binding.protocol = "mcp"` plus the `mcp` child;
- root `mcp_connection`;
- root `mcp_server`;
- root `assistant`.

Do not add an alternate declaration frontend or package-init registration.
Ensure module exports can carry MCP bindings into a root server. Add stable
diagnostic codes for invalid protocol children, duplicate tool names,
unsupported types, unsafe sensitive projection, invalid effect hints, route
collisions, source-root escapes, missing exact locks, unsupported adapter,
unsupported external transport/auth, and cyclic server/connection references.

Add schema and graph golden fixtures under the current native corpus. Add one
dedicated `testdata/assistant` app for later real-process validation.

Proof is deterministic source/effective/expanded JSON, exact provenance,
formatter round trips, stable diagnostics, semantic diff coverage, and green
focused compiler/spec tests.

### Milestone 2: Build local MCP projection and the private Go gateway

Add small packages rather than one cross-cutting package:

```text
internal/mcpcontract/
internal/mcpprojection/
internal/mcpgateway/
```

`internal/mcpcontract` owns provider-neutral manifest and wire values.
`internal/mcpprojection` converts only canonical compiler output into the
manifest. `internal/mcpgateway` owns the MCP server, private transport,
principal filtering, invocation, limits, and result mapping.

Add the official Go MCP SDK at exact version `v1.6.1`. Advertise and negotiate
MCP `2025-11-25`. Do not import a pre-release SDK.

Generate runtime registration that dispatches local tool calls through the
same generated binding/execution path used for Scenery semantic calls. Do not
call user service methods directly. Propagate cancellation, request metadata,
trace context, auth principal, idempotency context, and result limits.

Bind the server to `127.0.0.1` on an allocated port. Require a short-lived
assistant assertion on every request. Validate Host and Origin defensively,
reject non-loopback access, enforce request body limits, and never register the
private endpoint with the public router.

For durable operations, return an execution receipt from the operation tool and
add framework-owned status and cancel tools only when the server contains at
least one durable binding. Do not depend on MCP Tasks in this plan.

Proof is an in-process MCP client test that lists only authorized tools, calls a
read tool, calls a write tool, receives a structured declared failure, starts
and polls a durable execution, propagates cancellation, rejects an unauthorized
principal, rejects a stale capability revision, rejects an oversized body, and
cannot reach the server through the public app route.

### Milestone 3: Define the neutral public assistant protocol against a fake helper

Create:

```text
internal/assistantapi/
internal/assistantcontrol/
internal/assistanttoken/
internal/assistantruntime/
runtime/assistant_gateway.go
```

`assistantapi` owns public request, response, event, and error contracts.
`assistantcontrol` owns the private Go-to-helper protocol.
`assistanttoken` owns sealed public handles and approval tokens.
`assistantruntime` defines a provider-neutral helper client and lifecycle
interface; it must not import the Eve adapter.

Implement the public routes and generated runtime registration. First connect
them to a deterministic fake helper implemented in Go. This forces the public
protocol, ownership, reconnect, cancellation, approval, error normalization,
and redaction contracts to stabilize before provider-specific code exists.

The fake helper must be able to emit arbitrary chunk boundaries, request a
capability, wait for approval, simulate a crash, resume from a sequence, and
return malformed private events.

Proof is runtime HTTP testing that covers the complete public protocol, two
principals, anonymous initiator identity, malformed and expired handles,
approval allow/deny, reconnect, cancellation, helper unavailability, invalid
private events, output redaction, and exact public JSON schemas.

### Milestone 4: Add external MCP federation

Use the official Go SDK as an MCP client for each `mcp_connection`. The Scenery
gateway, not the helper, owns remote URLs and secrets. Namespace remote tools
as `<connection-namespace>__<remote-name>`, validate the final name, and reject
collisions with local or other remote tools.

Support Streamable HTTP, static no-auth, bearer-secret, and one
header-secret scheme. Require HTTPS except for loopback development URLs.
Apply exact allow or block filters; reject source that specifies both. Treat
remote descriptions and annotations as untrusted metadata. Enforce Scenery's
configured approval and effect policy rather than trusting remote hints.

A required connection participates in assistant readiness. An optional
connection is omitted from `tools/list` while unavailable and produces one
rate-limited developer diagnostic. Refresh tool inventories on supported MCP
change notifications and on a bounded TTL; preserve deterministic ordering.

Proof uses a local fake remote MCP server to test version negotiation, auth,
tool filtering, namespace collisions, tool changes, result-size enforcement,
timeouts, cancellation, required/optional readiness, and credential
non-disclosure.

### Milestone 5: Add the managed Node/V8 runtime and Eve adapter

Add Node `24.18.0` as a `Home: true` binary artifact to both checked-in
toolchain manifests for the repository's supported app platforms. Use official
Node release archives and signed SHA-256 evidence. The default executable is
`bin/node`; npm is resolved from the same home. Do not use ambient `node`,
`npm`, `npx`, `pnpm`, or `bun`.

Create the developer-only package:

```text
internal/assistantadapter/eve/
```

It owns exact Eve knowledge and nothing public imports it. Install
`eve@0.29.5` in a fixture with an exact `package-lock.json`, then read the
installed `node_modules/eve/docs/README.md` and the bundled MCP connection,
custom channel, auth, approval, build, session, event stream, instrumentation,
and deployment guides before writing adapter code. Record any mismatch between
this plan and the pinned package under Surprises & Discoveries and adjust only
the private adapter contract.

The build overlay copies authored assistant source to a transient workspace and
injects reserved generated files without touching source:

```text
<assistant-build-root>/
  package.json
  package-lock.json
  agent/
    ...authored files...
    channels/
      scenery.ts          # generated and reserved
    connections/
      scenery.ts          # generated and reserved
  .scenery/
    bootstrap.mjs
    runtime-manifest.json
```

Fail compilation if authored source claims the reserved generated paths.

The generated custom channel exposes only the private control protocol on
loopback. It uses Scenery-supplied continuation tokens, returns the private
session ID to Go, obtains durable event streams by private session ID and start
index, maps approval/cancel operations, and normalizes provider events to
`assistantcontrol` records.

The generated MCP connection points only to the private Scenery MCP URL. Its
header callback creates a short-lived HMAC assertion from active session auth
and channel state. Claims include audience, assistant address, principal,
conversation digest, capability revision, expiry, and nonce. The helper never
receives the user's original application token or external MCP credentials.

The authored developer tree remains a normal Eve filesystem project. Developers
may add tools, skills, connections, hooks, subagents, schedules, and evals.
Provider-local tools that require private Scenery state must call a declared
Scenery MCP capability rather than a private Go method or hidden runtime route.

Proof is a real Node child using the pinned package, a deterministic local fake
model endpoint, the generated private channel, the generated Scenery MCP
connection, one authored provider-local tool, and one generated local MCP tool.

### Milestone 6: Integrate supervision, build embedding, deployment, and inspection

Add a typed assistant child to `cmd/scenery`'s dev supervisor rather than an ad
hoc command. Use source IDs such as `assistant:support`; public status never
uses the provider name. Track process PID, private addresses, expected/actual
runtime and capability revisions, readiness, restart count, last failure, and
log source.

Startup order is:

1. compile the graph;
2. materialize the assistant overlay;
3. resolve or sync managed Node;
4. start the private MCP gateway;
5. start the helper;
6. verify private `/health` and `/info`;
7. verify runtime, adapter, and capability revisions;
8. start or mark ready the public assistant surface.

A helper failure makes only its assistant unavailable unless the source marks a
required assistant; the default for a declared assistant is required. Use
bounded exponential restart with rate-limited diagnostics. Active public
streams receive `assistant.runtime.restarting` and may reconnect. Never retry a
capability call after bytes or side effects may have been committed.

Watch lanes are independent:

```text
assistant instructions/skills/tools  -> rebuild/restart helper only
generated adapter or MCP manifest    -> rebuild helper; refresh MCP gateway
Go implementation                    -> rebuild Go app
assistant package/lock               -> rebuild dependency cache and helper
frontend assistant client            -> existing frontend HMR
```

Current implementation note: edits to generated provider channels and
connections stay on the helper-only lane. The graph-derived
`.scenery/runtime-manifest.json` is routed through the app lane so the
app-owned MCP gateway is rebuilt with the new manifest. A live gateway refresh
API does not exist yet, so this lane restarts the app; add that narrower
cross-process refresh boundary in a follow-up before changing the routing.

For production builds, create deterministic compressed artifacts for:

- the platform-matched Node home needed at runtime;
- each compiled assistant capsule;
- a provider-neutral runtime descriptor with exact digests and revisions.

Embed those archives as bytes in the generated Go app workspace. On startup,
extract them beneath the runtime state root using a content-addressed path,
file lock, staging directory, digest verification, atomic rename, and executable
mode restoration. Never execute from a partially extracted directory. Reuse an
existing verified extraction if its descriptor and digest match.

The child process receives only provider-neutral environment names such as:

```text
SCENERY_ASSISTANT_ID
SCENERY_ASSISTANT_CONTROL_TOKEN
SCENERY_ASSISTANT_CONTROL_ADDR
SCENERY_MCP_URL
SCENERY_MCP_BRIDGE_SECRET
SCENERY_CAPABILITY_REVISION
SCENERY_RUNTIME_REVISION
```

Add:

```text
scenery inspect assistants -o json
scenery inspect assistants --implementation -o json
scenery assistant status <name> -o json
scenery logs --source assistant:<name>
```

Default inspection is provider-neutral. `--implementation` is explicitly
developer-only and may report adapter name, exact Node version, exact Eve
version, package-lock digest, overlay path, and private process details.

`scenery doctor` reports missing or invalid managed Node, unsupported platform,
lock drift, reserved-path conflicts, missing production token key, capsule
digest failures, and runtime revision mismatch with stable diagnostic codes.

Proof includes dev start, hot reload, helper crash/restart, direct production
binary execution, extraction recovery after interruption, tamper rejection,
and `scenery ps`/inspection/log output.

### Milestone 7: Generate the provider-neutral browser client and scaffolder

Extend the declared TypeScript client generator only when an assistant surface
is reachable. Generate provider-neutral types and methods, for example:

```ts
client.assistants.support.createConversation(...)
client.assistants.support.sendTurn(...)
client.assistants.support.streamEvents(...)
client.assistants.support.resolveApproval(...)
client.assistants.support.cancelRun(...)
```

The stream helper must support `AbortSignal`, reconnect from the last sequence,
duplicate suppression, strict event decoding, and bounded backoff. Generate no
provider import, package, route, event, or type.

For React-enabled clients, generate a headless
`useSceneryAssistant("support")` adapter. It owns no visual design and composes
with app-owned UI. It must not add a second router or QueryClient.

Add an explicit idempotent command:

```text
scenery assistant init <name> --mcp-server <name> --client <name> -o json
```

The command uses the normal source transaction/evolution path to add missing
canonical blocks and creates an authored assistant skeleton only when files do
not exist. It supports `--dry-run`. It writes exact `package.json` and
`package-lock.json`, minimal `agent/agent.ts`, `agent/instructions.md`, and an
empty eval directory. It never writes generated channel or connection files to
authored source and never overwrites a developer file.

Add:

```text
scenery assistant sync <name> -o json
```

This command resolves managed Node/npm and populates a Scenery-owned dependency
cache from the exact lock. `scenery up` and `scenery build` may consume or
populate that cache, but must never modify `package.json` or
`package-lock.json`. Lock drift fails with an actionable diagnostic.

Proof includes exact generated-client fixtures, TypeScript conformance,
idempotent initialization, dry-run JSON, no-overwrite tests, cache reuse, and a
small React fixture that renders streamed Scenery events without provider code.

### Milestone 8: Public leak gate, real-process acceptance, docs, and completion

Add `scripts/check-assistant-public-surface.sh`. It scans only public artifact
roots and captured public HTTP fixtures, not developer-only source or this
plan. It must inspect:

- generated public TypeScript output;
- generated OpenAPI and public JSON schemas;
- built browser JavaScript and browser source maps;
- public route manifests;
- public response headers, cookies, bodies, and error fixtures;
- default public inspection JSON;
- generated public documentation fragments.

Use lexical boundaries for the exact token and explicit known signatures. The
script must prove that `event` is allowed and that split-chunk `E` + `ve` is
redacted.

Add `scripts/accept-assistant-runtime.sh testdata/assistant`. It runs a
deterministic fake model server and fake external MCP server, starts the real Go
app and managed Node helper, drives the public API, captures artifacts, invokes
the leak script, and always stops child processes. It must require no external
model credentials.

The acceptance flow must prove:

1. initial conversation creation and NDJSON streaming;
2. follow-up and reconnect from a cursor;
3. local operation MCP call with structured output;
4. declared domain-error outcome;
5. durable execution receipt/status/cancel;
6. external federated MCP tool call;
7. provider-local authored tool call;
8. approval allow and deny;
9. cross-principal conversation rejection;
10. stale capability revision rejection;
11. helper cancellation and crash/restart;
12. public normalized error during helper outage;
13. no public private-listener route;
14. no provider token/signature in any public artifact;
15. `scenery up` does not change authored package files;
16. `scenery assistant init` is idempotent and preserves an edited file;
17. production app binary extracts verified runtime assets and runs without an
    ambient Node installation.

Update `ARCHITECTURE.md`, `docs/local-contract.md`, `docs/agent-guide.md`,
`docs/app-development-cookbook.md`, relevant `docs/spec/` files,
`docs/schemas/`, `SKILL.md`, and `docs/knowledge.json`. Document the public
Scenery assistant model separately from the developer-only Eve adapter.

After final validation, fill Outcomes & Retrospective with exact behavior,
commands, timings, remaining limitations, and follow-up plan candidates. Move
the plan to the completed index without rewriting historical decisions.

## Plan of Work

### Canonical model and package ownership

Add source schema definitions in `internal/spec/source_schemas.go` and the
associated metadata/revision tables. Add graph records in the narrow graph
files that already own bindings and root resources. Do not let graph packages
import the MCP SDK, HTTP, Node, or adapter code.

Add compiler validators close to current binding, type, route, workspace-root,
secret, and reference validation. The compiler produces only provider-neutral
assistant and MCP records. The string `eve` may exist in the developer
implementation value but must not cause compiler packages to import provider
code.

Extend formatter ordering and schema projection so `scenery schema`,
`scenery fmt`, `scenery compile`, `scenery list`, `scenery get`,
`scenery graph`, and `scenery explain` work without special-case text parsing.

Update semantic diff/evolution so:

- adding a tool is additive;
- removing or renaming a tool is breaking for the MCP server;
- changing input/output schema follows existing contract compatibility;
- changing effect or approval metadata is reported;
- changing assistant public route/auth is reported as a binding change;
- changing adapter/source/lock changes implementation identity, not the public
  contract revision;
- changing external connection URL/auth changes implementation/deployment
  identity and invalidates readiness evidence.

### Generation

Generate the capability manifest, runtime registration, public route
registration, TypeScript client, OpenAPI projection, provider build overlay,
runtime descriptor, and embedded production archives from one compiler result.

Use the existing artifact-set transaction. Render every selected output before
one commit. `--check` is read-only. All generated paths remain beneath declared
managed roots or Scenery's external build/cache roots.

The provider overlay is not a declaration surface. Deleting it and rebuilding
must reproduce the same bytes from source, lock, graph, toolchain, and Scenery
version.

### Runtime and MCP dispatch

`internal/mcpgateway` should accept generated registrations through a small
provider-neutral interface. Runtime code supplies request context, auth
principal, and generated dispatch clients. The gateway never imports
`cmd/scenery` or the Eve adapter.

The generated dispatcher should expose an interface similar to:

```go
type ToolDispatcher interface {
    CallTool(
        context.Context,
        ToolCallContext,
        string,
        json.RawMessage,
    ) (ToolOutcome, error)
}
```

`ToolCallContext` contains principal, assistant address, conversation digest,
capability revision, request ID, trace context, and cancellation. It does not
contain raw browser credentials.

### Private control protocol

Define versioned machine identities and JSON schemas:

```text
scenery.assistant.control.request
scenery.assistant.control.response
scenery.assistant.control.event
scenery.assistant.runtime-descriptor
scenery.assistant.public-event
scenery.inspect.assistants
```

The Go side rejects unknown private event types and malformed data. The adapter
may know provider-native event structures, but it emits only the private
Scenery control schema. Public normalization occurs once more in Go.

### Runtime packaging

Extend the toolchain manifest with Node as a full home. Build-time code obtains
the target platform artifact through `internal/toolchain`; it does not download
from arbitrary code paths.

Create a deterministic archive format for the production Node home and
assistant capsule. Normalize archive paths, modes, mtimes, ownership, and order.
Reject absolute paths, `..`, symlinks escaping the root, devices, and duplicate
entries during extraction.

Do not store Node or capsule bytes in the source checkout. They live in the
transient generated build workspace and final app binary.

### Observability

Create spans for public request, helper control request, MCP list/call, local
operation dispatch, external MCP call, and helper restart. Correlate with:

```text
scenery.app.id
scenery.assistant.name
scenery.conversation.digest
scenery.run.id
scenery.capability.name
scenery.capability_revision
scenery.runtime_revision
mcp.protocol.version
```

Do not put raw prompts, sensitive tool fields, private session IDs, private
continuation tokens, bridge secrets, or external credentials into default
logs/traces. Developer opt-in content tracing is a separate future feature.

## Concrete Steps

All commands below run from the repository root unless a different working
directory is shown.

1. Copy this plan into the repository and activate it.

   ```sh
   cp /path/to/0149-mcp-native-scenery-assistants.md \
     docs/plans/0149-mcp-native-scenery-assistants.md
   ```

   Edit `docs/plans/active.md` and `docs/knowledge.json` in the same change.
   Read root `AGENTS.md`, `PLANS.md`, and every child `AGENTS.md` for a directory
   before editing that directory.

2. Capture baseline state and preserve any pre-existing failure in Progress.

   ```sh
   git status --short
   go test ./...
   ./scripts/build-dashboard-ui-embed.sh
   ```

   If `.scenery/harness/bin/scenery` is absent, run the installed binary once:

   ```sh
   scenery harness self --summary --write
   ```

   Then use only the worktree-local binary:

   ```sh
   .scenery/harness/bin/scenery harness self --summary --write
   ```

3. Refresh the changed-area oracle before implementation and at each milestone.

   ```sh
   .scenery/harness/bin/scenery harness self --quick --summary --write
   python3 - <<'PY'
   import json
   from pathlib import Path
   p = Path(".scenery/harness/agent-context.json")
   data = json.loads(p.read_text())
   changed = data.get("changed_area", {})
   print("validation_classes:")
   for value in changed.get("validation_classes", []):
       print("  ", value)
   print("recommended_commands:")
   for value in changed.get("recommended_commands", []):
       print("  ", value)
   PY
   ```

   Copy the actual union into Progress and run every command it prints before
   declaring the corresponding milestone complete.

4. Implement Milestone 1 test-first. Add failing source-schema, formatter,
   compiler, graph, diagnostic, semantic-diff, and evolution tests. Then add the
   source and graph implementation. Keep MCP SDK and runtime imports out of
   foundational packages.

5. Add `github.com/modelcontextprotocol/go-sdk v1.6.1` and implement
   Milestone 2 behind provider-neutral packages. Commit the module checksum
   changes with the code. Add fake generated dispatchers; do not depend on the
   helper yet.

6. Implement public and private assistant schemas plus the fake helper in
   Milestone 3. Pin public JSON and generated schema fixtures before writing the
   Eve adapter.

7. Implement external federation in Milestone 4 using a local fake remote
   server. Do not make real network services part of unit tests.

8. Add the managed Node artifact. Fetch Node `24.18.0` release archives only
   from the official release origin, verify signed SHASUMS, and write exact
   SHA-256 values into both toolchain manifests. Then run:

   ```sh
   .scenery/harness/bin/scenery system toolchain sync --tool node -o json
   .scenery/harness/bin/scenery system toolchain verify --tool node --strict -o json
   NODE="$(
     .scenery/harness/bin/scenery system toolchain path --tool node
   )"
   "$NODE" --version
   "$(dirname "$NODE")/npm" --version
   ```

   The observable versions must be Node `v24.18.0` and its bundled npm. Record
   the exact npm output in Progress.

9. Scaffold the developer-only adapter fixture with exact `eve@0.29.5`, create
   an exact `package-lock.json`, and install it using the managed npm. Read the
   bundled docs before adapter code. Record the exact files read in Progress.

10. Build Milestones 5 and 6. Keep the provider package below
    `internal/assistantadapter/eve` and transient overlay roots. No package
    outside the adapter, adapter tests, developer docs, or explicit
    implementation inspection may import it or use provider-native types.

11. Implement generated client and scaffolding in Milestone 7. Regenerate both
    committed TypeScript fixtures using the repository's exact fixture
    commands.

12. Add the acceptance and leak scripts in Milestone 8. The scripts must use
    `trap` cleanup and emit paths to captured logs, response fixtures, public
    bundles, and runtime descriptors.

13. Complete all validation below. Paste concise evidence into Progress,
    Surprises & Discoveries, and Artifacts and Notes. Fill Outcomes &
    Retrospective, then update active/completed indexes.

## Validation and Acceptance

Expected changed-area classes are:

```text
compiler/generator
CLI JSON contract
runtime/release-sensitive
multiple Go packages
docs/knowledge contract
TypeScript generated client
toolchain manifest
```

Do not touch the dashboard UI in this plan. If implementation changes
`apps/console` or dashboard embedded assets beyond the fresh-worktree preflight,
record the scope change in Decision Log and add the exact dashboard lint,
typecheck, build, and browser-acceptance commands from root `AGENTS.md`.

### Fresh worktree preflight

Run from the repository root:

```sh
./scripts/build-dashboard-ui-embed.sh
```

If `.scenery/harness/bin/scenery` does not exist, the exact skip condition for
the next command is false; run:

```sh
scenery harness self --summary --write
```

Once the local binary exists, run:

```sh
.scenery/harness/bin/scenery harness self --summary --write
```

A preflight command may be skipped only when its stated file/condition proves
it unnecessary; record the condition and evidence in Progress.

### Focused Go validation

After the named packages exist, run:

```sh
go test ./internal/spec
go test ./internal/graph
go test ./internal/compiler
go test ./internal/evolution
go test ./internal/mcpcontract
go test ./internal/mcpprojection
go test ./internal/mcpgateway
go test ./internal/assistantapi
go test ./internal/assistantcontrol
go test ./internal/assistanttoken
go test ./internal/assistantruntime
go test ./internal/assistantadapter/eve
go test ./internal/generate
go test ./internal/toolchain
go test ./runtime
go test ./cmd/scenery
```

Run race validation for concurrent transport, stream, restart, and supervisor
code:

```sh
go test -race \
  ./internal/mcpgateway \
  ./internal/assistantruntime \
  ./runtime \
  ./cmd/scenery
```

### Generator and client validation

Run:

```sh
go test ./internal/generate
go test ./cmd/scenery -run 'TestGenerate'
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc \
  -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc \
  -p internal/generate/testdata/tsconfig.catalog.json
```

Regenerate the two committed fixtures exactly:

```sh
go run ./cmd/scenery generate \
  --target typescript_client.public_api \
  --app-root internal/compiler/testdata/native \
  -o json

go run ./cmd/scenery generate \
  --target typescript_client.public_api \
  --app-root internal/compiler/testdata/house \
  -o json
```

After accepting expected fixture changes, run both commands again. Their JSON
must report no further change.

### Managed runtime validation

Run:

```sh
.scenery/harness/bin/scenery system toolchain sync --tool node -o json
.scenery/harness/bin/scenery system toolchain verify \
  --tool node --strict -o json
go test ./internal/assistantadapter/eve \
  -run 'Test(GeneratedOverlay|ManagedRuntime|MCPConnection|CustomChannel)' \
  -count=1
```

The exact test regular expression may be expanded as tests are added, but these
four named behaviors must remain represented. Record the final test names in
Progress rather than silently replacing them.

### Fixture app and real-process proof

Run:

```sh
(cd testdata/assistant && \
  ../../.scenery/harness/bin/scenery check -o json)

(cd testdata/assistant && \
  ../../.scenery/harness/bin/scenery generate \
    --target typescript_client.public_api -o json)

(cd testdata/assistant && \
  ../../.scenery/harness/bin/scenery inspect assistants -o json)
```

Then run the deterministic acceptance script:

```sh
./scripts/accept-assistant-runtime.sh testdata/assistant
```

The script must start its own fake model and fake external MCP server, use the
real Go app and managed Node helper, drive all seventeen Milestone 8 acceptance
cases, save evidence beneath `.scenery/harness/assistant-acceptance/`, and stop
all processes even on failure.

Run the public leak gate independently against the captured artifacts:

```sh
./scripts/check-assistant-public-surface.sh testdata/assistant
```

The leak command exits zero only when all scanned public files and HTTP
captures are clean and its positive/negative self-tests pass.

### Production binary proof

Build the fixture:

```sh
(cd testdata/assistant && \
  ../../.scenery/harness/bin/scenery build \
    --target development \
    --output ./bin/assistant-fixture)
```

Run it with a clean `PATH` that contains no system Node, using only the
environment required by the deterministic fake model and framework secrets.
The acceptance script may own this sub-proof, but it must print the exact
binary path, extraction root, Node digest, capsule digest, helper PID, and final
exit status.

Delete half of a staged extraction, rerun the binary, and prove recovery uses a
fresh staging directory and atomic rename. Mutate one extracted runtime byte
and prove digest verification rejects it and restores a verified copy.

### Full repository validation

Run:

```sh
go test ./...
.scenery/harness/bin/scenery harness self --summary --write
```

Refresh the changed-area oracle one final time:

```sh
.scenery/harness/bin/scenery harness self --quick --summary --write
```

Inspect `.scenery/harness/agent-context.json` and run every command in the final
`changed_area.recommended_commands` union, including any command not already
listed here.

Finally run:

```sh
git status --short
```

The final status may contain intentional source, tests, docs, fixtures, and
lockfiles. It must not contain generated cache roots, `node_modules`, extracted
Node homes, assistant runtime state, test credentials, or transient acceptance
artifacts.

### Acceptance criteria

The plan is complete only when all of these statements are true:

- `scenery compile --view expanded -o json` shows canonical MCP and assistant
  resources with deterministic revisions and provenance.
- A manual Go operation plus MCP binding is discoverable and callable.
- A provider-local authored tool is discoverable and callable.
- External MCP tools are federated under deterministic namespaces without
  exposing their credentials to the helper.
- Every tool invocation re-authenticates and re-authorizes in Go.
- A second principal cannot read, continue, approve, or cancel the first
  principal's conversation.
- A stale runtime/capability revision fails closed with a Scenery error.
- Public streams reconnect without duplicate events and cancel active turns.
- The helper may crash without crashing the Go app.
- `scenery up` restarts the helper with bounded policy and typed health.
- A direct production app binary starts its extracted child runtime without a
  system Node installation.
- No supported public artifact contains the forbidden provider token or known
  provider-specific signatures.
- No browser bundle imports provider code.
- `scenery assistant init` is dry-runnable, idempotent, and never overwrites
  authored files.
- `scenery up`, `check`, and `build` never rewrite authored package manifests
  or locks.
- Generation and extraction are deterministic, transactional, and resumable.
- All focused, race, generator, fixture, full-suite, and self-harness commands
  pass.

## Idempotence and Recovery

Source changes made by `scenery assistant init` use the existing workspace
transaction mechanism. An interrupted mutation is recovered or rejected by the
same ownership rules as other source changes. File creation is staged and
committed only after graph validation. Existing authored files are never
overwritten.

Generated MCP manifests, runtime overlays, TypeScript clients, runtime
descriptors, and embedded archives use one atomic artifact-set transaction.
A failed render leaves the previous complete artifact set intact.

Dependency installation uses the exact package lock and a content-addressed
Scenery cache. A failed install may be retried after removing only its staging
directory. Do not delete the developer's source or lock. `assistant sync` is
safe to repeat.

Toolchain synchronization is digest-bound and safe to repeat. Node archives are
downloaded through `internal/toolchain`, not bespoke adapter code.

Development helper replacement is blue-green at the process boundary. Start
and validate the new helper before routing new turns to it. Active turns on the
old helper may drain for the bounded shutdown interval. After that interval,
cancel and terminate the old process. Existing conversations use the latest
ready helper on their next turn and the same provider-owned durable session.

Production extraction is content-addressed. Use an exclusive lock per digest,
extract to a sibling staging directory, verify every expected file and digest,
then rename. A crash before rename leaves a removable staging directory. A
verified final directory is immutable and reusable.

Private ports and tokens are per process. On shutdown, cancel streams, close
MCP clients and listeners, stop the helper, wait for it, then remove only
ephemeral private state. Do not remove durable provider session state or
content-addressed verified runtimes.

Required external MCP connections fail assistant readiness but do not corrupt
the app graph. Optional connections disappear from discovery while unhealthy
and recover on the next bounded refresh.

A failed public stream can reconnect with `after=<last-sequence>`. The runtime
must never replay a tool invocation merely because a stream or HTTP response
failed.

Rollback between milestones is additive:

- Milestone 1 can be reverted without runtime state.
- Milestones 2 through 4 are unreachable until an assistant is declared.
- Milestone 5's adapter is isolated below one developer-only package.
- Milestone 6's runtime assets are content-addressed and can fall back to the
  last verified digest during development.
- Milestone 7's scaffolder never owns developer content after creation.
- Public contracts cannot be silently removed after Milestone 8; use normal
  evolution and compatibility rules.

## Artifacts and Notes

Keep concise implementation evidence here as work proceeds:

- Activation baseline commit:
  `5694410935306e6505ce725fead9cce578b34e0c`.
- Activation full self-harness artifact:
  `.scenery/harness/self-latest.json`. All executable validation steps,
  including cached full Go tests, Go vet, two parallel worktree runtimes,
  PostgreSQL probe, dashboard build/typecheck, TypeScript conformance and
  catalog checks, fixture matrix, storage probe, and schema validation passed.
  Architecture checks alone failed on the 100 intentional current MCP-token
  diagnostics recorded under Surprises & Discoveries.
- Activation expanded native-fixture evidence: 18 resources, including three
  `scenery.binding` resources for `http`, `internal`, and `cli`.
- Milestone 1 guardrail evidence:
  `go test ./cmd/scenery -run
  'TestRunHarnessArchitectureStepValidAndInvalidFixtures|TestCheckCurrentSurfaceResidue'`
  passed, and the refreshed quick self-harness reported `scenery: self harness
  ok` with all eight quick steps green.
- Milestone 1 final spec revision:
  `sha256:351b04a1172e8db7c4a15a1c0295ceb3c7d9c765d162ac3c0c01d63be04331ba`.
  Native workspace/contract revisions are
  `sha256:9d2a9ac81e4eecd14dcc74cf5b5c91f3a5e7f9f8d7516b77f3cb9e632bea6679`
  and
  `sha256:20e421c6848bc48cd6b002ae9ba16bcf69f326ac098d6d96d2ce9cb7ba73c542`.
  The dedicated assistant fixture's repeated source/effective/expanded output
  hashes were respectively `81dbee0f...49bb5`, `06065832...102868`, and
  `db2e7571...6b354f`, with no diagnostics.
- Milestone 1 focused validation passed for `internal/spec`, `internal/scn`,
  `internal/graph`, `internal/deployplan`, `internal/compiler`,
  `internal/evolution`, and `internal/generate`. The native graph golden checks
  exact MCP/assistant addresses, module-export substitution, field provenance,
  the sensitive-output default, deterministic source/effective/expanded JSON,
  and implementation-versus-contract revision behavior.
- Milestone 2 SDK evidence: `go.mod` pins
  `github.com/modelcontextprotocol/go-sdk v1.6.1`; `go.sum` records module sum
  `h1:0zOSupjKUxPKSocPT1Wtago+mUHU2/uZ4xSOY0FGReU=`. The direct-dependency
  architecture allowlist records its private Streamable HTTP rationale.
- Milestone 2 native capability manifest golden:
  `internal/mcpprojection/testdata/native_manifest.golden.json`, logical digest
  `sha256:806f7fac2cd46a4de83b7e3ad2ff1ddcaa1a662a451783f1c64c9fdfae6f2902`.
- Milestone 2 validation passed:
  `go test ./internal/mcpcontract ./internal/mcpprojection
  ./internal/mcpgateway ./runtime ./internal/generate
  ./internal/contractagent`, `go test -race ./internal/mcpgateway ./runtime`,
  and `go test ./...`. The native and house generator commands were rerun twice
  and remained `changed: []`.
- Milestone 3 public schemas:
  `docs/schemas/scenery.assistant.public.request.schema.json`,
  `scenery.assistant.public.response.schema.json`,
  `scenery.assistant.public-event.schema.json`, and
  `scenery.assistant.public-error.schema.json`. Private control schemas cover
  request, response, event, and runtime descriptor identities in the same
  directory. `cmd/scenery/assistant_public_schema_test.go` and
  `assistant_control_schema_test.go` validate current variants and reject
  unknown or stale shapes.
- Milestone 3 runtime evidence: `internal/assistantapi`,
  `internal/assistantcontrol`, `internal/assistanttoken`, and
  `internal/assistantruntime` own the separated contracts; runtime HTTP proof
  is in `runtime/assistant_gateway_test.go`. Generated native composition owns
  `app/assistant/support` through synthetic registration
  `scenery/assistants`, while its service adapter owns and registers
  `house/binding/process_scene_mcp` through the normal generated invocation
  path.
- Milestone 3 validation passed:
  `go test ./internal/assistantapi ./internal/assistantcontrol
  ./internal/assistanttoken ./internal/assistantruntime ./internal/generate
  ./runtime ./cmd/scenery`, `go test -race ./internal/assistantapi
  ./internal/assistantcontrol ./internal/assistanttoken
  ./internal/assistantruntime ./runtime`, and `git diff --check`. The native and
  house TypeScript client generator commands were each run twice and returned
  `changed: []` on both passes; `go test ./...` then passed. Materializing the
  new generated Go composition changed the native workspace revision to
  `sha256:fc049a8693fe2d3aee32846811fcd7aa1428be6326fbc1be1c54937d0ddd721d`;
  the refreshed capability-manifest golden has file digest
  `sha256:41eaf698a0983e2cf69eba6795927816b1406a1432d5ca60765d87390a3ec5e5`.
- Milestone 4 implementation evidence: `internal/mcpfederation` owns official
  SDK clients and connection lifecycle; `internal/mcpgateway` merges and
  dispatches local and federated capabilities; `runtime/mcp_federation.go`
  owns generated registration, provider secret resolvers, shared lookups, and
  typed live readiness; and
  `internal/generate/generate_application_mcp_federation.go` emits private
  deterministic registrations without changing the public manifest shape.
- Milestone 4 validation passed: `go test ./internal/mcpfederation
  ./internal/mcpgateway ./runtime ./internal/generate ./internal/mcpprojection
  ./internal/compiler -count=1`, `go test -race ./internal/mcpfederation
  ./internal/mcpgateway ./runtime -count=1`, `go vet
  ./internal/mcpfederation`, `git diff --check`, and `go test ./...`. Native and
  house contract materialization plus TypeScript generation all reported
  `changed: []`; the native fixture's generated composition also passes its
  own `go test ./...` with the exact MCP SDK dependency in its checked-in
  module files.
- Milestone 5 toolchain evidence: both checked-in manifests pin Node
  `24.18.0`; Linux amd64 archive digest
  `sha256:783130984963db7ba9cbd01089eaf2c2efb055c7c1693c943174b967b3050cb8`
  and Darwin arm64 digest
  `sha256:e1a97e14c99c803e96c7339403282ea05a499c32f8d83defe9ef5ec66f979ed1`
  came from the official clear-signed checksum file verified by Node release
  key `C82FA3AE1CBEDC6BE46B9360C43CEC45C17AB93C`. Managed strict verification
  reported Node `v24.18.0`, npm `11.16.0`, and V8
  `13.6.233.17-node.50`.
- Milestone 5 adapter evidence: exact Eve `0.29.5` fixture lock digest is
  `sha256:d51e5dbc632e4ce1f5d78d6b6ef4d55b38e07c1ced48bb3229c1d62601810dae`.
  The generated overlay passed Eve `info --json` and
  `build --skip-sandbox-prewarm` under managed Node. A real helper run emitted
  the authored `local` proposal/completion, `connection_search`, an approval
  wait for `scenery__echo`, and—after `approval.resolve`—sequences 9 through
  13 containing the actual MCP completion and `mcp:fixture-mcp` response. The
  fake private Streamable HTTP server observed initialize, notification,
  tools/list, and tools/call with the Scenery assertion header on every
  request.
- Milestone 5 validation passed: `go test ./internal/assistantadapter/eve
  ./internal/toolchain -count=1`, `go test -race
  ./internal/assistantadapter/eve ./internal/toolchain -count=1`, `go vet
  ./internal/assistantadapter/eve ./internal/toolchain`, and scoped
  `git diff --check`.
- Milestone 6 evidence: runtime asset install tests cover strict
  archives, locking, concurrent reuse, interrupted staging, tamper rejection,
  and verified recovery. Production composition embeds complete Node and
  capsule tree descriptors, requires a stable operator token key, launches the
  extracted Node executable with an allowlisted environment, and keeps mutable
  helper HOME state outside verified trees. Focused normal/race tests pass for
  `runtime`, `internal/runtimeassets`, `internal/assistantruntime`,
  `internal/build`, `internal/generate`, and the CLI supervisor. The registry
  test helper now snapshots durable-owner data under lock rather than copying
  its mutex-bearing store; `go vet ./...` is clean.
- Milestone 7 client evidence: generated native `assistant.ts` passed the
  generated-client and catalog TypeScript projects; Bun conformance passed 20
  tests and 70 assertions covering create, fatal UTF-8, exact JSON numbers,
  cursor reconnect, duplicate suppression, abort, and terminal semantics. The
  public leak gate scanned seven generated files (133402 bytes) cleanly.
- Milestone 7 CLI evidence: a real `assistant init extra --dry-run` returned a
  revision-bound evolution plan without changing source. A real
  `assistant sync support` used managed Node/npm, populated digest
  `sha256:f2f52d43665f2163ef0177b808c30f1aa72cef203540eb7c059cabb0731cdcf1`,
  and preserved authored package/lock digests
  `sha256:7de8f9de9607e5cec64dea482092234612f2636d99a9d2e79f3981701a662afd`
  and
  `sha256:f2f52d43665f2163ef0177b808c30f1aa72cef203540eb7c059cabb0731cdcf1`.
  Unit and race tests additionally prove apply, exact scaffold lock digest
  `sha256:d51e5dbc632e4ce1f5d78d6b6ef4d55b38e07c1ced48bb3229c1d62601810dae`,
  idempotence, edited-file preservation, cache reuse, and lock-drift failure;
  full `go test ./cmd/scenery -count=1` passes.
- Milestone 8 real-process evidence lives under
  `testdata/assistant/.scenery/harness/assistant-acceptance/`. Its 24-row case
  report digest was
  `sha256:4feddaa18e02262a5dc9bb67de85b98a94a5a4d80241fa6ee61054dfdf795a67`;
  every row passed in two consecutive complete runs. The helper recovery case
  replaced PID `39081` with PID `39809`. Production listener readiness took
  three seconds and helper readiness one second within respective 180- and
  60-second bounds.
- The artifact-role runtime bundle digest was
  `sha256:e537349efc03fddc8984bad82f0c1c397bc7b39b5e6b7088c518f5fb3b712b74`.
  It records Node archive/tree digests
  `sha256:aca61c2ad161df7e258d71666852ee65a2d3ffef92d4925e59c946d6a968e45b`
  and
  `sha256:7b032d9c400da41fcf13a1ee9ee2abbe8577005b412e9bd02a5a77f1b466e689`,
  plus capsule archive/tree digests
  `sha256:b53853ba686acdb8d4883fd4d3a07bfadfcd24e0f0dd13a50d98912849040bb2`
  and
  `sha256:6ee027b40e662293abbe788f8eede6da093017ab7a3afb9a9e810df2ddf64593`.
  The direct binary completed a public conversation with an empty `PATH` and
  reused those verified assets on the second complete acceptance run.
- The final provider-leak gate scanned 81 public files totaling 365,699 bytes
  and passed its positive and negative self-tests. The full browser UI harness
  passed all seven dashboard routes against `testdata/apps/basic`.
- The final worktree-local self-harness passed all 22 executable steps,
  including architecture, Go tests and vet, parallel runtimes, PostgreSQL,
  dashboard, TypeScript, fixtures, storage, and 41 schemas. The changed-area
  union additionally passed every listed package command, dashboard
  lint/typecheck/build, both generators at `changed: []`, and managed Node
  sync/strict verification. The no-app-root form of the UI recommendation is
  invalid in this repository root; the documented fixture-root form passed.
- Implementation remained an intentional uncommitted worktree over baseline
  commit `5694410935306e6505ce725fead9cce578b34e0c`; no commit or push was part
  of this plan execution.

Expected new or materially changed paths include:

```text
internal/spec/
internal/graph/
internal/compiler/
internal/evolution/
internal/mcpcontract/
internal/mcpprojection/
internal/mcpgateway/
internal/assistantapi/
internal/assistantcontrol/
internal/assistanttoken/
internal/assistantruntime/
internal/assistantadapter/eve/
internal/generate/
internal/toolchain/
runtime/
cmd/scenery/
testdata/assistant/
scripts/accept-assistant-runtime.sh
scripts/check-assistant-public-surface.sh
docs/spec/
docs/schemas/
docs/local-contract.md
docs/agent-guide.md
docs/app-development-cookbook.md
ARCHITECTURE.md
SKILL.md
scenery.toolchain.json
internal/toolchain/scenery.toolchain.json
```

Reference material current at plan authoring:

```text
https://modelcontextprotocol.io/specification/2025-11-25
https://github.com/modelcontextprotocol/modelcontextprotocol/releases
https://github.com/modelcontextprotocol/go-sdk
https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.6.1
https://nodejs.org/download/release/v24.18.0/
https://github.com/vercel/eve
https://eve.dev/docs
```

The installed package documentation is authoritative for adapter code:

```text
<assistant-cache>/node_modules/eve/docs/README.md
```

Do not rely on the provider repository's `main` branch when it differs from the
exact installed package.

## Interfaces and Dependencies

### Go dependencies

Add exactly:

```text
github.com/modelcontextprotocol/go-sdk v1.6.1
```

Use its `mcp`, `jsonrpc`, and transport packages only below the MCP gateway and
federation boundaries. Foundational source, graph, spec, and compiler packages
must not import it.

No V8, libnode, C++, cgo, or JavaScript engine Go bindings are permitted.

### Managed runtime dependencies

Pin:

```text
Node.js 24.18.0
npm bundled with Node 24.18.0
eve 0.29.5
```

All package dependencies are exact through `package-lock.json`. No version
ranges appear in generated assistant scaffolds.

### Provider-neutral Go interfaces

`internal/mcpcontract` should expose immutable values similar to:

```go
type Manifest struct {
    SchemaRevision     string
    SourceRevision     string
    ContractRevision   string
    Capabilities       []Capability
    Connections        []Connection
}

type Capability struct {
    ID                 string
    Name               string
    Title              string
    Description        string
    InputSchema        json.RawMessage
    OutputSchema       json.RawMessage
    OperationAddress   string
    ExecutionAddress   string
    Approval           ApprovalPolicy
    ReadOnly           bool
    Destructive        bool
    Idempotent         bool
    OpenWorld          bool
    MaxInputBytes      int64
    MaxResultBytes     int64
}
```

`internal/mcpgateway` should depend on interfaces similar to:

```go
type PrincipalResolver interface {
    ResolveMCPPrincipal(context.Context, string) (Principal, error)
}

type ToolDispatcher interface {
    CallTool(
        context.Context,
        ToolCallContext,
        string,
        json.RawMessage,
    ) (ToolOutcome, error)
}
```

`internal/assistantruntime` should expose:

```go
type Client interface {
    Health(context.Context) (Health, error)
    StartConversation(context.Context, StartRequest) (StartResult, error)
    SendTurn(context.Context, TurnRequest) (TurnResult, error)
    StreamEvents(context.Context, StreamRequest) (io.ReadCloser, error)
    ResolveApproval(context.Context, ApprovalRequest) error
    CancelRun(context.Context, CancelRequest) error
    Close() error
}

type Launcher interface {
    Start(context.Context, LaunchSpec) (Client, Process, error)
}
```

The provider adapter implements `Launcher`; runtime and public API code depend
only on these provider-neutral interfaces.

### Public TypeScript interface

Generate stable types similar to:

```ts
export type AssistantEvent =
  | AssistantRunStarted
  | AssistantMessageDelta
  | AssistantMessageCompleted
  | AssistantCapabilityProposed
  | AssistantApprovalRequired
  | AssistantCapabilityStarted
  | AssistantCapabilityCompleted
  | AssistantRunCompleted
  | AssistantRunCancelled
  | AssistantRunFailed
  | AssistantRuntimeRestarting;

export interface AssistantConversationClient {
  createConversation(input: CreateConversationInput): Promise<Conversation>;
  sendTurn(conversationId: string, input: SendTurnInput): Promise<Run>;
  streamEvents(
    conversationId: string,
    options?: { after?: number; signal?: AbortSignal },
  ): AsyncIterable<AssistantEvent>;
  resolveApproval(
    conversationId: string,
    approvalId: string,
    decision: "approve" | "deny",
  ): Promise<void>;
  cancelRun(
    conversationId: string,
    runId: string,
  ): Promise<void>;
}
```

No public TypeScript type contains adapter, provider session, provider event,
provider package, private MCP URL, or private control fields.

### Stable machine identities

Add schema-backed identities for at least:

```text
scenery.mcp-capability-manifest
scenery.assistant.control.request
scenery.assistant.control.response
scenery.assistant.control.event
scenery.assistant.runtime-descriptor
scenery.assistant.public-event
scenery.inspect.assistants
```

Follow the repository's existing artifact-identity and schema-revision
conventions. Update schemas and tests in the same change whenever a shape
changes.
