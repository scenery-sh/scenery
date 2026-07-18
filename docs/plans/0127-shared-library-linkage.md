# 0127 Shared Library Linkage: Compile pkg/ Libraries From Source or Load Prebuilt .so/.dylib

This ExecPlan is a living document maintained per `PLANS.md`. It originated as an ONLV plan (`docs/agent/exec-plans/active/scenery-shared-library-linkage.md` in the ONLV repo, 2026-07-18) and moved here because the capability is Scenery-generic; the ONLV side retains only a pointer.

## Purpose / Big Picture

Scenery apps can declare a `pkg/` Go library (first target: ONLV's `pkg/maps3d`) as a contract-bearing **library** whose consumers choose a linkage mode by configuration, not code:

- **source**: the library compiles into the consuming Go application as a normal package import (today's behavior — zero overhead, full debuggability).
- **shared**: the library is consumed as a prebuilt platform shared object (`.dylib` darwin/arm64, `.so` linux/amd64) loaded at runtime via `dlopen` — no source access required by the consumer, hot-swappable to a newer version while the consumer process keeps running.

Observable outcome: a separate Go application without ONLV source calls maps3d operations through a Scenery-generated typed facade; flipping linkage from `source` to `shared` changes no application code; replacing the shared object and triggering a swap serves subsequent calls from the new version without a process restart. Measured overhead of shared linkage is ~2% for coarse-grained calls (feasibility prototype, see Artifacts and Notes).

## Progress

- [x] (2026-07-18 12:35Z) Feasibility prototype completed in ONLV local workspace `x/wasmbench` (gitignored; all findings folded into this plan). Validated: c-shared build of the maps3d pipeline, cgo-free consumption via purego, +2.3% overhead, byte-identical output, multi-version coexistence, hot-swap protocol, two-target build matrix, runtime platform selection.
- [x] (2026-07-18 13:05Z) Plan drafted, all owner decisions resolved (platform matrix, FMA parity, signing, grammar/config shape, wasm rejection).
- [x] (2026-07-18 13:20Z) Plan moved from the ONLV repo into Scenery as 0127.
- [ ] Milestone 1: `library` contract concept (declaration parsing, contract hash, validation).
- [ ] Milestone 2: generation — dual-backend facade + c-shared export shim.
- [ ] Milestone 3: `scenery build --lib` artifact pipeline + manifest.
- [ ] Milestone 4: ONLV adoption — `pkg/maps3d/scenery.package.scn` library declaration and facade consumption.
- [ ] Milestone 5: hot-swap runtime support in the generated shared-linkage client.
- [ ] Milestone 6: guardrails + docs (ONLV repoharness rules, AGENTS.md chain updates in both repos).

## Surprises & Discoveries

Findings from the 2026-07-18 feasibility prototype (evidence lived in ONLV's gitignored `x/wasmbench`; the numbers here are the durable record):

- Observation: WebAssembly was evaluated first and rejected. Under wazero (the only cgo-free Go wasm runtime) the maps3d merge+GLB pipeline ran 7–8x slower than native; allocation-free float kernels showed a 3.4x floor, integer kernels 1.5x; Node/V8 managed 3.3x overall. TinyGo 0.41 compiled the pipeline but crashed in its own runtime under three configurations (conservative GC OOB in `runtime.hashmapSet` during init, `-opt=1` same, precise GC panic).
  Evidence: wazero/wasmtime/Node benchmark harnesses over a 300-tile synthetic scene, single worker.
- Observation: `-buildmode=c-shared` + purego (`github.com/ebitengine/purego`, dlopen without cgo) achieves +2.3% overhead versus in-process native with byte-identical output. The consumer builds with `CGO_ENABLED=0`; only the artifact build needs the cgo toolchain.
  Evidence: native best 536.6ms vs .so best 548.8ms on the same workload, identical sha256.
- Observation: Two independently built versions of the c-shared library coexist in one process when loaded with `RTLD_LOCAL` (each embeds its own Go runtime); the old version stays callable after a newer one loads, enabling drain-then-abandon swaps. `dlclose` returns success on darwin but a Go runtime can never actually be torn down (scheduler threads persist); treating that success as a real unload risks crashing those threads.
  Evidence: hot-swap probe — v1 and v2 loaded together, both served identical results, process survived a `dlclose(v1)` probe with v2 unaffected.
- Observation: Outputs are deterministic per-architecture but differ between arm64 and amd64 because the Go compiler fuses `a*b+c` into FMA on arm64 only. Same-arch results are bit-stable across native, .so, and every wasm runtime tested.
  Evidence: arm64 GLB sha `3c82f93d...` vs amd64/wasm sha `d84036ba...`, identical sizes; a `GOARCH=amd64` native rebuild reproduced the wasm hash exactly.
- Observation: Cross-compiling c-shared from darwin to linux/amd64 requires a target C toolchain; building inside `--platform linux/amd64 golang:1.26` works, and the produced `.so` loaded and ran on debian bookworm. `zig cc` is a docker-free alternative.
  Evidence: platform-selecting loader executed `libworkload_linux_amd64.so` inside a bookworm container.
- Observation: Go dropped binary-only package support in Go 1.13; there is no compiler-level way to import a prebuilt Go package. Any "import the library" path must cross a C-ABI dlopen boundary (or a subprocess), which is why the design centers on a declared operation contract rather than the raw Go API.
  Evidence: Go release history; this constraint shaped the whole design.

## Decision Log

- Decision: Use `c-shared` + purego (dlopen without cgo) as the shared-linkage mechanism; reject WebAssembly and `-buildmode=plugin`.
  Rationale: wasm misses the ≤20% overhead requirement by 6–8x under cgo-free runtimes; `plugin` locks host and plugin to identical Go versions and dependency graphs and requires a cgo-enabled host. c-shared over a C ABI has no Go-version coupling and measured +2.3% overhead. Wasm is out of scope entirely — the owner may reconsider in the far future if Go-on-wasm toolchains mature, but any revisit starts from fresh measurements.
  Date/Author: 2026-07-18 / Claude (with owner).
- Decision: The substitution unit is a declared operation contract (`library` block with `record` inputs/outputs in `scenery.package.scn`), not the package's full Go API. Consumers import a generated facade with two backends.
  Rationale: a shared object only exposes a C ABI; transparent source-level substitution of an arbitrary Go API is impossible (no binary package imports since Go 1.13). Scenery already has records, operations, contract hashing, and generator infrastructure to express exactly this.
  Date/Author: 2026-07-18 / Claude (with owner).
- Decision: Reuse the Scenery wire encoding for values crossing the C ABI; no new serialization format.
  Rationale: the wire format already has generated encoders/decoders and contract-hash semantics; a second boundary format would double the parity surface.
  Date/Author: 2026-07-18 / Claude.
- Decision: The shared-linkage platform matrix is exactly `darwin/arm64` (`.dylib`) and `linux/amd64` (`.so`). No other targets are built, tested, or promised; the generated loader fails with a clear "unsupported platform for shared linkage" error elsewhere (source linkage remains available everywhere Go compiles).
  Rationale: owner decision. Matches the actual dev (Mac Studio) and deploy (linux/amd64 server) fleet; each extra target adds a C toolchain, CI leg, signing story, and determinism surface for no current consumer.
  Date/Author: 2026-07-18 / Petr.
- Decision: Hot-swap is load-alongside: verify artifact digest → `Dlopen(RTLD_NOW|RTLD_LOCAL)` → check exported ABI hash → atomically switch the routing handle → drain and abandon the old handle. Never call `dlclose` on a Go c-shared library.
  Rationale: a Go runtime cannot be unloaded; abandoned versions cost tens of MB plus parked threads, acceptable at realistic swap cadence. `RTLD_LOCAL` is mandatory so coexisting Go runtimes' symbols do not collide.
  Date/Author: 2026-07-18 / Claude (with owner).
- Decision: Cross-arch FMA divergence is accepted; per-architecture determinism is the contract. No FMA-suppression edits to consumer math code; parity tests compare outputs within one architecture only.
  Rationale: owner decision. Production artifacts are produced on linux/amd64; darwin/arm64 is a development platform.
  Date/Author: 2026-07-18 / Petr.
- Decision: No codesigning/notarization work in this plan. The darwin dylib serves internal tooling only; ad-hoc signing suffices. If a third-party-distributed macOS consumer appears, signing becomes a new plan.
  Rationale: owner decision. Gatekeeper only gates distribution to machines outside our control.
  Date/Author: 2026-07-18 / Petr.
- Decision: The `.scn` grammar mirrors the existing `service` idiom: a `library "<name>"` block with `runtime`/`artifact`, and `operation` blocks referencing `library.<name>` — no new grammar concepts beyond the block type. Linkage is configured on the consumer side as part of its dependency declaration (per-environment resolvable); `local` defaults to `source`.
  Rationale: smallest grammar delta; keeps the choice with the party it affects. Final field spelling settles in Milestone 1 against the actual parser.
  Date/Author: 2026-07-18 / Claude.
- Decision: First iteration exposes only coarse-grained operations; no opaque state handles across the boundary.
  Rationale: per-call wire encoding punishes chatty interfaces; maps3d's natural surface (build scene → GLB bytes) is coarse. Handle-based stateful APIs can be layered in later.
  Date/Author: 2026-07-18 / Claude.

## Outcomes & Retrospective

Not yet completed. The feasibility prototype fully de-risks the dlopen mechanics; contract design and generator work have not begun.

## Context and Orientation

Definitions:

- **c-shared**: `go build -buildmode=c-shared` produces a platform shared object embedding a full Go runtime, exposing `//export`-marked functions through the C ABI. Building requires the cgo toolchain; loading does not.
- **purego**: `github.com/ebitengine/purego`, pure-Go dlopen/dlsym/trampolines. Lets a `CGO_ENABLED=0` Go binary load and call C-ABI shared objects on darwin and linux — the entire supported matrix.
- **Linkage**: the per-consumer choice between `source` (compile the pkg in) and `shared` (dlopen the prebuilt artifact).

Where things live:

- Scenery contract machinery: root `contract*.go` (hashing, validation), `scenery.package.scn` parsing, and the generators that render contract packages and clients into external build/editor caches. The `library` concept extends this machinery.
- Reference app: ONLV at `/Users/petrbrazdil/Repos/onlv` (`.scenery.json` app `clean-tech`). Its `pkg/maps3d/` is a plain Go library today (scene pipeline in `pkg/maps3d/meshops/`, local rules in `pkg/maps3d/AGENTS.md`, strict no-cgo subprocess contract for its external encoders). Existing `scenery.package.scn` shape to imitate: ONLV's `audit/scenery.package.scn`.
- ONLV-side pointer: ONLV `docs/agent/exec-plans/README.md` references this plan; ONLV keeps no duplicate plan body.
- Development against ONLV uses the worktree-local Scenery source: `go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery ... --app-root /Users/petrbrazdil/Repos/onlv`.

## Milestones

1. **Library contract** (Scenery): `library` block parses, validates, and contributes to contract hashes. Repo stays green with no app using it.
2. **Generation** (Scenery): source-backend facade generation lands first (pure Go, provable with generate --check); shared-backend facade and c-shared export shim follow.
3. **Artifact pipeline** (Scenery): `scenery build --lib` produces the two-target matrix + manifest.
4. **Adoption** (ONLV): maps3d declares the library; one internal consumer routes through the facade in source mode with no behavior change.
5. **Hot-swap** (Scenery): shared-backend version registry, swap protocol, observability of loaded versions.
6. **Guardrails/docs** (both repos): ONLV repoharness rules; AGENTS.md updates; this plan moved to completed.

Each milestone keeps both repos testable; shared-backend work (2b, 5) is additive behind the linkage config.

## Plan of Work

Milestone 1 — extend the `scenery.package.scn` schema with a `library` block (sibling of `service`): name, `runtime = "go"`, `artifact { name = ... }`, plus `operation` blocks referencing `library.<name>` with `record` inputs/outputs and `handler { method = ... }` mapping to an exported function in the package. Restrict declarations to `pkg/`-rooted packages. Fold library operations into contract-hash computation so every library has a stable ABI hash. `scenery check` rejects non-wire-encodable operation types, non-`pkg/` paths, and missing handler methods, with actionable diagnostics.

Milestone 2 — two generator outputs per library, both generator-owned (never committed): (a) the **facade package** consumers import — one typed Go function per operation, backend chosen by resolved linkage: `source` calls the handler directly (thin compile-time alias, no serialization); `shared` wire-encodes input, invokes the exported symbol via purego, decodes output, maps error records to Go errors; (b) the **export shim** — a `package main` with one `//export` per operation plus `SceneryLibVersion`, `SceneryLibABIHash`, and `SceneryLibFree`, wire-decoding inputs and encoding outputs. Boundary memory protocol: the shim returns a malloc'd (ptr,len) buffer; the facade copies then immediately calls `SceneryLibFree`.

Milestone 3 — `scenery build --lib <pkg>` (exact CLI shape finalized here) builds the shim with `-buildmode=c-shared` for the fixed matrix — darwin/arm64 natively, linux/amd64 via a pinned `golang:<go-version>` container on the oldest supported glibc base — stamps the version via `-ldflags -X`, and writes `lib<name>_<GOOS>_<GOARCH>.<dylib|so>` plus a manifest: library name, semantic version, ABI hash, per-artifact sha256, build Go version, glibc floor.

Milestone 4 — ONLV: add `pkg/maps3d/scenery.package.scn` declaring the maps3d library with its first coarse operations (build-scene-to-GLB; records shaped against `meshops.Scene`/`OutputMesh` during implementation). Route one internal consumer through the facade in `source` mode to prove no behavior change; leave other direct imports untouched.

Milestone 5 — shared backend runtime: a version registry keyed by manifest, an `atomic.Pointer` routing handle, and a swap entry point implementing verify-digest → dlopen(`RTLD_NOW|RTLD_LOCAL`) → ABI-hash check → flip → drain. Expose current/loaded versions for observability. No `dlclose`, ever; document the resident-memory cost per abandoned version and recommend process recycling for high-churn consumers.

Milestone 6 — ONLV `internal/repoharness` checks (`library` blocks only under `pkg/`; no cgo in library package source — the only cgo is the generated shim; record-typed operations only), `pkg/maps3d/AGENTS.md` library section, ONLV root `AGENTS.md` index if needed, Scenery docs (`docs/index.md` / cookbook) for the new declaration and CLI.

## Concrete Steps

Scenery development loop (repo root `/Users/petrbrazdil/Repos/scenery`):

    go test ./...
    go run ./cmd/scenery check -o json --app-root /Users/petrbrazdil/Repos/onlv
    go run ./cmd/scenery generate --check --app-root /Users/petrbrazdil/Repos/onlv

ONLV validation loop (repo root `/Users/petrbrazdil/Repos/onlv`):

    just repo-harness
    scenery check --json
    go test ./pkg/maps3d/...

Linux artifact build (pattern proven by the prototype; exact invocation becomes `scenery build --lib` in Milestone 3):

    docker run --rm --platform linux/amd64 \
      -v /Users/petrbrazdil/Repos:/Users/petrbrazdil/Repos -w <pkg dir> \
      -e GOWORK=off -e GOFLAGS=-buildvcs=false golang:1.26 \
      go build -buildmode=c-shared -o lib<name>_linux_amd64.so ./<shim>

## Validation and Acceptance

1. Contract: `scenery check` accepts the maps3d `library` declaration and rejects a deliberately broken one (non-record type, non-`pkg/` path, missing handler) with actionable diagnostics.
2. Parity: a golden test calls the same operation through the source-mode facade and a locally built shared artifact and asserts byte-identical outputs on the same architecture (deterministic fixture inputs only).
3. Overhead: a benchmark comparing source vs shared facade on a coarse operation shows ≤20% overhead (prototype: +2.3%; materially worse indicates a boundary regression such as chatty calls or double-copying).
4. Hot-swap: a scripted scenario loads v1, serves a call, loads v2 alongside, verifies both answer, flips routing, and confirms new calls hit v2 without a restart. ABI-hash mismatch refuses to load with a clear error.
5. Cross-platform: the documented build produces both artifacts; the linux `.so` loads and answers in a bookworm container via the platform-selecting loader; unsupported platforms fail with the explicit error.
6. Repo health: Scenery `go test ./...`; ONLV `just repo-harness`, `scenery check --json`, `go test ./...`, `scenery generate --check` all pass; no generated files committed in either repo.

## Idempotence and Recovery

All build and generation steps are idempotent; regeneration is `--check`-verifiable, and generated projections live in external caches, so a broken generator iteration cannot corrupt the ONLV tree. If a shared artifact is corrupt or ABI-mismatched, the facade refuses at load time and the consumer keeps running on the previously loaded version (or fails closed at startup if none); recovery is replacing the artifact and re-triggering the swap. Never attempt recovery via `dlclose`. The ONLV prototype workspace `x/wasmbench` is disposable; this plan is its durable record.

## Artifacts and Notes

Prototype measurements (2026-07-18, Mac Studio, darwin/arm64, 300-tile synthetic scene, single worker):

    native in-process:        536.6 ms   sha 3c82f93d...  (baseline)
    c-shared via purego:      548.8 ms   sha 3c82f93d...  (+2.3%)
    wazero (wasip1):          2990  ms   sha d84036ba...  (+458%)  — rejected
    Node/V8 (js/wasm):        1762  ms   sha d84036ba...  (+225%)  — rejected
    wasmtime v25 (cgo):       8169  ms   sha d84036ba...  — rejected (also cgo)
    GOARCH=amd64 native:                 sha d84036ba...  (arm64/amd64 divergence is FMA, not wasm)

Hot-swap probe transcript (abridged):

    loaded libworkload-v1.dylib -> version v1.0.0
    v1 run: 556ms sha=3c82f93d36b4
    loaded libworkload-v2.dylib -> version v2.0.0 (v1 still resident: v1.0.0)
    v2 run: 560ms sha=3c82f93d36b4
    v1 still answers after swap: version v1.0.0
    dlclose(v1) returned success (library NOT actually torn down safely)
    v2 run after dlclose(v1): 533ms
    SURVIVED

Known operating costs of shared linkage: each resident library version keeps its own Go runtime (own GC, `GOMAXPROCS=NumCPU` threads, tens of MB) forever; host pprof cannot see into library runtimes and crash stacks from the artifact are unsymbolized; `GODEBUG`/`GOMEMLIMIT` env applies to every runtime in the process; linux artifacts carry a glibc floor from their build image; `dlopen` executes arbitrary code, so artifact digests must be verified before load.

## Interfaces and Dependencies

- `scenery.package.scn` grammar: new `library` block + `operation.library` reference. Right surface because records/operations/contract-hashing already define serializable, hashable boundaries and the generator already owns adapters.
- Scenery wire encoding: the value format across the C ABI, reused for its generated encoders and contract-hash coupling.
- `github.com/ebitengine/purego` (new dependency of generated shared-backend facades): dlopen/dlsym without cgo. Pin the version; it is runtime-critical.
- `go build -buildmode=c-shared` + per-target C toolchain (docker `golang:1.26` for linux/amd64): artifact production. Consumers never need cgo.
- Exported ABI symbols per artifact: `SceneryLibVersion`, `SceneryLibABIHash`, `SceneryLibFree`, plus one C symbol per operation — the load-time handshake that makes hot-swap safe.
- ONLV `internal/repoharness`: enforcement point for pkg/-only, records-only, no-cgo-in-source rules on the app side.
