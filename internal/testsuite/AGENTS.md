# Test Suite Runner

## Purpose

`internal/testsuite` runs the explicit fresh-test lane from content-addressed
Go test binaries so fresh measurement does not relink unchanged packages.

## Ownership

- Own repository/package discovery, linked-binary caching, longest-first package
  scheduling, fresh test execution, and Go JSON event output.
- Keep harness policy, budgets, and diagnostics in `cmd/scenery`.
- Keep the `--fresh-tests` integration in `cmd/scenery` and the manual adapter
  in `scripts/testsuite`.

## Local Contracts

- Execute test bodies with `-test.count=1`; the cache may reuse binaries, never
  test results.
- Preserve every `./...` package and test. Packages without tests still appear
  in JSON output.
- Invalidate manifests from the Go toolchain, build-affecting environment, and
  tracked/untracked workspace contents. Committing unchanged contents must not
  invalidate the manifest.
- Build disposable test binaries with VCS stamping disabled; the workspace
  fingerprint remains the source-change guard.
- Store disposable state only under `.scenery/harness/test-binaries/`.
- Route process environment reads through `internal/envpolicy`.

## Work Guidance

- Prefer Go build IDs over a parallel source-dependency model.
- Keep platform-specific locking and process cancellation in the existing
  `*_unix.go` / `*_other.go` files.
- Do not weaken execution scope or add skipped/gated tests for timing.

## Verification

```sh
go test ./internal/testsuite
go run ./scripts/testsuite -run 'a^' -record-timings=false
go run ./scripts/testsuite
```
