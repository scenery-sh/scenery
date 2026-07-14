# SSH Source-Sync Deployment

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
the `Decision Log`, and `Outcomes & Retrospective` current while implementing
it.

- Status: completed
- Owner: scenery CLI / app configuration
- Created: 2026-07-14

## Purpose / Big Picture

Add the smallest useful remote deployment command:

```text
scenery deploy <ssh-target>
```

The command validates the current app, connects through an ordinary OpenSSH
host alias, stops an existing copy of that app, synchronizes the current
working tree with `rsync`, then runs the existing remote `scenery up --detach
--wait ready`. Success means the remote Scenery process reported readiness.
Brief downtime is intentional.

This is source synchronization for a single server or preview environment. It
is not a deployment provider, release system, remote agent, process manager,
rollback system, or zero-downtime orchestrator.

## Progress

- [x] 2026-07-14 Design reduced to direct `rsync` over passwordless OpenSSH.
- [x] 2026-07-14 Current app-config and `scenery deploy` command boundaries
      inspected; implementation locations and command collision rules recorded.
- [x] 2026-07-14 Added and validated `deploy.ssh` in app configuration and
      its JSON Schema.
- [x] 2026-07-14 Added the positional SSH-target deployment path and focused
      tests, including exact child exit-code preservation.
- [x] 2026-07-14 Updated help, local contract, agent guide, README, portable
      skill, root agent instructions, and repository knowledge.
- [x] 2026-07-14 Focused tests, cached full Go tests, self-harness, and real
      OrbStack SSH acceptance passed.

## Surprises & Discoveries

- `scenery deploy` already owns `plan`, `apply`, `setup`, `status`, `enable`,
  `disable`, `resume`, and `teardown`. The positional target form must coexist
  with those current commands instead of replacing them.
- `internal/app.Config.Deploy` and `DeployConfig.IsZero` already provide the
  correct config boundary; only one `SSH []string` field is needed.
- `runSceneryCheck` already exposes the current check path inside
  `cmd/scenery`, so deployment need not spawn another Scenery binary for local
  validation.
- The CLI already preserves an `*exec.ExitError` exit code through wrapped
  errors because `cliExitCode` uses the existing `ExitCode()` contract.
- Live OrbStack acceptance showed that local check creates a machine-specific
  Scenery-owned `go.work`; uploading it points the remote Go tool at local cache
  paths. SSH sync therefore excludes `go.work` and `go.work.sum` and lets the
  remote Scenery binary maintain them.
- Live acceptance also showed that `scenery down` returned exit 10 when no
  runtime was registered, despite the existing command already presenting that
  state as an ordinary skipped stop in its output path. The shared down resolver
  now treats a missing runtime as idempotent success, while preserving errors for
  real agent or shutdown failures.
- First ONLV deployment exposed 4.4 GB of ignored local backups plus caches
  that fixed excludes alone would upload. Rsync now consumes per-directory
  `.gitignore` rules; the fixed state/secret exclusions remain fail-safe.
- A pristine ONLV host had no agent socket, so calling remote `scenery down`
  could not list sessions. The remote stop now runs only when both the app
  marker and default agent socket exist; an existing but unhealthy socket
  still fails closed.

## Decision Log

- 2026-07-14, Petr and agent: use `rsync` over OpenSSH. Do not add Git push,
  receive hooks, release bundles, deployment providers, remote agents, or a Go
  dependency.
- 2026-07-14, Petr and agent: configuration is only
  `deploy.ssh: ["<ssh-target>"]`. OpenSSH remains the source of truth for host,
  user, port, identity, jump hosts, host keys, and connection reuse.
- 2026-07-14, Petr and agent: derive the remote app directory as
  `$HOME/.scenery/apps/<app-id>`, where `<app-id>` is `Config.AppID()`. There is
  no remote-path option.
- 2026-07-14, Petr and agent: stop, sync, and restart in place. Brief downtime
  is part of the contract; no staging directory, symlink swap, rollback, or
  second runtime is added.
- 2026-07-14, agent: reserve current deploy command names. An SSH target must be
  a safe OpenSSH alias made of ASCII letters, digits, dots, underscores, and
  dashes, must start with a letter or digit, and must not equal an existing
  deploy subcommand. This prevents option and remote-shell injection without
  implementing an SSH parser.
- 2026-07-14, agent: use fixed excludes for `.git/`, `.scenery/`, `.env`,
  `node_modules/`, `go.work`, and `go.work.sum`, and consume per-directory
  `.gitignore` rules. ONLV demonstrated the need by keeping multi-gigabyte
  backups and caches outside its tracked source.
- 2026-07-14, agent: the first version streams terminal output only. Do not add
  a second machine protocol or buffer remote logs into a final JSON payload.

## Outcomes & Retrospective

The shipped surface remains one config field and one positional command. It
uses only `os/exec`, OpenSSH, rsync, and the existing Scenery check/down/up
paths. Existing deployment-plan and public-edge subcommands are unchanged.

Validation completed on 2026-07-14:

- `go test ./internal/app -run 'TestDiscoverRoot.*Deploy'` passed.
- `go test ./cmd/scenery -run 'TestDeploySSH|TestResolveDownSessionAllowsMissingRuntime'`
  passed.
- cached `go test ./...` passed; the final `cmd/scenery` package run took
  40.012 seconds.
- cached `go test ./cmd/scenery` passed from cache.
- `scenery harness self --summary --write` passed every lane, including the
  41.330-second cached Go-test lane, schema validation, fixture matrix,
  architecture checks, and dashboard checks.

Real acceptance used the passwordless OpenSSH alias `orb` and a disposable
Ubuntu OrbStack target. A second deployment stopped the registered remote
owner and started exactly one replacement. A final deployment changed actual
Go source and returned `{"message":"deployed:remote"}` from the remote route,
proving remote recompilation rather than binary reuse. The target reported one
running session, one supervisor, and one app process. Remote `.env` and
`.scenery` markers survived, a stale source file was deleted, local-only
`.git` and `node_modules` content was absent, and the remote Scenery-owned
`go.work` contained only the target's Linux source/cache paths.

The first real ONLV deployment then used alias `onlv-209` against a pristine
Ubuntu 24.04 host. The ignored `var/` backups, caches, local datasets, and
experimental workspaces were absent remotely; the app reached ready with one
supervisor and one app process; VictoriaLogs, VictoriaMetrics, and
VictoriaTraces all answered; and the backend `/healthy` route returned
`{"status":"ok"}`. The advertised route remains loopback-only by design;
public listener/domain setup is outside this source-sync command.

## Context and Orientation

App configuration is decoded in `internal/app/root.go`. `DeployConfig`
currently contains `Domain` and `Root`, and `Config.MarshalJSON` omits the
deploy object when `DeployConfig.IsZero()` is true. The public configuration
shape is checked by `docs/schemas/scenery.config.schema.json`.

The CLI routes `scenery deploy ...` through `runDeployCommand` in
`cmd/scenery/deploy.go`. It dispatches the existing public-edge subcommands and
delegates `plan` and `apply` to deployment planning. Add the new orchestration
in `cmd/scenery/deploy_ssh.go`; keep the already-large edge implementation in
`deploy.go` otherwise unchanged.

The local validation path is `runSceneryCheck` in `cmd/scenery/check.go`.
App-root discovery uses `resolveAppRoot`. Child processes use `os/exec`, inherit
the CLI streams, and preserve child exit status.

The remote host is configured by the operator in `~/.ssh/config`, for example:

```sshconfig
Host some-id
    HostName example.com
    User deploy
    IdentityFile ~/.ssh/id_ed25519
```

The target must already have passwordless OpenSSH access, `rsync`, `scenery`,
and the app toolchain. An unlocked SSH agent is allowed. Normal OpenSSH
host-key checking remains enabled.

## Milestones

### Milestone 1: singular config field

Extend the existing type, not the deployment graph or provider model:

```go
type DeployConfig struct {
    Domain string   `json:"domain,omitempty"`
    Root   string   `json:"root,omitempty"`
    SSH    []string `json:"ssh,omitempty"`
}
```

Update `IsZero`, config validation, config tests, and
`docs/schemas/scenery.config.schema.json`. Reject empty entries, duplicates,
unsafe aliases, and names reserved by current deploy subcommands. Preserve
list order in JSON; membership, not order, authorizes deployment.

Acceptance: config round-trip succeeds for `{"deploy":{"ssh":["some-id"]}}`;
invalid or duplicate aliases fail with a path-specific message; domain-only
config behavior is unchanged.

### Milestone 2: direct orchestration

In `runDeployCommand`, keep every existing subcommand exact. When the first
argument is not reserved, treat it as an SSH target and allow only the
ordinary app-root option. Reject unlisted targets before running a child.

Implement this sequence in `cmd/scenery/deploy_ssh.go`:

1. Resolve the app root and load `.scenery.json`.
2. Require the requested target in `cfg.Deploy.SSH`.
3. Run the existing local check path for that app root. Stop on failure.
4. Run SSH with `BatchMode=yes` and `ConnectTimeout=10`. In the same preflight,
   require remote `scenery` and `rsync`, then create the remote app directory.
5. If the remote directory contains `.scenery.json`, run remote `scenery down
   --app-root "$HOME/.scenery/apps/<app-id>"`. Rely on current idempotent down
   behavior for an already-stopped app; do not hide other errors.
6. Run `rsync -az --delete` from the app root to
   `<target>:.scenery/apps/<app-id>/`, excluding `.git/`, `.scenery/`, `.env`,
   `node_modules/`, `go.work`, and `go.work.sum`. The rsync remote shell is
   `ssh -o BatchMode=yes -o ConnectTimeout=10`.
7. Run remote `scenery up --detach --wait ready --app-root
   "$HOME/.scenery/apps/<app-id>"` and stream its output.

Set child stdin/stdout/stderr to current CLI streams. Wrap failures with the
operation name and `%w` so the failing executable's exit code remains the CLI
exit code. Do not run a later step after a failure.

### Milestone 3: contract and live proof

Update `cmd/scenery/help.go`, `README.md`, `docs/local-contract.md`,
`docs/agent-guide.md`, `SKILL.md`, and `docs/knowledge.json`. Describe this as
beta single-server source sync with downtime, not production orchestration.
Keep the existing public-edge and deployment-plan commands documented.

Prove the command against a real disposable SSH target or maintainer-owned
server. The target must be an OpenSSH alias, not explicit connection flags.

## Plan of Work

First extend `DeployConfig`, its zero check, validation, schema, and tests. Keep
target validation in `internal/app` so every command sees one trusted shape.

Then add positional dispatch and one orchestration file. Use `os/exec` directly
and fake `ssh`/`rsync` executables in test `PATH`; do not introduce a command
runner interface. Tests record argv and order in a temporary file.

Finally update public docs and run one real remote deployment. Mocked command
tests alone are not completion.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

1. Edit `internal/app/root.go`, `internal/app/root_test.go`, and
   `docs/schemas/scenery.config.schema.json`.
2. Add `cmd/scenery/deploy_ssh.go` and `cmd/scenery/deploy_ssh_test.go`; minimally
   adjust `cmd/scenery/deploy.go` and `cmd/scenery/help.go`.
3. Update the contract/docs files named in Milestone 3.
4. Run cached focused tests:

   ```sh
   go test ./internal/app -run 'TestDiscoverRoot.*Deploy'
   go test ./cmd/scenery -run 'TestDeploySSH'
   ```

5. Run cached final validation:

   ```sh
   go test ./...
   go test ./cmd/scenery
   scenery harness self --summary --write
   ```

6. With a configured disposable target, run:

   ```sh
   ssh -o BatchMode=yes -o ConnectTimeout=10 some-id true
   scenery deploy some-id
   ssh -o BatchMode=yes some-id 'scenery ps -o json --app-root "$HOME/.scenery/apps/my-app"'
   ```

   Verify the remote session is running and an advertised route is reachable.

Do not use `-count=1` unless investigating nondeterminism. Do not run `go
install` as validation unless Petr explicitly requests installation.

## Validation and Acceptance

Focused tests prove:

- an allowed safe alias reaches all operations in order;
- an unlisted, duplicate, unsafe, or reserved alias fails before SSH;
- local check failure runs no SSH or rsync;
- SSH/preflight or down failure runs no rsync;
- rsync failure does not run `up`;
- `up` failure returns its actual child exit status;
- paths containing spaces remain single argv elements;
- rsync receives `--delete` and every fixed exclusion;
- existing deploy subcommand dispatch remains unchanged.

Runtime acceptance requires a successful deployment of the current working
tree, including an intentional uncommitted marker; absence of local `.git`,
`.scenery`, `.env`, and `node_modules` on the target; preservation of a remote
`.env` marker and remote `.scenery` state; deletion of a stale remote source
file; readiness from remote `scenery up`; and a reachable app route.

The plan is complete only when focused tests, cached `go test ./...`, cached
`go test ./cmd/scenery`, self-harness, and real remote acceptance all pass and
the documentation/indexes describe the shipped command.

## Idempotence and Recovery

Re-running `scenery deploy <target>` is safe: local validation repeats, remote
`down` tolerates an already-stopped app, rsync converges remote source, and
remote `up --detach --wait ready` starts the synchronized app.

If preflight fails, the running app is untouched. If stop, rsync, or startup
fails, print the failing operation and preserve the child exit status. There
is no rollback. Fix the remote requirement or source error and rerun. Data
survives because ordinary `scenery down` does not delete it and `.scenery/` is
excluded from rsync deletion.

Never use `--delete-excluded`; it would delete the remote `.env` and
`.scenery` state this contract preserves.

## Artifacts and Notes

Expected source changes:

```text
internal/app/root.go
internal/app/root_test.go
cmd/scenery/deploy.go
cmd/scenery/deploy_ssh.go
cmd/scenery/deploy_ssh_test.go
cmd/scenery/help.go
docs/schemas/scenery.config.schema.json
README.md
SKILL.md
docs/local-contract.md
docs/agent-guide.md
docs/knowledge.json
```

No deployment registry, release bundle, Git repository, receive hook, remote
service definition, remote agent, or new dependency is an artifact of this
plan.

## Interfaces and Dependencies

The only new configuration interface is:

```json
{
  "name": "my-app",
  "deploy": {
    "ssh": ["some-id"]
  }
}
```

The only new CLI form is:

```text
scenery deploy <ssh-target> [--app-root <path>]
```

Existing deploy subcommands retain their meanings. The implementation uses
only Go's standard library plus external `ssh` and `rsync`. OpenSSH owns
authentication and connection configuration. The remote current Scenery
binary owns build, runtime, readiness, and persistent-state behavior.
