# Local Agent State Instructions

## Purpose

`internal/agent` owns the local agent protocol and durable machine ownership records for sessions, substrates, deploy targets, and edge processes.

## Ownership

- Keep process/session ownership and durable state identity here.
- Keep command output payload identities in `cmd/scenery` and edge process lifecycle in `internal/edge`.

## Local Contracts

- Cross-process state uses unversioned artifact kinds, digest schema/spec revisions, and producer identity.
- Durable identity migrations preserve the exact legacy bytes in an owner-only backup, fsync the replacement, and write an idempotent completion marker.
- Never recreate deploy ownership, live process ownership, or credentials after a decode failure.
- Closing or restarting the agent never signals registered substrate processes. Substrate-specific owners perform destructive shutdown explicitly.
- `agent.lock` is held for the control-plane process lifetime. `edge.lock` is inherited by managed Caddy on Unix so a second owner fails before binding.

## Verification

```sh
go test ./internal/agent
go test ./internal/edge
go test ./cmd/scenery
```
