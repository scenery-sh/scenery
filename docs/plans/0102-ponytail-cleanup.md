# Ponytail Cleanup: Singular Dashboard, Native CLI Parsing, and Dead Surface Removal

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current while implementing the cleanup.

## Purpose / Big Picture

Remove every over-engineering finding from the 2026-07-09 whole-repository Ponytail audit without changing supported Scenery behavior. After this work, ConsoleNext remains the sole dashboard, CLI grammar remains compatible while using one standard-library parser layer, dormant compatibility endpoints and unreachable code are gone, and the remaining public packages expose only used contracts.

## Progress

- [x] 2026-07-09 - Re-read repo instructions and current contracts; captured the audit baseline and clean Git state.
- [x] 2026-07-09 - Removed the obsolete runnable `ui/` dashboard while preserving `ui/registry` and registry source.
- [x] 2026-07-09 - Replaced all 44 hand-written `parse*Args` flag switches, plus the build/deploy loops found during the sweep, with a shared `flag.FlagSet`-backed parser.
- [x] 2026-07-09 - Removed legacy dashboard GraphQL, trace compatibility, dormant RPCs, and every `cmd/scenery` or `internal` helper reported unreachable.
- [x] 2026-07-09 - Removed the test-only memory store and unused `ErrDetails` extension point.
- [x] 2026-07-09 - Synchronized docs, indexes, agent instructions, toolchain source locks, and harness UI ownership.
- [x] 2026-07-09 - Passed the full validation and live dashboard acceptance surface; repaired the empty Service Catalog marker found by the first browser-harness run.

## Surprises & Discoveries

- The obsolete `ui/` app and the supported registry share a directory. Deleting the directory wholesale would break the documented `@scenery/*` registry, so cleanup must preserve `ui/registry`, `ui/src/components/registry`, registry layouts/primitives, and the installer script.
- The current release and embed scripts already build only `apps/consolenext`; the old dashboard is not in the release path.
- The first post-removal dead-code pass exposed 51 unreachable functions under `cmd/scenery` and `internal`, plus an unused dashboard interface method; deleting those revealed one additional parser helper after the CLI migration.
- Go's `flag.FlagSet` stops at the first positional by default, so preserving Scenery's existing interspersed flags and task/script pass-through grammar required one small normalization adapter rather than per-command parsing loops.

## Decision Log

- Decision: Preserve the downstream Scenery registry and remove only the runnable old dashboard sources, assets, router, and app-only build dependencies.
  Rationale: The registry is a documented product surface; ConsoleNext is the singular dashboard surface.
  Date/Author: 2026-07-09 / Codex.
- Decision: Use a shared standard-library `flag.FlagSet` adapter that accepts the existing documented placement of positional arguments and `--` pass-through.
  Rationale: Scenery must keep working as before while eliminating repeated switch-based flag parsing.
  Date/Author: 2026-07-09 / Codex.
- Decision: Remove compatibility endpoints only when current ConsoleNext, CLI, docs, and tests have no supported caller.
  Rationale: This removes obsolete surface without guessing about live behavior.
  Date/Author: 2026-07-09 / Codex.

## Outcomes & Retrospective

Completed on 2026-07-09. ConsoleNext is the sole runnable dashboard; `ui/` is now only the reusable registry. All 44 audited `parse*Args` functions use the shared standard-library flag adapter, and the additional build/deploy argument loops found during implementation use it too. Legacy GraphQL, trace-event compatibility, dormant RPCs, MemoryStore, ErrDetails, and all dead-code findings under `cmd/scenery` and `internal` are gone.

The final tracked diff removes more than ten thousand lines before the small shared parser, plan, and registry-test additions. `go test ./...`, both frontend validation surfaces, embed rebuild, docs inspection, dead-code checks, self-harness, and the complete fixture browser journey pass. Self-harness remains `pass_with_warnings` only for pre-existing review-due documents, large-file warnings, and test timing budgets; it reports no blocking error.

## Context and Orientation

`apps/consolenext` is the Vite/Astryx dashboard built by `scripts/build-dashboard-ui-embed.sh` and embedded under `cmd/scenery/dashboard_static/dist`. `ui/` contains both an older TanStack Router dashboard and the Scenery shadcn registry. The registry is validated by `cmd/scenery/harness_ui.go` and installed by `ui/scripts/scenery-shadcn.mjs`.

CLI parsing is spread across `cmd/scenery` in `parse*Args` functions. Public grammar is documented in `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, help output, and tests. Dashboard RPC dispatch is in `cmd/scenery/dashboard_rpc.go`; current ConsoleNext calls are visible under `apps/consolenext/src`.

## Milestones

1. Remove the old runnable dashboard and app-only dependencies while keeping the registry install and static validation surface.
2. Introduce the shared `flag.FlagSet` parser adapter and migrate every `parse*Args` function without changing accepted grammar or errors relied on by tests.
3. Remove old GraphQL and trace-event compatibility paths, dormant dashboard RPCs, statically unreachable helpers, MemoryStore, and ErrDetails.
4. Update all affected contracts and complete full validation, embed rebuild, fixture UI harness, and browser smoke proof.

## Plan of Work

Make removals in independently testable slices. Preserve supported types and JSON shapes unless their only consumer is removed in the same slice. For parser migration, keep each command's semantic validation near its option struct and move only token/flag mechanics into the shared parser.

## Concrete Steps

1. Delete old dashboard routes, router, app context, app-only tests/assets, and GraphQL frontend. Adjust `ui/package.json`, lockfile, toolchain manifest, harness architecture paths, and UI documentation while retaining registry sources and installer.
2. Add `cmd/scenery/cli_flags.go` around `flag.FlagSet`, including interspersed-position normalization and `--` pass-through. Convert parser functions incrementally and run their focused tests after each command family.
3. Remove `/__graphql`, old trace event RPCs/encoders, unused onboarding/editor/telemetry shims, and related tests/state. Remove every function reported unreachable by the current `deadcode -test ./...` pass unless a generated/runtime entry point proves it live.
4. Delete `internal/storage/memory.go` and its duplicate test. Replace `ErrDetails` with `any` only if a live details payload exists; otherwise remove the field and accessors.
5. Update `AGENTS.md`, `docs/ui-agent-contract.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/knowledge.json`, and plan indexes where contracts changed.

## Validation and Acceptance

Acceptance requires all of the following from the repository root:

    gofmt -w <changed Go files>
    go test ./cmd/scenery
    go test ./...
    cd apps/consolenext && bun run lint && bun run typecheck && bun run build
    ./scripts/build-dashboard-ui-embed.sh
    go run ./cmd/scenery inspect docs --json
    go run golang.org/x/tools/cmd/deadcode@v0.46.0 -test ./...
    go run ./cmd/scenery harness self --summary --write
    go run ./cmd/scenery harness ui --json --write --app-root testdata/apps/basic

The final tree must contain no old dashboard app entry point, `/__graphql` handler, compatibility trace encoder, production MemoryStore, `ErrDetails`, or unreachable internal functions. Existing help/CLI parser tests and current ConsoleNext workflows must pass.

## Idempotence and Recovery

All deletions are Git-tracked and recoverable from history. Apply cleanup in slices and run focused tests before continuing. If a parser migration changes grammar, restore the prior accepted token order in the shared adapter rather than adding a compatibility parser beside it.

## Artifacts and Notes

Baseline: 969 tracked files, 137,329 Go/TypeScript/JavaScript/shell lines, 6,119 lines in the old runnable UI slice, 1,889 lines in `parse*Args`, and 53 internal functions reported unreachable by `deadcode -test ./...`.

## Interfaces and Dependencies

No new third-party dependency is allowed. The parser uses Go's `flag` package. Expected dependency removal from `ui/package.json`: `@tanstack/react-router`, `@vitejs/plugin-react`, and direct `vite` once the old app build is gone. ConsoleNext retains its current Astryx/StyleX stack.
