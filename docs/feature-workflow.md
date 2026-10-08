# Local Feature Integration

Keep five feature worktrees active when useful, one primary checkout on `main`,
and one local landing operation at a time. Only `main` is published by this
workflow. Feature creation, editing, focused checks and candidate preparation
can proceed independently. Starting five runtimes is unnecessary: use `scenery up`
only in the app checkout whose current feature needs live acceptance.

## Repository Setup

Use the checkout's prepared `.scenery/harness/bin/scenery` (or an explicitly
selected matching executable), never install a shared CLI for this workflow.
`main` must be checked out, and `origin` must have one publication URL exposing
`refs/heads/main`. The primary checkout may contain unrelated staged/unstaged
work; overlapping edits block its fast-forward and are never stashed or reset.
Unpublished/divergent main commits must be resolved before preparing a candidate.

Commit the repository-owned [scenery.features.json](../scenery.features.json)
policy on `main` before the first landing. Its
[checked schema](schemas/scenery.feature.policy.schema.json) declares focused
`development` and required `landing` checks as executable plus argv, without a
shell. `{base}` is the captured main commit; `{revision}` is the authored input
revision for that check. `when_paths` optionally selects literal repository path
prefixes, including deleted paths. Requirements from both the base and combined
candidate policy apply; removing a gate cannot waive that gate for its own change.
Every landing must select at least one gate.

`probe_limit` controls shared expensive-check admission for all worktrees in the
Git repository; start with one or two. The committed main policy is the ceiling,
so a feature cannot enlarge its own limit. Mark external/runtime/database checks
`expensive: true`; these never reuse past execution. Declare the named probes
and owner acceptance required by the changed boundary in the policy before
marking the feature ready. Runtime/UI acceptance remains required even when a
repository's mechanical checks pass.

Scenery's source gate (`scripts/feature-check`) selects quick for documentation
and full for source/contract changes. It reads the selected run's compact context
first, then completes the full changed-area command union and lint. External
checks are separate policy gates, admitted through the shared limit. Applications
supply their own path-to-check command; ONLV uses its pinned `validate changed`.
Scenery's policy selects CLI-process acceptance for report and shared schema
checks, and observability acceptance for query, development status/client and
their HTTP proof owners. Shared schema changes select both boundaries.

## Create, Inspect And Work

```sh
.scenery/harness/bin/scenery feature create sql-traces --purpose "Inspect executed SQL" -o json
.scenery/harness/bin/scenery feature create trace-ui --purpose "Display SQL in the console" --depends-on sql-traces -o json
.scenery/harness/bin/scenery feature register designer --path /absolute/existing/worktree --purpose "Improve the Designer shell" -o json
.scenery/harness/bin/scenery feature list -o json
.scenery/harness/bin/scenery feature list --watch -o jsonl
.scenery/harness/bin/scenery feature check sql-traces -o json
.scenery/harness/bin/scenery feature set sql-traces --stage ready -o json
```

New checkouts use sibling directories and `feat/<name>` branches. Registration
requires an existing branch checkout in the same Git repository, distinct from
main. Names use lowercase letters, digits and hyphens. A purpose is required;
dependencies refer to registered features and cannot form cycles. `set` can change
purpose, stage (`working`, `ready`, `parked`) and dependencies; `--no-dependencies`
clears dependencies explicitly.

The ledger reports purpose, dependencies, committed and dirty outstanding paths,
path overlaps, validation, landing and read-only runtime status. States are
`working`, `ready`, `blocked`, `partially_landed`, `fully_landed`, `parked` and
`closed`. Ready is the author's intent; exact candidate validation is still
required. A successful development check certifies its exact current authored
snapshot, not future edits. Old conflicting candidates remain inspectable but
cannot obscure a newer candidate or completed landing. A missing checkout,
rewritten feature history or a landing absent from main is an explicit blocker.

Watch emits a snapshot only when meaningful state changes. JSONL uses current
`scenery.cli.event` envelopes, increasing sequences and one terminal summary
when stopped or failed. Path overlap is an
early warning, not a semantic conflict detector. Refresh a feature only when it
needs a landed dependency, a shared contract changed, or it is ready to integrate.
There is no automatic rebase of every feature after another lands. Land a small
shared foundation before dependent work; dependencies can also be listed first
in a combined candidate.

## Prepare, Review And Land

Commit the feature's intended checkpoint. Dirty edits remain outstanding and
are excluded from that checkpoint. Preview one feature, or an ordered batch of
independent/dependent checkpoints:

```sh
.scenery/harness/bin/scenery feature land sql-traces -o json
.scenery/harness/bin/scenery feature prepare sql-traces trace-ui -o json
.scenery/harness/bin/scenery feature prepare sql-traces --commit <exact-feature-commit> -o json
.scenery/harness/bin/scenery feature inspect <candidate-id> -o json
```

Preparation fetches the latest published main from the bound publication URL and
merges each captured commit into its own detached candidate checkout. It records
checkpoint-to-integration-commit mappings and shows the complete resulting diff.
Conflicts retain that checkout and release local coordination; an independent
candidate can land while the conflicting candidate waits. Resolve/stage files
in the reported candidate path, then `inspect` to finish the merge. Commit any
later candidate correction, inspect again, and review the new diff/revision.

The review revision binds base, tip, tree, publication destination, checkpoint
mappings and selected check argv. To check or apply the reviewed result:

```sh
.scenery/harness/bin/scenery feature check --candidate <candidate-id> --expect-revision <reviewed-revision> -o json
.scenery/harness/bin/scenery feature land --candidate <candidate-id> --yes --expect-revision <reviewed-revision> -o json
```

Use apply only within the human's publication authorization. The command holds
one repository-wide publication lock, verifies the exact revision and unchanged
remote main, validates the combined result, fast-forwards the primary main and
pushes only `refs/heads/main` without force. Concurrent landing fails promptly;
feature development and checks continue. A batch retains separate feature/merge
commits and validates the union once; it does not wait for all five features.

Scenery's verifier accepts `--base <commit>` so clean candidates include committed
base-to-HEAD changes as well as outstanding edits. This scope is included in run
input identity and the changed-area step; an empty dirty tree does not waive the
combined candidate's checks.

## Evidence, Recovery And Cleanup

State is private local metadata beneath the shared Git common directory's
`scenery-features/`: feature records, candidate checkouts/refs, check logs and
receipts, and immutable `landings/<candidate-id>.json`. It is shared by the
repository's worktrees, not pushed as application configuration. Every receipt
records exact argv, cwd, authored bytes, executable/environment fingerprint,
exit status, duration and archive. Successful source checks opt into `reuse`
only for identical inputs, argv, cwd and executable/environment, backed by an
unchanged successful archived receipt. Expensive checks always execute anew.
Changed bytes, including index-hidden candidate edits, invalidate acceptance.

A changed remote main requires a new combined candidate and review. Failed
validation never advances main. A failed push retains the validated local main;
retry the same candidate/review revision after resolving the failure. If the
remote accepted the push before the CLI stopped, retry verifies that publication
and completes its receipt without publishing another feature. Locks use kernel
ownership; stale lock files confer no ownership. A surviving expensive-check
child retains admission after the CLI dies, until that child exits. Child-owned
expensive admission is supported on Unix; Windows refuses expensive checks
rather than allowing uncounted surviving children.

Landing one checkpoint leaves subsequent committed, staged, unstaged and
untracked work visible as `partially_landed`. Full landing requires the exact
recorded checkpoint, no outstanding edits and the receipt's integration in
current main. Do not substitute ancestry for proof of reviewed conflict handling.

```sh
.scenery/harness/bin/scenery feature close sql-traces -o json
.scenery/harness/bin/scenery feature set sql-traces --stage working -o json
```

Close requires an exact published receipt and clean authored checkpoint. It
records closure without removing the checkout, branch, retained data, candidates
or evidence. Reopen with `--stage working` if later work appears. Cleanup must
consult these records, preserve partial/unrecorded work and follow separately
authorized Git/runtime/data ownership rules. Branch deletion or worktree removal
does not authorize database/storage deletion.
