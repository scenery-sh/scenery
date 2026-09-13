# Go Plugin Reload Feasibility

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under `PLANS.md`.

## Purpose / Big Picture

Determine whether a stable Go host can load a newly compiled real ONLV
implementation island fast enough to justify further work toward the architecture
described in `next.md`. The existing stock-Go process-per-edit experiment already
reduced the island from 620 to 238 packages, but still measured 518.559 ms build
p50, 401.598 ms launch/attestation/ready p50, and 934.457 ms build-to-typed-response
p50. This plan tests one materially different hypothesis: keep the host process
alive and replace only a uniquely built Go plugin containing the real AHJ
implementation and minimal typed dispatch.

This is a repository benchmark and architecture decision, not a new product
runtime mode. It must not change Scenery application APIs, `.scn`, build/deploy
artifacts, `scenery up`, public identity meanings, or the current monolithic
runtime. A failed timing, compatibility, lifecycle, or retention result ends the
experiment without production integration.

## Progress

- [x] 2026-09-14: Reconcile clean committed implementation head `59189167`, the
  untracked developer-authored `next.md`, Plans 0180/0181/0186, the current
  architecture decision, verifier ownership, and local Go plugin constraints.
- [x] 2026-09-14: Add a reproducible `native-reload-plugin` repository benchmark using the
  pinned real ONLV AHJ implementation in an owned disposable worktree.
- [x] 2026-09-14: Build one stable host and five run-unique implementation plugins; bind and
  verify exact source, toolchain, contract, host, artifact, generation, worktree,
  and session identities before typed invocation.
- [x] 2026-09-14: Measure package closure, artifact size, initial/warm build, plugin open and
  initialization, activation, first typed response, RSS growth, and retained
  generations. The predeclared early gate failed, so the run correctly stopped
  after five measured edits rather than continuing to 30.
- [x] 2026-09-14: Record the NO-GO decision and keep only the reusable evidence
  harness or remove a failed prototype that cannot answer the decision safely.
- [x] 2026-09-14: Run the exact cumulative validation selected by the changed-area oracle and
  commit this independently reviewable experiment without modifying ONLV source.

## Surprises & Discoveries

The local Go 1.27 `plugin` package supports darwin/arm64, initializes newly loaded
packages during the first `plugin.Open`, cannot close a plugin, and requires the
host and plugin to use identical toolchain, flags, and common dependency source.
Those are acceptance inputs, not assumptions that the mechanism is promotable.

Every implementation generation needs a distinct Go plugin package import path,
not merely a different output filename. Setting the linker's `-pluginpath` on a
fixed compiled package produced an artifact whose expected exported symbol was
absent. The corrected experiment generated a run-private package directory for
each sample; seven distinct implementations then loaded and executed correctly.

The supposedly smaller dynamic artifact was not smaller: it retained the same
238-package closure as Plan 0181 and grew from 8,166,370 bytes for the executable
island to 15,483,074 bytes for a representative plugin. Warm plugin build/link
and `plugin.Open` were both slower than the corresponding rejected process
boundary. Removing `exec` did not remove the measured native load floor.

## Decision Log

- Decision: use a separate `native-reload-plugin` verifier benchmark and Plan
  0187. Rationale: Plan 0181's process-per-edit result remains immutable evidence;
  a plugin has different lifecycle, compatibility, retention, and portability
  properties and must not silently change that benchmark's meaning. Date:
  2026-09-14. Author: Codex.
- Decision: reuse the pinned ONLV commit, AHJ package, generated typed contract,
  unique-edit mechanism, input capture, statistics, bounded evidence, and owned
  cleanup from Plan 0181. Rationale: only the execution artifact boundary should
  differ, so the comparison remains attributable. Date: 2026-09-14. Author:
  Codex.
- Decision: set the feasibility gate to build p50 at most 200 ms, first
  `plugin.Open` plus activation p50 at most 50 ms, and build-to-new-typed-response
  p50 at most 250 ms. Stop after five if combined p50 exceeds 325 ms or the
  closure exceeds 310 packages. Rationale: a stable host is worthwhile only if
  the mechanism removes the measured process boundary and leaves headroom for
  the advertised endpoint. Date: 2026-09-14. Author: Codex.
- Decision: never reuse a plugin path and never claim unloading. Rationale:
  `plugin.Open` caches a loaded plugin and Go exposes no close operation. Every
  sample therefore has a unique artifact path and the benchmark measures RSS and
  retained generation growth honestly. Date: 2026-09-14. Author: Codex.
- Decision: reject standard Go plugins for Scenery's ordinary reload boundary.
  Rationale: five unique real ONLV edits measured 1,333.538 ms build p50,
  453.407 ms `plugin.Open` plus activation p50, and 1,814.363 ms native
  replacement p50, failing all 200/50/250 ms gates and worsening Plan 0181's
  already rejected 518.559/401.598/934.457 ms process results. Date:
  2026-09-14. Author: Codex.

## Outcomes & Retrospective

The experiment is complete and is a clear NO-GO for standard Go plugins. On an
Apple M2 Ultra with 64 GiB RAM, 24 logical CPUs, Go 1.27.0, darwin/arm64 and
existing Go caches retained, two warmups preceded five unique compiled changes
to the pinned real ONLV AHJ handler. Every successful typed result contained its
run-unique behavior and exact implementation generation; no identity or
correctness failure occurred.

| Boundary | Plan 0181 process | Plan 0187 plugin |
|---|---:|---:|
| Package closure | 238 | 238 |
| Artifact bytes | 8,166,370 | 15,483,074 |
| Warm build p50 / p95 | 518.559 / 567.220 ms | 1,333.538 / 1,383.796 ms |
| Launch-ready or open-activate p50 / p95 | 401.598 / 417.637 ms | 453.407 / 476.757 ms |
| Native replacement p50 / p95 | 934.457 / 977.022 ms | 1,814.363 / 1,860.485 ms |
| Edit through typed response p50 / p95 | 1,303.663 / 1,393.062 ms | 2,194.590 / 2,260.516 ms |

Activation itself was 0.140--0.293 ms and steady typed calls had per-sample p50s
of 0.066--0.089 ms. Neither dispatch nor application construction is the
remaining floor. Plugin build/link and dynamic loading dominate.

The stable experimental host used 222 packages and 11,654,882 bytes. Loading
seven unique generations grew its RSS from 20,660,224 to 45,187,072 bytes, a
24,526,848-byte increase; Go exposes no unload. A plugin compiled against a
changed common generated contract failed closed with `plugin.Open` reporting a
different version of `clean.tech/solar/ahjs/scenerycontract`, and the prior
generation remained active.

The predeclared first-five rule stopped the 30-edit series. This is valid
rejection evidence, not final latency acceptance. No stable host, plugin loader,
transport, SDK, application runtime, build/deploy output or public contract was
added to production Scenery.

Corrected raw evidence is under
`.scenery/harness/minimal-native-reload-plugin/attested-2471988027/`;
`report.json` SHA-256 is
`c69d1189e9b6aeeb91ca1508de3d8a6e9f24a76a0e13568799c18c1be006a925`.
The report binds framework source digest
`sha256:4fd4fd51ee71b69b2afa85d55d6688c092a79052ded683c511c7df1d7d467cf3`,
prepared executable digest
`sha256:faa0d41f8d0acb167892a9fb8db65fdc634ada7df4b73d3d78705a448879f0a6`,
ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`, and the exact host/plugin/input
identities. The host stopped, the owned worktree was removed, and the original
ONLV checkout status was unchanged.

Validation passed: `go test ./scripts/verify`, `go test ./cmd/scenery`,
`go test ./...`, `golangci-lint run ./...`,
`go run ./scripts/verify --quick --summary --write`, and
`go run ./scripts/verify --summary --write`. The default verifier reported only
the existing knowledge/architecture warnings plus an advisory 5.820 s cached
suite duration over its 5 s target. The exact plugin benchmark command passed
its selected repository checks while truthfully reporting `decision: no_go`.
Race, release, database and production functional probes were not selected:
this slice changes only the repository evidence benchmark and adds no product
execution boundary.

## Context and Orientation

`scripts/verify/harness_self_native_reload*.go` owns the current Plan 0181 real
ONLV process experiment: it creates an owned detached worktree, selects the exact
prepared framework, generates contracts, mutates one real handler with unique
behavior, captures the complete Go dependency set, builds the island, verifies
linked identity, invokes the generated typed codec, retains raw evidence, and
removes owned resources.

The new benchmark belongs beside that owner. Its host is an experimental binary
built once inside the disposable ONLV module. Each plugin is a `main` package
built with `go build -buildmode=plugin`; it imports the unchanged authored AHJ
package and generated contract, exports a small typed-codec dispatch function and
a linked immutable identity record, and owns its real constructor state. The host
imports the generated contract but not the AHJ implementation, verifies the
plugin file and linked record, activates it, and invokes the selected operation.

`plugin.Open` runs package initialization before returning. Activation remains a
separate explicit call, but this experiment cannot claim that arbitrary
application `init` side effects are fenced. A successful timing result is only an
admission gate for later SQL, auth, internal-call, streaming, lifecycle, debugger,
portability, rollback, and bounded-retention work.

## Milestones

1. Produce one valid real AHJ plugin and invoke its generated typed input/outcome
   through a host that does not import the implementation package.
2. Bind host and plugin to exact common source, toolchain, target, framework,
   contract, build input, implementation, session, worktree, and artifact
   identities; reject stale or incompatible records before activation.
3. Run two warmups and five unique edits, separating input capture, build,
   artifact digest, open/initialization, activation, first invocation, and total
   edit-to-response time.
4. Continue to 30 only under the early gate. Measure stable-host RSS before and
   after every retained plugin and record that none is unloadable.
5. Publish a clear GO/NO-GO decision and validation evidence without adding a
   product runtime path.

## Plan of Work

Extend the repository verifier grammar with the explicit benchmark identifier
`native-reload-plugin`, requiring the same `--workload-root` argument as the
existing native reload benchmark. Reuse the existing input capture and command
recording helpers. Do not add a product CLI option or environment variable.

Create experimental host and plugin templates under `scripts/verify/testdata/`.
Build the host once before warmup. The host must report its own executable digest,
PID, toolchain, target, worktree and session over private inherited pipes. It
loads only an absolute plugin path beneath the owned experiment root, hashes the
artifact immediately before and after `plugin.Open`, and compares the exported
linked identity before calling activation or dispatch.

For each sample, edit the real `solar/ahjs/service.go` validation message to a
run-unique value, capture the complete plugin closure and input digests, build a
unique `.so`, calculate its digest and request that the existing host load it.
The plugin dispatch uses `UnmarshalListAhjsInput`, calls the real `ListVNext`, and
uses `MarshalListAhjsOutcome`; the chosen oversized query exercises typed
validation without claiming a database transaction.

Record every command, failure, source digest, identity, response and interval.
Verify the source set again after execution, restore the original handler, stop
the host with the existing concrete supervisor, confirm all owned children stop,
and remove only the exact owned worktree/root. An incompatible common-package
build must fail closed rather than crash or become active.

## Concrete Steps

All commands run from `/Users/petrbrazdil/Repos/scenery`.

1. Add Plan 0187 to `docs/plans/active.md` and `docs/knowledge.json`.
2. Add parser/catalog coverage for `--benchmark native-reload-plugin
   --workload-root <path>` and update `docs/harness-engineering.md` plus the
   repository-only section of `docs/local-contract.md`.
3. Add the stable host/plugin templates and focused in-process tests for strict
   identity, framing, unique paths, statistics, and early-stop selection.
4. Run `go test ./scripts/verify`, then execute the real benchmark against
   `/Users/petrbrazdil/Repos/onlv` with `--write`.
5. Update this plan with exact measurements and the decision. If the result is
   NO-GO, do not add production host or plugin code.
6. Refresh changed-area selection, run the cumulative commands below, and commit
   the bounded evidence harness and decision.

## Validation and Acceptance

Expected classes are `go-package`, `cli-json-contract`, documentation, and
repository verifier benchmark ownership. Run:

```sh
go test ./scripts/verify
go test ./...
golangci-lint run ./...
go run ./scripts/verify --quick --summary --write
go run ./scripts/verify --summary --write
go run ./scripts/verify --benchmark native-reload-plugin --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
git diff --check
```

After quick verification, read `.scenery/harness/agent-context.json` and execute
every cumulative recommended command not already listed. The selected benchmark
must report whether feasibility passed independently of the verifier step status,
must retain failures, and must confirm host/worktree cleanup.

Default/race/release and production functional probes are not interchangeable.
Run `go run ./scripts/verify --race --summary --write` only if the changed-area
oracle selects race or shared concurrency code outside the benchmark changes.
Do not run the release gate: no production runtime, deployment, public API,
compiler, generator, storage, assistant, or database owner changes in this plan.
If implementation crosses one of those boundaries, update this plan first and
run its exact owning command rather than treating the benchmark as proof.

Acceptance requires a reproducible report bound to exact framework and ONLV
commits, two warmups, at least five unique compiled behaviors, no identity or
correctness failure, separated timing phases, and truthful retained-plugin/RSS
evidence. Production promotion additionally requires 30 samples and all three
numerical gates. A successful repository command with `decision: no_go` is valid
experimental completion, not performance acceptance.

## Idempotence and Recovery

Every run uses a new session, temporary root, detached ONLV worktree, host path,
and plugin path. Evidence publication uses the existing atomic writer. Cleanup
first stops the exact supervised host, verifies the owner marker, removes the
owned worktree through Git, and then removes the exact temporary root. A failed
or canceled run retains bounded evidence when `--write` is selected and reports
any unconfirmed child instead of deleting its root.

The original ONLV checkout and its services/data remain read-only. The benchmark
does not clear Go caches, install a Scenery binary, mutate shared global state,
or use branch names/PIDs as ownership authority.

## Artifacts and Notes

Raw evidence belongs under ignored
`.scenery/harness/minimal-native-reload-plugin/`. The report must name the exact
host executable and plugin paths, but those disposable binaries are not committed.
`next.md`, Plan 0181, and
`docs/native-reload-execution-technology-decision.md` remain the architectural
brief and prior evidence; this plan does not rewrite their historical timings.

## Interfaces and Dependencies

The experimental plugin exports a strict record and three functions equivalent
to:

```go
var SceneryNativeReloadIdentity string
func SceneryNativeReloadActivate(fail bool) error
func SceneryNativeReloadInvoke(operation, binding string, input []byte) ([]byte, error)
```

The host owns plugin-path/digest verification and atomic current-generation
selection inside the experiment. The plugin owns application initialization,
constructor state, the real AHJ implementation, generated typed codecs, and the
rejecting SQL capability used by the existing validation-only sample. No new Go
dependency, daemon, database, public SDK, product command, configuration key, or
environment variable is introduced.
