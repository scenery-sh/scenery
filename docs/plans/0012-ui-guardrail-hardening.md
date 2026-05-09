# UI Guardrail Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

onlava now has the right UI architecture direction: an `@onlava` shadcn registry, a guarded shadcn wrapper, onlava-owned primitives and layouts, a UI agent contract, and self-harness static checks. Before building larger UI surfaces such as a dashboard data explorer, harden the guardrails so they are reliable enforcement rather than mostly guidance.

Do not undo the `@onlava` registry model. Do not build new product UI in this plan. The goal is to close the small but important bypasses in the wrapper, registry item validation, import scanning, and className drift detection.

Target outcome:

```text
agent or human runs:
  bun run shadcn:add @onlava/button

wrapper:
  uses pinned shadcn CLI behavior
  rejects unknown flags and unsafe overwrite
  fails if the fixed registry port is occupied
  serves only local onlava registry items

self-harness:
  validates registry item write targets and sources
  catches import/export/dynamic/require bypasses
  catches common className drift forms
  has fixture tests for known bypasses
```

After this plan, `0013 Dashboard Data Explorer` can safely use `DataExplorerLayout` as the proving ground for the onlava UI composition model.

## Progress

- [x] (2026-05-09 08:23Z) Created this ExecPlan and assigned historical ID 0012.
- [x] (2026-05-09 08:23Z) Linked this ExecPlan from `docs/plans/active.md`.
- [x] (2026-05-09 12:33Z) Made `ui/scripts/onlava-shadcn.mjs` fail hard when port `127.0.0.1:4873` is occupied.
- [x] (2026-05-09 12:33Z) Made the wrapper reject every unknown flag; only `--dry-run`, `--overwrite`, and `-o` are accepted.
- [x] (2026-05-09 12:33Z) Pinned the shadcn CLI version through `ONLAVA_SHADCN_VERSION ?? "shadcn@4.7.0"`.
- [x] (2026-05-09 12:34Z) Added UI static validation for registry item `files[].source` and `files[].target`.
- [x] (2026-05-09 12:34Z) Strengthened import scanning for multiline imports, `export ... from`, dynamic `import()`, and `require()`.
- [x] (2026-05-09 12:34Z) Strengthened className scanning for `cn(...)`, template literals, and conditional literal forms.
- [x] (2026-05-09 12:35Z) Added fixture tests for UI static check bypasses.
- [x] (2026-05-09 12:35Z) Made Tailwind explicit with a pinned `tailwindcss` devDependency in `ui/package.json`.
- [x] (2026-05-09 12:36Z) Added `PageToolbar` and the `@onlava/page-toolbar` registry item.
- [x] (2026-05-09 12:36Z) Fixed optional slot behavior in `DashboardPage` and `DataExplorerLayout`.
- [x] (2026-05-09 12:40Z) Ran validation and marked this ExecPlan complete.

## Surprises & Discoveries

- Official shadcn docs say target aliases in `files[].target` were added in `shadcn@4.7.0`, including alias targets such as `@ui/ai/prompt-input.tsx`. This makes a pinned CLI version important because onlava registry items already rely on `target` alias behavior.
- Official shadcn registry item docs allow `registryDependencies` to be names, namespaces, or URLs, and allow `files[].target` to write to root-relative paths such as `~/.env`. onlava must explicitly forbid those capabilities in the approved registry.
- Current `ui/scripts/onlava-shadcn.mjs` treats `EADDRINUSE` as success by returning a dummy `close` function. That can make the wrapper proceed against an unknown process already listening on `127.0.0.1:4873`.
- Current wrapper parsing accepts `--cwd` and silently ignores unknown flags beginning with `-`. That is too permissive for an agent-facing install gate.
- Current self-harness import scanning is line-oriented and only catches simple one-line imports. It can miss multiline imports, re-exports, dynamic imports, and CommonJS requires.
- Current className scanning catches only `className="..."` and `className='...'`. The common `className={cn("...")}` pattern is not counted.
- Registry item sources previously used `../../src/...`. The stricter source validation is clearer if registry sources are UI-root-relative paths such as `src/components/primitives/Button.tsx`.
- `ReactNode` can include values such as `0`, so layout class composition needs `Boolean(...)` for optional slot flags when the value is only used as a conditional CSS token.
- The existing dashboard still has four className warnings after the stronger scanner. They are pre-existing migration targets, not new hard failures.

## Decision Log

- Decision: Keep `@onlava/*` and the shadcn wrapper, but make wrapper behavior stricter before building more UI.
  Rationale: The architecture is right; the risk is bypasses in enforcement. A small hardening pass gives agents clearer failure modes.
  Date/Author: 2026-05-09 / Codex

- Decision: Fail hard on occupied registry port instead of trusting whatever process owns it.
  Rationale: The fixed `components.json` registry URL points to `127.0.0.1:4873`; using an unknown existing server would let an external registry become the write authority.
  Date/Author: 2026-05-09 / Codex

- Decision: Pin `shadcn@4.7.0` or newer in the wrapper.
  Rationale: onlava's registry model depends on CLI semantics, especially target aliases. `latest` is convenient but undermines deterministic guardrails.
  Date/Author: 2026-05-09 / Codex

- Decision: Keep className drift warning-only in this plan.
  Rationale: Existing dashboard route files still contain utility-heavy class strings. This plan should improve detection without blocking the repo before a dedicated className-reduction migration.
  Date/Author: 2026-05-09 / Codex

- Decision: Use UI-root-relative registry source paths under `ui/src`.
  Rationale: Paths such as `src/components/primitives/Button.tsx` are easier to validate mechanically than `../../src/...` and make registry item source declarations clearly scoped to the UI package.
  Date/Author: 2026-05-09 / Codex

- Decision: Add `tailwindcss@4.2.2` explicitly to `ui/devDependencies`.
  Rationale: `ui/src/index.css` imports Tailwind directly and the UI already uses Tailwind-style classes. Relying on an indirect dependency would make local builds less reproducible.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed on 2026-05-09.

Shipped:

- The shadcn wrapper rejects occupied registry port, unknown flags, unsafe overwrite, non-`@onlava` items, URLs, and local path-style items before invoking shadcn.
- The wrapper defaults to pinned `shadcn@4.7.0` while preserving `ONLAVA_SHADCN_VERSION` as an explicit escape hatch.
- Registry item validation now treats `files[].source` and `files[].target` as write-capability declarations and rejects traversal, root writes, package/config/lock/script targets, missing sources, and non-`ui/src` sources.
- UI import scanning now catches multiline imports, side-effect imports, re-exports, dynamic imports, and CommonJS requires.
- ClassName scanning now warns on common expression forms such as `cn(...)`, template literals, and conditional literals outside the approved primitive/layout/vendor layers.
- `PageToolbar` is implemented and exported, with an `@onlava/page-toolbar` registry item.
- `DashboardPage` and `DataExplorerLayout` no longer create empty fixed-width side columns for omitted optional slots.
- `docs/ui-agent-contract.md`, `docs/local-contract.md`, and `docs/harness-engineering.md` describe the hardened guardrails.

Validation:

```text
go test ./cmd/onlava
go test ./...
cd ui && bun run typecheck
cd ui && bun run test
cd ui && bun run build
cd ui && bun run shadcn:add @onlava/button --dry-run
cd ui && bun run shadcn:add button --dry-run           # expected failure
cd ui && bun run shadcn:add @onlava/button --foo       # expected failure
cd ui && bun run shadcn:add @onlava/button --cwd .     # expected failure
occupied-port wrapper check                            # expected failure
git diff --check
go install ./cmd/onlava
onlava harness self --json --write
```

The final self-harness result was `ok: true`. UI static checks reported `errors: 0`, `registry_items: 10`, and four existing className warnings that remain follow-up migration work.

## Context and Orientation

This plan is for the `github.com/pbrazdil/onlava` repository.

Read these files first:

- `docs/plans/0011-onlava-ui-registry-and-agent-guardrails.md`
- `docs/ui-agent-contract.md`
- `ui/components.json`
- `ui/scripts/onlava-shadcn.mjs`
- `ui/package.json`
- `ui/registry/onlava/*.json`
- `ui/src/components/primitives/*`
- `ui/src/components/layouts/*`
- `cmd/onlava/harness_ui.go`
- `cmd/onlava/harness_self.go`
- `docs/local-contract.md`
- `docs/harness-engineering.md`

Current wrapper behavior to change:

```text
file: ui/scripts/onlava-shadcn.mjs
- starts a local HTTP registry on 127.0.0.1:4873
- currently treats EADDRINUSE as success
- currently accepts --cwd/-c and ignores unknown flags
- currently invokes shadcn@latest
```

Current self-harness behavior to harden:

```text
file: cmd/onlava/harness_ui.go
- validates components.json namespace and alias
- validates package scripts
- validates basic registry item name/type/dependencies
- scans simple import lines
- scans simple className string literals
```

Official shadcn facts verified on 2026-05-09:

- `shadcn@4.7.0` added package imports and aliases in `files.target`.
- Registry item `files[].target` may use placeholders at the start of the target: `@components/`, `@ui/`, `@lib/`, and `@hooks/`.
- Registry item `files[].target` may also point to root-relative locations such as `~/.env`; onlava should reject those for the approved registry.
- `registryDependencies` can include un-namespaced names, namespaced items, or URLs; onlava should allow only `@onlava/*`.

Reference docs:

- `https://ui.shadcn.com/docs/changelog/2026-05-package-imports-target-aliases`
- `https://ui.shadcn.com/docs/registry/registry-item-json`

## Milestones

Milestone 1: Wrapper hardening.

Change `ui/scripts/onlava-shadcn.mjs` so it is deterministic and conservative:

```text
- fail if 127.0.0.1:4873 is already in use
- reject all unknown flags
- remove --cwd/-c support
- allow --dry-run
- allow --overwrite/-o only when ONLAVA_SHADCN_OVERWRITE=1
- pin CLI to ONLAVA_SHADCN_VERSION or shadcn@4.7.0
```

Do not dynamically allocate a port in this milestone. Dynamic ports require temporarily changing registry resolution and are not needed yet.

Milestone 2: Registry item file validation.

Extend `checkUIRegistryItems` in `cmd/onlava/harness_ui.go` so registry files are treated as write-capability declarations.

Validate every `files[]` entry:

```text
source:
  required when onlava uses local source expansion
  must exist
  must stay under ui/src or an approved registry source root
  must not contain ..
  must not be absolute

target:
  required for onlava registry items
  must start with @components/, @ui/, @lib/, or @hooks/
  must not contain ..
  must not start with ~/
  must not be absolute
  must not target package.json, bun.lock, package-lock.json, pnpm-lock.yaml, yarn.lock
  must not target vite config, tsconfig, scripts, components.json, or dotfiles
```

Keep registry dependencies strict:

```text
registryDependencies:
  only @onlava/* is allowed
  URLs and un-namespaced shadcn names are forbidden
```

Milestone 3: Stronger import scanning.

Replace or extend `uiImportSpecifiers` so it scans whole files and catches:

```text
import { Button } from "@/components/ui/button"
import {
  Button
} from "@/components/ui/button"
import "@/components/ui/button"
export { Button } from "@/components/ui/button"
export * from "@/components/ui/button"
await import("@/components/ui/button")
require("@/components/ui/button")
```

The implementation can be a small scanner or carefully scoped regexes. It does not need a full TypeScript parser yet, but tests must cover the bypasses.

Milestone 4: Stronger className scanning.

Keep className drift warning-only, but detect the real patterns:

```text
className="..."
className='...'
className={`...`}
className={cn("...", condition && "more")}
className={condition ? "..." : "..."}
```

Do not warn inside:

```text
ui/src/components/primitives/**
ui/src/components/layouts/**
ui/src/components/vendor/shadcn/**
```

Milestone 5: Fixture tests.

Add focused tests for `cmd/onlava` UI static check behavior. Prefer Go tests in `cmd/onlava` using temporary fixture trees. Tests should cover:

```text
- raw shadcn script rejected
- non-@onlava registry rejected
- registry dependency URL rejected
- registry file target ~/ rejected
- registry file target package.json rejected
- registry source escaping rejected
- multiline import rejected
- export-from rejected
- dynamic import rejected
- require rejected
- cn(...) className warning emitted
- conditional className warning emitted
```

Milestone 6: Tailwind dependency decision.

`ui/src/index.css` imports Tailwind with:

```css
@import "tailwindcss";
```

Make the dependency state explicit. Preferred short-term decision:

```text
Add tailwindcss to ui/devDependencies and lock it.
```

If implementation chooses to remove Tailwind instead, update the Decision Log with why that is safe despite existing utility-style classes.

Milestone 7: PageToolbar and optional slots.

Either add `PageToolbar` or rewrite `docs/ui-agent-contract.md` to avoid referencing it. Preferred outcome:

```text
ui/src/components/layouts/PageToolbar.tsx
```

It should expose typed actions so agents avoid hand-rolling toolbar layouts.

Fix optional slot behavior:

```text
DashboardPage:
  sidebar only -> sidebar + content, no empty inspector column
  inspector only -> content + inspector, no empty sidebar column
  both -> sidebar + content + inspector
  neither -> content only

DataExplorerLayout:
  inspector/eventStream absent -> no empty right rail
  eventStream without inspector -> still render a right rail only if eventStream exists
```

## Plan of Work

Start with tests for the static checks and wrapper parsing where practical. The risky parts are bypasses; tests should prove each bypass is caught before refactoring the scanner.

For the wrapper, keep it dependency-free. A small parser with an explicit allowlist is enough. Do not pass through `--cwd`, path options, URL installs, `--all`, or unknown flags. Keep the local `components.json` check.

Then strengthen registry item validation. Define a small struct for item files:

```go
type registryItemFile struct {
    Path   string `json:"path"`
    Source string `json:"source"`
    Type   string `json:"type"`
    Target string `json:"target"`
}
```

For source validation, onlava's current registry item JSON uses `source` as an onlava wrapper extension. The source path is resolved relative to `ui/registry/onlava` by the wrapper. The harness should validate both the source and the target so local registry metadata cannot write surprising files.

For import scanning, prefer collecting specifiers with context:

```text
kind: import-from | side-effect-import | export-from | dynamic-import | require
specifier: string
```

That makes diagnostics easier and tests clearer.

For className scanning, avoid trying to fully parse JSX. Detect the common forms conservatively and return warnings. False negatives are acceptable only if tests cover the known common patterns; false positives should remain warnings and include precise file paths.

Finally, add `PageToolbar` and fix layout optional slots. Keep layout visual behavior conservative. This plan is hardening, not redesign.

## Concrete Steps

1. Edit `ui/scripts/onlava-shadcn.mjs`:
   - replace `shadcn@latest` with `process.env.ONLAVA_SHADCN_VERSION ?? "shadcn@4.7.0"`
   - reject `EADDRINUSE`
   - remove `--cwd` support
   - reject unknown flags
2. Add or update wrapper tests if there is already a suitable JS test surface; otherwise cover wrapper expectations through shell validation and keep parser logic simple.
3. Extend `cmd/onlava/harness_ui.go` registry item structs and validation.
4. Add tests in `cmd/onlava` for registry validation failures.
5. Replace import scanning with whole-file scanning that catches import/export/dynamic/require forms.
6. Add tests for import bypasses.
7. Extend className literal extraction to catch `cn`, template literal, and conditional forms.
8. Add tests for className warning behavior.
9. Add explicit `tailwindcss` devDependency to `ui/package.json` and update `ui/bun.lock`, unless the Decision Log records a different choice.
10. Add `ui/src/components/layouts/PageToolbar.tsx`, export it from `ui/src/components/layouts/index.ts`, and add an `@onlava/page-toolbar` registry item if useful.
11. Update `docs/ui-agent-contract.md` example to use the implemented `PageToolbar`.
12. Fix optional slot layouts in `DashboardPage` and `DataExplorerLayout`.
13. Update docs if self-harness diagnostics or wrapper commands change.
14. Run validation and update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective.

## Validation and Acceptance

Run from the onlava repo root unless specified:

```sh
go test ./cmd/onlava
go test ./...
cd ui && bun run typecheck
cd ui && bun run test
cd ui && bun run build
cd ui && bun run shadcn:add @onlava/button --dry-run
go install ./cmd/onlava
onlava harness self --json --write
```

Additional manual wrapper checks:

```sh
cd ui && bun run shadcn:add button --dry-run
cd ui && bun run shadcn:add @onlava/button --foo
cd ui && bun run shadcn:add @onlava/button --cwd .
```

Each manual wrapper check should fail with a clear refusal message.

Acceptance criteria:

```text
- wrapper never proceeds when 127.0.0.1:4873 is already occupied
- wrapper invokes pinned shadcn CLI by default
- unknown flags are rejected
- registry item sources and targets are validated by self-harness
- import scanner catches multiline, export-from, dynamic import, and require bypasses
- className scanner warns on cn/template/conditional drift forms
- fixture tests cover the important bypasses
- Tailwind dependency state is explicit
- UI contract references only implemented examples
- optional layout slots do not create empty fixed-width columns
- self-harness remains green
```

## Idempotence and Recovery

All changes are source-level and should be safe to rerun. The wrapper must remain idempotent for dry runs and repeated installs.

If registry validation becomes too strict and blocks a legitimate target, add the smallest explicit allowlist entry with a rationale in the Decision Log. Do not broadly allow raw paths or root writes.

If import scanning creates false positives, keep the diagnostic but narrow the match to exact import forms. Do not remove checks for dynamic imports or require.

If adding `tailwindcss` changes lockfile resolution more than expected, inspect the lockfile diff carefully and record the dependency decision. Do not rely on an undeclared transitive Tailwind dependency.

If optional slot fixes change visual output, stop and add a minimal component test or fixture before continuing. The layout API should get safer without redesigning the dashboard.

## Artifacts and Notes

Expected files touched:

```text
ui/scripts/onlava-shadcn.mjs
ui/package.json
ui/bun.lock
ui/src/components/layouts/DashboardPage.tsx
ui/src/components/layouts/DataExplorerLayout.tsx
ui/src/components/layouts/PageToolbar.tsx
ui/src/components/layouts/index.ts
ui/registry/onlava/page-toolbar.json
ui/registry/onlava/registry.json
cmd/onlava/harness_ui.go
cmd/onlava/harness_ui_test.go
docs/ui-agent-contract.md
docs/local-contract.md
docs/harness-engineering.md
docs/plans/0012-ui-guardrail-hardening.md
```

Potential diagnostics:

```json
{
  "stage": "ui static architecture",
  "severity": "error",
  "file": "/repo/ui/registry/onlava/bad.json",
  "message": "registry file target may not write package.json",
  "suggested_action": "Use @components/, @ui/, @lib/, or @hooks/ and keep registry writes inside approved source roots."
}
```

## Interfaces and Dependencies

Interfaces changed or hardened:

```text
bun run shadcn:add @onlava/<item>
ONLAVA_SHADCN_OVERWRITE=1
ONLAVA_SHADCN_VERSION
ONLAVA_SHADCN_REGISTRY_ROOT
onlava harness self --json --write
ui static architecture diagnostics
```

Dependency decision:

```text
Tailwind must be explicit if `ui/src/index.css` imports it.
Default target is a pinned `shadcn@4.7.0` CLI invocation through bunx.
```

Do not add a frontend lint framework in this plan. The enforcement belongs in the existing self-harness unless implementation proves a parser dependency is worth the cost.
