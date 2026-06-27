# Contributing To scenery

Thanks for helping improve scenery. Keep changes small, explicit, and easy to validate.

## Setup

Requirements:

- Go 1.26+
- Bun, only for dashboard UI or benchmark changes

Install the CLI from the repo root:

```sh
go install ./cmd/scenery
scenery version --json
```

## Development Loop

Run the Go test suite:

```sh
go test ./...
```

Rebuild the CLI after repository changes:

```sh
go install ./cmd/scenery
```

For substantial changes, run the self-harness when practical:

```sh
scenery harness self --json --write
```

For dashboard UI changes:

```sh
cd ui
bun run typecheck
bun run build
```

## Pull Requests

Before opening a pull request:

- run the relevant tests and mention the commands in the PR
- update docs when user-facing behavior changes
- add or update tests at stable boundaries
- keep dependencies minimal and justify new dependencies clearly
- avoid committing local artifacts such as `.DS_Store`, `.scenery/`, logs, databases, generated cache directories, or `ui/dist/`

Good test boundaries include parser validation, generated code, runtime HTTP behavior, CLI JSON contracts, and fixture apps.

## Code Style

- Prefer the Go standard library unless an external dependency has a clear payoff.
- Keep public packages small and user-facing.
- Keep parser-derived app semantics in the app model before codegen or runtime wiring.
- Use deterministic generated artifacts.
- Prefer plain, boring Go over reflection when the parser already knows the shape.

## Security

Do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).
