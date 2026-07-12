# scenery UI Agent Contract

Agents must compose UI from scenery layouts and primitives.

Agents must not use shadcn directly in app or dashboard screens. shadcn is an implementation input for the scenery registry and promotion flow, not a screen-authoring API.

This contract applies to registry and promoted-component work under `ui/`. The runnable dashboard lives only under `apps/consolenext/` and is governed by its local Astryx + StyleX `AGENTS.md` contract.

## Allowed

- Import primitives from `@/components/primitives/*`.
- Import layouts from `@/components/layouts/*`.
- Compose feature components from scenery primitives and layouts.
- Use named layout slots such as `toolbar`, `content`, `objectList`, `table`, `inspector`, and `eventStream`.
- Add small feature-specific CSS only when no existing primitive or layout owns the behavior.
- Install approved registry items with `bun run shadcn:add @scenery/<item>` from `ui/`.

## Forbidden

- Do not run raw `shadcn add`, `bunx shadcn add`, `npx shadcn add`, or install from a URL.
- Do not install un-namespaced shadcn items such as `button` or `dashboard-01`.
- Do not install from registry namespaces other than `@scenery`.
- Do not import from `@/components/vendor/shadcn/*` in routes, pages, or feature screens.
- Do not import from `@/components/ui/*`.
- Do not import Radix, `class-variance-authority`, `clsx`, `tailwind-merge`, or lucide icons directly in screens. Put those dependencies behind scenery primitives, layouts, registry sources, or small scenery-owned helpers.
- Do not edit generated or vendor shadcn files directly from app screens.
- Do not add long Tailwind-style `className` strings in routes or pages when a primitive or layout should own the behavior.

## Install Flow

Use the scenery wrapper:

```sh
cd ui
bun run shadcn:add @scenery/button
bun run shadcn:add @scenery/dashboard-page
```

The wrapper accepts only `@scenery/*` items, starts the local scenery registry server for the command, runs shadcn with `--dry-run` first, rejects URLs, local paths, unsupported flags, and an occupied registry port, pins the shadcn CLI to `shadcn@4.7.0` by default, and refuses overwrite unless `SCENERY_SHADCN_OVERWRITE=1` is set.

Forbidden examples:

```sh
bunx shadcn@latest add button
bunx shadcn@latest add dashboard-01
bunx shadcn@latest add https://example.com/button.json
```

## Import Examples

Good:

```tsx
import { Button } from "@/components/primitives/Button";
import { DashboardPage } from "@/components/layouts/DashboardPage";

export function UsersPage() {
  return (
    <DashboardPage
      title="Users"
      toolbar={<Button size="sm">Invite</Button>}
      content={<UsersTable />}
    />
  );
}
```

Bad:

```tsx
import { Button } from "@/components/vendor/shadcn/button";
import * as DialogPrimitive from "@radix-ui/react-dialog";

export function UsersPage() {
  return <button className="inline-flex h-9 items-center rounded-md px-3 text-sm">Invite</button>;
}
```

Better:

```tsx
import { DashboardPage } from "@/components/layouts/DashboardPage";
import { PageToolbar } from "@/components/layouts/PageToolbar";

export function UsersPage() {
  return (
    <DashboardPage
      title="Users"
      toolbar={<PageToolbar primaryAction={{ label: "Invite", onClick: invite }} />}
      content={<UsersTable />}
    />
  );
}
```

## Layout Rules

Agent-facing layouts use named props instead of free-form compound children. The layout owns grid, spacing, scroll behavior, responsive behavior, ARIA landmarks, empty state placement, and DOM markers.

Preferred:

```tsx
<DashboardPage title="Requests" toolbar={<RequestToolbar />} content={<RequestTable />} />
```

Avoid:

```tsx
<DashboardPage>
  <div className="grid gap-4" />
</DashboardPage>
```

Layouts must expose stable markers for future browser harness checks:

```tsx
<section data-scenery-ui="DashboardPage">
  <header data-slot="header">{toolbar}</header>
  <main data-slot="content">{content}</main>
</section>
```

## Promotion Flow

When a new shadcn component is needed:

1. Inspect the upstream shadcn component in a scratch location.
2. Promote the vetted source into `ui/src/components/vendor/shadcn` only if a vendor copy is needed.
3. Wrap it in `ui/src/components/primitives` with stable scenery props.
4. Add a slot layout under `ui/src/components/layouts` if the component is structural.
5. Add tests, a fixture, or a dashboard example that proves the public wrapper.
6. Add a registry item under `ui/registry/scenery`.
7. Update the allowlist or static checks only for the scenery wrapper, not for direct screen usage.

Decision record template:

```text
Component: Dialog
Source: shadcn dialog, Radix primitive
Public scenery wrapper: ui/src/components/primitives/Dialog.tsx
Allowed screen API: <Dialog />
Not allowed: direct @radix-ui/react-dialog imports in routes
```

## Enforcement

`scenery harness self -o json --write` runs UI static architecture checks. The first checks hard-fail direct shadcn/script/registry/import boundary violations. Existing className-heavy dashboard code is reported as warnings while it is migrated into layouts and primitives; new work should reduce those warnings, not add to them.

For dashboard behavior changes, also run:

```sh
scenery harness ui -o json --write
```

The browser UI harness visits core dashboard routes, runs route-specific semantic journeys, checks stable `data-scenery-ui` markers, and writes screenshots, DOM snapshots, console logs, and network artifacts under `.scenery/harness/ui/`.
