# scenery Agent Instructions

Use Czech with developers unless requested otherwise; keep code, comments,
tests, docs, product copy, and commits in English. Keep solutions simple and
task-scoped.

## Core References

- [ARCHITECTURE.md](ARCHITECTURE.md): package boundaries and hard invariants.
- [docs/spec/SPEC.md](docs/spec/SPEC.md): `.scn` language, types, and runtime contracts.
- [docs/spec/WORKER-ABI.md](docs/spec/WORKER-ABI.md): Host-to-worker HTTP/2 UDS protocol and capabilities.
- [SKILL.md](SKILL.md): workflow for agents working inside Scenery apps.
- Machine contracts: `docs/schemas/*.schema.json`, `docs/environment.registry.json`, and `scenery help <command> -o json`.

## Autonomy & Safety

Continue authorized reading, local edits, builds, cached tests, and worktree
`scenery up`, including corrections caused by the change. Ask only for material
unresolved scope choices or missing authorization.

- Do not spawn subagents unless the human explicitly asks; skills cannot grant permission.
- Validate with `.scenery/harness/bin/scenery`. Do not run `go install ./cmd/scenery` unless the human explicitly requests installation; worktrees share that installed path. For co-developing Scenery with a local consuming app on the same machine, link this checkout from the app with `scenery framework link <scenery-checkout>` (not by pushing WIP changes to `main` or adding `replace` lines to the app's `go.mod`); `scenery up` watches the linked checkout and hands off automatically on edits, and `scenery framework unlink` returns the app to its pinned version.
- Preserve unrelated dirty work and verified runtime/data ownership. Branch switches and worktree removal do not authorize data deletion. Never adopt or reset mismatched resources.
- Land on `main` only through `scenery feature land`. Once a worktree's work is fully on `main`, remove it completely in the same task (close its feature record, remove checkout, local branch, and candidate checkout). Retain worktrees with uncommitted/unlanded work or running processes and report them.
- Add no environment-variable knobs unless requested; register approved env vars in `docs/environment.registry.json`.
- Only humans may modify the top-level roadmap document unless explicitly instructed.

## Engineering Rules

- Prefer Go's standard library and a small public surface; justify new direct dependencies (allowlisted in `scripts/verify/harness_arch.go`).
- End-user HTTP surfaces must not contain "scenery" (case-insensitive): URLs, headers, cookies, response bodies, errors, and generated browser code/assets. Developer/operator tools and source packages may retain the name.
- Keep one rolling specification, compiler, runtime, and machine protocol. Remove obsolete spellings without compatibility aliases, old decoders, or fallback paths. Preserve `app.scn`, `package.scn`, `.scenery.json`, `scenery.sh/...`.
- Declare service, operation, binding, durable-work, schedule, event, subscription, data, page, renderer, and middleware identities in `.scn`; domain UI stays in app-owned slots. Changed persisted input with retained durable `external_name` needs a new `revision`.
- Change JSON/model contracts together with `docs/schemas/`, tests, and harness expectations. Use the checked diagnostic catalog (`SCN8xxx` request failures; `SCN9xxx` internal failures expose sanitized messages and opaque report tokens only).
- Keep module sources in the non-symlink workspace and generated output beneath declared managed roots as one atomic artifact-set transaction. Never commit `.scenery/`, Victoria state, `node_modules`, coverage, `.DS_Store`, or `.env*`.
- Non-generated source over 1,000 lines warns; over 2,500 fails. Keep `AGENTS.md` lean (<2,500 words).

## Validation Matrix

- Documentation-only edits: `go run ./scripts/verify --quick --summary --write`.
- Source / contract / runtime edits: run `go run ./scripts/verify --summary --write` (or `--quick` for scoped development checks, full before completion/landing), read `.scenery/harness/agent-context-summary.json`, and complete the `changed_area.recommended_commands` union.
- Always run `golangci-lint run ./...` for Go changes; suppress only narrowly justified intentional violations. Run `cargo test --locked` in `sdk/rust/scenery-kernel` or `sdk/rust/scenery-sdk` for Rust Host/SDK changes.
- Keep Go's test cache enabled (`-count=1` / `--fresh-tests` only for explicit fresh measurement or nondeterminism investigation). Every exact top-level Go test root must stay below 100ms p95; put process/service/network/OS proof in named integration probes (`--probe <id>`).
- UI catalog or generator changes require `tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json` and regenerating committed fixtures.
- Release certification uses `scripts/release-gate.sh`.

## Completion Contract

Continue through required validation and change-caused failures. Demonstrate the
requested runtime/UI scenario and current served identity for runtime/UI tasks;
a patch/build alone is insufficient. Report behavior, commands/results, skipped
checks with exact reasons, and remaining risks.
