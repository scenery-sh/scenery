# Agent Instruction Evaluation — Second Round

Recorded on 2026-10-03 for [Plan 0209](plans/0209-consistent-agent-workflows.md).
This follows the [first evaluation](agent-instruction-evaluation.md).

## Delivered Changes

1. Generated fast-loop guidance takes its commands from the shared changed-area
   classifier. It no longer inserts doctor/quick before implementation or invents
   checks for an empty change set. Static-check failure reproduction uses quick,
   not release; explicitly selected probes keep their own reproduction commands.
2. Instruction checks accept soft-wrapped authorization prose while keeping
   separate paragraphs and list items isolated. The skill routes to required
   workflows instead of repeating a full-verifier command. Local link anchors
   are checked, including cross-file anchors. Four stale links were corrected.
3. ExecPlans have eight core sections instead of thirteen. Context, milestones,
   commands and interfaces can share Plan of Work. Decision logs retain choices
   affecting contracts, scope, risk, recovery or handoff. A magic living-document
   sentence is unnecessary; completed plans retain their historical structure.
4. Application validation distinguishes documentation, frontend, declarations,
   Go and runtime changes. Matches accumulate and app-specific checks still apply.
   Unmatched paths remain unverified. Browser acceptance, runtime identity and
   data/authorization boundaries remain explicit.
5. Each written verifier run publishes `self.json`, `summary.json` and
   `agent-context.json` together under `.scenery/harness/runs/<run-id>/`. All three
   carry the same run identity and before/after input revisions. Source drift
   fails verification. Existing archives cannot be replaced; latest copies are
   navigation conveniences. Full reports retain their embedded topic evidence.
6. Six independent sequential sessions evaluated the before/after instructions
   on three identical task prompts. Results and limits follow below.

## Independent Decision Evaluation

The evaluation used `codex exec`, model `gpt-6-astra`, reasoning `medium`,
`--ignore-user-config --ephemeral --sandbox read-only --skip-git-repo-check`.
Each session received a fresh isolated copy of the same nine-file instruction
inventory and one task, without this conversation, expected answers, or variant
labels. No session spawned subagents, edited files or ran validation. Before
tasks ran documentation/runtime/frontend; after tasks ran frontend/runtime/
documentation. Each condition has one observation per task, not repeated trials.

The before files came from commit `40ee352e`. The after snapshot was frozen after
the policy edits and before final link repairs, static-check rerun correction and
completion bookkeeping. Manifests preserve exact file hashes; the experiment
therefore evaluates the frozen instruction decisions, not every final source byte.

| Task | Before decision | After decision |
|---|---|---|
| Repository README typo | Quick verification and root-required lint; no doctor, plan or full | Same checks; use the run archive for context |
| Runtime HTTP fix with successful same-input full verification | Reuse full/package/repository tests; lint and live acceptance remain | Same reuse and acceptance; context comes from the run archive |
| Client app button-color change, CSS only | Three app frontend checks plus `scenery check`, `generate --check`, `go test ./...` and `scenery harness` | Three app frontend checks and browser acceptance; no unrelated Go/Scenery checks |

All six sessions followed the policy supplied to them and requested no user
clarification. The CSS case demonstrates removal of four unnecessary planned
checks while preserving the app's lint, typecheck, build and browser acceptance.
Repository documentation still requires root-mandated lint; this change does not
silently waive that existing policy. Neither runtime answer repeated already
successful validation.

| Task / version | Wall seconds | Read commands | Observed tool-output bytes | Reported input tokens | Cached input tokens | Output tokens |
|---|---:|---:|---:|---:|---:|---:|
| Docs / before | 35.508 | 3 | 18,523 | 85,997 | 68,736 | 523 |
| Docs / after | 33.631 | 3 | 18,742 | 96,929 | 69,632 | 510 |
| Runtime / before | 54.847 | 4 | 165,177 | 127,967 | 112,000 | 763 |
| Runtime / after | 37.498 | 3 | 45,319 | 97,100 | 66,816 | 669 |
| Frontend / before | 36.176 | 3 | 34,320 | 90,123 | 70,912 | 543 |
| Frontend / after | 30.200 | 2 | 27,198 | 76,227 | 54,656 | 427 |

Input totals are cumulative usage reported by the CLI across a session, including
cached input, not unique loaded context or peak context size. Tool-output bytes
are observed command output, not a tokenizer measurement. Aggregate input fell
from 304,087 to 270,256 tokens, but uncached input rose from 52,439 to 79,152;
the documentation case also used more total input. Cache state, execution order,
server conditions and model variance were not controlled. No compaction count
was exposed. These observations do not establish a causal speed or cost benefit.

This was an independent planning/routing evaluation, not an end-to-end coding,
live-runtime or visual correctness experiment. It measures selected checks and
unnecessary clarification, not time saved actually executing those checks.

## Verification and Run Evidence

Focused tests cover wrapped permissions and unrelated-block rejection, reachable
skill anchors, plans without boilerplate, classifier-derived fast loops, safe
static-check reproduction, input hashing, archive/context identity and refusal to
replace an existing archive. Publication tests also prove that a later run moves
latest while the original report stays byte-for-byte unchanged.

- `go test ./scripts/verify ./cmd/scenery ./internal/machine`: passed.
- `golangci-lint run ./...`: passed after checking intentional infallible hash
  writes explicitly. Lint also passed after the final source edits.
- `go run ./scripts/verify -o json --write`: full mode passed, including the
  repository Go suite, vet, schema, drift, architecture and knowledge checks.
  The initial full run found four invalid section links; those were fixed.
- The stock skill validator passed in a task-local Python environment with
  PyYAML; no project or global dependency was added.
- `git diff --check`: passed.

The first and second actual verifier runs produced separate immutable bundles;
all three files shared each run's identity and both input revisions matched.
Each archived full report equaled its independently captured command output after
the subsequent run. The passing run reported 38 documentation freshness warnings,
21 architecture warnings and one advisory cached-suite timing warning. These are
not failures or performance proof. Final output and warnings are retained in
`final-verification.json` after completion bookkeeping.

The changed-area union is CLI JSON, Go package and release-sensitive script work.
The full verifier's successful repository suite covers the matching package checks;
`internal/harnessreport` has no test files and is exercised through consumers.
No compiler/generator, UI catalog or product external boundary changed, so fixture
regeneration and external probes are unselected. No release certification or
all-root timing audit was run. No application runtime or retained data changed.

## Reproduction Evidence

Machine-local ignored evidence is under
`.scenery/harness/instruction-refresh-0209/`. `evaluate.py` contains the exact
three prompts and CLI arguments. `before/`, `after/` and their manifests preserve
the instruction inputs. Each `<variant>-<case>` has raw JSONL events, stderr,
answer and metadata with model, settings, elapsed time and exit status.
`evaluation-results.json` records extracted usage and decisions. Each evaluation
can be reproduced with `python3 evaluate.py <before|after> <repo_docs|repo_runtime|app_frontend>`
from that evidence directory; new runs replace those local evaluation outputs,
so copy them first when retaining another cohort. Verifier archives are separate
and never replaced by that runner.

`first-full.json`, `second-full.json`, `acceptance-full.json` and
`final-verification.json` preserve the
verifier stdout; each contains `run.archive_path` for its authoritative bundle.
Focused-test and lint logs, the dependency-install log and snapshot metadata are
retained alongside them. No local evidence or environment is committed.
