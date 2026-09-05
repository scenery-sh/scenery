# Local Agent State Instructions

## Purpose

`internal/agent` owns the local agent protocol and durable machine ownership records for sessions, substrates, deploy targets, and edge processes.

## Ownership

- Keep process/session ownership and durable state identity here.
- Keep command output payload identities in `cmd/scenery` and edge process lifecycle in `internal/edge`.

## Local Contracts

- Cross-process state uses unversioned artifact kinds, digest schema/spec revisions, and producer identity.
- Agent home is injected with `PathsForHome`, `RunOptions.Home`, or `EnsureWith`. Tests pass a temp dir; only the CLI/runtime boundary reads `SCENERY_AGENT_HOME`.
- The privileged edge helper is the one reader that must NOT use strict current decoding: it outlives scenery upgrades as a root LaunchDaemon, so it reads target metadata only through `LoadEdgeHelperTarget` in `edgehelper.go` — a frozen, tolerant, read-only handoff contract identified by `EdgeHelperContractRevision`. Never route helper reads through `LoadDurableArtifact`, never let the helper rewrite the file, and bump the contract revision when a frozen field is renamed, removed, or revalidated differently (additive fields need no bump).
- Durable identity migrations preserve the exact legacy bytes in an owner-only backup, fsync the replacement, and write an idempotent completion marker.
- Never recreate deploy ownership, live process ownership, or credentials after a decode failure.
- Closing or restarting the agent never signals registered substrate processes. Substrate-specific owners perform destructive shutdown explicitly.
- Ensuring an agent never replaces a live agent based on build age. Require
  the current health schema/spec identity before sharing it; incompatible or
  unreadable health fails without signals or durable writes. Operator restart
  is explicit, and tests use private homes and router ports.
- Owner-checked session deletion atomically removes only that session's leases from shared substrate metadata; it never deletes or stops the shared substrate and preserves unrelated leases.
- Public deploy requests read one immutable in-memory route snapshot. Validate
  candidate public-route owner fingerprints while restoring or registering
  sessions, rebuild the snapshot when deploy/session state changes, and
  monitor owners for exit; do not load deploy state or inspect processes on the
  HTTP request path.
- `agent.lock` is held for the control-plane process lifetime. `edge.lock` is inherited by managed Caddy on Unix so a second owner fails before binding.
- `launchd.go` owns launchd supervision of the agent (`dev.scenery.agent`, KeepAlive). Installation means a bootstrapped job, never just a plist; removal boots the job out first. `StartProcess` must route agent starts through the supervisor whenever the installed plist manages the requested socket (`SupervisesSocket`), so no caller spawns an unsupervised agent that races a KeepAlive respawn. Keep launchctl access behind the package hooks so tests never touch real launchd.
- `systemd.go` is the Linux mirror: `scenery-agent.service` (Restart=always) and the `scenery-deploy-resume.service` boot oneshot under `/etc/systemd/system`. The same supervision rules apply. Deploy targets and published frontends record the named environment that owns them; missing environment remains readable only as durable pre-cutover state and is never selected for a new deploy.

## Verification

```sh
go test ./internal/agent
go test ./internal/edge
go test ./cmd/scenery
```
