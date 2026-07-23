# Scenery UI Components

## Purpose

`ui/components/` owns reusable component behavior within the catalog. Parent `ui/AGENTS.md` rules still control catalog ownership, dependencies, exports, materialization, and general verification.

## Local Contracts

- Use the shared `Problem`/`RequestState` vocabulary and `queryStateProps` adapter for catalog request lifecycles instead of component-local loading/error unions.
- Keep `ClientAppShell` router-agnostic: generated adapters own route selection, active state, and the router outlet, while the shell accepts resolved navigation plus fixed visual slots.
- Route provenance is intrinsic: generated descriptors stamp `origin: "generated"` and app adapters normalize omitted app-owned descriptors to `"authored"` before `SideNavigation`. Reuse the exported `NavigationOrigin` and `data-origin` attribute; do not add an authored `.scn` field or recreate the icon tint in app code.
- Custom page headers use `PageNavigationToggle` to consume the `PageLayoutProvider` navigation state; do not create a second sidebar collapse store in the consuming app.
- Windowed `DataTable` rendering observes the Astryx scroll container's size; layout or viewport changes must update the visible range without a scroll event, and the observer disconnects on unmount.
- Keep `QueryTable` chrome-less: generated `table_page` adapters own the surrounding `Page` shell, stats, header actions, and dialogs, while the catalog component owns controlled query state, CSV export, row detail, and windowing. Read the full behavior contract in `docs/ui-agent-contract.md` § QueryTable before changing any of these behaviors.
- Generated stats tiles share `QueryTable` request state when they set, toggle, or clear a typed filter or predicate; do not build a parallel filter store. Date/datetime presets write local-calendar inclusive bounds into the ordinary paired filter inputs, and invalid datetime cells must render safely instead of exposing `Invalid Date`.
- `WorkspacePage` is the thin generated-workspace shell (Astryx tabs or grouped sidebar, adapter-controlled `?tab=` selection, mount-on-first-visit-then-keep). Full contract: `docs/ui-agent-contract.md` § WorkspacePage.
- `DetailPageLayout`, `DetailSection`, `DetailField`, `DetailRelated`, and `DetailDialog` are the shared layout surface for generated `detail_page` content; one generated content component feeds both routed and dialog wrappers, and domain workflows stay in the app. Full contract: `docs/ui-agent-contract.md` § Detail Pages.

## Verification

Run the catalog-wide verification in `ui/AGENTS.md`. For behavior changes, also exercise the affected component through a regenerated consuming client and its applicable typecheck, lint, tests, build, and `scenery generate --check`.
