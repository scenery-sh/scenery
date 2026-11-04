# Contract-Preserving JSON v2 Adoption

## Purpose / Big Picture

Use Go 1.27's standard-library `encoding/json/jsontext` and
`encoding/json/v2` to simplify JSON processing without changing Scenery's
declared wire representations or content revisions. The motivation is the
Go team's [JSON v2 announcement](https://go.dev/blog/jsonv2): syntax validation
and Go-value marshaling now have separate APIs, with strict syntax handling
and streaming support.

The first delivery replaces the token decoder in
`internal/contract/contract_json_exact.go`. This package owns the exact JSON
values used by generated contract records and runtime HTTP requests. Here,
"exact" means numeric values retain their decimal precision, including integers
outside JavaScript's safe range. Success means the existing accepted values
produce identical canonical bytes, and malformed or ambiguous values remain
rejected. Later deliveries cover other input boundaries, ordinary output
encoding, and generated streaming methods; they have independent acceptance
gates below.

## Progress

- [x] 2026-11-04: Inspect the blog, current Go 1.27.0 toolchain, contract codec,
  canonical writers, generator, machine decoder, and repository validation rules.
- [x] 2026-11-04: Create and index this plan. The user authorized the plan and
  the start of implementation; the initial delivery is milestone 1.
- [x] 2026-11-04: Replace the contract canonicalizer's token decoder with
  `jsontext`; extend existing fixtures for exact numbers, UTF-16 ordering,
  escaped/nested duplicates, invalid Unicode, and trailing data.
- [x] 2026-11-04: `go test ./internal/contract ./runtime` passes; the bounded
  fast-path fuzz run on final source passes 145,900 executions with 381 baseline
  inputs (an earlier run passed 146,254 executions).
  `golangci-lint run ./...` passes with zero issues after using `errors.Is`
  for the new decoder's end-of-input check.
- [x] 2026-11-04: The stable-input full verifier passes with warnings; its complete
  Go suite covers all 112 packages plus the CLI/contract command union. The
  native-contract probe passes all eight segments, including one live generated
  client test and public restart reuse; its fixture and process cleanup is verified.
- [x] 2026-11-04: Audit the remaining strict-input owners; migrate machine
  envelopes/current artifacts, same-schema upgrade field extraction, and the
  authoritative environment document. Remove their repeated token walks and
  preserve duration/byte-array representations. Retention decisions are below.
- [x] 2026-11-04: Migrate environment document output with explicit deterministic,
  nil-map, indentation, escaping and raw-string options. Byte-parity fixtures and
  the complete Go suite pass. Retain other output owners as recorded below.
- [x] 2026-11-04: Evaluate generated streaming methods and retain the existing
  methods: a wrapper still buffers/reparses, while a real rewrite duplicates
  schema-directed ordering/validation without an established additional benefit.
- [x] 2026-11-04: Complete eight alternating microbenchmark pairs on all 12 cases,
  and 12 alternating HTTP pairs on three generated-app scenarios. Both reject
  statistically detected slowdowns and uncertainty extending beyond +5%; every
  case passes. Preserve raw observations, source digests, served identity and cleanup.
- [x] 2026-11-04: Complete frozen-input full verification, lint, all three leaf
  race suites, Linux vet, and CLI-process/native-contract/configuration probes.
  Verify cleanup, retain performance source identity, and close the plan.

### Resume here

2026-11-04: Complete on uncommitted `main`, based on `57fbfdaa`. No implementation,
acceptance action, or human answer remains outstanding. Frozen-input source
validation and cleanup passed, and this plan is recorded in the completed index.
Only plan/index evidence edits follow those source checks; final documentation
verification is linked in the editing turn's final response so this historical
plan is not rewritten afterward. Future work must use a new plan and remeasure
affected cases against the recorded source digests, rather than reopen the
retained-v1/streaming decisions as unfinished work.

## Surprises & Discoveries

- Scenery already requires Go 1.27.0 in `go.mod`; the installed toolchain reports
  the same version and exposes `encoding/json/jsontext` without an experiment
  setting. No Go-version bump, dependency, or environment switch is needed.
- The current contract parser already rejects duplicate names, invalid UTF-8,
  unpaired surrogates, extra top-level values, and excessive nesting. JSON v2's
  stricter defaults are an opportunity to remove duplicated syntax work here,
  rather than evidence of a newly discovered vulnerability.
- `go doc encoding/json/jsontext.Value.Canonicalize` documents IEEE-754 double
  conversion and precision loss beyond +/-2^53. It cannot replace Scenery's
  exact numeric canonicalization. `go doc encoding/json/v2.Deterministic` also
  limits its byte-stability guarantee across different builds, so that option
  alone cannot replace `internal/spec.MarshalCanonical` for revisions.
- `runtime/contract_http_test.go` requires malformed input to remain a 400
  `transport.invalid_request`, with a stable truncated-input message and no
  handler invocation. Changes to decoder errors must preserve that behavior.
- The first full verifier at
  `.scenery/harness/runs/20261104T193056.480089000Z/` rejects its input identity
  because the `errors.Is` lint correction happened during the run. Its resulting
  failure step also exposes a null-command schema diagnostic. Do not count this
  archive as a completed verification; rerun on stable inputs instead of changing
  the unrelated verifier schema. Its context selects `cli-json-contract` and
  `go-package`, requiring quick verifier, all Go tests, CLI tests, and contract
  tests; the full run covers the Go union and final doc verification covers quick.

## Decision Log

- 2026-11-04: Deliver one reviewable parser migration first. Keep the remaining
  call sites on `encoding/json` until their owning contracts have been checked.
  The v1 package remains supported and already benefits from the new underlying
  implementation; replacing every import is not an acceptance target.
- 2026-11-04: Use `jsontext.Decoder.ReadToken` for syntax, retaining number token
  text as `json.Number`. Never route exact JSON through `float64`. Keep one
  production parser, with no v1 fallback or compatibility decoder.
- 2026-11-04: Preserve all successful output bytes and declared schema identities
  in milestone 1. Existing invalid-input error classification and truncated-input
  wording remain acceptance gates; other low-level parser diagnostics may come
  from `jsontext`. No normative wire-spec change is intended.
- 2026-11-04: The initial delivery did not authorize measurements. The subsequent
  request to finish and ensure no speed regression explicitly authorizes focused
  paired JSON and generated end-to-end HTTP measurement. It does not select an
  all-root timing audit, production load certification, or unrelated optimization.
- 2026-11-04: Finish adoption by owner, not by import count. Go 1.27 already runs
  v1 on the v2 engine; retain call sites without an established simplification or
  measured benefit, especially heterogeneous caller-owned and foreign protocols.
  The inventories below are final retention decisions, not deferred migrations.
- 2026-11-04: Exact member names already follow the checked JSON Schema property
  names/additionalProperties rules. Rejecting ambiguous names and malformed Unicode
  enforces those current shapes. No field, output value, schema digest or persisted
  representation changes; retained-state and durable-input revisions are unchanged.
- 2026-11-04: Reject any paired 95% interval entirely above 1.0 and any interval
  whose upper bound exceeds 1.05. Thus a statistically detected small slowdown is
  not permitted, and noisy evidence cannot hide a material regression. Stable
  code paths may have point estimates on either side of 1.0 within measurement noise.

## Outcomes & Retrospective

Complete. The adopted v2 paths remove repeated syntax work, preserve successful
wire representations and meet the focused performance gates recorded below.
The selective adoption removes more production code than it adds. Generator
streaming, unrelated v1 owners and revision canonicalization deliberately remain
as recorded in the owner inventory; none is deferred acceptance work.

Final continuation evidence, all from 2026-11-04:

- `go run ./scripts/verify --summary --write`:
  `.scenery/harness/runs/20261104T195815.311701000Z/`; all Go tests, vet,
  architecture, contract drift and schemas pass. Stable input revision:
  `sha256:4923073287a1d9da2fff27a4d92bfa765b98625e848bfc3840375e2fb7211f46`.
  The compact/full context command union is covered by this full run and lint.
  There are zero errors, 44 existing document-review warnings and one advisory
  cached-suite wall-time warning (12.764 seconds versus the 5-second budget).
- `golangci-lint run ./...`: `0 issues.`
- `go test -race ./internal/appconfig ./internal/machine ./internal/contract`:
  all pass. `GOOS=linux go vet ./internal/appconfig`: pass.
- `go run ./scripts/verify --probe cli-process --probe native-contract --probe configuration --summary --write`:
  `.scenery/harness/runs/20261104T195857.661402000Z/`; the same stable input
  revision, zero errors, 44 document-review warnings. CLI process/telemetry,
  native generated client and restart reuse, and cross-worktree environment
  updates/rejection/unset/consumer-only restart all pass. Configuration reports
  cleanup passed; native `down` and shared build-cache cleanup succeed and its
  temporary fixture root is absent. The native app serves implementation
  `sha256:a382414b1fcdcb249408d798c11aa361182d16fb6bbf490b7dcf34fe3ce46f69`
  with build-input digest
  `sha256:06b39ed2184444a56f16dea59fc7daf98e844f47b8738af1cd51136ccf0ea456`.
- Both focused performance reports still match the final production source
  digest `d37e425ec6277002c5a4c04bf133bcc63f158c6ed642773f8918819e5bdfeebe`.
  All 12 JSON and six HTTP p50/p95 comparisons satisfy the regression gate.

Only documentation/evidence closure edits follow those source checks. Final
quick verification checks that closure; its immutable archive is linked in the
handoff instead of creating an endless plan-edit/reverify cycle. Normative
field shapes, schema identities, persisted input revisions, canonical digest
writers, generator templates, and generated artifacts are unchanged. The living
local contract now explicitly documents strict syntax and exact member names.
Auth/storage transports and secret-backend implementations are unchanged, so
their separate probes are unselected. Release certification, production capacity,
cross-platform performance, and all-root timing certification are not claimed.

Initial-delivery evidence remains historical: milestone 1 passed full verifier
`.scenery/harness/runs/20261104T193249.445676000Z/`, native-contract probe
`.scenery/harness/runs/20261104T193330.067915000Z/`, and final doc verification
`.scenery/harness/runs/20261104T193528.259843000Z/`. Those archives do not certify
the continuation's machine/appconfig changes. The invalidated first run is
excluded as described in Surprises.

## Plan of Work

All commands below run from the repository root. Use the prepared local CLI at
`.scenery/harness/bin/scenery`; do not install over the shared `scenery` binary.
Preserve unrelated working-tree changes and use no subagents.

### Milestone 1: exact contract token decoding

In `internal/contract/contract_json_exact.go`, replace the v1 token decoder with
`jsontext.NewDecoder` and dispatch on `Token.Kind`. Consume string and number
text before the next decoder call, since decoder tokens refer to its temporary
buffer. Let `jsontext` reject duplicate decoded object names, invalid Unicode,
malformed syntax, and excess nesting. Keep the explicit end-of-input check:
`jsontext` accepts streams, but contract values contain exactly one document.

Retain `writeExactCanonicalJSON`, `normalizeExactJSONNumber`, the output
expansion budget, and the fast fixed-point check. Extend the existing fixture
tables in `internal/contract/contract_json_exact_check_test.go` for exact numbers
above 2^53, long fractions, escaped duplicate names, nested duplicates, invalid
surrogate sequences, and trailing garbage. Existing string, depth, collection,
and wire tests remain the byte-compatibility evidence. Exercise the actual full
pass as well as the fast-path entry so a fast-path hit cannot hide a parser bug.

Run the named `native-contract` probe to exercise a real generated application,
its HTTP/TypeScript path, current served build identity, restart reuse, and
owned fixture cleanup. This parser delivery changes no CLI schema, generated
source template, persisted input revision, or normative specification; leave
their documents unchanged unless testing demonstrates an actual contract change.

### Milestone 2: strict input boundaries

Inventory `internal/machine/envelope.go`, `internal/appconfig/document.go`,
artifact readers, and configuration decoders using v1 `DisallowUnknownFields`.
Record each boundary's accepted field names, duplicate-name handling, Unicode,
trailing-data rules, unknown-field behavior, and exact-number requirements.
Migrate by owner using `jsontext` or v2 `Unmarshal`/`UnmarshalRead`, with explicit
options. Retain schema-directed record unknown-field policies. Distinguish
enforcing an existing requirement from changing accepted inputs; update affected
schemas, docs, revision descriptors, and tests together for a contract change.
Do not add a global JSON wrapper package or change third-party decoders.

The source inventory used
`rg -n 'DisallowUnknownFields' --glob '*.go' --glob '!**/*_test.go'`.
The following owner decisions complete this milestone; retaining v1 keeps its
existing contract on Go 1.27's new implementation, without a second decoder.

| Owner / paths | Decision and reason |
| --- | --- |
| `internal/machine/{envelope,artifact,artifact_upgrade}.go` | Migrate the common envelope/current-artifact entrypoint and upgrade object extraction. This also covers `DecodeArtifact` consumers in agent, storage, build, and retained-state owners. Explicitly retain nanosecond durations and numeric byte arrays. |
| `internal/appconfig/document.go` | Migrate strict document decoding in one pass; remove the recursive duplicate-name preflight and trailing token read. Raw values preserve numeric spelling. |
| `internal/contract` typed value/object/compiled-expression readers and `runtime/contract_{input,cli}.go`, `datasource/crud.go` | Retain schema-directed semantics, `UseNumber`, generated validation, presence and caller-owned typed views. The syntax boundary has already migrated in milestone 1. A second semantic rewrite has no demonstrated benefit. |
| `internal/{build,compiler,graph,evolution,deployplan,feature,workspacetx,storagefs,snapshotarchive}` direct readers | Retain their owner-specific payload, recovery and revision validation. This task changes their common `DecodeArtifact` boundary where used, not independent retained-state formats or custom projection decoding. |
| `internal/app/root.go`, `internal/{envpolicy,repoinfo,toolchain}` | Retain authored structural config and repository/toolchain document models. Replacing a single decode call adds no removed processing and would broaden their separate input semantics. |
| `auth`, assistant/MCP/control/token/provider packages, `internal/contractagent`, generated assistant-asset readers | Retain their external or independently versioned protocols, custom methods and unknown-field policies. Do not infer foreign protocol migration from the Go package version. |
| `runtime/process_*`, `runtime/deployment_config.go`, `cmd/scenery` private control/config/assistant/binding readers | Retain each private protocol's current decoder and data projection. Their valid artifacts still pass the full suite and named CLI/config/runtime probes; no API-only conversion is required. |
| Maintenance/export scripts and verifier reload protocols | Retain historical migration input and test-only protocol semantics. They are not application JSON processing hot paths. |

### Milestone 3: ordinary output encoding

Review JSON-tagged models by owner before migrating their encoder calls.
Explicitly preserve null versus empty arrays/objects, zero-value omission,
duration representation, custom methods, HTML escaping, and JSONL framing.
Use `omitzero` or caller options only where they implement the owning contract.
Keep `internal/spec/canonical.go` as the revision-byte authority; a replacement
must demonstrate identical revision inputs and digest outputs independently of
ordinary marshaling determinism. Update schemas and owning docs only for an
intentional contract change. Do not enable permissive global compatibility
defaults or maintain dual protocol spellings.

`Document.encode` is the selected ordinary output owner. It has a fixed shape
without `omitempty`, custom duration types, or caller-defined marshalers. Its v2
options preserve sorted maps, nil maps as null, raw escape spellings, HTML/JS
escaping, two-space indentation and the final newline; tests compare the complete
bytes with stdlib v1. Its paired encode medians improve by 18-20%.

Retain generic CLI/JSONL encoders because their `any` payloads include heterogeneous
`omitempty`, nil collections and caller methods; applying a global v1 option set
would merely spell the current implementation differently. Retain catalog digest
inputs, `internal/spec.MarshalCanonical`, same-schema upgrade output/payload
comparison, secret-version indexes and pins: byte identity and existing ownership
are more valuable than an API-only change without measured benefit. These are
the complete output-owner decisions for this plan.

### Milestone 4: generated streaming methods

Inspect `internal/generate/generate_go.go`, the schema-directed contract helpers,
and `runtime/contract_prepared.go` for repeated full-value encoding/decoding.
Evaluate `MarshalJSONTo` and `UnmarshalJSONFrom` on nested records, collections,
and outcome envelopes. Preserve optional/nullable presence, unknown fields,
validation, exact scalars, and stdlib-v1 interoperability. Retain an
implementation only when it simplifies the code with equivalent behavior or an
explicitly authorized end-to-end experiment demonstrates the performance
benefit. Record a no-change decision if neither condition is met. Regenerate
committed artifacts through the local CLI; never hand-edit them.

Evaluation result: no generator/runtime-prepared change is retained. Generated
records first validate their schema-directed fields into raw values and then
order the complete object; unknown-field collision/preservation also needs that
view. A `MarshalJSONTo` wrapper over the existing bytes still buffers and validates
them; `UnmarshalJSONFrom` calling `ReadValue` and the old method still reparses.
A real streaming rewrite would need another schema-aware traversal and ordering
strategy. That complexity is not justified by these measurements. Existing v1
methods already interoperate with v2, and the actual generated HTTP path is
measured below. `runtime/contract_prepared.go` remains outside a separate
prepared-payload optimization; no such optimization is needed to meet this plan.

### Focused performance experiment

The baseline is an immutable `git archive` of `57fbfdaa`, stored in
`.scenery/harness/json-v2/baseline`; only identical benchmark test definitions are
copied into it. `scripts/bench-json-v2.py` builds test binaries for contract,
machine and appconfig and alternates baseline/candidate order with `GOMAXPROCS=1`.
It records the same Go toolchain, source digests, raw samples, allocations, medians
and paired bootstrap 95% intervals. Reproduce the bounded micro comparison with:

```sh
python3 scripts/bench-json-v2.py --baseline .scenery/harness/json-v2/baseline --candidate . --output .scenery/harness/json-v2/micro-initial --pairs 8 --benchtime 100ms
```

The retained result has all 12 cases, with no statistically detected slowdown and
every interval upper bound below +5%. Fast-path canonicalization is unchanged
within noise (+0.4% point estimate, paired interval -1.4% to +0.6%). Full-pass
canonicalization improves 9.3%, string cases 9.7-26.2%, machine envelope decoding
51.1%, artifact decoding 37.6%, document encoding 17.7-20.0%, and document decoding
31.2-38.8%. These are native Apple M2 Ultra JSON microbenchmarks, not endpoint
latency claims. Original throughput-bearing benchmark lines were reparsed to
include both canonicalization cases; the checked-in runner now fails closed on
an incomplete expected case inventory.

The actual HTTP experiment is archived with its driver at
`.scenery/harness/json-v2/http-bench.py` and results at
`.scenery/harness/json-v2/http-initial/report.json`. It copies the native fixture
into two disposable apps and adds the same optional JSON input/JSON echo output
to both generated contracts. `framework use --source` prepares content-bound
executables; ordinary unstamped `go build` executables were correctly refused
before measurement. Every response verifies the host PID, build-input digest,
implementation revision, service PID and exact decoded payload. Twelve alternating
pairs of 150 keep-alive requests per variant/scenario follow 150 warmups: 10,800
measured requests plus 900 warmups, all successful. The temporary app/state roots
are removed and no owned processes survive. Both runtime bundles and CLI hashes
are retained in the report/archive.

| Generated HTTP scenario | Baseline p50 / candidate p50 | Baseline p95 / candidate p95 |
| --- | --- | --- |
| Small JSON body | 525.5 / 530.5 us | 809.9 / 808.1 us |
| Canonical nested JSON, 100 exact-ID records | 1428.5 / 1394.3 us | 2113.7 / 2077.4 us |
| Pretty JSON with Unicode escapes, same records | 1489.3 / 1490.2 us | 2222.3 / 2139.6 us |

All six paired p50/p95 intervals include no statistically detected slowdown and
exclude a regression above 5%; small point differences lie within noise.
The pretty/escaped p95 improves about 3.7%. This is sequential local generated
HTTP proof, not production capacity, load-tail, RSS, or cross-platform performance
certification. No production source edit may follow without checking the recorded
source digest and remeasuring affected cases.

## Validation and Acceptance

For milestone 1, the expected cumulative classes are Go packages,
release-sensitive/runtime, and documentation. Select the full verifier after
editing, not quick followed by full. Run:

```sh
go test ./internal/contract ./runtime
go test ./internal/contract -run '^$' -fuzz '^FuzzCanonicalExactJSONFastPath$' -fuzztime=5s -parallel=2
go run ./scripts/verify --summary --write
golangci-lint run ./...
go run ./scripts/verify --probe native-contract --summary --write
git diff --check
```

The full verifier supplies the required cached `go test ./...` and vet proof
and prepares the local product binary under the fresh-worktree workflow in
`docs/agent-guide.md`. Read each reported archive's `agent-context-summary.json`
first, then `agent-context.json`; complete the exact union of
`changed_area.recommended_commands` and record commands, results, warnings,
blockers, and archive paths here. Reuse successful checks for unchanged inputs
and scope. Plan-only evidence updates do not require rerunning unchanged Go
tests or probes, but validate their final documentation state with the verifier.

Acceptance requires unchanged canonical fixture bytes, exact large numbers and
fractions, invalid-input rejection including escaped and nested duplicate names,
unchanged maximum nesting and expansion limits, correct HTTP problem outcomes,
and a successful native-contract assertion inventory with current served
identity and cleanup. A skipped or blocked probe is not a pass. The bounded
fuzz run supplements deterministic cases and does not establish exhaustive
correctness or a performance claim.

For milestone 2, run `go test ./internal/machine ./internal/appconfig ./runtime`
plus the affected-owner commands returned by `inspect docs`, the full verifier,
lint, and `go run ./scripts/verify --probe cli-process --probe native-contract --summary --write`.
For milestone 3, run affected-owner commands, full verifier, lint, and the same
two probes. If an auth, configuration, or storage external boundary changes,
also run respectively `go run ./scripts/verify --probe auth --summary --write`,
`go run ./scripts/verify --probe configuration --summary --write`, or
`go run ./scripts/verify --probe storage --summary --write`; skip each only when
the final changed paths and owner analysis confirm that boundary is unchanged,
and record that analysis in Progress.

Milestone 4 adds the compiler/generator class. Run
`go test ./internal/contract ./internal/generate ./runtime`, regenerate both
committed clients with
`.scenery/harness/bin/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`
and the same command with `--app-root internal/compiler/testdata/house`, then run
the full verifier, lint, and
`go run ./scripts/verify --probe generation --probe native-contract --probe ui --summary --write`.
The UI probe owns dependency preparation, generated-client conformance, and the
exact TypeScript checks. These additional checks are unselected for milestone 1
because its final change contains no compiler/generator/UI edits; record that
condition alongside its final diff.

For the remaining delivery, also run the appconfig owner's
`go test -race ./internal/appconfig` and `GOOS=linux go vet ./internal/appconfig`,
plus `go test -race ./internal/machine ./internal/contract`. The named configuration
probe is selected because its authoritative document codec changed. Auth and
storage-specific probes are unselected: their transports, data formats, policy,
and storage owners are unchanged; the shared artifact decoder has unit/full-suite
coverage and the CLI/native/config probes exercise its real consumers. Generator
regeneration/UI probes remain unselected because milestone 4 retains every
template and generated artifact unchanged.

Release certification, production deployment and all-root timing audits are
unselected. Focused performance comparisons above are explicitly authorized.

## Idempotence and Recovery

All validation commands may be retried using Go's test cache. Verifier archives
are immutable local evidence and must remain uncommitted. The native-contract
probe owns its disposable application/process resources and must report cleanup;
if it fails, inspect its report and ownership before removing anything. Do not
reset another application's data or adopt mismatched retained resources.

No application data or persisted schema is changed by milestone 1. To abandon
its implementation, reverse only this delivery's explicit source/test edits;
preserve unrelated edits and update this active plan with the outcome. Later
milestones that change persisted inputs must specify their owner migration and
durable revision/drain procedure before implementation, rather than introducing
an old decoder as recovery.
