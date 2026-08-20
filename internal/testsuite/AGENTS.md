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
- Build at most four missing test binaries concurrently. The harness and manual
  adapter share `DefaultBuildParallelism`; change it only with same-host,
  interleaved macOS and local-Linux A/B wall-time evidence.
- Store disposable state only under `.scenery/harness/test-binaries/`.
- Test-binary manifests use the current `scenery.test-binary-cache` artifact
  identity; an identity mismatch invalidates the disposable cache and rebuilds it.
- Route process environment reads through `internal/envpolicy`.
- Report pre-execution cost on `Result.Prepare`: total elapsed, package-listing
  elapsed, and one `BinaryBuild{Package, BuildID, Elapsed}` per linked binary,
  ranked slowest first. Also report `TestPackageCount` (packages that produce a
  test binary). Linking is invisible to Go's per-package test timings, so a
  cold-run penalty is unattributable without this breakdown.
- Treat the sum of per-binary elapsed durations as attribution only: concurrent
  builds overlap, so it is neither wall time nor CPU time. Cold binary-count and
  prepare-wall budgets live in `cmd/scenery`, not here.

## Work Guidance

- Prefer Go build IDs over a parallel source-dependency model.
- Keep platform-specific locking and process cancellation in the existing
  `*_unix.go` / `*_other.go` files.
- Do not weaken execution scope or add skipped/gated tests for timing.
- Prefer folding a new leaf's tests into a consumer that already links it.
  A dedicated leaf test binary counts against the `cmd/scenery` count budget.

## Verification

```sh
go test ./internal/testsuite
go run ./scripts/testsuite -run 'a^' -record-timings=false
go run ./scripts/testsuite
```

`-builds N` prints the prepare breakdown and the N slowest links to stderr:

```sh
go run ./scripts/testsuite -run 'a^' -builds 20 -record-timings=false
```
