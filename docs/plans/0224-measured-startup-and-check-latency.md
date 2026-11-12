# Measured Startup, Shutdown And Check Latency

## Purpose / Big Picture

CLI telemetry of 2026-11-12 showed that a warm `scenery up` of the ONLV
application (44 service processes, 36 SQL services, two assistant helpers)
took 7.5 to 10.3 s, `scenery down` 2.2 to 2.5 s and a repeated `scenery check`
about 4 s, although nothing had changed between runs. This plan removes work
that a second run of unchanged inputs repeats, keeps every verification that
reads current bytes, and fixes one defect the measurements exposed. After it a
developer or agent sees an unchanged worktree start in about 4.5 s, stop in
about 1.6 s and pass `scenery check` in about 2.3 s.

## Progress

- [x] 2026-11-12: Measured baselines on ONLV through `scenery framework use
  --source` in a disposable ONLV worktree; timelines read from the session's
  `control/dev/*.log` build steps.
- [x] 2026-11-12: Session executables of the generation that served last stay
  in `run/app`; other retained executables are released at close. Retention is
  a clone where the filesystem clones files.
- [x] 2026-11-12: A passed implementation check records a key of its inputs in
  the build state; a build of exactly those inputs skips the Go analysis.
- [x] 2026-11-12: The retained Go build input graph is kept in the workspace
  and proven current by the next process.
- [x] 2026-11-12: `scenery check` trusts a recorded proof for unchanged
  analysis inputs.
- [x] 2026-11-12: Framework sources are read once and in parallel when a
  selection is verified.
- [x] 2026-11-12: Assistant helpers stop concurrently; their prepared trees
  are restored by a verified clone.
- [x] 2026-11-12: Migration status of services is read concurrently; retained
  executables are verified by several readers; first-generation processes are
  prepared beside database setup; the migration preflight compiles beside
  framework verification.
- [x] 2026-11-12: `scenery doctor` reports the first-execution penalty.
- [x] 2026-11-12: `ps --wait` no longer captures a target before the first
  generation has published its snapshot.

### Resume here

Completed 2026-11-12; nothing to resume. Remaining latency is recorded under
Development Edit Latency in [the debt tracker](../tech-debt.md#development-edit-latency).

## Surprises & Discoveries

- macOS spends 0.2 to 0.6 s on the first start of an executable it has not
  started before, about 0.2 s even for a 2 MB program, unless the application
  that starts Scenery is enabled under Developer Tools. A copy, a clone and a
  hard link of an unexecuted file all pay it; a file that was executed before
  does not. This is why keeping retained executables across sessions matters
  more than avoiding their copy.
- Persisting preflight proofs across sessions was implemented and removed: the
  proof key deliberately includes the start environment, which differs between
  sessions (database port, telemetry ports, supervisor PID), and
  `TestDevProcessProofsCoverOnlyTheSameExecutableIdentityAndEnvironment`
  protects that rule.
- Copying an assistant tree with eight workers was slower than one worker
  (3.2 s against 2.0 s): creating about 7,800 files concurrently contends in
  the filesystem. A directory clone takes about 0.1 s; reading the clone with
  six readers verifies it in about 0.9 s.
- `ps --wait` on unmodified `main` timed out in 6 of 8 sessions when it ran
  right after `scenery up` returned. The target was captured with zero
  compiler-membership files against 890 in the served snapshot.
- The verification proof key first differed between identical runs: the
  framework source digest is filled in during the build, and the Go
  environment of a target carries the whole process environment.
- The doctor probe once started copies of a Go test binary, each of which ran
  the tests again. `testing.Testing()` now refuses it; the runaway processes
  and their temporary directories were removed.
- bun 1.3.14 ends an inline script that throws after a `require` with status
  0. ONLV's wrapper checks relied on the throw; they now exit explicitly.

## Decision Log

- 2026-11-12: Reuse is allowed only where every consumed input is named or the
  retained state proves itself current by reading metadata or bytes. Nothing
  trusts a timestamp alone.
- 2026-11-12: Published-artifact freshness and package ABI invariants are
  checked on every build and check; only the Go type analysis is reused.
- 2026-11-12: A proof is recorded only when the check added no diagnostic of
  any severity, so a reused result never hides a warning.
- 2026-11-12: Compiler results are not retained across runs; the compiler's
  owner forbids it (`reuseStartupCompilerResult`).
- 2026-11-12: `runtime-framework.json` is still written after the migration
  preflight and record load, not earlier beside them: a failed start must not
  leave a locator that names this producer.
- 2026-11-12: Applying migrations stays serial in declared order; only status
  reads run together, and only for services with distinct schemas.

## Outcomes & Retrospective

Measured on ONLV, same machine, medians of the runs after the first:

| Scenario | Before | After |
|---|---:|---:|
| `scenery up --detach --wait ready`, unchanged worktree | 7.5 to 10.3 s | 4.5 to 4.9 s |
| the same when every executable must be retained | 5.8 s | 4.9 s |
| disk used by retained executables per worktree | 745 MB | about 0 (clones) |
| `scenery down` | 2.2 to 2.5 s | 1.6 to 1.7 s |
| repeated `scenery check` | 4.1 s | 2.3 s |
| `scenery framework inspect` | 160 ms | 95 ms |
| edit of a handler body to `ps --wait` served | 1.47 s | 1.40 to 1.45 s |
| `ps --wait` right after `up`, first edit adds a file | 2 of 8 served | 10 of 10 served |

The reload path was already tight; its remaining time is the Go build
(about 0.5 s), the first execution of the new executable (about 0.3 s, a
host setting that `scenery doctor` now reports) and publication (0.14 s).

Validation on 2026-11-12: `go test ./...`, `golangci-lint run ./...` (0
issues) and `go run ./scripts/verify --summary --write` pass, the last with
the existing 16 review-due warnings and the advisory suite-time warning. The
probes `postgres`, `dev-process`, `process-model`, `assistant-runtime`,
`generation`, `fixtures`, `cli-grammar`, `cli-process` and `worktree` pass.
`assistant-journey` fails with "a receipt of the uncertain epoch stayed
usable"; it fails identically on unchanged `main` (`3b4d3175`) and is recorded
in the debt tracker, not repaired here. The `fixtures` probe rewrote the stale
committed contracts of `testdata/apps/basic` and `storage-basic` on `main` as
well, which failed every later probe of a run; they are regenerated.
Proof invalidation was exercised live: a new Go file, a type error and a
changed handler signature each repeat the analysis, and both errors are
reported.

## Plan of Work

Each change was measured on the ONLV worktree before and after, then covered
by an in-process test, documented in `docs/local-contract.md` and committed
separately. The owning files are `cmd/scenery/dev_app_binary.go`,
`cmd/scenery/dev_process_*`, `cmd/scenery/dev_assistant_cache.go`,
`cmd/scenery/db_migrate.go`, `cmd/scenery/dev_source_wait.go`,
`internal/build/verification.go`, `internal/build/build_input_retained.go`,
`internal/build/framework_fingerprint.go`, `internal/generate/verify_go_proof.go`,
`internal/parse/input_digest.go`, `internal/gotarget/tool_identity.go` and
`internal/doctor/first_execution.go`.

## Validation and Acceptance

From the repository root:

```sh
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --probe postgres --probe dev-process --probe process-model --summary --write
go run ./scripts/verify --probe assistant-runtime --probe generation --probe fixtures --probe cli-grammar --probe cli-process --probe worktree --summary --write
```

Acceptance on an application: start it twice with `scenery up --detach --wait
ready`; the second session's log records `implementation.check` with cache
`hit` and reason `verified_inputs_unchanged`, `go.input_discovery` with cache
`hit`, and `process.retain` steps of a few milliseconds.

## Idempotence and Recovery

Every retained artifact is disposable. Deleting `.scenery/sessions/*/run/app`,
`.scenery/build/implementation-check`, the workspace's
`.scenery-build-inputs.json` or the `verified_implementation` field of the
build state only makes the next run do the full work again.
