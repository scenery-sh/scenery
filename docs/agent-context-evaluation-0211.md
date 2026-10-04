# Scoped Agent Context Evaluation

Recorded on 2026-10-04 for [Plan 0211](plans/0211-scoped-agent-context.md).

## Delivered Behavior

Documentation inspection accepts repeatable `--for-path`, unions applicable
instructions, documents, sections and validation commands, and lets full
verification supersede quick. `--include-text` adds exact complete-line excerpts,
at most 2,048 UTF-8 bytes per section and 8,192 bytes across the response. Each
excerpt identifies its source content and any truncation. Owned TypeScript
renderers route to normative sections rather than broad runtime keyword matches.

Every written repository verifier run archives a fourth observation,
`agent-context-summary.json`, alongside the complete context. It retains exact
run/input identity, warning/error counts, applicable instruction and plan paths,
and covered, remaining or conditional checks. Only recorded successful same-input
checks count as covered. Separately successful lint/regeneration proof remains
separate; application acceptance is conditional on its verified runtime.

Root instructions shrink from 1,918 to 1,037 words and preserve authorization
and engineering boundaries while moving the detailed validation matrix and documentation ownership table to their
current owning references. Nine existing active plans have dated `Resume here`
checkpoints describing recorded proof, remaining work and authorization boundaries.
Those checkpoints are not fresh remote, installed-machine or runtime observations.

## Decision Exercise

The explicit runner is `scripts/verify/evaluate-agent-context.py`. It creates
independent sequential `codex exec` sessions using `gpt-6-astra`, reasoning
`medium`, ignored user configuration, ephemeral history and a read-only sandbox.
Three identical cases each run three times against before and after snapshots,
with variant and case order reversed on alternating repetitions. Sessions receive
no conversation history, expected answer or variant label. They may read the
snapshot but may not edit, run verification, browse, delegate or mutate external state.

The cases are a README prose typo, normative TypeScript integer/JSON discovery,
and interrupted Plan 0101 resumption. The last explicitly assumes matching archived
inputs for the decision exercise; it does not assert that a partial documentation
snapshot has the full repository's input hash or proves literal reboot acceptance.

Before contains 137 files, including the old routed context and full archived
validation context. After contains 140 files, including the new schema/plan and
compact routed/validation contexts. Manifests retain exact hashes. The snapshots
precede final evaluation reporting and plan closure; the clarified snapshot changes
the root quick mnemonic, Local Contract explanation and actual compact packet from
run `20261003T225551.149822000Z`; subsequent bookkeeping does
not rewrite the evaluated snapshots.

An initial cohort was stopped after snapshot preparation failed to copy a context
packet into the after inventory. Its outputs remain as failed setup evidence and
are excluded from the complete cohort. The runner now rejects missing required
snapshot files before creating an output directory.

## Results

All 18 main-cohort sessions exited successfully, left their copied snapshots
unchanged and used only local read commands. Every documentation answer selected
quick plus lint, without a plan, full verification or unnecessary permission.
Every TypeScript answer found normative §7: `int64`/`uint64` use `bigint`, with
canonical decimal strings as the default JSON representation and explicit contract
selection for safe JSON numbers. All selected the cumulative generator checks.
All resumption answers kept Plan 0101 open for literal post-fix reboot/login,
rejected controlled job reload as equivalent acceptance, preserved the operator
permission boundary and reused successful same-input repository/package tests.

| Case | Before median output bytes | After median output bytes | Change | Before/after median read commands |
|---|---:|---:|---:|---:|
| README typo | 16,445 | 25,863 | +57.3% | 2 / 2 |
| TypeScript contract discovery | 91,386 | 49,521 | -45.8% | 5 / 4 |
| Interrupted plan | 142,098 | 86,714 | -39.0% | 4 / 3 |

The documentation case loaded more owning references after the root matrix moved.
One of three after answers also proposed optional path discovery for a known typo.
The initial after resumption answers treated three absent receipts as unexecuted
checks, which the packet did not establish. This is a real limitation, not a pass.

The implemented follow-up retains the cheap documentation-only quick decision in
root instructions and explicitly says that each remaining check may have passed
separately: inspect matching-input/scope evidence before rerunning. A separate
three-repeat before/after cohort for docs and resumption completed 12 further
successful read-only sessions with unchanged snapshots. All three clarified after
resumption answers first sought separate matching-input/scope receipts and proposed
no automatic rerun commands. The reboot/login and authorization conclusions remained
correct. This repairs the observed receipt interpretation problem.

| Follow-up case | Before median output bytes | Clarified after median output bytes | Change |
|---|---:|---:|---:|
| README typo | 16,438 | 21,902 | +33.2% |
| Interrupted plan | 157,510 | 83,071 | -47.3% |

The documentation mnemonic did not establish reduced read volume: after values
ranged from 15,234 to 76,525 bytes and two answers proposed optional discovery.
The instructions preserve the cheap decision, but this exercise demonstrates no
reliable context benefit for a typo. Both cohorts remain evidence; there were 30
completed independent sessions overall.

The routed TypeScript response selects 2,731 words across precise sections versus
27,808 before (23,799 of those were unrelated Local Contract sections). Its bounded
source text is 8,190 bytes; metadata plus opt-in text makes the JSON packet larger,
15,206 versus 7,410 bytes. The main compact validation packet is 4,213 bytes versus
30,292 for the prior complete packet; adding the receipt clarification makes the
follow-up packet 4,633 bytes. These are different representations with different
purposes; the complete context remains available.

Main-cohort cumulative reported input totals were 1,154,652 before / 777,613 after,
including 841,472 / 519,296 cached tokens. Uncached input was 313,180 / 258,317.
These usage totals are not unique context size, peak context, or causal cost proof.

## Repository Verification

- Focused Go packages: `go test ./cmd/scenery ./scripts/verify ./internal/machine` passed.
- `golangci-lint run ./...`: passed after final source edits, zero issues.
- Native, house and assistant TypeScript client regenerations: passed with empty changed sets.
- Bun conformance and development-runtime tests: 52 passed, 272 assertions.
- Both generated-client and catalog TypeScript configurations: passed.
- Full run `20261003T224626.347485000Z` and clarified full run
  `20261003T225551.149822000Z`: stable inputs, zero warnings/errors, repository Go
  suite, vet, schemas, drift, architecture and knowledge checks passed.
- Final source-acceptance run `20261003T230945.925331000Z`: stable inputs and zero warnings/errors.
- CLI-grammar run `20261003T224756.196408000Z`: 342 help requests, 282 refused
  malformed requests and 42 accepted requests passed. The cache sweep passed;
  no `scg.*` disposable grammar home remained in `/tmp` at cleanup inspection.
- Python syntax/help and rejection of an incomplete snapshot before output creation: passed.
- `git diff --check`: passed; closure metadata is checked again before delivery.

Focused tests cover normalized/deduplicated multi-path unions, full superseding
quick, exact TypeScript sections, schema identity, excerpt bounds/truncation and
invalid UTF-8 rejection,
immutable four-file archive identity, failed/unstable/missing-vet coverage rejection,
outside-repository package rejection, and conditional logs never becoming covered
from a successful command alone. Initial failures in instruction budgets, exact
human-only governance wording and the changed CLI usage expectation were fixed
without weakening checks. Failed runs remain in their immutable archives.

The cumulative dirty union includes prior warning/generator work, so its generator
checks were fulfilled as well. No new product runtime external boundary changed.
Other probes, release certification, application runtime logs/acceptance and all-root
timing audits are unselected, not passes. No shared installation or operator reboot
was performed; the Plan 0101 exercise does not complete that plan.

The cumulative generator checks used these exact commands (all passed):

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
```

## Reproduction And Limits

Machine-local ignored evidence is under `.scenery/harness/context-0211/`.
`before/`, `after/`, `after-clarified/`, manifests, `evaluation-02/` and
`evaluation-clarified/` retain frozen packets,
raw JSONL, stderr, structured answers and per-session metadata. Preserve an existing
cohort by choosing a new output directory (add `--cases docs resume` with
`--after .scenery/harness/context-0211/after-clarified` for the follow-up):

```sh
python3 scripts/verify/evaluate-agent-context.py \
  --before .scenery/harness/context-0211/before \
  --after .scenery/harness/context-0211/after \
  --output .scenery/harness/context-0211/evaluation-new
```

Observed tool-output bytes count returned command output, not unique loaded context
or tokenizer size. CLI input usage accumulates across a session and includes cached
input. Three repetitions and alternating order do not control server/cache state
or establish causal speed/cost improvement. This is a planning/discovery exercise,
not end-to-end coding, live runtime acceptance or an all-root timing audit.
