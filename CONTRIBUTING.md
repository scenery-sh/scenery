# Contributing To scenery

Thanks for helping improve scenery. Keep changes small, explicit, and easy to validate.

## Setup

Requirements:

- Go 1.27+
- Bun for dashboard, generated TypeScript, and full self-harness validation

Build a checkout-local CLI from the repo root:

```sh
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery version -o json
```

## Development Loop

Read [AGENTS.md](AGENTS.md) and the child instructions for the area you change.
Use [Fresh Worktree Preflight](docs/agent-guide.md#fresh-worktree-preflight)
before UI or full self-harness validation.

After editing, refresh the validation selection:

```sh
.scenery/harness/bin/scenery harness self --quick --summary --write
cat .scenery/harness/agent-context.json
```

Run the exact `changed_area.recommended_commands` union and any additional
child-scope checks. [The validation matrix](AGENTS.md#validation-matrix)
defines the required proof, including the release loop for runtime changes.
Go changes require affected-package tests and `go test ./...`; retain the Go
test cache unless measuring fresh execution or investigating nondeterminism.
Rebuild the local CLI after source changes; worktrees share the installed CLI.

For dashboard UI changes:

```sh
cd apps/console
bun run lint
bun run typecheck
bun run build
```

For catalog changes, follow [ui/AGENTS.md](ui/AGENTS.md). `ui/` is embedded
source, not a standalone Bun app; its checker and fixture commands run from
the repository root.

## Pull Requests

Before opening a pull request:

- run the relevant tests and mention the commands in the PR
- update docs when user-facing behavior changes
- add or update tests at stable boundaries
- keep dependencies minimal and justify new dependencies clearly
- avoid committing local artifacts such as `.DS_Store`, `.scenery/`, logs, databases, generated cache directories, or frontend `dist/` directories

Good test boundaries include parser validation, generated code, runtime HTTP behavior, CLI JSON contracts, and fixture apps.

## Code Style

- Prefer the Go standard library unless an external dependency has a clear payoff.
- Keep public packages small and user-facing.
- Keep parser-derived app semantics in the app model before codegen or runtime wiring.
- Use deterministic generated artifacts.
- Prefer plain, boring Go over reflection when the parser already knows the shape.

## Security

Do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).
