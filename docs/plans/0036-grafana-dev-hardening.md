# Grafana Dev Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The existing Grafana integration in `onlava dev` is directionally correct: it supervises a local Grafana process, writes provisioning under `.onlava/grafana`, pins Grafana and Victoria datasource plugin versions, exposes state through the dashboard and JSON surfaces, and keeps Grafana out of `onlava run`.

This plan hardens the trust edges that remain before Grafana should be treated as a first-class daily development surface. The target behavior is that `onlava dev` only advertises Grafana dashboard links when the Grafana server, expected plugins, expected datasources, and expected dashboards are actually usable. A plain `/api/health` response is not enough.

The starting review was static and GitHub-based. It did not run the test suite or a live Grafana smoke test. Treat the findings as code and design review input that must be verified against the current working tree during implementation.

Primary outcomes:

* Default `auto` mode must not trust an arbitrary Grafana already listening on the configured port.
* Grafana's generated `root_url` must match the browser-facing URL, including local proxy hostnames such as `https://grafana.onlv.localhost/`.
* Starter dashboards must query telemetry that onlava definitely emits, or the runtime must emit the metric contract those dashboards use.
* Managed pinned Grafana should be preferred over random `PATH` Grafana.
* Grafana startup must be isolated from ambient `GF_*` environment variables by default.
* Provisioning should prune stale datasources and expose verified readiness, not merely process health.

Non-goals:

* Do not add Grafana to `onlava run`.
* Do not introduce Docker as a requirement.
* Do not make Grafana Cloud or any remote service a dependency.
* Do not polish speculative dashboards before the telemetry contract is verified.
* Do not add a broad compatibility layer for non-onlava naming or config.

## Progress

* [x] 2026-05-26: Create this ExecPlan as `docs/plans/0036-grafana-dev-hardening.md` and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Split Grafana upstream URL and public browser URL throughout config, provisioning, state, and dashboard links.
* [x] 2026-05-26: Make local proxy Grafana public URL available before Grafana provisioning is written.
* [x] 2026-05-26: Change external Grafana reuse to verified-only, with default `auto` preferring a managed Grafana on a free port when possible.
* [x] 2026-05-26: Prefer the managed pinned Grafana binary over `PATH` Grafana and add version probing for fallback binaries.
* [x] 2026-05-26: Filter inherited `GF_*` environment variables from the Grafana child process by default.
* [x] 2026-05-26: Add Grafana download checksum verification.
* [x] 2026-05-26: Add `prune: true`, `orgId`, and datasource versions to datasource provisioning.
* [x] 2026-05-26: Disable UI/dashboard links unless Grafana is verified usable.
* [x] 2026-05-26: Replace speculative dashboard queries by implementing the `onlava_request_duration_seconds` metric contract.
* [x] 2026-05-26: Add fake-process, external-verification, provisioning, local-proxy root URL, and optional live smoke tests.
* [x] 2026-05-26: Run repository validation and record outcomes.

## Surprises & Discoveries

Starting review discoveries to verify:

* `startGrafanaForDev` checks for an external Grafana on the port before writing provisioning. If a user already has Grafana on the default port, onlava can expose dashboard links even though that external instance probably does not have onlava datasources or dashboards.
* `grafana.ini` currently renders `root_url` from the direct loopback URL. The dashboard/local proxy state may later rewrite links to `https://grafana.<workspace>.localhost`, leaving Grafana itself configured for `http://127.0.0.1:<port>/`.
* The starter dashboards appear to query metric and label names such as `onlava_request_duration`, `onlava_service`, `onlava_endpoint`, `onlava_is_error`, and `onlava_log_service`. The review only found those names in Grafana provisioning, not in telemetry producers.
* Existing trace query code already understands Jaeger/VictoriaTraces data with tags such as `onlava.service`, `onlava.endpoint`, and `onlava.is_error`. Trace-driven dashboards may therefore be more honest than metric-name-driven dashboards until a stable metric contract exists.
* `resolveGrafanaBinary` appears to check `PATH` before using or downloading a managed pinned Grafana, which can bypass the version and plugin compatibility story.
* Grafana download integrity appears weaker than Victoria sidecar download integrity because Grafana extraction does not verify checksums.
* The Grafana child process appears to inherit the parent environment. Grafana supports `GF_*` overrides, so a developer's shell environment can silently override generated config.
* Readiness appears to check `/api/health`, which proves the server is up but not that plugins, datasources, or dashboards loaded.
* Datasource provisioning can drift if traces or logs are disabled after a previous run unless provisioning enables pruning.
* The UI may expose clickable Grafana links while Grafana is degraded or unavailable.
* First-run Grafana startup can block `onlava dev` for a long time because download and plugin preinstall are synchronous.

Record new findings here with commands, test output, or file references as implementation proceeds.

* 2026-05-26: The implementation already emitted a duration metric from `cmd/onlava/victoria_export.go`, but it used the dotted OTLP name `onlava.request.duration` and dotted trace-style attributes. The dashboards queried underscore-style Prometheus names. The hardening patch makes the emitted metric contract explicit as `onlava_request_duration_seconds` with underscore labels, then updates dashboard JSON generation to query those emitted names.
* 2026-05-26: Grafana's `.tar.gz.sha256` files are available from the same `dl.grafana.com/oss/release/` archive URL with a `.sha256` suffix. A `curl -fsSI` check for `grafana-13.0.1+security-01.darwin-arm64.tar.gz.sha256` returned HTTP 200.

## Decision Log

* Decision: Treat this as hardening of the existing Grafana integration, not a replacement.
  Rationale: The architecture is already correct at a high level. The risky parts are readiness, verification, public URL consistency, reproducibility, and telemetry contract honesty.
  Date/Author: 2026-05-26 / Codex

* Decision: Verified Grafana readiness means server health plus expected onlava assets.
  Rationale: A working health endpoint can still produce dead dashboard links, missing datasource UIDs, missing dashboard UIDs, or empty dashboards caused by missing plugins.
  Date/Author: 2026-05-26 / Codex

* Decision: Default `auto` should avoid unverified external Grafana reuse.
  Rationale: A random Grafana already on the port is not equivalent to the managed onlava workbench. Reuse should be explicit or verified through Grafana HTTP APIs.
  Date/Author: 2026-05-26 / Codex

* Decision: Split upstream and public Grafana URLs.
  Rationale: The supervisor talks to a loopback upstream, while users may reach Grafana through the local proxy. Using one field for both causes `root_url`, dashboard links, and reverse-proxy behavior to diverge.
  Date/Author: 2026-05-26 / Codex

* Decision: Do not polish metric dashboards until the metric contract is proven or implemented.
  Rationale: A dashboard that uses non-emitted metric names is worse than a simpler trace-first dashboard because it presents a polished but false surface.
  Date/Author: 2026-05-26 / Codex

* Decision: Implement the metric contract instead of switching the first dashboards to trace-only.
  Rationale: onlava already exports a per-request duration metric to VictoriaMetrics. Renaming it to `onlava_request_duration_seconds` with underscore labels gives Grafana a concrete metric contract and keeps dashboard panels useful without inventing a parallel trace-only dashboard model.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed on 2026-05-26.

Shipped outcome:

* `grafanaConfig` now separates direct upstream URL from browser-facing public URL. Local proxy Grafana URLs are computed before provisioning so `grafana.ini` writes the proxy `root_url` when proxying is enabled.
* External Grafana reuse is verified-only. `/api/health` alone is treated as server health, not readiness; onlava verifies expected datasource and dashboard UIDs before reporting `external` or exposing links.
* Grafana readiness now checks server health, datasource UIDs, and dashboard UIDs. `GrafanaState` exposes `available`, `server_ready`, `datasources_ready`, and `dashboards_ready`, and the dashboard UI disables links for unavailable or degraded Grafana states.
* Managed pinned Grafana is preferred over `PATH` Grafana. `PATH` fallback probes `grafana -v` and rejects mismatched versions unless the user explicitly chooses a binary with `ONLAVA_GRAFANA_BIN`.
* Grafana downloads are checksum-verified using Grafana's `.sha256` sidecar file. Custom download URLs must provide `ONLAVA_GRAFANA_DOWNLOAD_SHA256`.
* The Grafana child process filters inherited `GF_*` environment variables by default and sets only onlava-owned path/server variables, with `ONLAVA_GRAFANA_PRESERVE_GF_ENV=1` for debugging.
* Datasource provisioning includes `prune: true`, `orgId: 1`, and `version: 1`.
* The emitted VictoriaMetrics contract is now `onlava_request_duration_seconds` with underscore labels, and the generated Grafana dashboards query those emitted names.
* Fixture apps now include intentionally empty `.env` files so repository integration tests satisfy the local startup contract.

Validation:

```sh
go test ./cmd/onlava
go test ./...
bun run typecheck   # ui/
bun run build       # ui/
bun run typecheck   # dbstudio/
bun run build       # dbstudio/
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

All validation commands above passed. The UI build still emits existing Lightning CSS warnings for Tailwind at-rules, but exits successfully and the self harness reports fresh UI artifacts.

## Context and Orientation

Relevant existing files to inspect first:

```text
cmd/onlava/grafana.go
cmd/onlava/grafana_provisioning.go
cmd/onlava/grafana_test.go
cmd/onlava/dev_supervisor.go
cmd/onlava/dashboard.go
cmd/onlava/dashboard_state.go
cmd/onlava/dashboard_rpc.go
cmd/onlava/localproxy.go
internal/localproxy/*
internal/devtools/versions.json
internal/grafanaassets/*
docs/grafana.md
docs/local-contract.md
docs/plans/0033-grafana-dev-integration.md
```

Key terms:

* Upstream URL: the direct loopback URL used by the supervisor and local proxy to reach Grafana, for example `http://127.0.0.1:10429`.
* Public URL: the browser-facing URL users should open, for example `https://grafana.onlv.localhost`.
* Verified external Grafana: an already-running Grafana instance whose HTTP APIs confirm the expected onlava datasource UIDs and dashboard UIDs exist.
* Managed Grafana: the pinned Grafana binary installed under `.onlava/grafana` according to `internal/devtools/versions.json`.
* Usable Grafana: Grafana is reachable and the expected plugins, datasource UIDs, and dashboard UIDs are present. Only this state should expose normal "open" links.

Expected datasource/dashboard identifiers to verify should be read from the current code, but the review called out these likely UIDs:

```text
onlava-victoriametrics
onlava-victorialogs
onlava-victoriatraces-jaeger
onlava-dev-overview
```

Relevant external references:

* Grafana provisioning docs: `https://grafana.com/docs/grafana/latest/administration/provisioning/`
* Grafana configuration docs: `https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/`

## Milestones

### Milestone 1: URL model and local proxy consistency

Split `grafanaConfig.URL` into upstream and public concepts. Add a `PublicURL` or equivalent field and use it when rendering `root_url`, dashboard state, JSON events, and UI links. If no proxy URL is available, fall back to the upstream URL.

Make the local proxy's Grafana URL available before Grafana provisioning is written. With `ONLAVA_LOCAL_PROXY=1` and workspace `onlv`, generated `grafana.ini` should use `https://grafana.onlv.localhost/` as the public root URL when that is the advertised route.

### Milestone 2: Verified readiness and external reuse

Change external Grafana handling so `external` does not mean "something answered `/api/health`". In default `auto`, prefer choosing another free port and starting managed Grafana when the default port is occupied by an unverified instance. If the port was explicitly configured or explicit external reuse is enabled, return a degraded state unless the instance verifies expected assets.

Add an `inspectExternalGrafana` or similarly named helper that verifies datasource UIDs and dashboard UIDs through Grafana's HTTP API. Keep precise messages for missing datasources, missing dashboards, missing plugins if discoverable, and auth failures.

### Milestone 3: Reproducible managed Grafana

Change binary resolution order to:

```text
ONLAVA_GRAFANA_BIN
.onlava/grafana/home/grafana-<pinned-version>/bin/grafana
download pinned version
PATH grafana fallback only when allowed
```

For `PATH` fallback, probe `grafana -v` or the equivalent version command and warn or degrade when the version is not the pinned version. Require enough homepath/config context that a system Grafana cannot accidentally use a user's normal Grafana state.

Add checksum verification for Grafana downloads. Prefer pinned checksums in `internal/devtools/versions.json` when practical. If using upstream checksum files, verify the checksum file retrieval and matching archive checksum before extraction. Keep the security posture aligned with Victoria downloads.

### Milestone 4: Isolated Grafana process environment

Add a helper like `grafanaChildEnv(base []string, cfg grafanaConfig) []string`. By default, remove inherited environment variables whose keys start with `GF_`, then set only values onlava owns:

```text
GF_SERVER_HTTP_ADDR
GF_SERVER_HTTP_PORT
GF_SERVER_ROOT_URL, if using env override instead of only grafana.ini
GF_PATHS_DATA
GF_PATHS_LOGS
GF_PATHS_PLUGINS
GF_PATHS_PROVISIONING
```

Add `ONLAVA_GRAFANA_PRESERVE_GF_ENV=1` only for debugging. Tests should prove ambient `GF_SERVER_ROOT_URL`, `GF_PATHS_DATA`, and similar values do not override generated defaults unless this override is set.

### Milestone 5: Provisioning drift and UI availability

Update datasource provisioning to include:

```yaml
apiVersion: 1
prune: true
datasources:
  - orgId: 1
    version: 1
```

Add `deleteDatasources` entries only if UIDs are renamed or old names are known. Update provisioning snapshots so changes are intentional.

Add explicit availability to `GrafanaState`, or make status semantics strict enough that the UI can disable links whenever Grafana is not usable. The UI should not offer "Open Grafana" or dashboard links for `degraded`, `unavailable`, or unverified external states. It should show actionable reset or doctor hints instead.

### Milestone 6: Dashboard data contract

Choose one of these approaches before polishing dashboards:

* Option A: Implement and document an emitted metric contract, preferably with Prometheus duration naming such as `onlava_request_duration_seconds` and labels for app, service, endpoint, method, status, and error state.
* Option B: Make dashboards trace-first using data already available through VictoriaTraces and Jaeger-compatible tags such as `onlava.service`, `onlava.endpoint`, and `onlava.is_error`.
* Option C: Generate app-specific dashboards from known services/endpoints so variables exist even before traffic exists.

Record the chosen option in the Decision Log. If Option A is chosen, add runtime tests that prove the metrics are actually emitted. If Option B is chosen, add trace-query or dashboard snapshot tests that use existing trace fields. If Option C is chosen, add deterministic generation tests for service and endpoint variables.

### Milestone 7: Tests and optional live smoke

Add focused tests before broad refactors:

* Fake Grafana process test: use a tiny fake executable or test helper that starts an HTTP server exposing `/api/health`; assert `startGrafanaForDev` starts it, reports ready only after verification, and `WaitOrKill` stops it.
* External Grafana verification test: one fake server returns only `/api/health` and must not get ready dashboard links; another returns expected datasource/dashboard UIDs and may be marked verified external.
* Local proxy root URL test: with `ONLAVA_LOCAL_PROXY=1` and workspace `onlv`, assert generated `grafana.ini` uses the proxied public URL.
* Provisioning snapshot tests for metrics only, metrics plus logs, metrics plus logs plus traces, no Victoria, proxied public URL, and explicit Grafana port.
* Optional live smoke behind `ONLAVA_TEST_GRAFANA=1`, for example `go test ./cmd/onlava -run TestGrafanaLiveSmoke`, that starts real Grafana, waits for health, verifies expected UIDs through Grafana APIs, and shuts down.

## Plan of Work

Begin by reading the current Grafana code and tests, especially `grafanaConfig`, local proxy route construction, and dashboard state serialization. Confirm whether the static-review assumptions still match the working tree, then update Surprises & Discoveries with any differences.

Implement URL splitting first because it affects provisioning, state, JSON output, and UI links. This should make later readiness work less confusing: the upstream URL is for process supervision and proxy routing, while the public URL is for Grafana config and user-facing links.

Next implement verified external detection and readiness checks. Keep the verification helper pure enough to test with `httptest.Server`. The state should distinguish server health from asset readiness, either with subfields such as `server_ready`, `datasources_ready`, and `dashboards_ready`, or with detailed degraded messages.

Then harden reproducibility by changing binary resolution, adding checksum verification, and filtering inherited `GF_*` environment variables. These changes should be small and independently testable.

After provisioning drift and UI availability are fixed, address dashboard data. Avoid spending time on visual polish until either the metric producer exists or the dashboards are trace-first.

Finish with validation and docs. Update `docs/grafana.md`, `docs/local-contract.md`, and any JSON/event schema documentation touched by state changes. Rebuild the CLI with `go install ./cmd/onlava` before finishing any repository change.

## Concrete Steps

1. Inspect current Grafana implementation and tests:

   ```sh
   rg -n "Grafana|grafana|ONLAVA_GRAFANA|GF_" cmd/onlava internal docs
   go test ./cmd/onlava -run Grafana
   ```

2. Update `grafanaConfig` to carry separate upstream and public URLs. Keep field names explicit, for example `URL` and `PublicURL`, or `UpstreamURL` and `PublicURL`. Update call sites and tests.
3. Compute the local proxy Grafana public URL before rendering Grafana provisioning. Update `renderGrafanaINI` so `root_url` uses `PublicURL` when set and trims trailing slashes before adding one canonical slash.
4. Add tests for direct URL and proxied public URL rendering.
5. Implement external Grafana verification through Grafana HTTP APIs. Verify expected datasource UIDs and dashboard UIDs. Treat auth or missing assets as degraded, not ready.
6. Change default `auto` behavior so unverified external Grafana is not reused unless explicit reuse is requested. Add an environment flag such as `ONLAVA_GRAFANA_REUSE_EXTERNAL=1` only if needed for users who intentionally manage Grafana themselves.
7. Update `GrafanaState` and UI code so links appear only for usable states. Add a boolean such as `available` if that is clearer than relying on status strings.
8. Change `resolveGrafanaBinary` order to prefer the explicit binary, then managed pinned version, then download, then `PATH` fallback. Add version probing for explicit and `PATH` binaries where feasible.
9. Add checksum verification for Grafana downloads. Store checksums in `internal/devtools/versions.json` or implement a clear upstream checksum-file verification flow.
10. Add `grafanaChildEnv` and set `cmd.Env` for the Grafana process. Filter inherited `GF_*` variables by default. Add `ONLAVA_GRAFANA_PRESERVE_GF_ENV=1` tests.
11. Update datasource provisioning with `prune: true`, `orgId: 1`, and `version: 1`. Add or update provisioning snapshot tests.
12. Decide the dashboard data strategy. If emitted metrics are added, use Prometheus-style `_seconds` duration naming and document labels. If trace-first dashboards are chosen, replace panels that depend on non-emitted metric names.
13. Add fake Grafana process, external verification, root URL, provisioning snapshot, and optional live smoke tests.
14. Update docs and schemas for new state fields, environment variables, readiness semantics, and reset/doctor guidance.
15. Run formatting and validation commands from the repository root.

## Validation and Acceptance

Minimum validation from `/Users/petrbrazdil/.codex/worktrees/fe3e/onlava`:

```sh
gofmt -w <changed-go-files>
go test ./cmd/onlava
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Targeted validation:

```sh
go test ./cmd/onlava -run 'Grafana|LocalProxy'
ONLAVA_TEST_GRAFANA=1 go test ./cmd/onlava -run TestGrafanaLiveSmoke
```

Manual smoke, when practical:

```sh
ONLAVA_LOCAL_PROXY=1 ONLAVA_DEV_GRAFANA=auto onlava dev
```

Acceptance criteria:

* Generated `grafana.ini` uses the public proxy URL when local proxy is enabled and falls back to the direct upstream URL otherwise.
* Unverified external Grafana does not get ready dashboard links in default mode.
* Verified external Grafana is only reported after expected datasource and dashboard UIDs are present.
* Managed pinned Grafana is preferred over `PATH` Grafana unless explicitly overridden.
* Ambient `GF_*` variables do not override generated config by default.
* Grafana downloads are checksum-verified before extraction.
* Datasource provisioning prunes stale provisioned datasources.
* UI and JSON state represent degraded/unavailable Grafana honestly.
* Dashboard queries are backed by emitted metrics or by trace/log data that exists today.

## Idempotence and Recovery

The implementation should remain safe to rerun. Generated Grafana state lives under `.onlava/grafana` and can be reset by deleting that directory. Tests should use temporary directories and fake HTTP servers rather than modifying a developer's real Grafana state.

If a managed Grafana download fails after creating a partial directory, remove or quarantine the partial directory before returning the error so the next run can retry cleanly.

If checksum verification fails, do not extract the archive and do not fall back silently to an unverified archive. Return an actionable error that includes the version, platform, and checksum source.

If external Grafana verification fails, keep the app and other dev services running in `auto` mode when possible, but mark Grafana degraded and suppress links. In required mode, fail with a message that distinguishes server health failure, datasource failure, dashboard failure, and auth failure.

If UI state fields change, preserve compatibility where practical by keeping old fields populated until callers can migrate. Add new fields for precision rather than overloading an existing field with a new meaning.

## Artifacts and Notes

The static review that produced this plan prioritized these fixes:

1. Do not reuse external Grafana unless onlava assets are verified.
2. Fix the local proxy `root_url` mismatch.
3. Make dashboards query telemetry definitely emitted by onlava, or make first dashboards trace-driven.
4. Prefer managed pinned Grafana over `PATH` Grafana.
5. Verify Grafana download checksums.
6. Filter inherited `GF_*` variables.
7. Verify datasources and dashboards after `/api/health`.
8. Add `prune: true` to datasource provisioning.
9. Disable UI links in degraded/unavailable states.
10. Consider background startup for `auto` mode if blocking startup remains too slow.

Potential future UX commands after this hardening:

```text
onlava grafana status
onlava grafana open
onlava grafana doctor
trace -> Open in Grafana link
logs -> Open in Grafana Explore link
```

Companion app note for `/Users/petrbrazdil/Repos/onlv`: if local proxy Grafana is part of the expected development workflow, consider making the proxy host explicit in `.onlava.json`:

```json
{
  "proxy": {
    "workspace": "onlv",
    "api_host": "api.onlv.localhost",
    "console_host": "console.onlv.localhost",
    "mcp_host": "mcp.onlv.localhost",
    "grafana_host": "grafana.onlv.localhost"
  }
}
```

Suggested ONLV Just targets, if they fit that repository's current workflow:

```make
@dev-onlava:
  ONLAVA_LOCAL_PROXY=1 ONLAVA_DEV_GRAFANA=auto {{onlava}} dev --app-root={{app_root}}

@grafana-required:
  ONLAVA_LOCAL_PROXY=1 ONLAVA_DEV_GRAFANA=1 {{onlava}} dev --app-root={{app_root}}

@grafana-reset:
  rm -rf {{app_root}}/.onlava/grafana
```

Before adding those ONLV changes, verify that its `onlava` Just variable points at the checkout containing the Grafana integration.

## Interfaces and Dependencies

Public and semi-public environment variables likely touched:

```text
ONLAVA_DEV_GRAFANA=auto|1|0
ONLAVA_DEV_GRAFANA_DOWNLOAD=1|0
ONLAVA_GRAFANA_BIN=/path/to/grafana
ONLAVA_GRAFANA_VERSION=<version>
ONLAVA_GRAFANA_PORT=<port>
ONLAVA_GRAFANA_DIR=<path>
ONLAVA_GRAFANA_REUSE_EXTERNAL=1
ONLAVA_GRAFANA_PRESERVE_GF_ENV=1
ONLAVA_LOCAL_PROXY=1|0
```

Grafana APIs to use for verification should come from the current Grafana documentation and be wrapped behind small helpers so tests can use fake servers. Expected checks include:

* `/api/health` for server health only.
* Datasource lookup by UID for `onlava-victoriametrics`, `onlava-victorialogs`, and optional traces datasource.
* Dashboard lookup by UID for provisioned onlava dashboards.

Implementation should keep dependencies minimal. Prefer the Go standard library for HTTP verification, checksum calculation, archive verification, environment filtering, and tests. Do not add a Grafana client dependency unless the API surface becomes broad enough to justify the maintenance cost.
