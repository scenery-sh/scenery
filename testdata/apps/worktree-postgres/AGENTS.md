# Worktree PostgreSQL Fixture Instructions

## Purpose

Own the small authored application used by explicit worktree runtime proof.

## Local Contracts

- `.scenery.json` declares managed development SQL; no dotenv or external DSN
  is required. `local` is default; `preview` tests same-root environment conflict.
- Go implements the declared library contracts. Borrowing uses one conditional
  SQL update so exactly one concurrent borrower wins.
- Keep Go projections ignored and TypeScript client fixtures current.
- The release runner owns temporary copies and their resources. Do not start
  this repository fixture against a developer database or installed shared CLI.

## Verification

From the repository root, use
`go run ./scripts/verify --probe worktree --summary --write` for functional
A1–A17 proof; it is also included in release. A18 resource measurement runs only
when explicitly requested, using
`go run ./scripts/verify --benchmark worktree-cost --summary --write`.
Keep real process, Docker, HTTP, concurrency and timing proof outside ordinary
Go unit tests. Every selected row must report its actual result and owned cleanup.
