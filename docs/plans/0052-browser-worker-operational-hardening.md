# Browser Worker Operational Hardening

This ExecPlan is a living document. Update `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` as implementation proceeds.

## Purpose / Big Picture

Scenery should be resilient when an app runs browser-backed TypeScript legacy async runtime workers. The immediate motivating case is an ONLV ChatGPT browser worker, but this plan is deliberately scoped to scenery itself: source/build preparation must ignore browser runtime artifacts, and `scenery dev` must confidently supervise and clean up generated TypeScript worker processes.

The visible end state for scenery is:

- Browser runtime directories such as `var/browser`, `var/chrome`, and `var/playwright` cannot break generated app build prep when they contain sockets, FIFOs, lock files, or browser profile files.
- Hidden app-local artifact roots such as `.scenery/artifacts/...` remain ignored by build/source walking.
- Dev-started TypeScript legacy async runtime workers are covered by tests for supervisor PID monitoring.
- Detached dev-session cleanup can reap stale generated `worker.ts` processes for the current app root without touching unrelated Bun or foreground `scenery worker typescript` processes.

The ONLV app endpoint, ChatGPT automation, Playwright dependency, smoke script, and DevBootstrap docs are out of scope for this scenery ExecPlan.

## Progress

- [x] 2026-05-30: Created this ExecPlan from the ChatGPT browser worker hardening notes, filtering out ONLV app-specific files and smoke workflow details.
- [x] 2026-05-30: Added build-prep skipping for `var/browser`, `var/chrome`, and `var/playwright`, plus non-regular-file skipping that leaves symlink behavior untouched.
- [x] 2026-05-30: Added `internal/build` regression coverage proving `listSourceFiles`, `copyTree`, and `Prepare(...)` ignore browser runtime artifacts and Unix socket fixtures.
- [x] 2026-05-30: Tightened generated TypeScript worker tests so `worker.ts` must read `SCENERY_DEV_SUPERVISOR_PID`, probe the supervisor with `process.kill(pid, 0)`, install an interval, and exit when the supervisor disappears.
- [x] 2026-05-30: Added dev-supervisor lifecycle coverage proving `Close()` interrupts the TypeScript worker child, waits for exit, and detaches it from supervisor state.
- [x] 2026-05-30: Added detached-dev-only stale TypeScript worker registry/reaper, with current app-root and generated `worker.ts` path matching, process-group termination, registry cleanup, and a dev dashboard cleanup event.
- [x] 2026-05-30: Focused validation passed: `go test -count=1 ./internal/build ./internal/workers ./cmd/scenery`.
- [x] 2026-05-30: Full validation passed: `go test -count=1 ./...`, `go install ./cmd/scenery`, `git diff --check`, and `scenery harness self --json --write`.

## Surprises & Discoveries

- 2026-05-30: The bug note names ONLV files such as `agents/chatgpt_login.ts` and `agents/chatgpt_browser.worker.ts`, but those are app-level deliverables and are not part of this scenery plan.
- 2026-05-30: The relevant scenery substrate risks are independent of ChatGPT selectors: browser profile files can create non-regular filesystem entries during build prep, and stale generated TypeScript workers can keep polling old legacy async runtime queues after detached dev sessions move on.
- 2026-05-30: The generated TypeScript worker already contained supervisor PID monitoring; the missing piece was regression coverage for the actual generated mechanics.
- 2026-05-30: Detached stale-worker cleanup cannot reliably inspect another process's environment portably, so the implementation uses a generated-dir registry written only by detached dev-supervised TypeScript workers. The reaper still verifies the registry against the current app root and generated `worker.ts` path before checking the process command and signaling.

## Decision Log

- Decision: Keep this plan limited to scenery runtime/substrate hardening.
  Rationale: The pasted note mixes scenery and ONLV work. The user explicitly asked for scenery-specific items and to ignore ONLV app items, so this plan excludes ONLV endpoint, workflow, Playwright, smoke-script, and docs changes.
  Date/Author: 2026-05-30 / Codex.
- Decision: Do not change standalone `scenery worker typescript` ownership semantics.
  Rationale: The foreground worker CLI intentionally owns its process directly. Stale-worker cleanup should target only dev-supervised or detached-dev-generated TypeScript workers with clear app-root ownership signals.
  Date/Author: 2026-05-30 / Codex.
- Decision: Use a detached-dev worker registry instead of broad Bun/process scanning.
  Rationale: A registry under `.scenery/generated/legacy-async-runtime/typescript/dev-worker.json` lets scenery match the stale worker to the current app root and generated `worker.ts` path without killing arbitrary foreground workers or unrelated Bun processes.
  Date/Author: 2026-05-30 / Codex.
- Decision: Signal stale TypeScript workers by process group where supported.
  Rationale: The generated worker may have a runtime process tree. Reusing the existing process-group lifecycle behavior makes detached stale cleanup consistent with normal supervisor shutdown.
  Date/Author: 2026-05-30 / Codex.

## Outcomes & Retrospective

Completed on 2026-05-30. Scenery now ignores browser runtime artifact directories and unsupported non-regular files during build prep, generated TypeScript worker supervisor monitoring is locked by tests, and detached dev sessions can conservatively reap registry-matched stale generated TypeScript workers while recording an observable dashboard event.

Validation passed with the default harness writing `.scenery/harness/self-latest.json`. The harness reported existing warning-level package timing and knowledge review diagnostics, but `ok` was true and no new validation failures remained.

## Context and Orientation

The source/build preparation code lives in `internal/build/build.go`. It discovers app source files, copies them into generated build workspaces, computes fingerprints, and skips generated or irrelevant directories. Existing hidden-directory behavior already keeps `.scenery/...` out of app source walking, but browser runtime directories outside hidden paths can still contain sockets or other non-regular files.

The TypeScript legacy async runtime worker generation and dev supervision code is split across:

- `internal/workers/typescript.go`, which generates TypeScript worker runtime files under `.scenery/generated/legacy-async-runtime/typescript/`.
- `cmd/scenery/dev_typescript.go`, which generates and starts TypeScript workers during `scenery dev`.
- `cmd/scenery/dev_supervisor.go`, which owns dev process lifecycle and shutdown behavior.
- `cmd/scenery/worker.go`, which owns the standalone `scenery worker typescript` foreground command and should not be swept into dev-session cleanup.

The generated TypeScript worker already has the right architectural shape: scenery owns the registry, worker entrypoint, legacy async runtime environment, and worker process startup. This plan adds defensive filesystem hygiene and lifecycle confidence around that runtime.

## Milestones

Milestone 1: Build prep ignores browser runtime artifacts.

Add explicit runtime-artifact directory skipping for `var/browser`, `var/chrome`, and `var/playwright` in source discovery and workspace copying. Add non-regular-file handling so sockets, FIFOs, and device-like files are not copied into build workspaces.

Milestone 2: TypeScript worker supervisor monitoring is locked by tests.

Add regression coverage that generated `worker.ts` reads `SCENERY_DEV_SUPERVISOR_PID` and exits when the supervisor process disappears. The test should assert generated source behavior, not only runtime logs.

Milestone 3: Dev-supervisor shutdown handles TypeScript workers explicitly.

Add lifecycle tests around dev-supervisor close behavior for TypeScript worker child processes. The test should prove interrupt, wait-or-kill, and state detachment behavior for worker processes started by `scenery dev`.

Milestone 4: Detached-session stale worker cleanup is conservative and observable.

Add a stale worker reaper for detached dev sessions only. It must match current app root and generated TypeScript worker path before signaling a process, and it should record cleanup in the dev dashboard store.

## Plan of Work

Start with build prep because it is the smallest and most isolated substrate issue. The change should be a local helper near the existing source skip logic, for example:

```go
func shouldSkipRuntimeArtifactDir(rel string) bool {
    rel = filepath.ToSlash(rel)
    return rel == "var/browser" ||
        strings.HasPrefix(rel, "var/browser/") ||
        rel == "var/chrome" ||
        strings.HasPrefix(rel, "var/chrome/") ||
        rel == "var/playwright" ||
        strings.HasPrefix(rel, "var/playwright/")
}
```

Apply the helper in both source listing and copying paths. The exact function names may differ; inspect `internal/build/build.go` before editing and keep the implementation aligned with the existing `shouldSkipDir` / `shouldSkipFile` style.

For non-regular files, preserve current symlink semantics. If existing code already has explicit symlink handling, do not collapse it into generic non-regular skipping. Add a narrow helper that returns true for unsupported non-regular entries and lets symlink logic continue to do what it does today.

Then add tests in `internal/build/build_test.go`. The regression fixture should include a minimal app plus:

```text
var/browser/SingletonSocket
var/browser/Default/Preferences
var/chrome/SingletonLock
var/playwright/cache-marker
```

On Unix, create `SingletonSocket` as a Unix socket when possible. If the platform cannot create that file type, use a FIFO or skip only the socket-specific assertion. The acceptance assertion is that `Prepare(...)` succeeds and the resulting source file list/build state excludes all `var/browser`, `var/chrome`, and `var/playwright` files.

Next, add generated-worker tests in `internal/workers/workers_typescript_test.go`. The generated `worker.ts` should continue to include supervisor monitoring tied to `SCENERY_DEV_SUPERVISOR_PID`. This prevents future edits from silently removing the cleanup safety net while still allowing foreground `scenery worker typescript` to run without dev-supervisor ownership.

Finally, implement dev-supervisor process cleanup. Keep the matching rules conservative:

- Process must be marked as dev-supervised, for example by environment such as `SCENERY_DEV_SUPERVISOR=1`.
- Process must belong to the current app root, for example `SCENERY_APP_ROOT=<current app root>`.
- Command line must include `.scenery/generated/legacy-async-runtime/typescript/worker.ts`.

When a stale process matches, send SIGTERM, wait for a short grace period, and use SIGKILL only if the process survives. Record the cleanup event in the dev dashboard store so the action is visible in logs/traces/status surfaces. Do not scan or kill arbitrary Bun processes.

## Concrete Steps

1. Edit `internal/build/build.go`.

   Add runtime-artifact directory skipping for:

   ```text
   var/browser
   var/chrome
   var/playwright
   ```

   Apply the skip in source discovery and copy paths. Add defensive non-regular-file handling without changing symlink behavior.

2. Add build-prep regression tests in `internal/build/build_test.go`.

   The tests should prove browser runtime artifacts do not fail `Prepare(...)` and are absent from the build result source-file list.

3. Add generated TypeScript worker tests in `internal/workers/workers_typescript_test.go`.

   Assert that generated `worker.ts` includes `SCENERY_DEV_SUPERVISOR_PID` handling and supervisor-disappearance monitoring.

4. Add dev-supervisor lifecycle coverage in `cmd/scenery/dev_supervisor_test.go`.

   Use a fake process or process abstraction already present in the supervisor tests. Verify that closing the supervisor interrupts and waits for TypeScript worker children and removes them from supervisor state.

5. Add detached dev-session stale-worker cleanup in `cmd/scenery/dev_typescript.go` and/or `cmd/scenery/dev_supervisor.go`.

   Keep the cleanup path detached-dev-only. It must not run for foreground `scenery worker typescript`.

6. Record cleanup events in the dev dashboard store.

   Use the existing devdash store/logging conventions rather than adding a new event subsystem.

7. Run formatting and validation.

   Use `gofmt` for Go edits. Keep dependencies unchanged unless an existing internal helper already provides the needed process inspection behavior.

## Validation and Acceptance

Run focused validation first:

```sh
go test ./internal/build ./internal/workers ./cmd/scenery
```

Run the full repository validation before marking the plan complete:

```sh
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Acceptance criteria:

- `internal/build` tests prove `var/browser`, `var/chrome`, and `var/playwright` are skipped during build prep.
- Build prep does not fail on browser-like non-regular files.
- Existing `.scenery/artifacts/...` hidden-directory behavior remains unchanged.
- Generated `worker.ts` tests prove dev-supervisor PID monitoring remains present.
- Dev-supervisor tests prove TypeScript worker children are interrupted, waited on or killed, and detached from supervisor state on close.
- Detached-session stale cleanup only targets workers for the current app root and generated TypeScript worker path.
- Foreground `scenery worker typescript` behavior remains unchanged.
- Cleanup events are visible through the existing dev dashboard store/logging surfaces.

## Idempotence and Recovery

The build-prep skip changes are deterministic and safe to rerun. If a test fixture creates a Unix socket or FIFO and a run aborts, remove the test temp directory or rerun the test; test temp roots should be disposable.

The stale-worker reaper must be safe to run repeatedly. A second run should find no matching stale workers and should not report an error. If a process exits between discovery and signal, treat it as already cleaned up.

If stale-worker cleanup behaves too broadly during development, disable or revert that milestone before merging. The conservative matching rules are more important than aggressive cleanup.

## Artifacts and Notes

Expected generated/local artifacts remain under `.scenery/` and are not committed.

The plan intentionally does not prescribe ONLV paths such as `agents/chatgpt_browser.worker.ts`, `scripts/smoke-chatgpt-browser.sh`, or ONLV `AGENTS.md`. Those belong to the ONLV app plan and should not be implemented in this scenery repository under this ExecPlan.

Useful local inspection commands while implementing:

```sh
rg -n "shouldSkipDir|shouldSkipFile|copyTree|listSourceFiles" internal/build/build.go
rg -n "SCENERY_DEV_SUPERVISOR_PID|worker.ts|typescript worker" internal/workers cmd/scenery
ps -axo pid,ppid,command | rg 'bun .*worker.ts|scenery dev' | rg -v rg
```

## Interfaces and Dependencies

Scenery interfaces in scope:

- Build/source walker behavior in `internal/build`.
- Generated TypeScript legacy async runtime worker runtime under `.scenery/generated/legacy-async-runtime/typescript/`.
- Dev supervisor TypeScript worker startup and shutdown behavior.
- Dev dashboard store/logging used for observable cleanup events.

External dependencies:

- No new Go dependencies are expected.
- Process inspection should use existing standard-library or repository-local helpers where possible.
- Browser runtimes such as Chromium, Playwright, or Bun are treated as external processes/filesystem producers; this plan does not add Playwright to scenery.
