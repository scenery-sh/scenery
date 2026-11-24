---
name: scenery
description: Build, run, debug, and validate applications using Scenery's .scn graph, polyglot workers, and generated clients.
---

# scenery

Scenery runs one supervised runtime per canonical app root. `.scenery.json`
marks the root; `app.scn` and package-local `package.scn` declare the canonical
resource graph; Go, Rust, JavaScript, and TypeScript implement generated contracts.

Read the consuming app's `AGENTS.md` for app-specific paths, environment names,
validation profiles, and product invariants. Discover exact CLI grammar anytime
with `scenery help <command> -o json`.

## Common Workflows

| Task | Commands |
|---|---|
| Understand a module, service, route, or source file | `scenery context --for <target> -o json` |
| Validate declarations and handlers | `scenery test <module> -o json`, `scenery check -o json` |
| Exercise a live binding | `scenery call <binding> --input-file input.json -o json` |
| Run or inspect local runtime | `scenery up`, `scenery ps -o json`, `scenery ps --wait -o json`, `scenery doctor -o json` |
| Debug failures & telemetry | `scenery logs -o jsonl --limit 200`, `scenery traces list --status error -o json`, `scenery inspect report <token> -o json` |
| Query a service's PostgreSQL schema | `scenery db query --service <module> '<sql>' --param <value> -o json` |
| Trigger a durable execution or schedule | `scenery durable run <target> --input '<json>' -o json` |
| Add a module with a starter test | `scenery add module <name> --runtime go\|rust\|ts\|js -o json` |
| Regenerate clients / UI | `scenery generate -o json` |
| Assistants | `scenery assistant status <name> -o json` |
| Databases, migrations, storage | `scenery db migrate`, `scenery inspect storage -o json`, `scenery snapshot save/load` |
| Code tasks & validation | `scenery task list -o json`, `scenery validate changed --base main -o json`, `scenery harness -o json --write` |
| Worktrees & features | `scenery worktree create/list/remove`, `scenery feature list/land -o json` |

See [examples/webhook-inbox/README.md](examples/webhook-inbox/README.md) for a
complete working app and [docs/spec/SPEC.md](docs/spec/SPEC.md) for `.scn`
language rules.

## Ownership & Safety Rules

- **Declare in `.scn`, implement against the generated contract.** Go comments and `init()` register
  nothing. Edit authored files; treat generated contracts, TypeScript clients,
  and `.scenery/gen/` as disposable outputs beneath declared managed roots.
- **Framework selection.** Set `framework_version` in `.scenery.json` to the
  exact version of a prebuilt CLI, then run that executable's
  `scenery framework use -o json` to check the selection. Go applications
  can select their CLI through the `scenery.sh` requirement in `go.mod` when
  they omit the explicit executable pin. For Go co-development,
  `scenery framework use --source <checkout>` snapshots local source. Keep
  the app-local replacement out of commits.
- **Prefer JSON output (`-o json` / `-o jsonl`).** Branch on stable `SCNxxxx`
  diagnostic codes. `SCN9xxx` internal failures withhold cause details; inspect
  them via `scenery inspect report <report-token> -o json`. A local
  `scenery call -o json` includes the matching report, logs, and spans when available.
- **Protect retained data.** Databases and object storage belong to their
  verified canonical worktree. Branch switches and worktree removal never
  authorize deleting data; reclaim orphaned state only via `scenery prune
  --orphaned`.
- **Repository work.** When modifying Scenery itself, read
  [AGENTS.md](AGENTS.md) and [ARCHITECTURE.md](ARCHITECTURE.md). Do not run
  `go install ./cmd/scenery` unless the human explicitly asks; use the
  worktree-local `.scenery/harness/bin/scenery`.

## Completion

Complete the requested behavior, its validation, and fixes caused by the change.
Runtime/UI tasks require verifying the live scenario and served identity—not
just compilation. Keep Go's test cache enabled unless explicitly measuring fresh
execution.
