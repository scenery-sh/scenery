# Edge Lifecycle Agent Instructions

## Purpose

`internal/edge` owns the managed Caddy process lifecycle used by `scenery system edge` (start, stop, reload, local-CA trust, and persistence of the matching edge state) plus the pure edge config and parsing surfaces: Caddyfile generation (`caddyconfig.go`), dnsmasq/resolver config generation and edge DNS state persistence (`dns.go`), launchd plist parsing helpers (`launchd.go`), the production-frontend deploy artifact publisher (`publish.go`), and the Linux systemd edge unit lifecycle (`systemd.go`).

## Ownership

- Keep CLI parsing, output rendering, managed-tool resolution, dnsmasq/helper process orchestration, and privileged-listener setup in `cmd/scenery`.
- Keep edge state schemas and path derivation in `internal/agent`.
- Do not import `cmd/scenery`; the command package adapts this package's concrete functions.

## Local Contracts

- `Start` writes both `agent.EdgeState` and `agent.EdgeTargetState` only after the Caddy process survives its startup window.
- On Unix, `Start` passes the agent home's `edge.lock` to the detached Caddy child for its full lifetime; a second owner must fail before spawning.
- `Stop` acts only on the PID recorded in edge state.
- `Reload` must address the configured Unix admin socket.
- `TrustLocalCA` must use a temporary admin-only Caddyfile and leave no listener or temporary directory behind.
- Preserve the platform-specific detached-child behavior in `process_*.go`.
- `PublishFrontendArtifact` writes immutable releases beneath the validated deploy-artifact root and switches one relative `current` symlink atomically; it rejects symlinks/special files, requires a regular `index.html`, retains a fixed release window, and never deletes outside the frontend directory. Caddy static routes must reference the `current` symlink so publication switches without a reload.
- Public domain sites render static frontends only when the `current` artifact resolves to a complete release (`renderableStaticFrontends`); anything else falls back to the agent proxy. Blocked Scenery paths and the `/api/*` agent proxy always precede static handlers.
- `systemd.go` owns the `scenery-edge.service` unit: managed Caddy binary, Scenery-rendered Caddyfile, admin-socket reload. Keep systemctl access behind the package hook so tests never touch real systemd, and validate candidate configs with `ValidateCaddyConfig` before install/reload.

## Work Guidance

Keep the exported interface small and concrete. Add command-specific policy to `cmd/scenery`, not to this package. The real-process tests are intentionally serial because parallel process startup worsened the measured full-suite wall time on the maintainer machine.

## Verification

```sh
go test ./internal/edge
go test -race ./internal/edge
go test ./cmd/scenery
```

## Child Agent Index

None.
