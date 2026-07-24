# 0144 Root Frontend Hardening: Fix Defects Found In Post-Completion Review

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as implementation proceeds.

## Purpose / Big Picture

ExecPlan 0143 shipped the root-frontend contract: top-level `.scenery.json`
`root` (or the single-frontend default) names one frontend served only at `/`
across the local base URL, dev domains, agent-proxied deploy targets, and the
published static edge. Two independent post-completion code reviews of commit
`b107d0b0` ("Make top-level root select the primary frontend route") found
defects the 0143 acceptance did not cover, because that acceptance exercised a
fresh publish and a dev-serve app, never the upgrade/rollback transition, the
localhost production-serve asset path, or exposure narrowing against the new
catch-all.

This plan consolidates every significant finding from both reviews, ranked by
severity, and drives them to fixed-and-proven or explicitly-decided-and-
documented. The highest-severity item breaks live deployed sites during the
binary upgrade window; the rest are routing correctness, surface-parity, and
migration-messaging defects.

## Progress

- [x] 2026-07-24: Milestone 1 — Publication base-path truth: registry records base path; edge render and rollback respect it
- [x] 2026-07-24: Milestone 2 — Localhost root-level asset ownership: dashboard fast path no longer shadows the root frontend
- [x] 2026-07-24: Milestone 3 — Agent routing containment: segment-boundary legacy check; agent/Caddy protected-path parity
- [x] 2026-07-24: Milestone 4 — Exposure semantics decision: unexposed prefixes 404 before the root catch-all
- [x] 2026-07-24: Milestone 5 — Migration messaging and hygiene: `deploy.root` "api" hint, label sanitizer unification, dead branch

2026-07-24: Plan drafted from two independent reviews of commit `b107d0b0`.
No implementation yet.

2026-07-24: All five implementation milestones and their focused regressions
are complete. Focused package validation passed:
`go test ./internal/app ./internal/agent ./internal/edge ./cmd/scenery`.

2026-07-24: Final validation passed: `go test ./...`,
`.scenery/harness/bin/scenery harness self --quick --summary --write`, and
`.scenery/harness/bin/scenery harness self --summary --write`. The isolated
production-serve fixture proved `/`, `/assets/app.js`, and `/favicon.ico`
come from the root frontend with the expected content types while
`/console/` and its `/console/assets/...` bundle remain reachable. No real
pre-fix publication was available; the allowed fixture path passed through
`TestCaddyConfigPreservesNamedMountForRootArtifactBuiltWithNamedBase` and
`TestCaddyStaticFrontendIntegration`.

## Surprises & Discoveries

- 2026-07-24 — (F1 evidence) The parent of `b107d0b0` built every published
  frontend — including the `Root: true` one — with Vite base `/<name>`
  (`runDeployPublishBuild(frontendRoot, "/"+name, ...)`, confirmed via
  `git show b107d0b0^:cmd/scenery/deploy_publish.go`) and rendered both the
  `/name/*` mount and the root handle. `DeployTargetFrontend`
  (`internal/agent/deploy.go`) records no base path, so the new renderer
  cannot distinguish old base-`/<name>` artifacts from new base-`/` ones.
- 2026-07-24 — (F1 evidence) In `runDeployPublish`
  (`cmd/scenery/deploy_publish.go`), the registry is written before the edge
  refresh and the document probe; probe-failure rollback restores the previous
  releases and previous registry, then re-renders the edge with the current
  binary's root-only layout.
- 2026-07-24 — (F3 evidence) Executable reproduction of
  `isProtectedFrontendRoutePath` (verbatim helper copies) for a root record
  backed by `web`: `/web/api/users` protected=true (intended), but
  `/webapi/users`, `/webruntime`, and `/web__scenery/x` also protected=true
  (spurious 404s), while `/webapp/settings` and `/projects/42` are false.
- 2026-07-24 — (F2 evidence) `cmd/scenery/local_path_router.go` routes
  `/assets/*`, `/favicon.ico`, `/site.webmanifest`, and
  `/apple-touch-icon.png` to the dashboard backend before session-route
  dispatch and agent delegation (`localPathRouterDashboardAssetPath` and its
  call in the main handler). The 0143 Surprises entry claimed the localhost
  listener needed no change; that claim missed this fast path.
- 2026-07-24 — Candidate publication routing can be activated without first
  mutating `deploy.json`: `CaddyConfigForDeployRegistry` renders an in-memory
  registry and the existing validated reload path accepts it. This makes the
  public document probe a real pre-commit gate instead of a post-commit
  rollback check.
- 2026-07-24 — The durable deploy artifact descriptor is intentionally
  unchanged because `base_path` is additive. Current-identity registries that
  lack it decode successfully, so `LoadDeployRegistry` performs a bounded
  one-time payload migration and persists the conservative historical
  `/<name>` build base.

## Decision Log

- 2026-07-24 (petr + agent) — Findings consolidated from two independent
  reviews of `b107d0b0` into one follow-up plan rather than editing the
  completed 0143 plan, which is an immutable historical record.
- 2026-07-24 (agent, confirm during Milestone 1) — The F1 fix must not add an
  open-ended "absent field means legacy behavior" compatibility branch; that
  contradicts the repo's no-legacy policy. Instead, record the artifact's base
  path as a current fact of the publication (`DeployTargetFrontend`), derive
  the Caddy mount shape from it, and order the registry commit after the
  base-`/` artifact is built and probed so rollback restores a coherent
  artifact-plus-routing pair.
- 2026-07-24 (agent) — Missing publication `base_path` is a bounded durable
  data migration, not a runtime compatibility lane. It is persisted as
  `/<name>` once because every pre-field artifact was built there; the next
  publish records `/` explicitly for the root artifact.
- 2026-07-24 (agent) — Exposure narrowing resolves the best route against the
  complete manifest first. If that route is omitted, the origin returns 404;
  an exposed root catch-all may handle only paths that no omitted sibling or
  dashboard route owns.
- 2026-07-24 (agent) — `publicRouteManifest` no longer interprets a
  non-frontend `RootService`. The removed `"api"` spelling receives an exact
  migration error; `/api/` remains automatic and `/` returns to the current
  root/default behavior.

## Outcomes & Retrospective

Completed 2026-07-24. Published frontend records now carry the artifact's
actual `base_path`, missing values are migrated once to the historically
correct `/<name>` value, and candidate edge routing is activated and probed
before the new registry becomes durable. A failed candidate therefore leaves
the previous artifact and routing pair coherent, while a successful root
republish records `/` and uses the singular root layout.

The localhost router no longer gives root-level asset paths to the dashboard:
root frontend assets remain at `/assets/*` and dashboard references are
rewritten into `/console/assets/*`. Agent and Caddy roots share protected-path
inputs, legacy backend prefixes require a segment boundary, exposure
narrowing resolves ownership before filtering, and obsolete non-frontend
root selection was removed with an exact `deploy.root: "api"` migration
message. Focused, repository-wide, full self-harness, live Caddy, and
isolated-runtime acceptance all passed.

## Context and Orientation

Terms:

- Root frontend: the frontend that owns `/`; declared by top-level `root` in
  `.scenery.json` (`Config.RootFrontend`, `internal/app/root.go`), defaulting
  to the single configured frontend.
- Route manifest: per-session routing table (`internal/agent/session.go`);
  the root frontend appears as one `root` record with `Kind: "frontend"`,
  `Path: "/"`, `Backend: <frontend>`, and no `/<name>/` named record.
- Catch-all: `routeForPath` (`internal/agent/router.go`) matches a
  frontend-kind root record for any path no other record claims.
- Deploy registry: `<agent home>/agent/deploy.json`
  (`internal/agent/deploy.go`), holding `DeployTarget.RootService` and
  published `DeployTargetFrontend` records that drive the managed Caddy
  render (`internal/edge/caddyconfig.go`).
- Localhost listener: the in-process router on the leased localhost port
  (`cmd/scenery/local_path_router.go`); it fast-paths dashboard traffic and
  delegates everything else to the agent.

The findings, ranked (line references are as of `b107d0b0`; anchor by
function name when lines drift):

1. F1 (High) — Persisted publications lack a base-path marker, so the
   root-only Caddy layout breaks existing published root frontends.
   `writePublicDomainSite` (`internal/edge/caddyconfig.go:163-177`) drops the
   `/name/*` mount for every `Root: true` publication, but pre-0143 artifacts
   were built with base `/<name>` and self-reference `/<name>/assets/...`.
   Any edge re-render after a binary upgrade (`scenery deploy resume` at
   boot, enabling another app, edge restart) serves the old index at `/`
   whose asset requests 404. A failed first publish after the upgrade is
   worse: `runDeployPublish` (`cmd/scenery/deploy_publish.go`) commits the
   registry before the probe, and rollback restores the old artifact and
   registry but re-renders the new layout — violating the documented "a
   failed build, invalid Caddyfile, or failed probe leaves the previous
   frontend public" guarantee.
2. F2 (Medium-High) — The localhost listener's dashboard-asset fast path
   (`cmd/scenery/local_path_router.go:169,402-408`) claims `/assets/*`,
   `/favicon.ico`, `/site.webmanifest`, and `/apple-touch-icon.png` before
   agent delegation. A root frontend in `serve: "production"` mode builds
   with base `/` and loads `/assets/<hash>.js` — answered by the dashboard
   backend (404), leaving the page blank on the canonical localhost URL. In
   dev serve, `public/` assets and the favicon are shadowed. Dev domains and
   the public edge route the same paths to the frontend, so localhost
   disagrees with every other surface. Note both sides genuinely want
   `/assets/*`: the dashboard's own root-absolute asset references are why
   the fast path exists, so the fix must move dashboard assets under the
   `/console/` namespace (or otherwise disambiguate), not simply delete the
   branch.
3. F3 (Medium) — `isProtectedFrontendRoutePath`
   (`internal/agent/router.go:503-517`) strips the legacy `/<backend>` prefix
   with a plain `strings.TrimPrefix`, not segment-boundary aware. Verified:
   for a root frontend backed by `web`, the SPA routes `/webapi/...`,
   `/webruntime`, and `/web__scenery/...` 404 on both the local path-mode
   listener and the public agent-proxied path.
4. F4 (Low-Medium) — Exposure narrowing leaks unexposed prefixes into the
   root catch-all. `filterExposedRouteRecords`
   (`internal/agent/router.go:262-275`) drops unlisted records and only
   runtime paths get an explicit 404; with `expose: ["root"]`, `/admin/` and
   `/console/` now render the root SPA where they previously returned 404.
   The excluded backend is never reached (no data exposure), but the
   documented "root is the lowest-precedence catch-all behind sibling and
   dashboard routes" precedence silently disappears on narrowed origins.
5. F5 (Low-Medium) — Agent-proxied and published-static roots enforce
   different protected paths. Caddy blocks `/runtime`, `/dashboard`,
   `/console`, `/__scenery` (`internal/edge/caddyconfig.go:156-162`);
   `isPublicRouteBlockedPath` (`internal/agent/router.go:193-201`) omits
   `/dashboard`, so the agent-proxied root SPA serves it. Conversely the F3
   legacy containment has no Caddy equivalent, so `/ui/__scenery/config` is
   404 via the agent but an SPA 200 via static Caddy. The same app changes
   route behavior when switching serve modes.
6. F6 (Low) — `deploy.root: "api"` migration messaging and leftover legacy
   path. The unknown-field hint (`unknownConfigFieldError`,
   `internal/app/root.go:930-935`) tells every `envs.*.deploy.root` user to
   "move the frontend name to top-level root", but `"api"` was previously a
   valid value and top-level `root` rejects it — the instruction cannot be
   followed, and the capability drop is recorded nowhere.
   `publicRouteManifest` (`internal/agent/router.go:206-211`) still honors
   non-frontend `RootService` values from stale registries, a retained legacy
   runtime path in a no-legacy codebase.
7. F7 (Low) — Label sanitizer mismatch. `pathRouteManifestForLease`
   (`cmd/scenery/dev_routing.go:35`) sanitizes the root backend with
   `sanitizeRouteLabel` (drops characters like `@`) while session backends
   are keyed with `localagentLabel` (maps them to `-`). Frontend names are
   unvalidated JSON keys, so a name like `web@2` yields a root record whose
   backend matches nothing: the catch-all 404s everything and
   `completePathRouteRecords` (`internal/agent/session.go:436-441`) fails to
   suppress the duplicate named mount.
8. F8 (Nit) — Dead branch: `deployConfigInfoDiagnostics`
   (`cmd/scenery/check.go:63-66`) — `if frontends == 1 { continue }` is
   unreachable because `cfg.RootFrontend() != ""` already continues for
   single-frontend apps.

## Milestones

Milestone 1 — Publication base-path truth (F1). Add a base-path field to
`DeployTargetFrontend` recording the base the artifact was built with;
`writePublicDomainSite` keeps the `/name/*` mount (and skips the root-only
shape) for publications whose recorded base is `/<name>`, and renders
root-only for base-`/` publications. Reorder `runDeployPublish` so the
registry state that declares a base-`/` root publication is committed only
after that artifact is built, published, and probed; rollback restores the
previous registry (whose recorded base paths again match the restored
artifacts). Update `docs/schemas/` and `docs/local-contract.md` for the
registry shape change.

Milestone 2 — Localhost asset ownership (F2). Serve dashboard assets under
the `/console/` prefix (adjusting the dashboard entry document or its asset
base) and drop the root-level dashboard-asset fast path when the session
manifest has a frontend-kind root record. Add an integration test proving
`/`, an HTML-referenced `/assets/...` request, and `/favicon.ico` reach the
root frontend while `/console/` stays functional.

Milestone 3 — Agent routing containment (F3, F5). Make the legacy-prefix
check segment-boundary aware (`stripped == "" || strings.HasPrefix(stripped,
"/")`). Align the reserved top-level path lists between
`isPublicRouteBlockedPath` and the Caddy blocked matcher, and render the
legacy containment shapes into the Caddy root site. Add a parity test that
runs one protected-path matrix against both the agent-proxied route and the
rendered static config.

Milestone 4 — Exposure semantics (F4). Decide (Decision Log) whether
narrowed origins 404 paths whose best unfiltered route is unexposed, or
whether the root catch-all swallows them; implement (resolve against the
unfiltered set before filtering, if 404 wins), document in
`docs/local-contract.md`, and test the omitted-sibling and omitted-console
cases with root exposed.

Milestone 5 — Migration messaging and hygiene (F6, F7, F8). Special-case the
`deploy.root` hint for `"api"` (state that `/` reverts to the agent
catch-all and the option is removed); decide whether `publicRouteManifest`
drops non-frontend `RootService` values; unify frontend-name labeling on one
sanitizer (or validate frontend names in `Config.validateFrontends` to the
charset where the sanitizers agree); delete the dead branch in
`deployConfigInfoDiagnostics`.

## Plan of Work

Work the milestones in order; each keeps the repo testable. Milestone 1
first because it is the only finding that breaks already-deployed sites and
its registry-shape change is the largest contract surface. Milestones 2 and
3 are independent of 1 and of each other. Milestone 4 depends on a recorded
decision. Milestone 5 is cleanup that should not gate the others.

For each finding, write the failing test first where a stable boundary
exists: the Caddy static tests (`internal/edge/caddystatic_test.go`) for F1
and F5, agent routing tests (`internal/agent/path_routing_test.go`,
`public_routing_test.go`) for F3, F4, and F5, localhost router tests under
`cmd/scenery` for F2, and config-validation tests
(`internal/app/root_test.go`) for F6 and F7.

## Concrete Steps

All commands run from the repository root.

1. Reproduce F1 before fixing: in `internal/edge/caddystatic_test.go`, add a
   fixture publication whose artifact was built with base `/<name>` but whose
   record is `Root: true`, render, and assert the current output has no
   `/name/*` handler (demonstrating the breakage), then flip the assertion as
   the fix lands.
2. Implement Milestone 1; regenerate/update `docs/schemas/` entries that
   embed the deploy registry or publish payload shapes; update
   `docs/local-contract.md` Deploy rules in the same change.
3. Implement Milestones 2–5 per their milestone descriptions, updating
   `docs/agent-guide.md` and `docs/local-contract.md` where routed behavior
   changes (F4 especially).
4. Refresh `.scenery/harness/agent-context.json` and run the exact
   `changed_area.recommended_commands` union before completion.

## Validation and Acceptance

Expected changed-area classes from the root Validation Matrix: multiple Go
packages (`internal/app`, `internal/agent`, `internal/edge`,
`cmd/scenery`), CLI JSON contract (registry/publish shape change in
Milestone 1), and release-sensitive/runtime (deploy and edge behavior).

Commands (repository root):

- `go test ./internal/app ./internal/agent ./internal/edge ./cmd/scenery`
  after each milestone, then `go test ./...` before completion.
- `.scenery/harness/bin/scenery harness self --quick --summary --write`
  after the Milestone 1 schema/doc updates.
- `.scenery/harness/bin/scenery harness self --summary --write` before
  completion (release-sensitive row; requires the Fresh Worktree Preflight
  in a fresh worktree).
- Runtime acceptance for F2: `scenery up` on a fixture app with one frontend
  in `serve: "production"`, then GET `/`, the HTML-referenced
  `/assets/<hash>.js`, and `/favicon.ico` on the localhost base URL — all
  three must be served by the frontend (non-404, correct content type), and
  `/console/` must still load the dashboard.
- Runtime acceptance for F1: on a machine with a pre-fix publication (or a
  fixture registry with a recorded base of `/<name>`), re-render the edge and
  GET `/` plus a `/name/assets/...` path — both must succeed; after a
  republish, `/` and `/assets/...` must succeed with the root-only layout.

Skip condition: the F1 runtime acceptance against a real pre-fix machine may
be skipped only when no such machine is available to the contributor, and the
fixture-registry Caddy render test in `internal/edge/caddystatic_test.go`
covering the same base-path matrix passes; record the skip and the passing
test name in Progress.

## Idempotence and Recovery

Every milestone is a normal code-plus-test change; re-running its tests is
idempotent. Milestone 1 changes the deploy registry shape: keep the change
additive (new field, no renames) so an interrupted rollout leaves old
registries readable; the registry's durable-artifact loading already rebinds
identity on schema-unchanged revisions and fails closed on schema changes —
verify which path the new field takes and record it in the Decision Log. If
a publish ordering change is interrupted mid-implementation, the existing
rollback path remains the recovery mechanism; do not remove it until the new
ordering is proven by tests.

## Artifacts and Notes

- Source reviews: two independent post-completion reviews of `b107d0b0`
  (2026-07-24, Claude session plus a second agent), merged here; the F3
  reproduction output is recorded in Surprises & Discoveries.
- ExecPlan 0143 (`docs/plans/0143-root-frontend.md`) is the immutable record
  of the original feature; this plan is referenced from
  `docs/plans/completed.md` under the Root Frontend entry.

## Interfaces and Dependencies

- `internal/agent/deploy.go` — `DeployTargetFrontend` gains a base-path
  field (Milestone 1); consumers: `cmd/scenery/deploy_publish.go`,
  `cmd/scenery/deploy.go`, `internal/edge/caddyconfig.go`
  (`StaticFrontendRoute`), `scenery deploy status` payloads.
- `internal/agent/router.go` — `isProtectedFrontendRoutePath`,
  `isPublicRouteBlockedPath`, `filterExposedRouteRecords`, `routeForPath`,
  `publicRouteManifest`.
- `cmd/scenery/local_path_router.go` — dashboard fast path and asset list;
  depends on the dashboard's asset base (Milestone 2 may touch
  `apps/console` build configuration; if so the Dashboard validation row
  applies: `cd apps/console && bun run lint && bun run typecheck && bun run
  build`, then `.scenery/harness/bin/scenery harness ui -o json --write`).
- `internal/app/root.go` — `unknownConfigFieldError`, `validateFrontends`.
- `docs/local-contract.md` Deploy rules and `docs/schemas/` for any JSON
  shape change; `docs/agent-guide.md` for routing behavior changes.
