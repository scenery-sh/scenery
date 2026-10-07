# Local Feature Integration

## Purpose

Own repository-local feature records, checkpoint candidates, validation receipts
and serialized publication to main. This is Git coordination, not app runtime
or retained-data lifecycle.

## Local Contracts

- All worktrees share records beneath the verified Git common directory.
- Capture exact commits; never commit, reset, stash or rebase a feature's edits.
- Only origin/main is published, through a validated fast-forward candidate.
- Keep conflicts in their own candidate; do not hold publication admission while
  waiting for resolution. Bind approval and evidence to exact candidate inputs.
- Preserve immutable landing/check receipts and recover an interrupted push from
  live Git refs. Ancestry without a receipt does not establish feature completion.
- OS locks own publication, metadata and expensive-command admission; stale files
  alone do not hold admission. Probe execution is outside ordinary Go tests.

## Verification

Run `go test ./internal/feature ./cmd/scenery`. Real Git, command, crash and
concurrency proof belongs to `go run ./scripts/verify --probe feature --summary
--write`, followed by cumulative root validation.
