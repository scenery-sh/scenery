# ONLV Layout Migration into scenery

Status: canceled 2026-05-26. This plan is retained as historical context only
and is no longer linked from the active plan index.

This ExecPlan is a living document only while active; it is now archived. Its Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective sections are retained as a historical snapshot.

## Purpose / Big Picture

scenery should own the reusable layout primitives currently living in ONLV app so future agents compose product UI from scenery-owned layouts instead of copying ONLV-specific components or writing page-local className-heavy markup.

The migration is not a redesign. It is a source-of-truth migration:

```text
ONLV app generic layout patterns
        |
        v
scenery ui/src/components/layouts
        |
        v
scenery @scenery/* registry items
        |
        v
ONLV imports updated to consume scenery-facing layout surfaces
        |
        v
visual output intentionally unchanged
```

The most concrete starting point is ONLV's `apps/app/src/components/app/product-layout.tsx`, which defines reusable product chrome primitives such as `LegacyAppSidebar`, `LegacyAppMain`, `LegacyAppHeader`, `LegacyAppToolbar`, `LegacyPanel`, and `LegacyMetaBox`. These map naturally to scenery-owned `AppSidebar`, `AppMain`, `AppHeader`, `AppToolbar`, `AppPanel`, and `AppMetaBox`.

scenery already has `ui/src/components/layouts/AppSurface.tsx` with matching scenery-named components and stable `data-scenery-ui` markers. Treat that as the current baseline, not as final proof that the migration is complete.

The goal is to make ONLV use scenery layout contracts while preserving ONLV's current pixels and behavior. ONLV's own agent notes explicitly say Linear-inspired product chrome should use semantic app tokens, keep dense quiet product chrome, and prefer `src/components/app/product-layout.tsx` for repeated product sidebars, main surfaces, headers, toolbars, panels, and metadata boxes.

Suggested first execution target: only reconcile and test `AppSurface` plus ONLV import adapters first. Do not start migrating page-level Tasks, Contacts, Jobs, Drive, Console, or Viewer shells until the basic app-surface family is proven with visual harness.

## Progress

* [x] Create this ExecPlan as `docs/plans/0026-onlv-layout-migration.md`.
* [x] Link this ExecPlan from `docs/plans/active.md`.
* [x] Inventory ONLV layout candidates.
* [x] Compare ONLV layout candidates against existing scenery layouts.
* [ ] Port missing generic layouts into scenery.
* [x] Confirm the existing `@scenery/app-surface` registry item covers the first migrated layout family.
* [x] Add layout render tests for `AppSurface`.
* [x] Update ONLV import adapters to use scenery-facing layout surfaces.
* [x] Update ONLV app-surface consumers to use scenery-facing `App*` names.
* [x] Run scenery validation.
* [x] Run ONLV validation and visual harness, with current failures recorded below.
* [ ] Resolve or approve ONLV visual harness diffs before completing this plan.
* [ ] Record outcomes and move this plan to completed.

## Surprises & Discoveries

Record discoveries here as work proceeds.

Known starting discoveries:

* ONLV app has explicit UI layering and visual harness expectations under `apps/app/AGENTS.md`. Any migration touching ONLV app UI should run the frozen visual harness and preserve pixels unless a visual change is deliberately approved.
* scenery already has a controlled UI contract: agents compose from `ui/src/components/primitives` and `ui/src/components/layouts`, not raw shadcn or vendor imports. The migration must preserve that contract.
* scenery already has `AppSurface.tsx`; the first task is audit/reconciliation, not blindly copying duplicate components.
* ONLV already had `apps/app/src/components/layouts/product-layout.ts` as an adapter path, but it re-exported the app-local implementation. The first implementation slice can preserve existing screen imports while making `AppSurface` the adapter source.
* The initial inventory found one additional app-surface user outside the originally named surfaces: `apps/app/src/pages/InvoicesPage/index.tsx`.
* scenery `AppSurface.tsx` covers the compiled dashboard baseline, while `ui/src/components/registry/layouts/app-surface.tsx` carries the downstream ONLV registry output, including `AppTwoPane`, `AppFilterControl`, and `AppFilterSelectTrigger`.
* `bun run shadcn:add @scenery/app-surface --dry-run` reports the ONLV-facing registry output and its `@scenery/select` dependency.
* ONLV app `bun run typecheck` was blocked by a duplicated `three` / `@types/three` version mismatch in `apps/viewer`. Aligning viewer to the same `0.184.0` versions already used by the blog app fixed ONLV app typecheck.
* ONLV app `bun run ui-harness` initially failed all 24 screenshots because the global workspace switcher was hidden in dev/E2E auth-bypass mode. The switcher now renders for E2E auth bypass, restoring the expected workspace chrome.
* ONLV app `bun run ui-harness` still fails all 24 screenshots. The run reaches the harness pages, but the frozen baselines differ from the current app. The visible UI catalog labels were restored to the previous `ONLV app*` names while the implementation imports use `App*`; remaining known contributors include sidebar drift around the newer `Data` development route and small app-page focus-ring differences such as close button versus edit button focus. Baselines were not updated because approval is required. Diff report: `/Users/petrbrazdil/Repos/onlv/apps/app/test-results/ui-harness/diff-report.md`.
* Browser verification of the current local tab was attempted, but the Browser tool blocked reloading the `127.0.0.1:5174` page under its URL policy. No workaround was attempted.
* ONLV app-surface consumers now render through `AppSidebar`, `AppMain`, `AppHeader`, `AppToolbar`, `AppPanel`, and `AppMetaBox`. app-prefixed exports remain only in compatibility shims, while `apps/app/src/pages/UiPage.tsx` intentionally keeps visible `ONLV app*` catalog labels for visual continuity.
* Running `bun run lint` in `apps/app` invokes `biome check --write` and reformatted many existing ONLV app files outside this migration slice. Treat those formatting changes separately when reviewing.

## Decision Log

* Decision: Use scenery-owned names, not app-prefixed names, for shared layouts.
  Rationale: The public scenery UI surface should not be ONLV app-branded. app-specific naming remains in ONLV only where the component is truly app-specific.
  Date/Author: 2026-05-09 / Codex

* Decision: Preserve ONLV visual output during migration.
  Rationale: The purpose is ownership and guardrails, not redesign. Any visual changes should be separately approved through ONLV's visual harness.
  Date/Author: 2026-05-09 / Codex

* Decision: Generic layouts move to scenery; app-specific feature components stay in ONLV.
  Rationale: scenery should own reusable shell/layout primitives, while ONLV keeps business-specific behavior and data wiring.
  Date/Author: 2026-05-09 / Codex

* Decision: Start with `AppSurface` reconciliation and ONLV import adapters only.
  Rationale: This proves the core primitive family and visual safety before broader page shell migration.
  Date/Author: 2026-05-09 / Codex

* Decision: Keep legacy `ONLV app*` aliases in the ONLV adapter during the first slice.
  Rationale: It changes the source of truth without forcing a broad page import/name migration before visual validation.
  Date/Author: 2026-05-09 / Codex

* Decision: Render `WorkspaceSwitcher` during the explicit E2E auth-bypass mode.
  Rationale: The visual harness uses E2E auth bypass but the frozen baselines include the workspace switcher. Rendering it there keeps screenshot chrome deterministic without changing normal unauthenticated behavior.
  Date/Author: 2026-05-09 / Codex

* Decision: Keep app-prefixed compatibility exports but remove app-prefixed usage from page consumers.
  Rationale: Screens should compose scenery-facing layout names now, while legacy import paths remain reversible until the visual harness and broader migration are complete.
  Date/Author: 2026-05-09 / Codex

* Decision: Keep ONLV UI catalog display labels as `ONLV app*` during the source migration.
  Rationale: The plan is a source-of-truth migration, not a visual/content rename. The code can import scenery-facing `App*` symbols while the app continues to display the existing app-specific labels until a separate naming/content change is approved.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Canceled on 2026-05-26 because the migration is no longer relevant. No further
implementation work should be driven from this ExecPlan.

## Context and Orientation

Relevant ONLV files to inspect first:

```text
apps/app/AGENTS.md
apps/app/src/components/app/product-layout.tsx
apps/app/src/pages/UiPage.tsx
apps/app/src/pages/TasksPage/index.tsx
apps/app/src/pages/ContactsPage/ContactsSidebar.tsx
apps/app/src/pages/JobsPage/JobsPage.tsx
apps/app/src/pages/drive/DrivePageView.tsx
apps/app/src/pages/ConsolePage.tsx
apps/app/src/pages/ViewerPage.tsx
```

Known ONLV layout source:

```text
apps/app/src/components/app/product-layout.tsx
```

Known scenery layout baseline:

```text
ui/src/components/layouts/AppSurface.tsx
ui/src/components/layouts/DashboardPage.tsx
ui/src/components/layouts/DataExplorerLayout.tsx
ui/src/components/layouts/PageToolbar.tsx
ui/src/components/layouts/AppShell.tsx
```

Known scenery registry/guardrail files:

```text
ui/components.json
ui/scripts/scenery-shadcn.mjs
ui/registry/scenery/registry.json
ui/registry/scenery/app-surface.json
ui/registry/scenery/page-toolbar.json
docs/ui-agent-contract.md
cmd/scenery/harness_ui.go
```

Architecture constraints:

* Do not introduce raw shadcn usage in app/dashboard screens.
* Do not import from `ui/src/components/vendor/shadcn` from screens.
* Do not create ONLV-branded public surfaces in scenery unless the component is explicitly kept internal.
* Prefer typed named slots over compound component APIs for agent-facing layouts.
* Add stable `data-scenery-ui` and `data-slot` markers to migrated layouts.
* Do not move app-specific ONLV data/state logic into scenery.

## Scope

Migrate generic layout primitives from ONLV app into scenery.

Candidate categories:

```text
generic layout:
  product sidebars
  product main surfaces
  product headers
  product toolbars
  panels
  metadata boxes
  page toolbars
  split panes
  record/detail layouts
  table/list shells
  empty/loading/error layout wrappers

app-specific, stays in ONLV:
  task-specific columns
  contact-specific filters
  job-specific cards
  viewer/canvas-specific HUDs
  business-specific copy or data loaders
  sync/TanStack DB data wiring
```

Non-goals:

```text
visual redesign
CRM rewrite
moving ONLV product logic into scenery
migrating ONLV data fetching or sync logic
replacing all ONLV UI components
adding new shadcn primitives unless needed by a migrated layout
```

## Milestones

### Milestone 1: Inventory ONLV layouts

Build an inventory of ONLV layout components and repeated layout patterns.

Search ONLV for:

```text
@/components/app
LegacyApp
LegacyPanel
LegacyMetaBox
AppSidebar
AppMain
AppHeader
AppToolbar
className-heavy repeated page shells
```

Create a small inventory table in this plan:

```text
ONLV source file
component/pattern
generic or app-specific
target scenery layout
ONLV migration strategy
visual-risk level
```

Acceptance:

* Inventory includes all direct uses of `LegacyApp*`, `LegacyPanel`, and `LegacyMetaBox`.
* Inventory identifies repeated page-level shells in Tasks, Contacts, Jobs, Drive, Console, and Viewer surfaces.
* Each candidate is classified as generic or app-specific.

### Milestone 2: Reconcile existing scenery layout baseline

Compare ONLV `product-layout.tsx` with scenery `AppSurface.tsx`.

Check:

```text
class names
semantic tokens
dimensions
DOM element types
accessibility expectations
data-scenery-ui markers
registry item coverage
tests
```

Acceptance:

* Existing scenery `AppSurface.tsx` is confirmed equivalent or updated.
* Any registry-only app-surface source is recorded as deliberate source-generator input, not a second app-facing layout API.
* Any semantic differences are recorded in Decision Log.

### Milestone 3: Port missing generic layouts

Add missing generic layouts under:

```text
ui/src/components/layouts/
```

Likely additions:

```text
AppPageShell.tsx
AppSplitLayout.tsx
AppListDetailLayout.tsx
AppRecordLayout.tsx
TableShell.tsx
EmptyStateLayout.tsx
```

Do not add all of these blindly. Add only the layouts justified by the ONLV inventory.

Every migrated layout must:

```text
use scenery-owned component names
use typed props
use named slots
include data-scenery-ui markers
include data-slot markers for important regions
avoid app-specific names/copy
avoid business logic
```

Example target shape:

```tsx
<AppListDetailLayout
  sidebar={<ContactsSidebar />}
  header={<ContactsHeader />}
  toolbar={<ContactsToolbar />}
  list={<ContactsList />}
  detail={<ContactDetail />}
/>
```

Acceptance:

* New layouts are generic and scenery-named.
* Existing ONLV visuals can be represented without page-local layout CSS.
* No raw shadcn imports are introduced.

### Milestone 4: Registry items

Add or update registry files under:

```text
ui/registry/scenery/
```

For each migrated layout, add a registry item:

```text
app-surface
product-page-shell
product-list-detail-layout
table-shell
empty-state-layout
```

Update:

```text
ui/registry/scenery/registry.json
```

Registry requirements:

* `source` points at an existing file under `ui/src`.
* `target` uses approved aliases such as `@components/layouts/...`.
* No registry item writes config, lockfiles, scripts, or package files.
* Dependencies use only `@scenery/*` registry dependencies.

Acceptance:

* `scenery harness self --json` UI static checks pass.
* `bun run shadcn:add @scenery/<new-layout> --dry-run` works for each new item.

### Milestone 5: Tests

Add render tests for migrated layouts.

Test location:

```text
ui/src/components/layouts/*.test.tsx
```

Test:

```text
renders root data-scenery-ui marker
renders all required slots
omits optional slots cleanly
does not create empty side columns for absent optional slots
preserves semantic element types where relevant
```

Acceptance:

* Each new migrated layout has at least one render test.
* Existing `AppShell`, `DashboardPage`, `DataExplorerLayout`, and `AppSurface` tests remain green or are expanded.

### Milestone 6: Update ONLV imports

In ONLV, update usage from app-specific generic layouts to scenery-facing layout surfaces.

Preferred import direction depends on ONLV's current package wiring. The migration should end with ONLV screens importing scenery-facing layout names, not local ONLV app layout primitives.

Possible ONLV adapter strategy:

```text
apps/app/src/components/layouts/*
  re-export scenery-owned layouts or mirror installed @scenery registry outputs
```

Do not immediately delete ONLV local components if that would increase visual risk. First add adapter/re-export paths, update screen imports, and only then remove unused local components.

Acceptance:

* ONLV screens no longer import generic layout primitives from `@/components/app/product-layout`.
* App-specific components remain in ONLV.
* ONLV visual output is unchanged.

### Milestone 7: Visual and harness validation

Run scenery validation:

```sh
go test ./...
go install ./cmd/scenery
cd ui && bun run typecheck
cd ui && bun run test
cd ui && bun run build
scenery harness self --json --write
```

Run ONLV validation:

```sh
cd /path/to/onlv
scenery check --json
go test ./...
```

Run ONLV visual harness as required by `apps/app/AGENTS.md`:

```sh
cd /path/to/onlv/apps/app
bun run ui-harness
```

If visual changes are expected:

```sh
bun run ui-harness:update -- --approved
```

Only update baselines after approval.

Acceptance:

* scenery validation passes.
* ONLV check/tests pass.
* ONLV UI visual harness passes, or visual diffs are explicitly reviewed and approved.
* No new guardrail violations are introduced.

## Plan of Work

Start by inventorying ONLV, not by copying files. ONLV's `product-layout.tsx` is already small and may already be mirrored by scenery's `AppSurface.tsx`; the valuable work is finding every repeated layout pattern that still lives only in ONLV or still encourages agents to write page-local layout CSS.

Use this classification:

```text
Generic:
  could be used by scenery dashboard, data explorer, CRM prototype, or another app

App-specific:
  depends on ONLV entities, sync state, copy, or product workflows

Borderline:
  port the structural shell to scenery; keep feature content in ONLV
```

When porting, rename app-prefixed symbols to scenery names:

```text
LegacyAppSidebar  -> AppSidebar
LegacyAppMain     -> AppMain
LegacyAppHeader   -> AppHeader
LegacyAppToolbar  -> AppToolbar
LegacyPanel           -> AppPanel
LegacyMetaBox         -> AppMetaBox
```

For larger page patterns, prefer typed slot layouts:

```tsx
export type AppListDetailLayoutProps = {
  sidebar: React.ReactNode
  header?: React.ReactNode
  toolbar?: React.ReactNode
  list: React.ReactNode
  detail?: React.ReactNode
  className?: string
}
```

Avoid free-form compound APIs for agent-facing layouts.

Keep visual tokens semantic. ONLV currently expects app tokens such as:

```text
--app-app-chrome
--app-sidebar-*
--app-work-surface
--app-panel-surface
--app-toolbar-surface
--app-field-surface
--app-separator-subtle
```

For scenery, decide whether to preserve those token names temporarily or add neutral aliases. Do not silently change colors/spacing. Record the token decision in the Decision Log.

## Concrete Steps

1. Create `docs/plans/0026-onlv-layout-migration.md`.

2. Link it from `docs/plans/active.md`.

3. In ONLV, search for generic layout imports and repeated page shells:

   ```sh
   rg "@/components/app|LegacyApp|LegacyPanel|LegacyMetaBox" apps/app/src
   rg "grid-cols|w-\[230px\]|min-h-14|app-work-surface|app-panel-surface" apps/app/src
   ```

4. Fill the inventory table in this plan.

5. Compare ONLV `apps/app/src/components/app/product-layout.tsx` with scenery `ui/src/components/layouts/AppSurface.tsx`.

6. Update `AppSurface.tsx` only if needed.

7. Add missing scenery layouts under `ui/src/components/layouts`.

8. Export them from `ui/src/components/layouts/index.ts`.

9. Add or update registry items under `ui/registry/scenery`.

10. Add render tests under `ui/src/components/layouts`.

11. Run scenery validation.

12. Update ONLV imports through an adapter/re-export path first.

13. Run ONLV check/tests.

14. Run ONLV visual harness.

15. Remove unused ONLV generic layout definitions only after imports and visual validation pass.

16. Update this plan's Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective.

## Inventory Table

Fill this during Milestone 1.

| ONLV source                                                | Component/pattern                                    | Generic? | scenery target                          | Migration strategy                           | Visual risk |
| ---------------------------------------------------------- | ---------------------------------------------------- | -------: | -------------------------------------- | -------------------------------------------- | ----------- |
| `apps/app/src/components/app/product-layout.tsx`       | `LegacyAppSidebar`                               |      yes | `AppSidebar`                       | mirrored; source adapter from AppSurface | low         |
| `apps/app/src/components/app/product-layout.tsx`       | `LegacyAppMain`                                  |      yes | `AppMain`                          | mirrored; source adapter from AppSurface | low         |
| `apps/app/src/components/app/product-layout.tsx`       | `LegacyAppHeader`                                |      yes | `AppHeader`                        | mirrored; source adapter from AppSurface | low         |
| `apps/app/src/components/app/product-layout.tsx`       | `LegacyAppToolbar`                               |      yes | `AppToolbar`                       | mirrored; source adapter from AppSurface | low         |
| `apps/app/src/components/app/product-layout.tsx`       | `LegacyPanel`                                        |      yes | `AppPanel`                         | mirrored; source adapter from AppSurface | low         |
| `apps/app/src/components/app/product-layout.tsx`       | `LegacyMetaBox`                                      |      yes | `AppMetaBox`                       | mirrored; source adapter from AppSurface | low         |
| `apps/app/src/pages/TasksPage/index.tsx`                 | app sidebar/main/header/toolbar/panel shell         | borderline | `AppSurface`, future list shell    | adapter first; page shell later              | medium      |
| `apps/app/src/pages/ContactsPage/index.tsx`              | app main/header/toolbar/panel shell                 | borderline | `AppSurface`, future list shell    | adapter first; page shell later              | medium      |
| `apps/app/src/pages/ContactsPage/ContactsSidebar.tsx`    | app sidebar                                         |      yes | `AppSidebar`                       | adapter first                                | low         |
| `apps/app/src/pages/JobsPage/JobsPage.tsx`               | responsive sidebar/detail shell                     | borderline | `AppSurface`, future split layout  | adapter first; classify shell later          | high        |
| `apps/app/src/pages/drive/DrivePageView.tsx`             | app sidebar/main/header/toolbar/panel/metabox       | borderline | `AppSurface`, future record layout | adapter first; classify shell later          | medium      |
| `apps/app/src/pages/ConsolePage.tsx`                     | app sidebar/main/header/toolbar shell               | borderline | `AppSurface`, future split layout  | adapter first; classify shell later          | medium      |
| `apps/app/src/pages/ViewerPage.tsx`                      | app sidebar/main/toolbar plus viewer HUD shell      | borderline | `AppSurface`; HUD stays in ONLV    | adapter first; keep viewer-specific UI local | high        |
| `apps/app/src/pages/InvoicesPage/index.tsx`              | app main/header/toolbar/panel table shell           | borderline | `AppSurface`, future table shell   | adapter first; classify shell later          | medium      |
| `apps/app/src/pages/UiPage.tsx`                          | UI catalog app-surface examples                 |      yes | `AppSurface`                       | adapter first; update examples later         | low         |

Additional page-local grid shells were found in DataPlatform, Annotation, Debug, invoice detail/create, related invoices, and viewer subregions. They are not part of the first slice and need separate classification before migration.

## Validation and Acceptance

scenery validation:

```sh
go test ./...
go install ./cmd/scenery
cd ui && bun run typecheck
cd ui && bun run test
cd ui && bun run build
cd ui && bun run shadcn:add @scenery/app-surface --dry-run
scenery harness self --json --write
```

ONLV validation:

```sh
cd /path/to/onlv
scenery check --json
go test ./...
```

ONLV visual validation:

```sh
cd /path/to/onlv/apps/app
bun run ui-harness
```

Acceptance criteria:

```text
- scenery owns the migrated generic layouts.
- ONLV screens use scenery-facing layout surfaces for generic product layout.
- ONLV app-specific logic remains in ONLV.
- scenery UI static architecture checks pass.
- scenery layout render tests pass.
- ONLV visual harness passes or approved diffs are documented.
- No intentional visual redesign is included.
- No raw shadcn or vendor shadcn imports are introduced in screens.
- Registry items exist for migrated layouts.
```

## Idempotence and Recovery

The migration should be reversible in small pieces.

Rules:

```text
- Port one layout family at a time.
- Keep ONLV adapter/re-export paths until all imports are migrated.
- Do not delete ONLV local layout files until visual validation passes.
- Do not update ONLV visual baselines without approval.
- If visual diff appears, stop and compare class names, token values, element types, and slot nesting.
- If scenery layout is too generic and loses required behavior, keep the feature-specific part in ONLV and port only the structural shell.
```

## Artifacts and Notes

Expected scenery artifacts:

```text
ui/src/components/layouts/AppSurface.tsx
ui/src/components/layouts/AppListDetailLayout.tsx
ui/src/components/layouts/TableShell.tsx
ui/src/components/layouts/EmptyStateLayout.tsx
ui/src/components/layouts/*.test.tsx
ui/registry/scenery/app-surface.json
ui/registry/scenery/product-list-detail-layout.json
ui/registry/scenery/table-shell.json
ui/registry/scenery/empty-state-layout.json
```

Expected ONLV artifacts:

```text
apps/app/src/components/layouts/* adapter/re-export files, if needed
updated imports in Tasks/Contacts/Jobs/Drive/Console/Viewer pages
test-results/ui-harness/diff-report.md, only if diffs occur
```

## Interfaces and Dependencies

No new dependencies are expected.

Use existing scenery UI primitives and layout system:

```text
@/components/primitives
@/components/layouts
@scenery/* registry items
```

Do not add new direct shadcn usage. If a new shadcn primitive is needed, follow `docs/ui-agent-contract.md` promotion flow first.
