# Local Feature Integration

## Purpose / Big Picture

Support five independent feature worktrees while publishing only `main`. A local
feature ledger answers what is ready, blocked and outstanding. Landing captures
exact checkpoint commits, prepares an isolated combined candidate, exposes its
diff/conflicts, validates that candidate, serializes publication and records the
checkpoint-to-main mapping. Later edits remain outstanding. Development uses
focused feedback; final combined validation belongs to landing.

## Progress

- [x] 2026-10-07: inspected live main, existing worktree/help/schema boundaries,
  validation contracts, shared skill and ONLV instructions.
- [x] 2026-10-07: implemented the feature ledger, overlap/dependency states and live overview.
- [x] 2026-10-07: implemented isolated checkpoint candidates, review, validation and serialized
  main publication with recoverable receipts.
- [x] 2026-10-07: added exact-input validation reuse and shared admission for expensive probes.
- [x] 2026-10-07: added public CLI/schema contracts, focused unit tests and a named
  real-Git probe with eleven functional assertions.
- [x] 2026-10-07: updated owning Scenery docs/skill, cleanup skill, ONLV policy and
  developer/validation-site guidance; both skills validate and the docs site builds.
- [x] 2026-10-07: passed full cumulative source validation, lint and the named
  feature, CLI grammar/process and validation-Git probes; audited all requirements.

### Resume here

2026-10-07: completed locally. Use [the living workflow](../feature-workflow.md)
for current operation. The configured combined source gate passed in
`20261007T115129.100245000Z` with stable inputs and zero lint issues; compact
context was read before the full command union. All eleven real-Git feature
assertions and the CLI grammar/process and validation-Git probes passed in
`20261007T120217.650326000Z`. ONLV's scoped quick policy, repository harness,
docs checks and site build passed, and both edited skills validate. Preserve
ongoing unrelated ONLV feature/recorder work and unpublished main commits.
Publication/setup is separate: commit the policy on published main before its
first landing; this task installed no shared CLI, pushed no real repository and
removed no real checkout or retained data.

## Surprises & Discoveries

The first real-Git run passed all seven feature scenarios in 20 seconds, including
partial landing, independent conflict progress, exact-input reuse, publication
exclusion, failed-gate refusal, batch union, shared probe admission and watch.
The enclosing verifier rejected missing plan freshness metadata and direct
process-environment reads; route those through `internal/envpolicy`. Evidence:
`.scenery/harness/runs/20261007T104818.339792000Z/`.

A clean candidate needs committed base-to-HEAD selection, not just dirty paths.
The verifier now accepts `--base`, binds that base/HEAD to its source revision,
and keeps the existing single path classifier. The source gate reads compact
archived context first and runs only the uncovered required union. No gate
silently waives conditional owner acceptance.

The expanded probe also passed hidden raw-source mutation refusal, dead-owner
publication/probe recovery, failed-push retry with source reuse and fresh external
proof, and receipt recovery when remote publication outlived its CLI. Source
identity normalizes neither raw bytes nor index-hidden changes. Unix children
inherit admission; Windows fails closed for expensive commands.

The existing `worktree` command handles application checkout lifecycle and
retained-state upgrades, not feature integration. Plan 0212 is a completed
one-time merge. Current repository rules still require full validation per
completed source change; they need an explicit development/landing distinction.

## Decision Log

- 2026-10-07: use a repository-level `feature` CLI that also works without an app
  config. Keep application runtime/data ownership unchanged. Store local records
  under the Git common directory so every worktree sees one ledger.
- 2026-10-07: keep `main` and `origin` as the single publication destination.
  Feature branches stay local. Each candidate has its own detached checkout;
  unresolved conflicts release admission so an independent candidate can land.
- 2026-10-07: repository-authored `scenery.features.json` declares focused and
  landing checks as argv, including selected expensive boundary commands. Use
  the existing verifier/app validation owners, not a second validation classifier.
- 2026-10-07: approval binds the reviewed candidate revision. Preserve merge
  ancestry and immutable landing receipts; ancestry alone never marks a feature
  complete. Non-source external probes are always freshly executed.

## Outcomes & Retrospective

Implemented the shared feature ledger, meaningful live overview, isolated exact
checkpoint/batch candidates, review-bound validation, one main publication owner,
immutable checkpoint mappings, exact-input source reuse and bounded fresh external
checks. Later edits remain outstanding after partial landing; conflicts release
coordination for independent work. Failed publication and interrupted publication
recover through the same recorded candidate without rewriting main.

Source/unit/schema checks and lint pass. The final named run proves five concurrent
feature records, publication exclusion, independent conflict progress, batch union,
raw authored input identity, shared probe admission including surviving children,
push failure retry, interrupted push receipt recovery and current JSONL/telemetry.
Its owned disposable remote/checkouts are cleaned; no live repository publication
is claimed. Eleven unrelated document freshness warnings remain; the full source
run also reported an advisory cached-suite duration warning. No release or timing
certification was requested. Scenery's owning docs/skill, cleanup skill and ONLV's
policy and developer/site guidance reflect the new workflow; ONLV's app toolchain
pin, runtime and unrelated work remain outside this change.

## Plan of Work

`internal/feature` owns local records, Git coordination, candidates, exact-input
command receipts and shared probe leases. `cmd/scenery` parses the public command,
reports producer-bound JSON and adds verified runtime status. Creation/register,
state/dependency updates, list/watch, prepare/inspect, check/land and close expose
one workflow. `land` previews by default; applying a reviewed candidate requires
its issued revision and performs validation before advancing/pushing main.

Support several checkpoints in one candidate, preserving a separate merge commit
per feature and running the check union once. Checkpoint snapshots never absorb
unstaged/staged/untracked later work. A changed remote main invalidates a candidate;
a failed push preserves local validated work and can be retried without rewriting
published history. Fully landed status requires an applicable receipt plus no
later committed or dirty changes. Close records completion without deleting a
checkout or retained data; cleanup consults the ledger before removing anything.

Wire exact CLI help, checked policy/result schemas and immutable evidence into
the existing catalogs. Use focused service-free unit tests for state, selection,
revision and receipt invariants. Real Git/process/crash/concurrency behavior runs
only in `go run ./scripts/verify --probe feature --summary --write`.

## Validation and Acceptance

From `/Users/petrbrazdil/Repos/scenery`, run:

```sh
go test ./internal/feature ./cmd/scenery ./scripts/verify
go run ./scripts/verify --summary --write
golangci-lint run ./...
go run ./scripts/feature-check --base HEAD
go run ./scripts/verify --probe feature --probe cli-grammar --probe validation-git --probe cli-process --summary --write
git diff --check
```

Read the written full run's `agent-context-summary.json`, then complete the exact
changed-area command union from `agent-context.json`, reusing successful checks
for unchanged inputs/scope. Compiler/generator/UI checks are selected only if
those paths change. No release certification or timing benchmark is requested.

The real-Git probe creates a disposable bare remote, primary main and five
feature worktrees. It must prove purpose/dependencies/status/overlap/outstanding
and runtime fields; an older exact checkpoint lands while later committed,
staged, unstaged and untracked edits remain; unresolved conflicts do not block
an independent feature; stale candidate approval and changed main are refused;
failed validation never advances main; receipts retain exact checks and mappings;
valid same-input checks are reused while changed inputs invalidate reuse; two
ready independent checkpoints can land together; publication is exclusive;
expensive command admission is shared across worktrees and recovers dead owners.
It verifies remote main, local main, feature work and cleanup ownership directly.

For scoped ONLV workflow edits, run `just repo-harness` and `git diff --check`
from `/Users/petrbrazdil/Repos/onlv`; do not run UI/runtime probes for docs/config
only. Validate every edited local skill with the skill-creator validator.

## Idempotence and Recovery

Keep candidates and immutable receipts after failure. Retry only the recorded
candidate when its revision, base and checks still match. Locks/leases must
verify a live owner and release on cancellation; never infer ownership from a
stale file. Do not reset/stash feature or primary work, force-push, remove feature
worktrees, delete data or install the shared CLI. A primary checkout with an
unreviewed local main divergence blocks publication rather than discarding it.
