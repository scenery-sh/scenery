# onlava v0 Release Readiness

This ExecPlan is a living document. The sections `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` must be kept up to date as work proceeds.

This plan follows the standard in [../../PLANS.md](../../PLANS.md). It supersedes a removed historical release-readiness product prompt and remains self-contained so an agent can read it without prior chat context.

Current contract note, reviewed 2026-06-07: this completed plan is historical
release-hardening context. The current command contract uses `onlava up` for the
local app session and `onlava serve` for headless API execution; use
`docs/local-contract.md` as the source of truth.

## Purpose / Big Picture

onlava is close to being a useful local-first runtime, but the current repository mixes stable app runtime behavior with development-platform behavior. The first production-ready release should be intentionally smaller, more boring, and easier to validate.

The goal of this plan is to freeze a reliable v0 contract. Stable v0 should include the app config file, runtime commands, build artifacts, typed/raw HTTP endpoints, auth handler, service initialization and shutdown, private/internal calls, secrets from environment and `.env`, basic logs/traces, and machine-readable CLI outputs. Development conveniences such as dashboard, local HTTPS proxy, trust-store installation, removed agent transport, Pub/Sub UI, and cron UI should be labeled dev-only or beta until their contracts are hardened.

The outcome should be observable from a clean checkout. A contributor should be able to run the documented release validation sequence and prove that the CLI builds, tests pass, generated artifacts are deterministic, stable APIs match docs, dev/admin endpoints are not exposed on the public app listener, secrets are not copied into build caches, and release archives do not contain local machine artifacts.

## Progress

- [x] (2026-04-27 16:34Z) Created this ExecPlan from the removed historical release-readiness product prompt.
- [x] (2026-04-27 17:36Z) Defined the stable v0 surface and marked everything else dev-only, beta, or compatibility-mode in `docs/local-contract.md`.
- [x] (2026-04-27 17:36Z) Confirmed the `onlava dev` / headless `onlava run` split is implemented and kept [0001-devrun-command-split.md](0001-devrun-command-split.md) as the detailed dependency.
- [x] (2026-04-27 17:36Z) Confirmed current checkout has `ui/dist`, added `onlava version --json`, and added `onlava.version.v1` schema.
- [x] (2026-04-27 17:36Z) Gated dev/admin/pprof endpoints behind explicit dev endpoint mode instead of registering them on the public app router by default.
- [x] (2026-04-27 17:36Z) Made local HTTPS proxy and trust-store installation opt-in through `onlava dev --proxy` and `onlava dev --proxy --trust`.
- [x] (2026-04-27 17:36Z) Documented onlava-native behavior as stable.
- [x] (2026-04-27 15:48Z) Centralized `.env` parsing in `internal/envfile`, wired runtime secrets, dev supervisor child env, dashboard DB discovery, discovery through it, and documented precedence.
- [x] (2026-04-27 17:36Z) Restricted build workspace copying so `.env`, `.env.*`, `.git`, `.onlava`, `node_modules`, `.DS_Store`, `__MACOSX`, and `coverage` are not persisted in build caches.
- [x] (2026-04-27 17:36Z) Added response JSON semantics tests for `json:"-"`, `omitempty`, embedded structs, headers, `onlava:"httpstatus"`, and custom marshalers.
- [x] (2026-04-27 17:36Z) Aligned CLI usage, docs, schemas, and implementation for the release-hardening slice.
- [x] (2026-04-27 18:38Z) Made missing declared secrets warn in local development but fail before serving under `onlava run --env production`.
- [x] (2026-04-27 18:48Z) Reclassified reachable-but-not-frozen surfaces so `onlava psql`, Victoria sidecars/downloads, Pub/Sub/Cron admin affordances, and trace/metric inspect semantics do not accidentally become stable v0 API.
- [x] (2026-04-27 19:06Z) Added `scripts/release-gate.sh` as the single pre-release gate for full tests, race, lint, UI builds, self-harness, clean install, fixture and optional external app smoke, router safety, secrets, and artifact hygiene.
- [x] (2026-04-27 15:48Z) Ran the release validation sequence and recorded the results.

## Surprises & Discoveries

Known audit findings from the source prompt:

- `onlava run` previously started development supervisor behavior, including dashboard, local HTTPS proxy, removed agent transport, and file watching.
- Generated app binaries could carry dev-platform behavior through `github.com/pbrazdil/onlava/runtimeapp`.
- `runtime/server.go` mounted dev/admin/platform/pprof endpoints on the app router.
- Local HTTPS proxy and trust-store behavior were enabled by default in development paths.
- The repo had conflicting guidance about strict onlava-only behavior versus migration compatibility support.
- The build workspace copied arbitrary app files, which risks copying `.env` and other local files into cache.
- Response encoding did not fully match normal `encoding/json` semantics for tags such as `json:"-"` and `omitempty`.

Implementation discoveries:

- `onlava dev` and headless `onlava run` were already split before this plan execution started, including tests that reject dev flags on `onlava run`.
- Runtime dev endpoints could be preserved for `onlava dev` by injecting `ONLAVA_DEV_ENDPOINTS=1` into the development child process.
- Local proxy startup could be made opt-in without changing Caddy configuration internals by changing defaults and adding `onlava dev --proxy` / `--trust` environment wiring.
- Full integration tests exposed older assumptions that `onlava run` reflected arbitrary CORS origins by default and that `onlava dev` always started the local HTTPS proxy. The tests were updated to assert the new release contract: explicit CORS allowlist for headless run and `onlava dev --proxy` for hostname routing.
- The shared `.env` loader can preserve package-init ergonomics by passing `.env` and `.env.local` values into the development child process before Go package initialization. This avoids requiring app code to manually parse local env files.
- Generated package init already runs secret population before the app server starts, so strict production secret validation can live in the runtime secret loader and still fail before serving.
- The implemented CLI surface is broader than the stable support surface. `onlava psql`, Victoria sidecars/downloads, local admin clear commands, and trace/metric inspect subjects are useful, but their semantics are still too implementation-shaped to freeze as stable v0.
- The release gate should be executable as one script so human and agent release checks do not drift into overlapping hand-run recipes.

## Decision Log

- Decision: Freeze a narrow stable v0 contract instead of freezing the whole current feature set.
  Rationale: The runtime, dev supervisor, dashboard, proxy, Pub/Sub, cron, removed agent transport, and migration compatibility are interwoven. A smaller stable surface reduces production risk.
  Date/Author: 2026-04-27 / Codex

- Decision: Treat the command split in [0001-devrun-command-split.md](0001-devrun-command-split.md) as a release-readiness dependency, not a duplicate workstream.
  Rationale: `onlava dev` versus headless `onlava run` is the highest-leverage boundary and already has its own detailed ExecPlan.
  Date/Author: 2026-04-27 / Codex

- Decision: Stable v0 should prefer onlava-native behavior, with any migration tooling kept explicit and separate.
  Rationale: Hidden compatibility makes APIs harder to freeze and contradicts the repository’s strict onlava naming goal.
  Date/Author: 2026-04-27 / Codex

- Decision: Dev/admin features should not live on the public app listener by default.
  Rationale: Users may bind apps to `0.0.0.0`; exposing pprof, platform stats, Pub/Sub clear, or dev config endpoints there is unsafe.
  Date/Author: 2026-04-27 / Codex

- Decision: Keep dev endpoints available under explicit dev mode instead of deleting them.
  Rationale: The dashboard and frontend proxy still need `/__onlava/config`, `/__onlava/pubsub/clear`, platform stats, and pprof during local development, but production-like `onlava run` and generated binaries should not expose them by default.
  Date/Author: 2026-04-27 / Codex

- Decision: Make local proxy trust installation a second opt-in after proxy startup.
  Rationale: Starting a local reverse proxy is less invasive than mutating system trust stores. `onlava dev --proxy` should not imply trust-store mutation.
  Date/Author: 2026-04-27 / Codex

- Decision: Use one shared local env parser and preserve process environment precedence.
  Rationale: Runtime secrets, dev child startup, DB discovery, and dashboard code were parsing `.env` independently. One parser reduces drift, and process env precedence keeps shells/CI explicit.
  Date/Author: 2026-04-27 / Codex

- Decision: Missing declared secrets are warnings outside production and startup errors in production.
  Rationale: Local development should stay forgiving, but `onlava run --env production` must fail before serving if an app declares secrets that are not present in process env or `.env`.
  Date/Author: 2026-04-27 / Codex

- Decision: Classify implemented local helpers separately from stable v0 API.
  Rationale: Being reachable from the CLI should not imply a long-term support promise. `onlava psql`, Victoria supervision/download behavior, Pub/Sub/Cron admin affordances, and trace/metric inspection semantics remain beta/dev-only until their contracts are intentionally frozen.
  Date/Author: 2026-04-27 / Codex

- Decision: Keep the release gate strict and explicit.
  Rationale: It is acceptable for `scripts/release-gate.sh` to be slower and stricter than normal development validation because it is the pre-release confidence check. Artifact hygiene failures should stop the gate instead of being silently cleaned up.
  Date/Author: 2026-04-27 / Codex

## Outcomes & Retrospective

Completed validation on 2026-04-27:

- `python3 -m json.tool docs/knowledge.json >/dev/null` passed.
- `test -f ui/dist/index.html` passed.
- `go test ./...` passed.
- `go test -race ./...` passed.
- `golangci-lint run ./...` passed.
- `go install ./cmd/onlava` passed.
- `onlava version --json` returned `onlava.version.v1`.
- `onlava inspect docs --json --repo-root <onlava-repo-root>` passed with `missing_count=0`, `review_due_count=0`, and `stale_count=0`.
- `onlava harness self --json --write` passed and wrote `.onlava/harness/self-latest.json`.

The v0 release-hardening slice now has explicit docs for stable/dev/beta/compatibility surfaces, including reachable helpers that are deliberately not stable yet. It also has gated dev/admin endpoints, opt-in proxy/trust behavior, safe build workspace filtering, centralized local env parsing, response encoding semantics tests, and a scriptified release gate.

## Context and Orientation

The release-readiness source audit was folded into this ExecPlan after the historical product prompt was removed. It recommends not freezing the current feature set as-is. It names the main risk as the mixing of app runtime, development supervisor, dashboard, local HTTPS proxy, Pub/Sub, cron, and removed agent transport.

The CLI dispatcher lives in `cmd/onlava/main.go`. The stable commands to freeze for v0 are expected to be `onlava run`, `onlava build`, `onlava check --json`, `onlava inspect ... --json`, `onlava logs --jsonl`, `onlava test`, and `onlava gen client`. `onlava dev` is the development-platform command after the command split.

The current development supervisor lives in `cmd/onlava/dev_supervisor.go`. It owns dashboard, local proxy, removed agent transport/dashboard endpoints, app child process lifecycle, file watching integration, process output capture, and dashboard state.

The file watcher lives in `cmd/onlava/watch.go`. It has historically watched only selected files such as `.onlava.json`, `.go`, `.cpp`, and `.h`, which may miss build-affecting files like `go.mod`, `go.sum`, `.env`, and `.env.local`.

The generated runtime entry point and build workspace logic live under `internal/build` and `internal/codegen`. Release readiness depends on deterministic generated artifacts and safe build workspace copying.

The public runtime server lives in `runtime/server.go`. Audit findings say this currently mounts app APIs and dev/admin endpoints on the same listener. The v0 app listener should serve user APIs by default. Dev/admin surfaces should move to a local supervisor listener, a CLI-only path, or an explicitly enabled local admin listener.

The local proxy lives under `internal/localproxy`. It uses embedded Caddy and can install local trust roots. The release contract must make this opt-in and clearly development-only.

The dashboard UI source lives in `ui/` and is embedded by the CLI through `github.com/pbrazdil/onlava/ui`. A clean release build must either include built `ui/dist` assets, generate them in the release process, or avoid requiring them for headless/stable builds.

Terms used in this plan:

- Stable v0 means the supported behavior that users and agents can rely on without beta labels.
- Dev-only means a feature is useful in `onlava dev` but not part of the production-like runtime contract.
- Beta means a feature can ship but its behavior is not frozen yet.
- Migration tooling means explicit commands or docs for one-time source transitions, not hidden parser/runtime behavior.
- Public app listener means the HTTP listener that serves user application endpoints.
- Admin/dev listener means a local-only or explicitly enabled listener for diagnostics, pprof, Pub/Sub controls, dashboard reporting, or platform operations.

## Milestones

Milestone 1 defines the release contract. At the end of this milestone, `docs/local-contract.md`, `AGENTS.md`, command usage, and docs index agree on the stable v0 commands, stable runtime features, and beta/dev-only features.

Milestone 2 completes the runtime/dev boundary. At the end of this milestone, `onlava dev` owns the development platform and headless `onlava run` starts only the app runtime. This milestone is complete when the acceptance criteria in [0001-devrun-command-split.md](0001-devrun-command-split.md) are satisfied.

Milestone 3 removes release build blockers. At the end of this milestone, a clean checkout can run `go install ./cmd/onlava` without missing embedded assets. Release docs state the required Go version and the Bun/UI build expectations. Release packaging excludes `.DS_Store`, `__MACOSX`, caches, and other local artifacts.

Milestone 4 hardens public-router safety. At the end of this milestone, pprof, `/__onlava/config`, `/__onlava/pubsub/clear`, platform stats, dashboard reporting, and other dev/admin endpoints are not mounted on the public app listener by default. If any remain, they are explicitly gated, documented, tested, and safe for local-only use.

Milestone 5 centralizes configuration and secrets. At the end of this milestone, one loader owns process environment, `.env`, and `.env.local` precedence. Development may warn for missing secrets, while production-like run/build paths fail early for missing required secrets unless explicitly configured otherwise.

Milestone 6 makes build artifacts safe and deterministic. At the end of this milestone, build workspace copying includes only files needed to compile and run the app. `.env`, `.env.*`, `.git`, `.onlava` runtime state, `node_modules`, editor files, caches, and local artifacts are excluded unless explicitly required and documented.

Milestone 7 fixes framework semantics before freeze. At the end of this milestone, response encoding honors expected Go JSON behavior for `json:"-"`, `omitempty`, embedded structs, pointers, headers, `onlava:"httpstatus"`, and custom marshalers, or documents any deliberate custom behavior with tests.

Milestone 8 runs the release gate. At the end of this milestone, the full release validation checklist passes and `Outcomes & Retrospective` records exact command results.

## Plan of Work

Start by updating the release contract before changing behavior. The repo should have one canonical local contract that says what is stable, what is beta, and what is dev-only. Use `docs/local-contract.md` as the canonical document, and keep `cmd/onlava/main.go` usage text aligned with it.

Next, finish the command split work tracked by [0001-devrun-command-split.md](0001-devrun-command-split.md). Do not make other release-hardening work depend on a dev supervisor hidden inside `onlava run`.

Then handle clean-checkout reproducibility. Verify whether `ui/dist` and other embedded assets are required for `go install ./cmd/onlava`. If they are required, choose one explicit release strategy: commit built assets, generate them in release packaging, or move embedding behind a development build boundary. The strategy must be documented and validated from a clean checkout.

After reproducibility, audit runtime routes. Move dev/admin endpoints out of `runtime/server.go` public routing by default. If dashboard or development reporting needs endpoints, keep them on the dashboard/supervisor server. If pprof is needed, expose it only through an explicit local admin mode.

Then make local HTTPS proxy and trust installation opt-in. `onlava dev` may support `--proxy` and a separate explicit trust flag. Do not surprise users by mutating system trust stores. `onlava run` should never install trust roots.

Then centralize `.env` and secrets loading. Replace duplicate parsers and loaders in runtime, supervisor, and tests with one package-level implementation. Document precedence and mode-specific missing-secret behavior.

Then lock down build workspace copying. Replace broad file copying with an allowlist plus explicit asset inclusion behavior. Add tests that prove `.env`, `.env.local`, `.git`, `.onlava`, `node_modules`, `.DS_Store`, and `__MACOSX` are excluded.

Finally, add the response encoding tests and fix semantics before declaring the runtime contract stable.

## Concrete Steps

Work from the repository root:

    cd <onlava-repo-root>

List current command and runtime boundary references:

    rg -n "case \"run\"|case \"dev\"|runCommand|devCommand|runWithWatch|newDevSupervisor|runtimeapp|localproxy|pprof|platform.Stats|pubsub/clear|__onlava/config" cmd internal runtime

Check clean checkout build assumptions:

    go install ./cmd/onlava
    test -f ui/dist/index.html
    test -f ui/dist/index.html

Update the canonical contract:

    $EDITOR docs/local-contract.md AGENTS.md docs/index.md

Use the command split plan:

    $EDITOR docs/plans/0001-devrun-command-split.md

Audit public runtime routes:

    rg -n "__onlava/config|pubsub/clear|platform.Stats|debug/pprof|Access-Control-Allow-Origin|Access-Control-Allow-Credentials" runtime cmd internal

Audit build workspace copying:

    rg -n "WalkDir|isSourceFile|copy|\\.env|node_modules|DS_Store|__MACOSX" internal/build cmd internal

Audit secrets loaders:

    rg -n "LoadDotEnv|\\.env|secrets|DatabaseURL|ONLAVA_ENV|ONLAVA_MODE" cmd internal runtime

Add or update tests for the release blockers:

    go test ./cmd/onlava ./internal/build ./runtime

Run the full validation gate:

    scripts/release-gate.sh

The script also runs `go install ./cmd/onlava`, `onlava harness self --json --write`, dashboard UI builds, a clean source-copy install, fixture smoke, optional external app smoke, public-router safety checks, production secrets checks, and artifact hygiene checks. Set `ONLAVA_RELEASE_GATE_EXTERNAL_APP_ROOT` to include a read-only external onlava app in the gate.

Record exact command results in `Outcomes & Retrospective` before marking this plan complete.

## Validation and Acceptance

Release readiness is accepted when all of these are true:

- A clean checkout can run `go install ./cmd/onlava`.
- `go test ./...` passes.
- `go test -race ./...` has either passed or has documented exclusions for tests where race mode is impractical.
- `onlava harness self --json --write` passes.
- `onlava inspect docs --json --repo-root <onlava-repo-root>` reports no missing or stale documents.
- CLI usage text and `docs/local-contract.md` describe the same commands.
- `onlava version --json` exists or the lack of version command is explicitly deferred before release.
- Stable, beta, dev-only, and compatibility-mode features are labeled in docs.
- `onlava run` is headless and production-like.
- `onlava dev` owns dashboard, local HTTPS proxy, frontend proxy, removed agent transport, file watching, and development-only UI.
- The public app listener does not expose pprof, platform stats, Pub/Sub clear, dashboard report endpoints, or arbitrary credentialed CORS by default.
- Local HTTPS proxy and trust-store installation are opt-in.
- `.env`, `.env.local`, `.git`, `.onlava` runtime state, `node_modules`, `.DS_Store`, and `__MACOSX` are not copied into build workspaces or release archives.
- Response encoding tests cover `json:"-"`, `omitempty`, embedded structs, pointer fields, header fields, `onlava:"httpstatus"`, and custom marshalers.
- The release validation sequence is documented in `Outcomes & Retrospective`.
- `scripts/release-gate.sh` passes before release artifacts are cut.

## Idempotence and Recovery

This plan should be executed in small, independently testable slices. Each milestone should leave the repo buildable. If a risky change fails, revert only that change and keep completed hardening work.

Do not delete development functionality while moving it behind `onlava dev`. The recovery path for a broken command split is to keep `onlava dev` on the existing supervisor path and continue narrowing `onlava run` separately.

When changing build workspace copying, expect some apps to rely on embedded assets. Preserve required app assets through explicit inclusion rules or clear diagnostics rather than broad copying.

When moving dev/admin endpoints, keep local dashboard functionality working by routing dashboard traffic through the supervisor/dashboard server instead of the public app listener.

## Artifacts and Notes

Primary source:


Related active ExecPlan:

    docs/plans/0001-devrun-command-split.md

Validation artifact:

    .onlava/harness/self-latest.json

Release-stable generated artifacts:

    .onlava/gen/app.json
    .onlava/gen/routes.json
    .onlava/gen/services.json
    .onlava/gen/manifest.json
    .onlava/build/latest.json

Stable v0 candidates:

    .onlava.json
    onlava run
    onlava build
    onlava check --json
    onlava inspect ... --json
    onlava logs --jsonl
    onlava test
    onlava gen client
    typed/raw HTTP endpoints
    auth handler
    service struct initialization and shutdown
    private/internal calls
    secrets from env/.env
    basic traces/logs

Dev-only or beta candidates:

    dashboard

    local HTTPS proxy
    trust-store installation
    removed agent transport server
    Pub/Sub UI
    cron UI
    source rewrite/direct-call behavior unless made inspectable

## Interfaces and Dependencies

No new external dependency is expected for this plan. Prefer the Go standard library and existing onlava packages.

Expected CLI interfaces to freeze:

    onlava dev [development flags]
    onlava run [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
    onlava build [--app-root <path>] [-o <path>] []
    onlava check --json [--app-root <path>]
    onlava inspect app|routes|services|endpoints|wire|build|paths|traces|metrics|docs --json
    onlava logs --jsonl [--app-root <path>]
    onlava test [--app-root <path>] [go test flags/packages...]
    onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]

Expected documentation interfaces:

    docs/local-contract.md
    docs/index.md
    docs/knowledge.json
    docs/plans/active.md
    docs/tech-debt.md

Expected implementation areas:

    cmd/onlava/main.go
    cmd/onlava/watch.go
    cmd/onlava/dev_supervisor.go
    cmd/onlava/build.go
    runtime/server.go
    runtime/secrets.go
    runtime/encode.go
    runtime/decode.go
    internal/build
    internal/codegen
    internal/localproxy
    internal/
