# Worktree PostgreSQL Fixture Instructions

## Purpose

Own the small authored application used by the worktree runtime release probe.

## Local Contracts

- `.scenery.json` declares managed development SQL; no dotenv or external DSN
  is required. `local` is default; `preview` tests same-root environment conflict.
- Go implements the declared library contracts. Borrowing uses one conditional
  SQL update so exactly one concurrent borrower wins.
- Keep Go projections ignored and TypeScript client fixtures current.
- The release runner owns temporary copies and their resources. Do not start
  this repository fixture against a developer database or installed shared CLI.

## Verification

Use the repository-local release harness from the repository root. Keep real
process, Docker, HTTP, concurrency, and timing proof in the named release probe,
not ordinary Go unit tests. Every acceptance row must report its actual result.
