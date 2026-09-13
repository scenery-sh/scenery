I would keep Scenery’s public contracts, but change its unit of compilation and replacement. I would not start with a language rewrite.

My clean-sheet design would be a persistent incremental build engine, a stable Scenery runtime host, and the smallest contract-safe replaceable Go execution artifact. The governing principle would be:

An implementation edit should invalidate its affected implementation work—not reconstruct, reverify, relink, and restart everything that happens to surround it.

I would still choose Go for much of Scenery. Go is a reasonable implementation language for the tool; rebuilding a large Go executable is a poor reload mechanism for a tiny application change. Those are different decisions.

I reviewed main at 27ebaf12, dated September 11, 2026. The timings below come from your checked-in investigation, not fresh benchmarks that I ran on your machine.

1. What the current evidence actually says

Your latest investigation already demonstrates why another round of watcher and copying optimizations is unlikely to deliver the experience you want.

Measurement recorded in the repository	Result
Latest same-source semantic-edit median, three samples	5,461 ms
Latest same-source unchanged-start median, three samples	4,032 ms
Changed service plus generated adapter, isolated body-edit diagnostic	About 170 ms
Link action in that diagnostic	About 960 ms
First-execution delay before main, separate diagnostic	709–715 ms

These are different experiments, not additive components of one benchmark. Nevertheless, they identify costs that a faster watcher cannot remove. The pre-main delay was not conclusively attributed to a particular operating-system component; it should not be casually labeled “Go initialization” or “Gatekeeper.”

The implementation still ultimately builds:

go build ... -o <generation-executable> ./scenery_internal_main

It already overlaps implementation verification with compilation and joins both before publishing success. That is good engineering, but it still produces a whole application executable as the replaceable unit.

Also, several tempting suggestions have already been tried and rejected: declaration-cache experiments, separating assistant generation identity into a smaller Go package, and executable copy-on-write staging did not demonstrate the required end-to-end improvement. I would not present those as unexplored breakthroughs.

My diagnosis: Scenery is doing too much generation-wide work for an implementation-local change, and the remaining native artifact is expensive to link and launch. Both problems need attention.

You are right that unchanged machinery should cost almost nothing. But “one line” needs a precise fast-path definition: changing a handler body is different from changing a widely used type, build tag, native dependency, or initialization routine.

I would make ordinary, warm handler-body edits the first-class fast path—not promise an identical bound for every possible one-line change.

2. The architecture I would choose

Conceptually:

Same CLI, .scn declarations, public Go APIs, and machine contracts
                              │
                 Persistent incremental build service
                 ├─ immutable input snapshots
                 ├─ dependency-aware analysis
                 ├─ content-addressed artifacts
                 └─ bounded, worktree-aware scheduling
                              │
                  Isolated worktree session
                              │
           Stable Scenery host ↔ Replaceable Go execution generation
           routing / policy      application implementations
           orchestration         thin Scenery SDK
           external helpers      required in-process dependencies

This is one product with managed internal processes—not a requirement for users to configure a fleet of services.

A. Separate framework lifetime from application-generation lifetime

I would aim to keep stable framework machinery running across ordinary application edits: routing infrastructure, supervision, helper management, and the framework-owned parts of request dispatch.

The replaceable Go generation would contain application implementations and only the framework code genuinely needed in their process.

Crucially, persistent framework code does not mean persistent application state. Generation-scoped registrations, configuration, authority, and lifecycle state must still be replaced according to the existing contract.

Scenery’s typed boundary is unusually helpful here. Operations use explicit generated inputs and outcomes rather than arbitrary HTTP objects; dependencies and internal clients are explicit. Those are useful seams for changing execution architecture without changing application source APIs.

B. Start with one application worker—not one process per service

I would initially keep all application Go implementations in one smaller execution worker.

Why not immediately split every service? Because “separate declared services” does not automatically imply “independent Go heaps.” Shared packages, native libraries, globals, object ownership, and lifecycle ordering can make that transformation observable.

There are concrete APIs to respect: scenery.sh/db.Get returns an actual *sql.DB, not an opaque remote database handle. I would keep such connections and transactions inside the execution worker, rather than introduce a transparent-looking database RPC layer that changes behavior.

Later, split expensive implementation groups only at existing substitutable boundaries or boundaries whose state and lifecycle semantics have been established. Your declared-library mechanism is one such starting point.

The objective is the smallest safe reload unit, not the largest number of processes.

C. Make the public Go SDK genuinely small

This is a concrete dependency inversion I would prioritize.

Today, the root scenery package imports scenery.sh/runtime for Meta, CurrentRequest, StartSpan, and the Span alias. That creates a dependency from the application-facing façade into the runtime implementation.

I would invert that relationship:

Current:
application → public façade → runtime implementation
Desired:
application → small public ABI / request-context layer
runtime host → that same small ABI / request-context layer

Public import paths and signatures can remain unchanged. Internally, values, capability interfaces, request-context plumbing, and implementation machinery should be separated.

This is not a recommendation to split files or create more Go modules and assume compilation becomes faster. The dependency closure actually has to shrink.

Your compiler-side packages already avoid importing the root façade and runtime through dedicated contract leaves. I would extend that discipline to the app-facing execution boundary.

D. Acknowledge the architectural contract being changed

The current architecture explicitly says application API execution stays inside the generated application binary. A persistent-host split changes that internal artifact model. It is not merely a harmless file reorganization.

I would preserve declarations, public Go APIs, wire behavior, errors, lifecycle guarantees, and the meanings of identity fields. But I would deliberately replace the internal runtime/artifact layout, with conformance evidence.

In particular, I would not silently reinterpret an “executable digest” as “some composite manifest hash.” Component identity and execution-generation identity need explicit internal modeling while existing externally exposed claims remain truthful.

The major uncertainty is whether the smaller worker is small enough. Application dependencies may still dominate its link and first-launch time. That must be measured before committing to the whole redesign.

3. Make incrementality the execution model, not an optimization layer

The second major change is to stop treating each build as a mostly fresh procedure with caches around selected steps.

I would use a persistent dependency-query engine.

Buck2’s architecture provides a useful model: a long-lived daemon, incremental dependency/action graphs, action identities derived from inputs, and controlled artifact materialization. I would borrow those ideas—not require Scenery users to author another build language. .scn should remain the source of truth.

Separate exact identity from invalidation scope

This distinction is fundamental:

The identity of the whole application must change when its implementation changes. That does not mean every intermediate computation depends on the whole application identity.

A TypeScript client projection should depend on its contract inputs, renderer, and relevant configuration—not an unrelated Go handler body.

A package’s implementation check should depend on its complete relevant source, generated types, imported analysis data, target context, and checker implementation—not an unrelated assistant’s writable runtime directory.

An aggregate generation manifest can bind all those results together afterward.

Your current graph-cache lookup rejects a changed source snapshot, and the latency investigation explicitly notes that the graph key includes implementation content. That is correct as an exact identity, but too coarse to be the universal reuse boundary.

I would give each computation its actual dependency set.

That does not mean hashing only exported Go signatures. Bodies can influence compiler export data and downstream work; let the Go toolchain establish its compilation dependencies rather than inventing a simplistic ABI detector.

Capture inputs once; stop repeatedly rediscovering them

The build engine should own one immutable input snapshot for a generation. Analysis, generation, compilation, and publication should consume that snapshot rather than independently walk mutable source trees and reconstruct overlapping fingerprints.

The snapshot must include more than file contents: directory membership, missing resolver alternatives, embedded-file matches, module replacements, build tags, native inputs, and toolchain identities can all affect the result.

A filesystem watcher is a scheduling hint, not proof of correctness. Overflow, uncertainty, or external mutation must trigger reconciliation. I would not replace your current freshness guarantees with “the timestamp looked unchanged.”

The target is one authoritative capture and incremental semantic computation—not the false claim that a mutable filesystem never needs rescanning.

Preserve validation coverage through dependency-aware reuse

I would retain all currently required selected/default-target verification before publishing readiness.

What changes is how that result is established. A check whose complete inputs are demonstrably unchanged can reuse its exact result; a changed dependency invalidates it.

That is a deliberate redesign of the evidence model, not an ad hoc “skip verification on the fast path” flag. Today, Scenery’s build/verification join and staged Go conformance rules are meaningful safeguards and should survive the redesign.

For an ordinary implementation-only edit, I would expect the resulting work selection to look like this:

Concern	Intended work
Unchanged .scn declarations	Reuse applicable semantic results
Unchanged public Go/TypeScript contracts	No regeneration
Changed Go implementation	Recheck/recompile affected dependency closure
Unchanged database requirements	No migration or seed execution
Unchanged assistant assets	No asset copying or helper restart
New implementation generation	New exact identity and activation proof

Some of those independent lanes already exist, particularly assistant-only changes. I would generalize that structure rather than add another parallel cache system beside it.

4. Redesign activation without weakening readiness

Your current handoff executes the candidate in preflight mode, lets that process exit, then performs sequential replacement. It also retains the previous executable and environment for rollback. Those are deliberate correctness properties.

In a redesigned worker protocol, I would investigate launch once, attest, then activate:

build exact candidate
        ↓
launch and verify candidate identity
        ↓
candidate waits at activation barrier
        ↓
retire predecessor's write authority
        ↓
activate candidate lifecycle and dispatch
        ↓
verify a response from the new generation

This could remove a redundant execution and separate preparation from activation. It does not automatically remove the first-execution delay.

Nor can the candidate simply be assumed side-effect-free: Go package initialization precedes main. A waiting process is not sufficient evidence that no application behavior has occurred. The protocol needs to account for that explicitly.

I would preserve sequential write-capable generations unless stronger fencing is actually implemented. Starting the new app while the old one still owns schedules or durable work is not a valid speed optimization.

The same care applies to the new private transport. Preserve principal inheritance, cancellation, deadlines, outcomes, error classification, and stream ownership. Your stream contract includes exact size and exactly-once reader closure; transporting that stream must not turn it into an eagerly buffered byte array.

Finally, define latency as:

Source change → a verified response executed by the intended new generation.

Keeping the old app available while building improves continuity. It does not mean the edit is ready.

5. Worktrees should be isolated sessions over shared immutable work

I would build worktree support into every identity and ownership decision, not attach it afterward.

Share computation, not application state

Share immutable source blobs, generated artifacts, toolchain installations, analysis results, and compilation outputs when their full action identities match.

Go’s build cache already supports concurrent invocations; recreating a separate cache for every worktree would discard useful reuse. Native dependencies need additional care: the Go command documents limitations around detecting changes in C libraries used through cgo.

Keep runtime state separate: endpoints, database allocations, storage namespaces, helper writable directories, credentials, working directories, and generation authority.

I would not share an assistant’s writable working copy merely because its immutable package contents match another worktree.

Also, identical source does not necessarily imply an identical reusable executable when worktree-specific paths or identities are linked into it. Share package-level work immediately; earn executable-level reuse by correctly separating code inputs from launch context.

Use durable identities, not branch names

Internally, distinguish:

repository identity
worktree identity
session / ownership epoch
execution generation
Scenery producer + Go toolchain + target identity

A branch name is a label, not ownership authority. Detached worktrees, renamed directories, concurrent framework versions, and reused paths must not confuse cleanup or reuse.

The existing rule against installing validation binaries into the shared global CLI path is sound. Keep worktree-local or content-addressed prepared executables, with exact producer selection.

Schedule for latency across agents

Ten worktrees should not independently launch ten maximum-parallelism build pipelines and fight over memory and linking capacity.

I would have a host-aware admission controller with bounded CPU, memory, and link concurrency; priority for interactive edits; and fairness so one busy worktree cannot starve others.

Identical actions should run once with multiple subscribers. Cancellation should remove a subscriber, not kill shared work another session still needs.

Within a worktree, use latest-generation scheduling: obsolete preparation may be canceled or finish as a reusable artifact, but it must never publish itself over a newer requested generation.

Keep durable state small

I would not add a mandatory database service for this architecture.

A bounded in-memory query engine, content-addressed filesystem artifacts, and small atomic ownership records are sufficient starting components. Artifact retention should be based on active references and explicit limits—not an ever-growing global JSON document.

For agentic operation, cache size, idle sessions, helper processes, and retained generations need bounded lifetimes. A system that is fast for twenty edits and consumes unbounded memory after a thousand is not fast in practice.

6. Is Go still the right choice?

For the Scenery implementation: yes, it remains my default choice. For the current reload strategy: no, I would change that strategy.

I would keep the application SDK and Go semantic-checking boundary in Go. I would also initially implement the persistent build service and runtime host in Go.

My reason is not allegiance to the language. It is that the measured bottlenecks are not evidence that another language would make the orchestration logic sufficiently faster. A Rust supervisor still has to wait for the Go application artifact to compile, link, and become executable.

Also:

Making Scenery persistent does not make the stock Go compiler or linker persistent and incrementally patchable.

That distinction must remain visible throughout the project.

Where I would consider alternatives

Approach	My decision
Go host + smaller native Go execution worker	First architecture to prototype
Rust build engine or host	Viable replacement for a measured bottleneck, not the first intervention
Existing c-shared library boundary	Useful for suitable isolated components; benchmark build, load, and long-run behavior
Go plugin as universal hot reload	Not my default
Custom incremental Go backend / code loader	Research track if the hard latency requirement survives smaller native artifacts

Your c-shared loader already verifies artifacts and swaps versions. But it intentionally retains loaded Go runtimes for the process lifetime and forbids unloading them—even after some post-load validation failures. Extending that mechanism to thousands of arbitrary edits in a permanent host would create a retention problem.

A recyclable private loader process could bound that problem, but it still needs measurement. A dynamically loaded Go artifact is not automatically cheap to build.

The standard Go plugin mechanism has its own documented constraints: plugins cannot be closed, race-detector support is poor, and compatible toolchains, flags, and shared dependencies are required. I would not make those constraints the foundation of Scenery’s general development loop.

For a hard 200–300 ms requirement, there is a legitimate next frontier: a persistent compiler/incremental code-loading backend behind the typed contract boundary. But that is a compiler-toolchain project involving runtime metadata, initialization, state, and debugging—not simply rewriting the daemon in Rust.

I would design an internal backend boundary now so that experiment remains possible. I would not build the compiler project before establishing the limit of a much smaller native worker.

Apply the same separation to developing Scenery itself

I would want a CLI-help change, compiler change, UI-catalog change, and runtime-SDK change to have different rebuild consequences.

A compiler implementation edit should replace the compiler component and invalidate affected analysis/generation results. It should not automatically force unrelated runtime code to become a different compilation input.

Keep exact whole-release provenance, but do not unnecessarily inject that aggregate provenance into every intermediate action key. Where current executable identity binds those concerns together, separating them belongs to the explicit artifact-model redesign—not a skipped producer check.

7. How I would make it stay fast

I would enforce work budgets as well as time budgets.

Timing detects that something became slower. Work assertions identify architectural regressions directly:

A handler-body edit did not regenerate TypeScript, rerun SQL setup, restage unrelated assistants, or rebuild unrelated framework components.

These assertions should be integration tests against actual work performed, not promises in documentation.

I would maintain a compact benchmark matrix covering no-op requests, real body edits, contract changes, dependency changes, native-code changes, and concurrent worktrees. Measure queueing, input capture, analysis, generation, compilation, linking, first execution, activation, and first verified response separately.

The warm body-edit acceptance target I would set is p50 ≤300 ms and p95 ≤500 ms on named reference hardware. That is a product target, not a prediction that the proposed native-worker architecture already achieves it.

Cold starts, schema migrations, broad dependency changes, and expensive application initialization need separate budgets. Preserving lifecycle semantics means a slow application Start cannot simply be skipped to make the graph look better.

For trustworthy evidence, use enough samples to observe tails, interleave comparisons where practical, retain failures, and verify the served generation for every sample. Add long-running sessions and repeated worktree creation/removal to catch memory growth, stale ownership, and cache degradation.

Your current watcher has a 100 ms settle delay. That consumes a substantial fraction of a 300 ms target, but lowering it only matters once the larger costs are removed. For agent-authored transactional edits, an explicit completed edit batch could avoid unnecessary settling while ordinary filesystem edits retain appropriate coalescing.

8. What I would do next

I would authorize an architectural feasibility effort before a wholesale rewrite, with four concrete deliverables:

1. Establish the smallest contract-equivalent Go execution artifact. Use the real application dependency closure, not a toy “hello world.” Measure body-edit compile, link, first execution, lifecycle startup, and served-response latency. This determines whether ordinary native replacement can fit the target.
2. Implement one incremental build path end to end. One authoritative snapshot, dependency-aware reuse, unchanged public generation outputs, full required verification, and exact publication identity. Demonstrate both reduced work and lower total latency.
3. Qualify the host/worker boundary. Exercise typed outcomes, internal calls, auth, real SQL transactions, streaming, cancellation, lifecycle failures, rollback, debugging, and process identity. Do not expand the architecture until these preserve the current contracts.
4. Prove worktree behavior under load. Concurrent versions, identical-action reuse, cancellation, stale-generation rejection, crash recovery, isolated mutable state, and bounded long-session resource usage.

The first deliverable is especially important. A smaller worker that still takes 800 ms to link and launch does not become a 300 ms edit loop because its surrounding architecture is elegant. That result would tell you to investigate finer existing contract boundaries or a different execution backend—not spend another cycle optimizing copies.

Bottom line

I would make Scenery a long-lived execution platform with replaceable application implementations, rather than treating every application edit as a small release of a complete runtime.

I would preserve the contracts and the strength of the evidence. I would replace the mechanisms that make proving those contracts expensive.

Change what gets rebuilt, what gets revalidated, and what gets restarted first. Change the implementation language only when a measured bottleneck gives you a reason.

Selected source links

Reviewed commit⁠￼ · Latest latency investigation⁠￼ · Go implementation contract⁠￼

Buck2 architectural model⁠￼ · Go build and cache documentation⁠￼ · Go plugin limitations⁠￼
