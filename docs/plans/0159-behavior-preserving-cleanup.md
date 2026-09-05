# Behavior-Preserving Cleanup

This ExecPlan is a living document maintained under `PLANS.md`.

## Purpose / Big Picture

Reduce redundant private plumbing and deletion-only tests without changing
runtime behavior, public APIs, diagnostics, or generated output. Preserve the
already dirty release-gate repair and its documentation.

## Progress

- [x] (2026-09-05) Run unused/unparam audit; no unused declarations, 99
  unparam candidates. Separate contracts and test seams from removable code.
- [x] Simplify 17 private parameters, two unused return values, nine forwarding
  wrappers, and one constant-result helper.
- [x] Remove five deletion-only tests and two duplicate assertions while retaining current CLI parser,
  security, ownership, migration, and live runtime coverage.
- [x] Run lint, affected tests, full Go, release harness, and release gate.

## Surprises & Discoveries

Prior lint cleanup already removed analyzer-visible dead declarations. Remaining
opportunities are mostly unnecessary private API plumbing rather than unreachable
public features. Do not treat every unparam finding as a deletion instruction.

The clean-checkout gate copied tracked paths without excluding working-tree
deletions. Its snapshot now excludes the exact `git ls-files --deleted` set.
One gate run failed during temporary-directory cleanup in the unchanged
`TestInspectUIJSONContract`; ten isolated repetitions then passed. The cause is
unconfirmed; no production or test workaround was introduced.

## Decision Log

- 2026-09-05 / Codex: Preserve interface methods, public exports, security
  rejection tests, and useful injected seams. Remove unused arguments only
  after verifying their call-site expressions have no side effects.
- 2026-09-05 / Codex: Keep historical plans immutable. Environment registry and
  current parser coverage remain authoritative after removing old spelling tests.

## Outcomes & Retrospective

Completed on 2026-09-05. Reviewed private plumbing and redundant tests were
removed without changing public behavior. Full release validation passed.
Unconfirmed candidates were retained rather than weakening current contracts.

## Context and Orientation

`cmd/scenery` contains command routing and harness orchestration; `auth` and
`runtime` contain app behavior. Candidate reports live under ignored
`.scenery/harness/slop-*`. No source changes in compiler or generator are planned.

## Milestones

1. Inspect candidate declarations and their callers.
2. Apply bounded mechanical simplifications and review their diffs.
3. Validate the resulting tree and record retained candidates.

## Plan of Work

Use Go syntax-aware edits for private signatures and forwarding calls. Inspect
every removed test's purpose, retaining current behavioral acceptance. Do not
introduce abstractions, dependencies, or tests merely to count deletions.

## Concrete Steps

From repository root:

```sh
golangci-lint run ./...
go test ./auth ./cmd/scenery ./runtime
go test ./...
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
.scenery/harness/bin/scenery harness self --release --summary --write
scripts/release-gate.sh
git diff --check
```

## Validation and Acceptance

Expected classes are Go package and release-sensitive/runtime. Run refreshed
changed-area recommended commands; release mode supersedes default harness.
Existing real-process fixture probes prove the preserved runtime. The external
app lane is skipped only when no external app root is supplied, as its log states.
No new Go test root was added. Although the generator implementation was not
changed, the changed-area classifier matched CLI generation plumbing; both
recommended fixture regenerations were run and produced no tracked diff.

## Idempotence and Recovery

Preserve all pre-existing edits. Keep mechanical scripts and reports ignored.
Never reset the repository or replace the shared installed CLI during validation.

## Artifacts and Notes

Go cleanup removes a net 112 lines across 42 files. The final unused/unparam
audit reports no unused declarations and 82 remaining parameter candidates,
down from 99. Interface methods, exported APIs, useful injected seams, and
security/migration rejection tests were deliberately retained.

- `golangci-lint run ./...`: passed, zero issues.
- `go test ./auth ./cmd/scenery ./runtime`: passed.
- `go test ./...`: passed.
- `go build -o .scenery/harness/bin/scenery ./cmd/scenery`: passed.
- `.scenery/harness/bin/scenery harness self --release --summary --write`:
  passed, including real-process probes and the full race suite; log at
  `.scenery/harness/slop-release.log`.
- `go test ./cmd/scenery -run '^TestInspectUIJSONContract$' -count=10`:
  passed while investigating the cleanup failure.
- Both `go run ./cmd/scenery generate --target typescript_client.public_api`
  commands above: passed, no tracked fixture changes; full Go suite passed again.
- `scripts/release-gate.sh`: passed, including clean-checkout install, fixture
  HTTP, router safety, and artifact hygiene. Logs:
  `.scenery/release-gate/20260905T191457Z/`.
- External-app smoke was explicitly skipped because no external app was selected.
  The recommended `scenery logs --limit 500 -o jsonl` is not applicable to this
  repository cleanup without an active app session; release probes own and
  capture their temporary runtime logs.
- `git diff --check`: passed. Quick self-harness refresh validates final plan
  bookkeeping after the release run.

No public contract changed; instruction and API documentation are intentionally
unchanged. Harness documentation records the working-tree snapshot correction.

## Interfaces and Dependencies

Only private signatures and call sites change. Public APIs, machine schemas,
resource declarations, and production dependencies remain unchanged.
