# 0125 Single `@scenery/ui` Import Surface: Token Facade and Blessed Primitives

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

## Purpose / Big Picture

App code in React-enabled Scenery clients currently imports UI building
blocks from two namespaces: composed components from the binary-owned
catalog (`@scenery/ui`, materialized into `react/scenery-ui/`) and
primitives plus theme tokens from Astryx (`@astryxdesign/core/<Component>`
and `@astryxdesign/core/theme/tokens.stylex`). Astryx is maintained by
Meta and stays an external peer dependency permanently — vendoring it is
ruled out. But the split surface costs real agent ergonomics: two mental
models, bracket-string token access
(`colorVars["--color-border"]`), and no scenery-owned place to curate a
semantic vocabulary. The 2026-07-17 guardrails audit (recorded in plan
0124) showed agents drifting precisely where curation is missing, e.g.
five chained `spacingVars["--spacing-10"]` refs in a `calc()` to express
one panel width.

This plan makes `@scenery/ui` the single import surface — the shadcn
model, with Astryx in the Radix role:

1. **Token facade**: a new catalog module `ui/tokens.stylex.ts` that is a
   genuine StyleX *defining* module (`stylex.defineVars`) whose values
   reference imported Astryx var group members. StyleX forbids
   re-exporting variables through barrels, but it does not forbid a
   defining module composed from other vars — Astryx itself ships this
   exact pattern (`dist/Layout/container.stylex.js` imports
   `spacingVars['--spacing-4']` into new definitions), and Astryx token
   defaults resolve to plain named CSS custom properties
   (`var(--color-accent)`), so runtime theming (light/dark, theme
   overrides) flows through unchanged. Apps write dot-accessed semantic
   tokens (`t.border`, `t.surface`) imported from the materialized
   catalog instead of bracket-string Astryx access.
2. **Blessed primitives**: the catalog `index.ts` exports the small set
   of Astryx primitives apps actually use (measured by import counts:
   `Text`, `Button`, `IconButton`, `Badge`, `TextInput`, `Selector`,
   `VStack`, `HStack`, `Heading`, `Icon`), as plain re-exports at first
   (component re-export is legal; only StyleX vars are not), each
   convertible to a real wrapper the moment a scenery convention needs
   injecting. Curation is the export list itself.

Success looks like: a page in the Micro platform app
(`~/Repos/Micro/platform/apps/platform`) compiles and renders with every
UI import — components *and* tokens — coming from `@scenery/ui`, theme
switching still working, and `scenery generate --check` green. Existing
apps keep compiling unchanged; migration is adoption, not a breaking
change, and plan 0124's report is the adoption meter (`markup.catalog`
rising, direct Astryx imports falling).

## Progress

- [x] (2026-07-17) Plan authored.
- [x] (2026-07-17) M1 feasibility spike: the two-token defining module
  compiled through Micro's production Vite + StyleX pipeline and rendered
  through a real light/dark theme flip; go decision recorded below.
- [x] (2026-07-17) M2 catalog token module `ui/tokens.stylex.ts` with
  measured vocabulary; embed, live materialization, ownership marker,
  materialization test, and catalog typecheck wired.
- [x] (2026-07-17) M3 blessed primitive exports and component types in
  `ui/index.ts`; consuming fixture and TypeScript conformance green.
- [x] (2026-07-17) M4 app-side wiring contract verified end-to-end in
  Micro: TypeScript, Vite, StyleX resolution, live theme flip, production
  build, generated-artifact check, and app validation lanes.
- [x] (2026-07-17) M5 docs synced across `ui/AGENTS.md`,
  `docs/agent-guide.md`, `SKILL.md`, `docs/local-contract.md`, root
  instructions, architecture, and Micro's app-local instructions.
- [x] (2026-07-17) M6 validation matrix green; plan 0124 adoption
  before/after, emitted CSS, bundle size, generated marker, and browser
  theme evidence recorded.

## Surprises & Discoveries

- (2026-07-17, pre-plan) Astryx's shipped dist proves the two facts this
  plan rests on. `node_modules/@astryxdesign/core/dist/theme/tokens.stylex.js`
  defaults tokens to plain named custom properties
  (`"--color-accent": "var(--color-accent)"`), so the runtime theme
  contract is stable human-named CSS variables, not hashed StyleX
  internals. `dist/Layout/container.stylex.js` composes imported var
  members into new definitions
  (`const SP4 = spacingVars['--spacing-4']; ... var(--astryx-card-padding, ${SP4})`),
  so cross-module var composition compiles in Astryx's own pipeline.
  What remains unproven is the same composition inside a *materialized
  catalog file* compiled by a consuming app's Vite StyleX plugin — hence
  M1 is a gating spike, not a formality.
- (2026-07-17, implementation) TypeScript and Vite aliases do not teach the
  StyleX Babel transform where a defining module lives. The first Micro
  production build failed at `import { t } from
  "@scenery/ui/tokens.stylex"` until the same exact path was supplied in
  `stylex.vite({ aliases: ... })`. The three-resolver requirement is now an
  explicit client-app contract.
- (2026-07-17, implementation) StyleX collapses the facade references into
  Astryx's stable named custom properties in produced CSS. The spike emitted
  ordinary uses of `var(--color-background-surface)` and
  `var(--spacing-4)` rather than a parallel runtime theme surface.

## Decision Log

- (2026-07-17, Claude + petr) Astryx is Meta-maintained; absorbing or
  vendoring its source into `ui/` is permanently ruled out. The single
  surface is achieved by facade-and-curation, keeping Astryx a peer
  dependency exactly as `ui/AGENTS.md` requires.
- (2026-07-17, Claude + petr) Tokens use a catalog-owned *defining*
  module referencing Astryx vars, not a re-export (StyleX forbids var
  re-exports) and not a bundler alias as primary design (an alias keeps
  Astryx's bracket-string naming and gives no curation). The alias
  approach (`@scenery/ui/tokens.stylex` resolving to Astryx's file via
  tsconfig/Vite/StyleX-plugin aliases) is the recorded fallback if M1
  fails.
- (2026-07-17, Claude + petr) Primitives are re-exported, not wrapped,
  until a concrete scenery convention justifies a wrapper. Rationale:
  re-exports have zero prop-drift maintenance and identical types;
  premature wrappers are pass-through noise. The blessed list is the
  curation surface, chosen from measured usage (Micro import counts:
  Text 42, Button 36, IconButton 23, Badge 22, TextInput 20,
  Selector 14; onlv `next` adds VStack/HStack/Heading/Icon).
- (2026-07-17, Claude + petr) The token export is one var group with a
  short name (working name `t`; final name decided at M2 review) holding
  a *semantic, curated* vocabulary — not a 1:1 mirror of every Astryx
  token. Apps needing an unblessed Astryx token may still import Astryx
  directly (in-guardrails per 0124); recurring direct use is the signal
  to bless a new semantic token.
- (2026-07-17, Claude + petr) `tokens.stylex.ts` is a deliberate,
  documented exception to the `ui/AGENTS.md` rule "do not expose
  internal subpath imports": StyleX bans barrel re-export of vars, so
  the token module must be imported by its own path. The exception is
  exactly one file.
- (2026-07-17, implementation) M1 is a go. Micro's production build,
  running warranty page, and real application theme switch all proved the
  materialized defining-module design. The alias fallback was not needed.
- (2026-07-17, implementation) The final var-group name is `t`. The
  vocabulary follows recurring Micro/ONLV use rather than mirroring Astryx:
  common semantic colors, borders/radii/shadows, spacing 0.5 through 12 at
  observed steps, the recurring body/code/supporting typography values, and
  the Scenery-level `pageGutter` and `panelWidth` concepts.
- (2026-07-17, implementation) The final facade adds 1.89 kB uncompressed
  CSS (203.65 kB spike build to 205.54 kB final build, about 0.49 kB gzip)
  and no JavaScript growth (4,251.19 kB to 4,251.18 kB). That bounded cost
  is accepted for one curated vocabulary.

## Outcomes & Retrospective

The implemented surface keeps Astryx external and singular while giving app
authors one curated component namespace and one unavoidable StyleX defining
subpath. Micro's warranty page now has zero direct Astryx component imports,
uses only facade tokens, generates without drift, and follows its existing
theme toggle. The independent StyleX alias was the only integration wrinkle;
recording it in every client-facing contract prevents the most likely repeat
failure.

## Context and Orientation

Terms:

- **Catalog**: `ui/` in this repo, the single editable source for
  `@scenery/ui`. Embedded via `ui/embed.go`
  (`//go:embed package.json global.d.ts index.ts components` — root
  files are listed individually, so new root files must be added to the
  directive) and materialized into each React-enabled client's
  `react/scenery-ui/` by `internal/generate/catalog.go`
  (`renderUICatalog` walks the embed FS or, in dev mode per plan 0122,
  the live `envs.local.ui_catalog` directory).
- **Astryx**: Meta's design system, `@astryxdesign/core`, peer
  dependency of every consuming app. Components import per-subpath
  (`@astryxdesign/core/Button`); theme tokens are StyleX var groups in
  `@astryxdesign/core/theme/tokens.stylex` (`colorVars`, `spacingVars`,
  `radiusVars`, `shadowVars`, `typeScaleVars`, `typographyVars`,
  `borderVars`), accessed with bracket keys like
  `colorVars["--color-border"]`.
- **StyleX defining-module rule**: `stylex.defineVars` may only appear
  in files named `*.stylex.ts`/`*.stylex.js`, and consumers must import
  the vars directly from that file; barrel re-exports of vars do not
  compile. Values inside `defineVars` may reference vars imported from
  another `.stylex` module (see Surprises).
- **Alias contract**: apps alias `@scenery/ui` →
  `<output_root>/react/scenery-ui/index.ts` in tsconfig and Vite
  (`ui/AGENTS.md`). This plan adds a second mapping,
  `@scenery/ui/tokens.stylex` → `react/scenery-ui/tokens.stylex.ts`,
  which must be visible to TypeScript, Vite, *and* the app's StyleX
  compiler plugin resolution.

Relevant code and testbeds:

- `ui/index.ts` — current export surface (composed components only).
- `internal/generate/catalog.go`, `internal/generate/generate_typescript_react.go`
  — materialization and generated-page imports (generated pages import
  by relative `./scenery-ui/index.js`; they are unaffected by this
  plan unless generated pages later consume the token facade).
- `internal/generate/testdata/tsconfig.catalog.json` — catalog
  typecheck lane (`apps/console/node_modules/.bin/tsc -p ...` from repo
  root).
- `~/Repos/Micro/platform` — primary live testbed: consumes
  `@scenery/ui` heavily (409 catalog tags per the 0124 audit), has the
  full Vite + StyleX pipeline, and can run the live catalog via
  `envs.local.ui_catalog` (plan 0122) for sub-second iteration.
- `~/Repos/onlv` (frontend `apps/next`) — secondary testbed; Astryx +
  StyleX but no catalog wiring yet.

Related plans: 0122 (live `ui_catalog` dev mode — the iteration loop
for M1/M2), 0124 (guardrails report — the adoption meter; its scanner
keys token identity off any `*.stylex` import, so the facade is
automatically in-guardrails and 0124 needs no changes for this plan).

## Milestones

**M1 — Feasibility spike (gating).** In the Micro platform app with
`envs.local.ui_catalog` pointed at this repo's `ui/`, add a throwaway
`ui/tokens.stylex.ts` defining two vars (one color referencing
`colorVars["--color-background-surface"]`, one spacing referencing
`spacingVars["--spacing-4"]`), import them into one existing page's
`stylex.create`, and confirm: dev compile, produced CSS references the
Astryx custom-property chain, rendered page follows light/dark theme
flips, and a production `bun run build` of the frontend passes. Record
go/no-go and the observed compiled CSS in the Decision Log. On failure,
pivot to the alias fallback and rewrite M2 accordingly before
proceeding. The spike file is a prototype: keep it only by evolving it
into M2's real module.

**M2 — Catalog token module.** Replace the spike with the curated
vocabulary derived from measured usage: audit token references in Micro
`apps/platform` and onlv `apps/next` (ripgrep for
`colorVars\[|spacingVars\[|radiusVars\[|shadowVars\[|typeScaleVars\[|typographyVars\[|borderVars\[`),
bless the recurring ones under semantic names, and add
scenery-semantic tokens where the audit showed workarounds (panel
width, page gutter). Wire the file: add `tokens.stylex.ts` to
`ui/embed.go`'s `go:embed` directive, confirm materialization includes
it (unit test in `internal/generate` asserting the materialized file
set), and extend the catalog typecheck fixture so
`tsconfig.catalog.json` covers it. Repo stays green.

**M3 — Blessed primitives.** Add re-exports to `ui/index.ts` for the
measured primitive set (`Text`, `Button`, `IconButton`, `Badge`,
`TextInput`, `Selector`, `VStack`, `HStack`, `Heading`, `Icon`), types
included. Update `internal/generate` golden fixtures and run the
TypeScript client conformance test. Catalog components themselves may
migrate to the facade opportunistically but are not required to in this
plan.

**M4 — App-side wiring contract.** Document and verify the required app
configuration: tsconfig path mapping and Vite alias for
`@scenery/ui/tokens.stylex`, plus whatever the app's StyleX plugin
needs to resolve that specifier to the materialized defining module
(expected: the plugin's alias/moduleResolution option mirroring the
Vite alias). Verify end-to-end in Micro: migrate one real page (pick
`apps/platform/src/pages/warranty.tsx` — small, already
catalog-heavy) to facade tokens + blessed primitives, run the app's
typecheck, lint, tests, production build, and
`scenery generate --check -o json`. The contract text lands in
`docs/agent-guide.md` (client-app integration) and `ui/AGENTS.md`.

**M5 — Docs sync.** `ui/AGENTS.md`: token-module subpath exception,
blessed-primitive policy (re-export until a convention needs a
wrapper), peer-dependency rule unchanged. `docs/agent-guide.md` and
`SKILL.md`: "import UI from `@scenery/ui`; tokens from
`@scenery/ui/tokens.stylex`; direct Astryx imports are the escape
hatch, recurring use means bless it." `docs/local-contract.md`: the
materialized artifact path `react/scenery-ui/tokens.stylex.ts` and the
app alias contract. `docs/knowledge.json` and `docs/plans/active.md`
entries. Root `AGENTS.md` mental-model line if the catalog contract
sentence there needs the token surface mentioned.

**M6 — Validation and adoption baseline.** Full matrix (below), then
run the 0124 report (or, until 0124 ships, its recorded prototype
methodology) against Micro to record the pre-migration adoption
baseline in this plan's Artifacts, so later catalog-share movement is
attributable.

## Plan of Work

The token module is small and hand-authored, shaped like:

    // ui/tokens.stylex.ts
    import * as stylex from "@stylexjs/stylex";
    import {
      borderVars, colorVars, radiusVars, shadowVars, spacingVars,
      typeScaleVars,
    } from "@astryxdesign/core/theme/tokens.stylex";

    export const t = stylex.defineVars({
      surface: colorVars["--color-background-surface"],
      popover: colorVars["--color-background-popover"],
      border: colorVars["--color-border"],
      borderEmphasized: colorVars["--color-border-emphasized"],
      textSecondary: colorVars["--color-text-secondary"],
      borderWidth: borderVars["--border-width"],
      radius: radiusVars["--radius-container"],
      shadowLow: shadowVars["--shadow-low"],
      space1: spacingVars["--spacing-1"],
      // ...curated from the M2 usage audit, plus scenery-semantic
      // additions such as panelWidth and pageGutter
    });

Consumers write:

    import { t } from "@scenery/ui/tokens.stylex";
    const styles = stylex.create({
      panel: { backgroundColor: t.surface, borderColor: t.border },
    });

Materialization needs no new logic — `renderUICatalog` walks whatever
the embed (or live catalog dir) contains; the work is the embed
directive, fixtures, and the typecheck lane. The blessed-primitive
exports are ordinary `export { X } from "@astryxdesign/core/X"` lines
in `ui/index.ts`; Astryx stays a peer dependency so the materialized
re-export resolves against the app's own Astryx copy, exactly like the
catalog's internal Astryx imports do today.

Risks to watch: per-app materialization means the facade's own hashed
var names differ across apps — harmless (they are app-internal aliases
resolving to Astryx's stable custom properties), but do not let any
generated artifact depend on the facade's hash names. Barrel re-exports
can affect tree-shaking; Vite tree-shakes ESM and Astryx ships ESM
dist, but M4's production build should compare bundle size before/after
as a sanity check. If the StyleX plugin in consuming apps cannot
resolve the aliased specifier, prefer teaching apps the plugin alias
option over renaming import paths; only if that fails does the relative
import (`./generated/scenery/react/scenery-ui/tokens.stylex`) become
the documented spelling — uglier but contract-equivalent.

## Concrete Steps

1. **M1 spike.** In `~/Repos/Micro/platform`: confirm `.scenery.json`
   `envs.local.ui_catalog` points at this repo's `ui/` (plan 0122); add
   the two-var `ui/tokens.stylex.ts` here; add the tsconfig/Vite/StyleX
   aliases in the Micro frontend; import `t` in one page; run
   `scenery up`, observe the render and theme flip; run the frontend's
   production build. From this repo the file is picked up live —
   remember the shipped path needs the embed change (M2) before any
   binary-embedded use. Record results in Decision Log with the
   compiled CSS snippet as evidence.
2. **M2.** Author the curated module from the usage audit; update
   `ui/embed.go` directive to
   `package.json global.d.ts index.ts tokens.stylex.ts components`; add
   or extend an `internal/generate` test asserting
   `react/scenery-ui/tokens.stylex.ts` is materialized with the
   generated-ownership marker; extend the catalog typecheck fixture.
   Run from repo root:
   `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
   and `go test ./internal/generate`.
3. **M3.** Add blessed re-exports to `ui/index.ts`; regenerate/refresh
   golden fixtures (`go test ./internal/generate`), run
   `bun test internal/generate/testdata/typescript_client_conformance.test.ts`
   and
   `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json`.
4. **M4.** Migrate `apps/platform/src/pages/warranty.tsx` in Micro to
   facade tokens + blessed primitives; run Micro's typecheck, lint,
   tests, production build, and `scenery generate --check -o json` with
   the worktree-local harness binary; write the alias contract into
   `docs/agent-guide.md` and `ui/AGENTS.md`.
5. **M5.** Remaining docs sync as listed in the milestone; update this
   plan's Progress and `docs/plans/active.md`.
6. **M6.** Validation matrix; record the adoption baseline numbers in
   Artifacts and Notes.

## Validation and Acceptance

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./internal/generate
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    go test ./...
    scenery harness self --summary --write

Against Micro (`~/Repos/Micro/platform`, worktree-local scenery
binary): app typecheck, lint, tests, production frontend build,
`scenery generate --check -o json` all green.

Acceptance:

- `react/scenery-ui/tokens.stylex.ts` is materialized into a consuming
  app with the generated-ownership marker, and importing `t` from it
  inside `stylex.create` compiles under the app's Vite + StyleX
  pipeline.
- The migrated Micro page renders identically before/after (manual
  check) and follows a light/dark theme flip — proving the facade
  resolves through Astryx's named custom properties at runtime.
- `import { Text, Button, VStack } from "@scenery/ui"` typechecks in a
  consuming app and in the catalog typecheck fixture.
- Apps that ignore this feature entirely keep compiling unchanged (no
  breaking change to the existing catalog surface or generated pages).
- The blessed export list and token vocabulary appear in
  `ui/AGENTS.md`, and `docs/agent-guide.md` documents the app alias
  contract including the StyleX-plugin resolution requirement.

## Idempotence and Recovery

M1's spike lives in the working tree plus one Micro page; reverting is
`git checkout` in both repos, and 0122's watcher re-materializes on
revert. Every later step is ordinary source + fixture work: re-running
generation, tests, or materialization is idempotent (materialization is
one artifact-set transaction). If M1 fails, the plan pauses at the
recorded fallback decision rather than proceeding on hope; if M4
reveals a plugin-resolution wall after M2/M3 landed, the catalog module
and re-exports are inert-but-harmless (nothing imports them) and the
repo stays green while the wiring is reworked. Do not partially
document: the M5 docs pass happens only after M4's contract is proven.

## Artifacts and Notes

Evidence base (2026-07-17, from the 0124 audit and Astryx dist
inspection in `~/Repos/Micro/platform/node_modules`):

- Astryx token defaults are plain named custom properties:
  `dist/theme/tokens.stylex.js` maps e.g.
  `"--color-accent": "var(--color-accent)"`.
- Cross-module var composition in Astryx's own dist:
  `dist/Layout/container.stylex.js` —
  `const SP4 = spacingVars['--spacing-4'];`
  `const cardShorthand = \`var(--astryx-card-padding, ${SP4})\`;`.
- Micro `apps/platform` Astryx import counts (blessed-list source):
  Text 42, Button 36, IconButton 23, Badge 22, TextInput 20,
  Selector 14; onlv `apps/next` adds VStack 5, HStack 4, Heading 2,
  Icon 3, Kbd 3.
- Curation motivator: `apps/platform/src/pages/mails.tsx`-era audit
  found `width: calc(5 × spacingVars["--spacing-10"])` — the shape a
  semantic `t.panelWidth` removes.
- M2 usage audit, Micro: the most frequent direct values were `space3` 296,
  `space2` 276, `borderWidth` 201, `border` 183, `textSecondary` 155,
  `space4` 146, `space1` 129, `radiusElement` 101, `body` 99,
  `textPrimary` 91, `radius` 84, and `surface` 62. ONLV independently
  repeated spacing 2/3/4/6/10/12, border, surface, text, full/container
  radii, and low shadow. The final facade is the intersection plus status
  colors and the two planned layout semantics.
- M1 produced-CSS evidence: Micro's spike build was 203.65 kB CSS
  (34.81 kB gzip) and 4,251.19 kB JS (1,045.34 kB gzip); emitted rules
  resolve facade-backed properties through `var(--color-background-surface)`
  and `var(--spacing-4)`. The final curated build was 205.54 kB CSS
  (35.30 kB gzip) and 4,251.18 kB JS (1,045.35 kB gzip).
- M1 live runtime evidence: on
  `http://localhost:4219/platform/warranty`, the spike's facade-backed
  content container measured background `rgb(17, 17, 17)` in the app's dark
  mode and `rgb(251, 251, 253)` after the existing theme control switched
  to light; padding and gap remained 16 px through Astryx's `--spacing-4`.
  The original dark mode was restored. The temporary surface declaration
  was then removed; the final migration replaces existing Astryx values
  one-for-one, so it introduces no new visual property.
- Plan 0124 adoption meter, before → after: whole frontend direct Astryx
  markup 704 → 695, catalog markup 397 → 406, raw markup unchanged at
  1479; the migrated warranty page direct Astryx markup 9 → 0 and catalog
  markup 9 → 18. Whole-frontend token references 1707 → 1708; warranty
  token references 9 → 10 with token share remaining 1.0. The movement is
  exactly the nine migrated primitives; token identity remains in-guardrails
  because the scanner recognizes the facade's `*.stylex` import.
- Final validation: catalog and generated-client TypeScript checks passed;
  generator and full `go test ./...` passed; client conformance passed 16
  tests; Micro passed 73 frontend tests, lint, typecheck, production build,
  generation check, `make verify`, and the worktree-local
  `make verify-scenery`; `scenery harness self --summary --write` passed all
  21 reported lanes and wrote `.scenery/harness/self-latest.json`.

## Interfaces and Dependencies

- `ui/tokens.stylex.ts` (new), `ui/index.ts` (extended), `ui/embed.go`
  (directive extended), `ui/AGENTS.md` (contract amended).
- `internal/generate`: no new logic expected; fixtures/tests assert the
  new materialized file and refreshed goldens. Generated pages keep
  importing `./scenery-ui/index.js` and do not consume the facade in
  this plan.
- Peer dependencies unchanged: React, Astryx, StyleX supplied by each
  app (`ui/package.json`); the facade imports Astryx exactly as catalog
  components already do.
- Consuming-app contract (documented, app-owned): tsconfig path
  mapping, Vite alias, and StyleX-plugin resolution for
  `@scenery/ui/tokens.stylex` → materialized
  `react/scenery-ui/tokens.stylex.ts`.
- Plan 0122's `ui_catalog` live mode is the M1/M2 iteration loop; plan
  0124's report is the adoption meter (no changes needed there — its
  scanner treats any `*.stylex` import as token identity).
