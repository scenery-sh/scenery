# Contributing to scenery

Keep changes small, explicit, and easy to validate.

## Setup

Requirements:
- Go 1.27+
- Bun (for generated TypeScript, `ui/` catalog checks, and UI probes)

Build a checkout-local CLI from the repo root:

```sh
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery version -o json
```

## Development Loop

Read [AGENTS.md](AGENTS.md) and [ARCHITECTURE.md](ARCHITECTURE.md). After
editing, run the verifier and complete its changed-area command union:

```sh
go run ./scripts/verify --quick --summary --write
cat .scenery/harness/agent-context-summary.json
```

Run `go test ./...`, `golangci-lint run ./...`, and full `go run ./scripts/verify
--summary --write` when source/runtime contracts change. Keep Go's test cache
enabled unless investigating nondeterminism or measuring fresh timings.

## Pull Requests

- Prefer the Go standard library; justify any new direct dependency.
- Keep public packages small and end-user HTTP surfaces free of `"scenery"`.
- Update `docs/schemas/` and tests whenever CLI/JSON contracts change.
- Never commit `.scenery/`, `.DS_Store`, `.env*`, `node_modules`, or coverage.

## Security

Do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).
