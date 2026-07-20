# Deployment Issues

## Source-sync production deployment on 2026-07-20

This document records issues observed while deploying a Scenery application
with `scenery deploy` to a configured SSH target. It describes the incident and
its evidence only.

### 1. The local and remote Scenery CLIs were incompatible

The local Scenery CLI had been updated from the current repository before the
deployment. The remote host still had an older Scenery CLI.

The SSH preflight completed because both `scenery` and `rsync` existed on the
remote host. The incompatibility appeared later, when the remote CLI read the
synchronized application configuration and rejected
`envs.local.ui_catalog`. That field was valid in the current local CLI and
belonged to the local environment, while the selected deployment environment
was `production`.

The deployment could not continue until the remote CLI was manually replaced
with a build from the current Scenery source.

### 2. The live runtime was stopped before the incompatibility surfaced

The observed command order was:

1. local `scenery check`;
2. SSH command/tool presence preflight;
3. remote `scenery down`;
4. source synchronization;
5. remote `scenery up`;
6. static frontend publication.

The remote configuration failure happened after `scenery down`. The deployment
therefore exited with the application runtime stopped. The previously published
static frontend remained present, but requests requiring the application
runtime did not have a running backend.

Restoring service required manual investigation and a separate remote startup.

### 3. The application source depended on files outside its synchronized root

The deployed application contained these local sibling references:

```text
go.mod: replace scenery.sh => ../../scenery
.scenery.json: envs.local.ui_catalog = ../../scenery/ui
```

`scenery deploy` synchronized the application root, not the sibling Scenery
checkout. The resulting remote application tree did not contain the source
referred to by the Go module replacement.

During recovery, the Scenery source had to be synchronized separately on the
remote host. A filesystem link was also required to satisfy the path expected
by the generated Go workspace.

### 4. A generated Go workspace contained a workstation-specific absolute path

The deployed/generated `go.work` referred to the Scenery checkout by its
absolute path on the local workstation. That path did not exist on the Linux
deployment host.

The remote generated workspace had to be removed and regenerated for the
remote filesystem before the application could build and start. The local
generated file was not edited.

The current SSH deploy implementation excludes `go.work` and `go.work.sum`;
the incident still exposed the portability problem when local workspace state
and external module replacements participate in deployment.

### 5. Runtime recovery and static frontend publication were separate states

After the remote runtime was restored and reported ready, the current static
frontend still had to be published with:

```text
scenery deploy publish --env production --app-root <remote-app-root> -o json
```

The successful publication produced a new release ID and independently probed
the entry document and API. Before that command completed, runtime readiness
did not by itself demonstrate that the current frontend source was live.

During the failed deployment, the previous static release remained published
while the dynamic runtime was stopped. This created a mixed state in which the
public frontend existed but the full application was unavailable.

### 6. Deployment state had to be reconstructed from several surfaces

Determining the final state required all of the following:

- inspecting the remote process command;
- running `scenery deploy status -o json`;
- checking the recorded release ID;
- probing the public domain;
- probing the origin directly;
- requesting a generated frontend asset;
- checking served HTML for Vite development-client markers.

The failed attempt itself did not expose a single record containing the CLI
versions, completed deployment step, runtime state, static release state, and
manual recovery actions.

### 7. Production execution used development-oriented names

The final remote process was:

```text
scenery up --env production -o jsonl --app-root <remote-app-root>
```

The selected environment was confirmed as `production`, the frontend was
served as a static production build through the managed edge, and no Vite
development client was present.

At the same time, Scenery output and internal status surfaces used names such as
“dev agent”, “dev session”, “local path router”, and “development supervisor”.
Those names made it unclear during the incident whether the remote process was
actually using the production environment.

### Final observed state

After the manual CLI update, external source synchronization, workspace
regeneration, runtime startup, and frontend publication:

- the remote session reported `environment: production`;
- systemd supervised the Scenery services;
- the runtime reported ready;
- the frontend mode was `caddy_static`;
- publication recorded a new release ID;
- the entry document and API probes passed;
- the public application routes returned HTTP 200;
- the served frontend did not contain Vite development-client markers.
