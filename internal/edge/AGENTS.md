# Edge Lifecycle Agent Instructions

## Purpose

`internal/edge` owns the managed Caddy process lifecycle used by `scenery system edge` (start, stop, reload, local-CA trust, and persistence of the matching edge state) plus the pure edge config and parsing surfaces: Caddyfile generation (`caddyconfig.go`) and launchd plist parsing helpers (`launchd.go`).

## Ownership

- Keep CLI parsing, output rendering, managed-tool resolution, helper process orchestration, and privileged-listener setup in `cmd/scenery`.
- `ListenTCP` owns only the existing dual-stack socket binding primitive;
  the CLI still selects privileged addresses, ports, and forwarding policy.
- Keep edge state schemas and path derivation in `internal/agent`.
- Do not import `cmd/scenery`; the command package adapts this package's concrete functions.

## Local Contracts

- `Start` writes both `agent.EdgeState` and `agent.EdgeTargetState` only after the Caddy process survives its startup window.
- On Unix, `Start` passes the agent home's `edge.lock` to the detached Caddy child for its full lifetime; a second owner must fail before spawning.
- `Stop` acts only on the PID recorded in edge state.
- `Reload` must address the configured Unix admin socket.
- `TrustLocalCA` must use a temporary admin-only Caddyfile and leave no listener or temporary directory behind.
- Preserve the platform-specific detached-child behavior in `process_*.go`.

## Work Guidance

Keep the exported interface small and concrete. Add command-specific policy to `cmd/scenery`, not to this package. Keep ordinary lifecycle tests in process with injected runners; real-process proof belongs in the release harness under the root timing policy.

## Verification

```sh
go test ./internal/edge
go test -race ./internal/edge
go test ./cmd/scenery
```

## Child Agent Index

None.
