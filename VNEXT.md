# Scenery

Scenery must become a long-lived execution platform with replaceable application
implementations, rather than treating every application edit as a small release
of a complete runtime.

An implementation edit should invalidate its affected implementation work—not
reconstruct, reverify, relink, and restart everything that happens to surround it.

## The architecture - conceptually

```text
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
```

The objective is the smallest safe reload unit, not the largest number of
processes.

## Make the public Go SDK genuinely small

This is a concrete dependency inversion I would prioritize.

Today, the root scenery package imports `scenery.sh/runtime` for `Meta`,
`CurrentRequest`, `StartSpan`, and the `Span` alias. That creates a dependency from
the application-facing façade into the runtime implementation.

I would invert that relationship:

**Current:**

```text
application → public façade → runtime implementation
```

**Desired:**

```text
application → small public ABI / request-context layer
runtime host → that same small ABI / request-context layer
```

Public import paths and signatures can remain unchanged. Internally, values,
capability interfaces, request-context plumbing, and implementation machinery
should be separated.

This is not a recommendation to split files or create more Go modules and assume
compilation becomes faster. The dependency closure actually has to shrink.

Your compiler-side packages already avoid importing the root façade and runtime
through dedicated contract leaves. I would extend that discipline to the
app-facing execution boundary.

## Make incrementality the execution model, not an optimization layer

The second major change is to stop treating each build as a mostly fresh
procedure with caches around selected steps.

I would use a persistent dependency-query engine.

Buck2’s architecture provides a useful model: a long-lived daemon, incremental
dependency/action graphs, action identities derived from inputs, and controlled
artifact materialization. I would borrow those ideas—not require Scenery users
to author another build language. `.scn` should remain the source of truth.

## Separate exact identity from invalidation scope

This distinction is fundamental:

The identity of the whole application must change when its implementation
changes. That does not mean every intermediate computation depends on the whole
application identity.

A TypeScript client projection should depend on its contract inputs, renderer,
and relevant configuration—not an unrelated Go handler body.

A package’s implementation check should depend on its complete relevant source,
generated types, imported analysis data, target context, and checker
implementation—not an unrelated assistant’s writable runtime directory.

An aggregate generation manifest can bind all those results together afterward.

Your current graph-cache lookup rejects a changed source snapshot, and the
latency investigation explicitly notes that the graph key includes
implementation content. That is correct as an exact identity, but too coarse to
be the universal reuse boundary.

I would give each computation its actual dependency set.

That does not mean hashing only exported Go signatures. Bodies can influence
compiler export data and downstream work; let the Go toolchain establish its
compilation dependencies rather than inventing a simplistic ABI detector.

## Capture inputs once; stop repeatedly rediscovering them

The build engine should own one immutable input snapshot for a generation.
Analysis, generation, compilation, and publication should consume that snapshot
rather than independently walk mutable source trees and reconstruct overlapping
fingerprints.

The snapshot must include more than file contents: directory membership, missing
resolver alternatives, embedded-file matches, module replacements, build tags,
native inputs, and toolchain identities can all affect the result.

A filesystem watcher is a scheduling hint, not proof of correctness. Overflow,
uncertainty, or external mutation must trigger reconciliation. I would not
replace your current freshness guarantees with “the timestamp looked unchanged.”

The target is one authoritative capture and incremental semantic computation—not
the false claim that a mutable filesystem never needs rescanning.

## Define latency as:

> Source change → a verified response executed by the intended new generation.

## Worktrees should be isolated sessions over shared immutable work

I would build worktree support into every identity and ownership decision, not
attach it afterward.

### Share computation, not application state

Share immutable source blobs, generated artifacts, toolchain installations,
analysis results, and compilation outputs when their full action identities
match.

Go’s build cache already supports concurrent invocations; recreating a separate
cache for every worktree would discard useful reuse. Native dependencies need
additional care: the Go command documents limitations around detecting changes
in C libraries used through cgo.

Keep runtime state separate: endpoints, database allocations, storage namespaces,
helper writable directories, credentials, working directories, and generation
authority.

I would not share an assistant’s writable working copy merely because its
immutable package contents match another worktree.

Also, identical source does not necessarily imply an identical reusable
executable when worktree-specific paths or identities are linked into it. Share
package-level work immediately; earn executable-level reuse by correctly
separating code inputs from launch context.

### Use durable identities, not branch names

Internally, distinguish:

- repository identity
- worktree identity
- session / ownership epoch
- execution generation
- Scenery producer + Go toolchain + target identity

A branch name is a label, not ownership authority. Detached worktrees, renamed
directories, concurrent framework versions, and reused paths must not confuse
cleanup or reuse.

The existing rule against installing validation binaries into the shared global
CLI path is sound. Keep worktree-local or content-addressed prepared executables,
with exact producer selection.

### Schedule for latency across agents

Ten worktrees should not independently launch ten maximum-parallelism build
pipelines and fight over memory and linking capacity.

I would have a host-aware admission controller with bounded CPU, memory, and link
concurrency; priority for interactive edits; and fairness so one busy worktree
cannot starve others.

Identical actions should run once with multiple subscribers. Cancellation should
remove a subscriber, not kill shared work another session still needs.

Within a worktree, use latest-generation scheduling: obsolete preparation may be
canceled or finish as a reusable artifact, but it must never publish itself over
a newer requested generation.

### Keep durable state small

I would not add a mandatory database service for this architecture.

A bounded in-memory query engine, content-addressed filesystem artifacts, and
small atomic ownership records are sufficient starting components. Artifact
retention should be based on active references and explicit limits—not an
ever-growing global JSON document.

For agentic operation, cache size, idle sessions, helper processes, and retained
generations need bounded lifetimes. A system that is fast for twenty edits and
consumes unbounded memory after a thousand is not fast in practice.

## Targets

The warm body-edit acceptance target I would set is **p50 ≤300 ms** and
**p95 ≤500 ms** on named reference hardware. That is a product target, not a
prediction that the proposed native-worker architecture already achieves it.
