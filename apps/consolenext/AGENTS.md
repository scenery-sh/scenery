# Console Next Agent Instructions

## Purpose

`apps/consolenext` is the Vite React Astryx + StyleX dashboard source served by `scenery up` under `/consolenext/`.

## Ownership

Keep this app isolated from `apps/console` and `ui/` unless a task explicitly asks to share code.

## Local Contracts

- Created from Vite's React TypeScript template and initialized with `bunx astryx init`.
- Astryx is wired through `@astryxdesign/build/vite` and StyleX.
- `src/main.tsx` imports Astryx reset and neutral theme CSS; component CSS comes from the StyleX Vite source-build pipeline, so do not add `@astryxdesign/core/astryx.css` unless the package export is fixed.
- Dashboard data must come through the existing local dashboard RPC/WebSocket surfaces (`status`, `logs/list`, `traces/list`, `process/output/list`, `api-call`, `db/query`, `stored-requests/*`, `symphony/*`). Do not read `.scenery/` caches or devdash storage directly from this app.
- The Symphony page uses `symphony/state`, `symphony/task/*`, `symphony/statuses/update`, `symphony/workflow/*`, and read-only `symphony/run/detail` RPCs backed by Scenery-owned dashboard SQLite. It must not call `/__graphql`, `postGraphQL`, or any Linear/GraphQL API. Manual runner RPCs such as `symphony/run/start` stay unavailable until the dashboard runner channel has an authenticated protocol; the current runner is server-side only, requires saved workflow markdown or app-root `WORKFLOW.md` for `auto`, claims eligible `Todo` tasks, moves claimed tasks to `In Progress`, moves succeeded tasks to `Human Review`, moves failed tasks to `Rework`, and exposes run workspace, summary, changed files, diff, and event details through the task modal.
- `scripts/build-dashboard-ui-embed.sh` builds this app and copies `dist/` into `cmd/scenery/dashboard_static/dist` for embedded dashboard binaries.
- Do not commit `node_modules/` or `dist/`.

## Work Guidance

Prefer Astryx components and StyleX `xstyle` overrides before adding local UI primitives or new UI dependencies.

## Motion

Use motion only when it clarifies a change, never for decoration. Most interactions should feel instant: a duration of `0ms` is often the snappiest and best choice, and the call is context-dependent. When motion genuinely helps, such as revealing or moving an element, keep it short and physical with the easing `cubic-bezier(0.175, 0.885, 0.32, 1.1)`: roughly 150ms for state changes, 200ms for popovers and tooltips, and 300ms for overlays and modals. Avoid long, looping, or attention-grabbing animation, and honor `prefers-reduced-motion` by dropping nonessential motion.

## Verification

Run from this directory when touching the app:

```sh
bun run lint
bun run typecheck
bun run build
```

## Child Agent Index

- No child `AGENTS.md` files are currently indexed under this directory.

<!-- ASTRYX:START -->
Astryx v0.1.2 · 148 components
CLI: run every command as `bunx astryx <cmd>` (shown below as `astryx ...`).

SETUP (once, in this Vite app entry e.g. main.tsx) — without these, components render unstyled:
  import "@astryxdesign/core/reset.css";
  import "@astryxdesign/theme-neutral/theme.css";
  // Component CSS is emitted by @astryxdesign/build/vite + StyleX.

WORKFLOW — discover, don't guess. Before writing UI:
1. `astryx build "<idea>"` — START HERE: returns a kit (closest [page] + [block]s + [component]s). No args = full playbook.
2. `astryx template <name> [--skeleton]` — scaffold the [page]/[block]s it named, or study their layout. Templates are reference code.
3. `astryx component <Name>` — props + examples for every component you use.

RULES:
- No <div> — components do all layout/spacing. Full page → AppShell; sidebar nav → SideNav.
- Custom styling: component props first; else the xstyle prop / StyleX tokens (@astryxdesign/core/theme/tokens.stylex). No raw hex/px.
- Tokens for every value (`astryx docs tokens`). Brand/accent via `astryx theme` — never override --color-* in :root.

MORE CLI:
  search "<query>"   find any component / hook / doc / template / block
  component --list   148 components by category
  template --list    page + block recipes
  docs <topic>       color, elevation, icons, illustrations, migration, motion, principles, shape, spacing, styling, theme, tokens, typography
  swizzle <Name>     eject component source (--gap reports why)
  upgrade --apply    run after any @astryxdesign/core bump
<!-- ASTRYX:END -->
