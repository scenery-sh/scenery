# Repository Verifier

## Purpose

Own verification of Scenery itself, outside the application executable.

## Ownership

This command owns repository orchestration, architecture/document checks,
release probes, timing policy and their report writers. `internal/testsuite`
retains fresh-test execution, binary caching and scheduling. Product commands
must not import this command or that engine.

## Local Contracts

- Preserve cached correctness, fresh execution, isolated timing and release
  evidence as separate modes. Every exact test root has isolated p95 <100ms;
  retain 20 serial-process samples, the 60ms candidate target and no exceptions.
- Link each confirmation package once in a disposable run-local directory, then
  execute every sample in a fresh process from that package's Go-reported
  directory. Do not add a persistent confirmation cache or change `internal/testsuite`.
- Exercise the absolute prepared worktree-local product binary. Never resolve
  `scenery` through PATH or substitute verifier build identity for target identity.
- Use public product commands/runtime boundaries or genuine production owners;
  do not copy private product orchestration or add test-only product APIs.
- Real processes, tools, network and services belong in explicit selected probes,
  not ordinary tests. Resources must be disposable and ownership-verified.
- Worktree A9 rejects allocation against an incompatible historical agent home,
  then proves coexistence/native migration in a separate current home inside the
  same disposable daemon. Preserve the old-data and continuous-sibling checks;
  do not add a product compatibility decoder to satisfy the old fixture.
- Reports use the existing machine envelope and shared report/evidence values.
- The `--probe auth` step (also mandatory in release) builds
  `testdata/authprobe` and runs all 15 inventoried public-boundary journeys
  in fresh native processes and owned databases. Missing Docker, incomplete
  assertion reports, failed journeys, or failed cleanup fail closed.

## Work Guidance

Plan 0168 records the extraction's assertion mapping and integrated acceptance.
Plan 0170 records fast iteration and explicit proof composition. Keep one
functional probe catalog for `--probe <id>` and `--release`. Default/quick/race
must not invoke that catalog. Benchmarks are explicitly selected separately,
never included in release; preserve their measurement algorithm and cleanup.
The sole repository command is `go run ./scripts/verify`; do not restore product
dispatch or a forwarding alias. CI and the release shell delegate common checks
here; unique source-snapshot and binary packaging checks remain in the shell.

The storage probe compiles the `scenery_storage_integration`-tagged reclamation
journey and executes its fresh binary. Keep that volume-dependent fixture in
the explicit probe; ordinary storage tests retain bounded failure-cut coverage.

## Verification

Run `go test ./scripts/verify` and the root validation union. For changed probe
execution or ownership, run its exact `--probe <id>` and check its assertion
inventory and cleanup. Full release and timing audits are explicit workflows,
not mandatory iteration steps. A successful build alone does not prove an
external boundary.
