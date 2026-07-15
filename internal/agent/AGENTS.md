# Local Agent State Instructions

## Purpose

`internal/agent` owns the local agent protocol and durable machine ownership records for sessions, substrates, deploy targets, and edge processes.

## Ownership

- Keep process/session ownership and durable state identity here.
- Keep command output payload identities in `cmd/scenery` and edge process lifecycle in `internal/edge`.

## Local Contracts

- Cross-process state uses unversioned artifact kinds, digest schema/spec revisions, and producer identity.
- The privileged edge helper is the one reader that must NOT use strict current decoding: it outlives scenery upgrades as a root LaunchDaemon, so it reads target metadata only through `LoadEdgeHelperTarget` in `edgehelper.go` — a frozen, tolerant, read-only handoff contract identified by `EdgeHelperContractRevision`. Never route helper reads through `LoadDurableArtifact`, never let the helper rewrite the file, and bump the contract revision when a frozen field is renamed, removed, or revalidated differently (additive fields need no bump).
- Durable identity migrations preserve the exact legacy bytes in an owner-only backup, fsync the replacement, and write an idempotent completion marker.
- Never recreate deploy ownership, live process ownership, or credentials after a decode failure.
- Closing or restarting the agent never signals registered substrate processes. Substrate-specific owners perform destructive shutdown explicitly.
- `agent.lock` is held for the control-plane process lifetime. `edge.lock` is inherited by managed Caddy on Unix so a second owner fails before binding.
- `launchd.go` owns launchd supervision of the agent (`dev.scenery.agent`, KeepAlive). Installation means a bootstrapped job, never just a plist; removal boots the job out first. `StartProcess` must route agent starts through the supervisor whenever the installed plist manages the requested socket (`SupervisesSocket`), so no caller spawns an unsupervised agent that races a KeepAlive respawn. Keep launchctl access behind the package hooks so tests never touch real launchd.
- `systemd.go` is the Linux mirror: `scenery-agent.service` (Restart=always) and the `scenery-deploy-resume.service` boot oneshot under `/etc/systemd/system`. The same supervision rules apply — installation means a loaded unit, `StartProcess` cooperates through `systemctl` when the unit supervises the requested socket, and systemctl access stays behind the package hooks. Deploy targets additively record published production frontends (`DeployTargetFrontend`); old registries without that metadata stay valid and proxy-only.

## Verification

```sh
go test ./internal/agent
go test ./internal/edge
go test ./cmd/scenery
```
