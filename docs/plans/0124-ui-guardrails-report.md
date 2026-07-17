# 0124 UI Guardrails Report: Design-System Adherence Inspection

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

## Purpose / Big Picture

Scenery's UI initiative gives agent-built apps two guardrails: the Astryx
design system (`@astryxdesign/core` components and `tokens.stylex` theme
tokens, styled with StyleX) and the binary-owned `@scenery/ui` catalog
(`ui/` in this repo, materialized into each React-enabled client). Agents
writing app frontends drift outside those guardrails in ways nobody can see
today: hand-rolled `<div>`/`<table>` layout where `VStack`/`QueryTable`
exist, hardcoded hex colors and pixel sizes where theme tokens exist, inline
`style={}` props. The human operating the app has no ranked view of where
the drift is worst, so "rewrite the sloppiest page first" is guesswork.

This plan adds a read-only inspection surface:

    scenery inspect ui [--frontend <name>] [--app-root <path>] -o human|json

It scans each declared frontend's hand-authored `.tsx`/`.jsx` source and
reports, per file, two independent adherence axes plus a combined slop
score:

- **Markup axis**: JSX tags classified by origin — Astryx design-system
  component, `@scenery/ui` catalog component, raw HTML element, local
  custom component, third-party library component, SVG icon internals —
  and the resulting design-system share of markup.
- **Style axis**: design values inside `stylex.create` classified as theme
  token references vs hardcoded colors (`#hex`, `rgb()`, `hsl()`,
  `oklch()`) vs hardcoded sizes (`px`/`rem`/`em`), plus true inline
  `style={}` props, and the resulting token share.

Success looks like: running `scenery inspect ui -o json` against
`~/Repos/Micro/platform` ranks `apps/platform/src/pages/invoices.tsx` and
the `analytics-*` family at the top with low markup share and high token
share, matching the manual audit recorded in Artifacts and Notes — and the
human immediately has a rewrite queue.

A second stage (check-time enforcement with ratcheted diagnostics) is
sketched as milestone M6 but is explicitly deferred until the report's
metric definitions survive at least one real cleanup driven by the report.

## Progress

- [x] (2026-07-17) Plan authored; prototype validated against
  `onlv/apps/next` and `Micro/platform/apps/platform` (see Artifacts).
- [x] (2026-07-17) M1 scanner package `internal/uireport` with golden
  fixture tests covering comments, strings, templates, bracket tokens,
  `xstyle`, inline style, SVG, import origins, raw values, and undefined
  shares.
- [x] (2026-07-17) M2 frontend resolution and generated/materialized
  exclusions, including escaping/symlink root protection, empty apps, legacy
  design-system-free summaries, generated markers, tests, dependencies, and
  build output.
- [x] (2026-07-17) M3 CLI surface `scenery inspect ui`, `--frontend`,
  exact JSON schema/revision, default human table, CLI contract/schema tests,
  and help/drift/self-harness schema registration.
- [x] (2026-07-17) M4 docs: local-contract, agent-guide, SKILL.md,
  knowledge index, architecture map, package-local instructions, and
  root AGENTS.md command list.
- [x] (2026-07-17) M5 validation: focused packages, full CLI, `go test
  ./...`, and 21-step worktree-local self-harness green; final JSON and human
  runs against Micro/platform and ONLV passed every recorded acceptance
  assertion. The `/tmp` prototype was deleted.
- [x] (2026-07-17) M6 remains deliberately deferred to a separate activation
  after a real report-driven cleanup; no baseline writes, diagnostics, or
  check-time enforcement shipped in this plan.

## Surprises & Discoveries

- (2026-07-17) Prototype findings that shaped the design, from
  `/tmp/ui-guardrails.mjs` runs (script preserved in Artifacts): the two
  axes fail independently, so a single blended score would hide real
  drift. `Micro/platform/apps/platform` (57 hand-authored files) measured
  88% token share but only ~35% design-system markup share (1636 raw HTML
  tags vs 901 Astryx + 409 catalog tags; hand-rolled tables, toolbars,
  pagination). `onlv/apps/next` (21 files) measured the inverse: ~57%+
  markup share but 68% token share (38 hardcoded colors including brand
  hexes `#F03E88`/`#FF5C9E` in `nav/side-navigation.tsx` and sky
  gradients in `viewer/SceneViewer.tsx`), and zero `@scenery/ui` usage.
- (2026-07-17) Naive `style={` counting is wrong: Astryx's `xstyle={}`
  style-slot prop contains the substring and is *inside* the guardrails.
  The first prototype run reported 86 inline styles in `apps/next`; after
  excluding `xstyle`, the true count was 1. Any implementation must match
  `style=` as a whole attribute name.
- (2026-07-17) Astryx tokens are accessed with bracket syntax
  (`colorVars["--color-border"]`), not dot access, and StyleX values can
  be nested responsive objects (`{ default: ..., "@media ...": ... }`) or
  template literals. Token detection must key off *imported identifier
  names* from `*.stylex` modules, not off value-shape regexes.
- (2026-07-17) The tokenizer-aware implementation intentionally reports
  slightly lower absolute markup/token counts than the regex prototype while
  preserving its ranking and ratios. Live `go run ./cmd/scenery inspect ui`
  measured Micro at 88.2% token share and 31.5% markup share with
  funding/change-orders/invoices as the top three; ONLV next measured 68.5%
  token share, exactly one inline style, and SceneViewer/scene/mails as the top
  three. The difference is expected evidence that strings, comments, and
  non-binding import text are no longer classified.

## Decision Log

- (2026-07-17, Claude + petr) Surface is `scenery inspect ui`, not a new
  top-level command family and not a harness lane. Rationale: `inspect`
  is the existing read-only noun family agents are told to prefer; a
  triage report is inspection, not validation. `scenery harness ui`
  (browser harness) lives in a different namespace; no collision.
- (2026-07-17, Claude + petr) Report the markup axis and the style axis
  as separate fields in schema and human output; the slop score is a
  convenience ranking, never a replacement. Rationale: the Micro vs onlv
  evidence above — each app passed one axis and failed the other.
- (2026-07-17, Claude + petr) What counts as "the design system" is
  built-in (`@astryxdesign/*` modules, `@scenery/ui` catalog,
  `*.stylex` token modules), not configurable. Rationale: one rolling
  Scenery specification, one UI stack; repo rules resist new knobs. A
  second design system would be a contract change, not a config option.
  Note (2026-07-17): Astryx is maintained by Meta, so absorbing its
  source into `ui/` is ruled out (it would fork an upstream package).
  The plausible "one import surface" shape is the shadcn model, which
  scenery already structurally has: `@scenery/ui` is the owned wrapper
  layer (shadcn role) over Astryx as the maintained engine (Radix
  role). The path is growing catalog-owned wrappers for the Astryx
  primitives apps actually use (per this report's import counts:
  `Text`, `Button`, `IconButton`, `Badge`, `TextInput` first) so app
  markup imports only `@scenery/ui` — not re-exporting or vendoring
  Astryx itself. Tokens can follow the same model despite StyleX's
  defining-module import rule: the catalog can ship its own
  `tokens.stylex.ts` whose `stylex.defineVars` values *reference* the
  imported Astryx var group members (Astryx itself uses this exact
  cross-module composition in `dist/Layout/container.stylex.js`, and
  its token defaults resolve to plain named custom properties like
  `var(--color-accent)`, so theming still flows from the one Astryx
  theme at runtime). Apps would then import tokens from the
  materialized `@scenery/ui` token module — a deliberate subpath
  exception to the index-only export rule, since StyleX forbids barrel
  re-exports. Needs a compile-level verification in a real consuming
  Vite app before being relied on. Either way both prefixes stay
  in-guardrails; the scanner keys token identity off imports from any
  `*.stylex` module, so this migration would not change the report
  contract, and the separate `markup.design_system` / `markup.catalog`
  counters are exactly the instrument that would measure it. This
  direction is now planned as ExecPlan 0125
  (`docs/plans/0125-scenery-ui-single-surface.md`).
- (2026-07-17, Claude) v1 scanning is a dedicated Go lexical scanner in
  `internal/uireport`, not an AST from the managed TypeScript checker.
  Rationale: `internal/tscheck` wraps the managed `tsgo` binary as a
  *checker* (exec + diagnostics output); it does not expose a stable AST
  dump surface, and the classifications needed (import origins, JSX tag
  names, `stylex.create` value literals) are syntax-level. The scanner
  must be tokenizer-grade (string/template/comment aware), not regex
  line-matching, and is pinned by golden fixtures. Revisit tsgo-based
  AST extraction only if fixtures prove the scanner insufficient.
- (2026-07-17, Claude + petr) Enforcement (M6) is deferred and will be
  warning-first with a committed baseline ratchet ("no new slop") rather
  than absolute thresholds. Rationale: heuristic hard failures would
  teach agents to route around the check; ratchets let existing debt
  stand while blocking regressions. Not activated by this plan's M1–M5.
- (2026-07-17, Codex) Frontends with no Astryx, `@scenery/ui`, or
  `*.stylex` imports retain a zero-value totals object but omit file rows.
  Rationale: a stable object shape is easier for exact machine consumers,
  while the empty rows keep legacy stacks quiet as planned.
- (2026-07-17, Codex) The human table is the default output only for
  `inspect ui`; all older inspect subjects retain their JSON requirement.
  Rationale: the ranked queue is directly useful to a human, while automation
  still receives the singular `scenery.cli` envelope with `-o json`.

## Outcomes & Retrospective

Completed 2026-07-17.

Scenery now exposes `scenery inspect ui` as a stable, read-only inspection
subject with a default ranked human table, an exact `scenery.cli` JSON payload,
one-frontend filtering, and a checked schema. The standard-library
`internal/uireport` package separates markup adoption from style-token
adoption, excludes generated/dependency/test/build source, collapses legacy
frontends to quiet summaries, and pins its tokenizer-aware rules with golden
fixtures.

Final live evidence:

- Micro/platform: 57 files; funding, change-orders, and invoices were the top
  three; markup share 0.315; token share 0.882.
- ONLV next: SceneViewer, scene, and mails were the top three; token share
  0.685; exactly one inline style. ONLV pulse and ui reported
  `design_system: "none"` with no file rows.
- `go test ./internal/uireport`, `go test ./cmd/scenery`, and `go test ./...`
  passed. A freshly built `.scenery/harness/bin/scenery` completed all 21
  self-harness steps. The first self-harness pass hit a stale child-envelope
  specification during the storage probe; the immediate rerun with the freshly
  rebuilt worktree-local binary passed, with no product change needed.

The regex prototype overcounted syntax that the real scanner correctly masks,
so absolute counters moved while the intended ranking and acceptance ratios
held. The deferred enforcement sketch remains useful future context, but the
report needs to drive an actual cleanup before its heuristic metrics become a
ratchet.

## Context and Orientation

Terms:

- **Frontend**: a named Vite/React app declared under `frontends` in the
  app root's `.scenery.json` (parsed in `internal/app/root.go`), e.g.
  `"platform": { "root": "apps/platform" }` in
  `~/Repos/Micro/platform/.scenery.json`.
- **Astryx design system**: React components imported from
  `@astryxdesign/core/<Component>` and theme tokens imported from
  `@astryxdesign/core/theme/tokens.stylex` (StyleX variable objects like
  `colorVars`, `spacingVars`, accessed as `colorVars["--color-border"]`).
- **Catalog**: `@scenery/ui`, the binary-embedded component set under
  `ui/` in this repo, materialized into a client's
  `react/scenery-ui/` directory by `internal/generate` (see
  `internal/generate/catalog.go` and `ui/AGENTS.md`).
- **Hand-authored source**: frontend files excluding the materialized
  catalog, generated client output (directories `internal/generate`
  writes with ownership markers, e.g. `src/generated/` in Micro),
  `node_modules`, and test files.

Relevant existing code:

- `cmd/scenery/inspect.go` — `inspect` subject dispatch
  (`parseInspectArgsInternal`, subject switch around line 196), the
  `scenery.cli` envelope writing, and per-subject response structs.
  `inspect_docs.go` / `inspect_observability.go` show the pattern for a
  subject with its own flags and JSON payload.
- `internal/app/root.go` — `.scenery.json` loading including `frontends`
  and env config; gives frontend names and roots.
- `internal/generate/catalog.go` and
  `internal/generate/generate_typescript_react.go` — how materialized
  catalog and generated pages are laid out and marked, which defines the
  exclusion rules.
- `docs/schemas/` — one JSON Schema file per CLI payload; the envelope
  schema is `scenery.cli.schema.json`.
- `internal/spec/diagnostics_catalog.go` — checked-in diagnostic catalog
  (only touched by deferred M6).

The prototype that de-risked the metrics is a standalone Bun script,
preserved verbatim in Artifacts and Notes. It is a prototype: the Go
implementation replaces it, and the script is deleted from `/tmp` (never
committed) once M5 passes.

## Milestones

**M1 — Scanner package.** New package `internal/uireport` exposing
`Scan(files []SourceFile) FileReport` (pure, no filesystem) and the
classification rules. Golden fixture tests pin every rule, including the
`xstyle` and bracket-token traps from Surprises. Repo stays green.

**M2 — App resolution.** `internal/uireport` (or a small
`internal/uireport/collect.go`) walks an app root: enumerate declared
frontends from `.scenery.json`, collect hand-authored `.tsx`/`.jsx`
files, apply exclusions (materialized `react/scenery-ui/`, generated
output directories, `node_modules`, `*.test.*`). Frontends with zero
design-system imports are reported with `design_system: "none"` and no
per-file rows (legacy stacks like onlv `apps/pulse` produce a one-line
summary, not noise).

**M3 — CLI surface.** `scenery inspect ui` with `--frontend <name>`
filter, `-o human` ranked table (slop-descending, columns matching the
schema fields) and `-o json` payload under the `scenery.cli` envelope.
New schema `docs/schemas/scenery.inspect.ui.schema.json`. CLI JSON
contract test in `cmd/scenery`.

**M4 — Docs.** `docs/local-contract.md` (command grammar + schema),
`docs/agent-guide.md` (triage workflow: run report, rewrite top offender
onto catalog/Astryx, re-run), `SKILL.md` (one-line mention for agents
inside apps), root `AGENTS.md` preferred-commands list, and
`docs/knowledge.json`.

**M5 — Validation.** Suites green; live read-only runs against
`~/Repos/Micro/platform` and `~/Repos/onlv` reproduce the audit ranking
recorded in Artifacts (invoices/funding/change-orders/analytics on top
for Micro; SceneViewer/scene on top for onlv).

**M6 — Deferred enforcement.** Not part of initial delivery. Sketch:
`scenery inspect ui --write-baseline` writes a committed
`scenery.ui-baseline.json` at the app root recording per-file counts;
`scenery check` gains catalog diagnostics — an absolute warning for
hardcoded colors / inline styles in design-system frontends, and a
ratchet warning when a file's raw counts exceed its baseline. Requires
new SCN codes in `internal/spec/diagnostics_catalog.go` and its own
activation decision after the report has driven at least one real
cleanup. Do not implement in this plan without that decision.

## Plan of Work

The scanner is the heart; everything else is plumbing into existing
patterns.

`internal/uireport` scans source text with a small tokenizer that is
aware of strings, template literals, comments, and JSX text, so
classification never fires inside string content. From each file it
extracts:

1. Import bindings: local name → module specifier. Origin per binding:
   `ds` for `@astryxdesign/*`, `catalog` for `@scenery/ui`, `local` for
   relative or `@/` specifiers, `lib` otherwise. Bindings imported from
   module specifiers ending in `.stylex` are the token identifier set.
2. JSX open tags: lowercase tags are intrinsic (SVG-family tag names
   counted separately as `svg`); capitalized tags resolve their head
   identifier through the import map (unresolved → `local`).
3. `stylex.create` argument bodies (brace-matched): occurrences of
   member/bracket access on token identifiers count as `token_refs`;
   color-function/hex literals as `raw_colors`; numeric `px`/`rem`/`em`
   literals as `raw_sizes`. Neutral keywords (`flex`, `hidden`, `0`,
   percentages) count toward neither axis.
4. Inline style props: JSX attribute named exactly `style` with an
   expression value (`xstyle` and any `*style` suffixed prop excluded).

Per-file output: the raw counts, `ds_share` =
(ds+catalog)/(ds+catalog+intrinsic+local+lib), `token_share` =
token_refs/(token_refs+raw_colors+raw_sizes), and
`score = intrinsic + 2*raw_colors + raw_sizes + 3*inline_style_props`.
Shares are omitted (not zero) when the denominator is zero. Weights are
constants in one place with a comment pointing at this plan.

The CLI subject follows `inspect docs`: parse `--frontend`, resolve the
app root with the standard machinery, build the response, write the
envelope. Human output is a fixed-width table sorted by score descending
with per-frontend totals, mirroring the prototype's output shape.

JSON payload sketch (fields are the contract; nesting per schema):

    {
      "frontends": [{
        "name": "platform",
        "root": "apps/platform",
        "design_system": "astryx",
        "files": [{
          "path": "src/pages/invoices.tsx",
          "lines": 1532,
          "markup": {"design_system": 30, "catalog": 26, "raw": 132,
                      "local": 21, "lib": 9, "svg": 1, "ds_share": 0.25},
          "style": {"token_refs": 75, "raw_colors": 0, "raw_sizes": 22,
                     "inline_style_props": 0, "token_share": 0.77},
          "score": 154
        }],
        "totals": { /* same counter shape summed, shares recomputed */ }
      }]
    }

Per-page attribution (joining files to declared page resources through
the expanded graph's `react_component` slots) is intentionally out of
scope for v1; the per-file ranking is what triage needs. Record a
follow-up note in `docs/tech-debt.md` if page attribution is wanted.

## Concrete Steps

All commands run from the repository root
(`/Users/petrbrazdil/Repos/scenery`) unless stated.

1. Create `internal/uireport/` with `scan.go` (tokenizer +
   classification), `report.go` (types, shares, score), and
   `scan_test.go` using golden fixtures under
   `internal/uireport/testdata/` — at minimum: an Astryx-heavy page, a
   raw-div page, a `stylex.create` body with bracket tokens + responsive
   objects + template literals, an `xstyle` file, an SVG icon file.
   Run `go test ./internal/uireport`.
2. Add `collect.go`: frontend enumeration from the app config
   (`internal/app`), file walking, exclusion rules. Unit test against a
   fixture app-root tree in `testdata/`. Run `go test ./internal/uireport`.
3. Add subject `ui` to `cmd/scenery/inspect.go` dispatch, new file
   `cmd/scenery/inspect_ui.go` with response structs, `--frontend` flag,
   human table writer, and envelope JSON. Add
   `docs/schemas/scenery.inspect.ui.schema.json`. Add
   `cmd/scenery/inspect_ui_test.go` covering JSON contract (golden
   payload against a fixture app) and human output smoke.
   Run `go test ./cmd/scenery -run TestInspectUI`.
4. Docs pass (M4 list above). Update `docs/plans/active.md` status and
   this plan's Progress.
5. Validation matrix (below), then live runs:
   `cd ~/Repos/Micro/platform && scenery inspect ui -o json | head` and
   `scenery inspect ui --frontend platform` (human), same for
   `~/Repos/onlv` with `--frontend next`, using the worktree-local
   harness binary (`.scenery/harness/bin/scenery`), not `go install`.
   Compare rankings to Artifacts and record the outcome in Progress.
6. Delete `/tmp/ui-guardrails.mjs` (prototype retired; source preserved
   below).

## Validation and Acceptance

    go test ./internal/uireport
    go test ./cmd/scenery
    go test ./...
    scenery harness self --summary --write

Acceptance:

- `scenery inspect ui -o json` against the Micro platform app root emits
  a valid `scenery.cli` envelope whose data validates against
  `scenery.inspect.ui.schema.json`, ranks `src/pages/invoices.tsx`,
  `src/pages/funding.tsx`, `src/pages/change-orders.tsx` in the top
  five by score, and reports app-level token share ≥ 0.8 with markup
  ds_share ≤ 0.45 (matching the recorded audit within rounding).
- Against onlv, frontend `next` reports token share near 0.68 and
  exactly 1 inline style prop; frontends `pulse` and `ui` collapse to
  `design_system: "none"` summaries.
- Golden fixtures pin: `xstyle` never counts as inline style; bracket
  token access counts as `token_refs`; SVG internals never count as raw
  markup; nothing inside string literals or comments is classified.
- `scenery inspect ui` on an app root with no React frontends exits 0
  with an empty `frontends` array, not an error.

## Idempotence and Recovery

Every step is additive and read-only at runtime: the command never
writes to the target app (M6's `--write-baseline` is deferred). Re-runs
of any test or CLI invocation are idempotent. If a step fails midway,
re-run it; there is no state to recover. If the schema shape must change
after M3 lands, change schema + payload + golden tests in the same
commit — machine-readable contracts must not drift piecewise.

## Artifacts and Notes

Audit evidence the acceptance ranking is pinned against (2026-07-17,
prototype `bun /tmp/ui-guardrails.mjs <src-dir>` at each app root):

- Micro `apps/platform` (57 hand-authored files, `generated/` and
  `scenery-ui/` excluded): tags ds=901 catalog=409 raw=1636 svg=113
  local=834; style tokens=2190 raw_colors=33 raw_sizes=265 (88% token
  share); inline=18. Top scores: invoices.tsx 154, funding.tsx 140,
  change-orders.tsx 115, commissions.tsx 103, analytics-ui.tsx 97,
  inventory.tsx 93 (2201 lines). The `analytics-*` family has 0–7%
  markup ds_share. `invoices.tsx` spot-check: zero `VStack`/`HStack`,
  28 hand-written flex/grid declarations, hand-rolled
  toolbar/search/pagination duplicating `QueryTable` chrome.
- onlv `apps/next` (21 files): tags ds=195 catalog=0 raw=66 svg=110
  local=82; style tokens=222 raw_colors=38 raw_sizes=65 (68% token
  share); inline=1. Top: SceneViewer.tsx 75 (24 raw colors — sky
  gradients), scene.tsx 51, mails.tsx 25 (86% markup share, 15 raw rem
  sizes). Hardcoded brand pinks `#F03E88`/`#FF5C9E` in
  `nav/side-navigation.tsx`.
- onlv `apps/pulse`: raw=1714, zero Astryx/StyleX (radix + cva legacy);
  `apps/ui` similar. These are the `design_system: "none"` cases.

The prototype script logic (import-origin mapping, brace-matched
`stylex.create` extraction, SVG tag set, `(?<![A-Za-z])style=\{`
inline-style rule, score weights) is the reference for M1's scanner
rules; the Go scanner supersedes its regex approximations with real
tokenization.

## Interfaces and Dependencies

- `internal/uireport` (new): pure scan + collect API used by
  `cmd/scenery`. No new third-party dependencies — standard library
  only; classification is lexical, no TypeScript typechecking.
- `cmd/scenery/inspect.go` dispatch + new `inspect_ui.go`: follows the
  existing `scenery.cli` envelope and exit-code conventions in
  `internal/machine`.
- `internal/app/root.go`: source of frontend names/roots from
  `.scenery.json`; no changes expected beyond read access.
- `internal/generate` layout knowledge (materialized `react/scenery-ui/`
  and generated-output ownership markers) defines exclusions; prefer
  reusing exported path/marker helpers over duplicating literals.
- `docs/schemas/scenery.inspect.ui.schema.json` (new): payload contract;
  referenced from `docs/local-contract.md`.
- Deferred M6 would additionally touch
  `internal/spec/diagnostics_catalog.go` (new SCN codes) and the
  `scenery check` pipeline; nothing in M1–M5 may depend on that.
