# Worktree PostgreSQL Acceptance Fixture

This source-authored library desk exercises ordinary managed `scenery up` in
the explicitly selected worktree runtime acceptance probe. It declares one SQL
capability and a small typed HTTP API with an atomic conditional borrow update.

The runner copies authored files into a temporary Git repository, uses an
explicit local Go-module replacement for the candidate source, creates real
Git worktrees, and starts each with the candidate worktree-local CLI. No `.env`,
external `DATABASE_URL`, pre-generated Go, fixed agent port, or database
container is supplied. The committed TypeScript fetch client is an API fixture,
not a generated Go runtime workspace.

`client/verify.ts` checks create/list/borrow/return, typed missing/conflict
outcomes, runtime input rejection, ten two-borrower races, and persisted data
after lifecycle operations. Pass the advertised `api` route URL, not the
localhost page root: path-mode app endpoints are beneath `/api/`.

Run functional A1–A17 proof (also included in release):

```sh
go run ./scripts/verify --probe worktree --summary --write
```

The named worktree runtime and PostgreSQL acceptance step records each scenario
and actual subprocess arguments. Its cleanup targets only full-identity-verified
probe resources. A failure retains sufficient authority for explicit retry;
never run global Docker prune or delete another worktree's ownership record.

This fixture is not a performance ceiling or proof of isolation for external
DSNs. A18 resource measurement runs only on explicit request through
`go run ./scripts/verify --benchmark worktree-cost --summary --write`.
The original measurement evidence and limitations remain recorded in
`docs/plans/0167-worktree-runtime-postgres.md`; current invocation policy is in
`docs/harness-engineering.md`.
