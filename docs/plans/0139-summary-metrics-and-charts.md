# Summary Metrics and Chart Blocks for Generated Pages

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.
Maintain this file according to `PLANS.md`.

## Purpose / Big Picture

The Micro platform's conversion wave (platform ExecPlan
`platform/docs/agent/exec-plans/active/0057-generated-page-conversion-wave.md`)
is converting six domains to generated pages with the templates that already
exist: `table_page`, `workspace_page`, `detail_page`, and `form_dialog`. Three
domains were deliberately excluded from that wave because they "need a
chart/summary-metrics block that no template provides yet": funding,
job-costing, and tickets. This plan builds that missing capability on the
scenery side so a follow-up platform wave can convert them.

The design is evidence-driven, not speculative. A shape survey of the three
excluded pages shows what they actually need:

- `funding.tsx` (1,818 lines): a hero `StatGrid` of clickable `StatTile`s that
  toggle a list filter (`milestoneFilter`), money-formatted tile values,
  sub-lines with record counts, and a second computed stats band — above an
  otherwise ordinary filtered table.
- `job-costing.tsx` (1,442 lines): `StatTile`s over money totals plus an
  expandable per-project cost-breakdown table (chevron rows revealing nested
  cost lines).
- `tickets.tsx` (1,267 lines): a bespoke table plus stat summaries. The
  "2 Chart" entries in 0057's shape survey are `lucide-react` `BarChart3`
  *icon* imports, not chart renderings; whether a real chart block is needed
  at all is a Milestone 0 decision, not an assumption.

This plan also harvests the two features ExecPlan 0131 deliberately deferred
("Milestone 6 remains intentionally absent: clickable tiles need no new
catalog API and datetime presets should be designed from a concrete adoption
page"): funding is now that concrete adoption page. The end state: a
`table_page` (or `workspace_page`) can declare a stats band whose tiles carry
typed formatted values and sub-lines, and whose tiles declaratively set or
clear a declared filter when clicked; date/datetime filters can declare preset
ranges; and — if Milestone 0 confirms the need — a small chart block renders a
declared aggregation without any new runtime dependency. The pilot proof is
the Micro funding page converted to a generated page under the standing parity
gate, unblocking the second platform wave.

## Progress

- [x] (2026-07-22) Initial plan drafted from the 0057 exclusion list and the
  funding/job-costing/tickets shape evidence.
- [x] (2026-07-22 18:20Z) Milestone 0: design survey — recorded feature list from the three
  excluded pages plus any 0057 wave findings; explicit in/out decision for
  the chart block and for hierarchical breakdown rows.
- [x] (2026-07-22 18:29 CEST) Milestone 1: declarative stats contract — tile appearance/format,
  sub-lines, tile→filter wiring, datetime filter presets; spec, compiler,
  diagnostics, SPEC.md.
- [x] (2026-07-22 18:29 CEST) Milestone 2: generation + catalog — generated stats interactions in the
  table request contract, catalog components, staged verification.
- [x] (2026-07-22 18:29 CEST) Milestone 3: pilot — Micro funding page converted, parity-verified,
  hand-written page deleted.
- [x] (2026-07-22 18:34 CEST) Milestone 4: docs/harness sync and plan registers.

## Surprises & Discoveries

- Observation: 0057's shape survey counted `BarChart3` lucide icon imports as
  "Chart" occurrences; no excluded page renders an actual chart today.
  Evidence: `grep -nE "svg|Sparkline|chartBar|distribution"
  apps/platform/src/pages/tickets.tsx` returns nothing; the only `Chart`
  matches in `tickets.tsx`/`job-costing.tsx` are `lucide-react` imports and
  icon usages (2026-07-22).
- Observation: Binding-backed date filters had generator support for paired
  `<name>_from`/`<name>_to` inputs but compiler validation incorrectly looked
  for one `<name>` input.
  Evidence: `resolveBindingTablePageSource` validated `shape.Fields[input]`
  while `renderReactTablePage` emitted `query.filters["<name>_from"]` and
  `query.filters["<name>_to"]`; Milestone 1 now validates and claims the same
  paired inputs the generated client sends.
- Observation: A fixed `predicate` could not previously participate in a tile
  action because it was omitted from generated query state.
  Evidence: generation now synthesizes one hidden typed filter option for an
  actionable predicate and uses that value to override the fixed request
  literal; compiler tests cover predicate target typing and generator tests
  cover the emitted override.
- Observation: Funding's default Total tile required an explicit clear action,
  not a fake filter value.
  Evidence: the contract now requires exactly one of `value` or `clear` for an
  actionable tile; the browser showed Total selected at 1,391 rows, M2 selected
  at 155 rows, and Total selected again after clearing.
- Observation: Copying the 209-code and mention-user support catalogs into
  every one of 1,618 Funding rows exceeded the canonical JSON 16 MiB budget.
  Evidence: the live API reported `canonical JSON exceeds 16777216-byte
  expansion budget`; the pilot now returns each catalog once and React Query
  shares that support response among workbench cells.
- Observation: Empty datetime strings exposed `Invalid Date` in generated
  cells during browser acceptance.
  Evidence: `QueryTable` now renders an em dash for empty datetime strings and
  preserves an invalid non-empty lexical value; the accepted first Funding row
  showed `—` for missing PTO instead of `Invalid Date`.

## Decision Log

- Decision: Scope this plan to capability (templates, catalog, generation) plus
  one pilot page; the six-domain conversion wave stays platform-owned in 0057.
  Rationale: 0057 already owns invoices→commissions with its own parity
  inventories and escalation rule; duplicating any of it here would create two
  owners for the same routes. Funding is excluded from 0057's wave precisely
  because it waits for this capability, so piloting on funding overlaps with
  nothing.
  Date/Author: 2026-07-22 / Claude (planning session with Petr).
- Decision: Treat the chart block as a Milestone 0 in/out decision instead of a
  committed deliverable.
  Rationale: The only concrete evidence of "charts" in the excluded pages is
  icon imports. Building a chart contract with no adoption page violates the
  0131 rule that catalog surface is designed from concrete pages. If Milestone
  0 (or 0057's Milestone 7 gap list) produces a real chart need, it enters
  scope with a named page; otherwise it is recorded as explicitly out.
  Date/Author: 2026-07-22 / Claude.
- Decision: Harvest 0131's deferred Milestone 6 here — declarative clickable
  tiles and datetime filter presets.
  Rationale: 0131 deferred both until "a concrete adoption page" demanded
  them. `funding.tsx` clickable milestone tiles toggling `milestoneFilter` are
  exactly that page.
  Date/Author: 2026-07-22 / Claude.
- Decision: No new runtime dependency for any visualization. Any chart or
  breakdown rendering that enters scope is a small catalog-owned SVG/StyleX
  component composed with Astryx primitives, checked Astryx-first.
  Rationale: Engineering rules require dependency growth to be intentional;
  the catalog composes Astryx and StyleX only, and the shapes in evidence
  (money tiles, count sub-lines, optional simple bars) do not justify a
  charting library.
  Date/Author: 2026-07-22 / Claude.
- Decision: Do not add a chart block or a hierarchical-breakdown block in
  0139.
  Rationale: Neither excluded production page renders a chart: both
  `BarChart3` occurrences are button icons. Job Costing's hierarchy and
  Tickets' expandable record surface match the existing `DataTable`/
  `table_page.row_detail` contract, so a second hierarchy abstraction would
  duplicate shipped capability without an adoption page.
  Date/Author: 2026-07-22 / Codex.
- Decision: Date presets use three current named ranges — `today`,
  `last_7_days`, and `month_to_date` — and materialize local-calendar
  inclusive from/to datetimes in the generated client.
  Rationale: Presets remain client sugar over the existing typed paired wire
  inputs, cover the concrete operational ranges in the surveyed pages, and do
  not introduce server-side date vocabulary or timezone negotiation.
  Date/Author: 2026-07-22 / Codex.

## Outcomes & Retrospective

Completed on 2026-07-22. Scenery now has one typed declarative stats surface:
table/workspace tiles format primary and optional sub-line values, render
semantic icons, and table tiles set, toggle, or clear a declared filter or
predicate through the existing request-state path. Date/datetime filters can
declare three local-calendar presets without changing server wire contracts.
No chart or hierarchy abstraction was added because the survey found no real
chart and existing row detail already owns hierarchy.

The Micro Funding pilot is fully cut over. Its generated route retains search,
status/financier filtering, sorting, CSV export, the guide and computed task
bands, project detail, three milestone editors, NF-code editing, threaded
milestone comments/replies/mentions/delete permissions, and the explicit EDGE
unavailable state. Authenticated browser acceptance exercised set/clear tile
filtering, formatted totals/counts, real table rows, project detail, and the
three-column comment dialog without mutating production data. The 1,818-line
hand-written `apps/platform/src/pages/funding.tsx` was then deleted and the
route was reloaded successfully from generated ownership.

Validation passed: focused spec/compiler/generator tests; catalog and generated
client TypeScript checks; conformance tests; fixture regeneration; `go test
./...` and `go test ./cmd/scenery`; worktree-local `scenery harness self
--summary --write`; Micro `scenery check`, `go test ./...`, `make
verify-scenery`, all four frontend lanes, `make verify`, and `scenery harness
-o json --write`.

## Context and Orientation

Scenery already has a `stats` block on `table_page` and `workspace_page`
(`internal/spec/source_schemas.go`, `tablePageStatsSourceSchema` /
`workspacePageStatsSourceSchema`): a `source` binding whose operation has unit
input and one flat numeric record, plus `tile` declarations naming result
fields (SPEC.md §"table_page", around line 2482). The catalog renders them
through `ui/components/StatTile.tsx`, whose `StatTile` already accepts
`onClick`, `active`, `tone`, `sub`, and `icon` — the *component* capability
exists; what is missing is the *declared contract* that generates those props:

- No way to declare a tile's click behavior (set/clear a declared filter or
  predicate) — today clickable tiles require a hand-written page.
- No tile value formatting (`money`, `count`, `percent`) or sub-line binding
  (second result field rendered under the value), so money dashboards like
  funding's cannot be expressed.
- No datetime/date filter presets (Today, Last 7 days, Month to date, custom
  range) on `table_page` filters.
- No declared aggregation/chart or hierarchical-breakdown surface (pending the
  Milestone 0 decision).

Relevant code:

- `internal/spec/source_schemas.go`, `internal/spec/schemas.go` — authored
  source schemas and resource schemas for pages; the diagnostic catalog lives
  in `internal/spec` (SCN2xxx compile diagnostics).
- `internal/compiler/table_page.go` — table_page validation/expansion,
  including the current stats contract checks; `detail_page.go` and
  `workspace_page.go` are the closest recent patterns for adding block
  validation with typed cross-references.
- `internal/generate/generate_typescript_react.go` and siblings — page
  component generation; the generated table request contract (filters,
  predicates, search, pagination) is what tile clicks must feed into.
- `ui/components/StatTile.tsx`, `ui/components/QueryTable.tsx`, `ui/index.ts`
  — catalog. Any new visual goes through `ui/AGENTS.md` rules (Astryx + StyleX
  composition, no raw interactive HTML, tokens via
  `@scenery/ui/tokens.stylex`).
- Fixture apps: `internal/compiler/testdata/house` (rich fixture; its
  `package.scn` already carries `table_page`, `workspace_page`, `detail_page`
  declarations) and `internal/compiler/testdata/native`. Regenerating both
  fixture clients is mandatory after compiler/generator changes.
- Micro platform (pilot): `/Users/petrbrazdil/Repos/Micro/platform`, domain
  package `funding/` with `package.scn`, hand-written page
  `apps/platform/src/pages/funding.tsx`, generated client under
  `apps/platform/src/generated/scenery/`. Platform plan 0057 documents the
  conversion recipe, the typed not-found rule (platform plan 0056), and the
  live dev URL `https://micro.scenery.sh/platform/*`.

## Milestones

0. **Design survey.** Read `funding.tsx`, `job-costing.tsx`, and `tickets.tsx`
   end to end; if 0057's Milestone 7 gap list exists by then, fold it in.
   Produce a recorded feature list in Artifacts: every stats band, tile
   behavior, formatted value, preset-range filter, breakdown/chart shape, with
   file/line evidence. Make and log the explicit in/out decisions: chart
   block (in only with a named adoption page and concrete shape) and
   hierarchical breakdown rows (likely a `table_page` concern — compare with
   the existing group/expansion contract before inventing a new block). The
   survey ends with the exact `.scn` grammar sketch for Milestone 1.
1. **Spec + compiler.** Extend the stats contract: `tile` gains `appearance`
   (`money`/`count`/`percent`/plain), an optional `sub` result-field binding
   with its own appearance, optional `icon` name, and an optional `filter`
   action (`filter = <declared filter name>`, `value = <literal>`; clicking an
   active tile clears it — funding's toggle semantics). Tile filter references
   must name a declared `filter` or typed `predicate` of the owning page and
   type-check the literal against it; new diagnostics follow the SCN26xx
   pattern with exact messages and suggestions. Add `preset` declarations to
   date/datetime filters (named ranges; server receives the same typed filter
   inputs — presets are client sugar, not new wire surface). Update SPEC.md
   prose, spec catalog tests, and compiler unit tests for expansion and every
   diagnostic. Run the spec-revision fallout recipe (fixture lock revisions).
2. **Generation + catalog.** Generated table pages wire declared tile clicks
   into the same request-state store the toolbar/filters use (set, toggle,
   clear), mark tiles `active` from current request state, format values
   (`money` uses the same locale-stable formatting as table money columns),
   and render presets in the filter UI. Catalog: extend `StatTile`/`StatGrid`
   only if the survey demands (props already cover click/active/tone/sub);
   add the preset picker to the filter toolbar components; add any surveyed
   chart/breakdown component as a catalog-owned SVG+StyleX composition.
   `ui/index.ts` exports, `catalog_contract_test.tsx` coverage, house fixture
   updated to declare clickable tiles + a preset filter, both fixture clients
   regenerated, staged catalog tsc and full Go suite green.
3. **Pilot: Micro funding page.** In `funding/package.scn`, declare the stats
   binding (computed totals per milestone — add the Go stats operation with
   tests if the contract lacks it), the table page with clickable milestone
   tiles, status filter, and any datetime presets the page needs; move
   irreducibly bespoke pieces (the funding guide banner, note summaries) into
   declared `react_component` slots. Follow 0057's recipe and gates verbatim:
   parity inventory first, `make verify` + all four `apps/platform` lanes,
   authenticated browser acceptance at
   `https://micro.scenery.sh/platform/funding` proving tile toggling drives
   the same rows as the hand-written page, then delete
   `apps/platform/src/pages/funding.tsx` in the same commit as the router
   cutover. Job-costing and tickets stay with the platform's follow-up wave;
   this plan converts exactly one page as capability proof.
4. **Docs/harness sync.** `docs/spec/SPEC.md` (done in M1),
   `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`,
   `docs/app-development-cookbook.md`, `ui/AGENTS.md` if the catalog contract
   changed, root `AGENTS.md` mental-model line for stats/presets, knowledge
   and plan registers (`docs/knowledge.json`, `docs/plans/active.md` →
   `completed.md`).

## Plan of Work

Work proceeds strictly in milestone order because each consumes the previous
one's output: the survey fixes the grammar, the grammar fixes generation, and
the pilot consumes the generated surface. Milestone 1 is additive — existing
`stats` declarations without `appearance`/`filter` keep today's behavior and
revisions only shift where fixtures adopt the new attributes. Milestone 2
regenerates both committed fixture clients in the same change (SCN6204
otherwise). Milestone 3 happens in the Micro repo and must not race 0057's
wave: coordinate on `apps/platform/src/router.tsx` by converting funding only
at a 0057 milestone boundary, and reuse the running converged dev session
rather than starting a second supervisor. If Milestone 0 pulls the chart block
into scope, it becomes its own milestone between 2 and 3 with the same
spec→generate→catalog structure, and the Decision Log records the adoption
page that justified it.

## Concrete Steps

Milestone 0 survey commands (from the Micro platform repo):

    grep -nE "StatTile|StatGrid|preset|milestoneFilter|setStatus" apps/platform/src/pages/funding.tsx
    grep -nE "StatTile|Chevron|expanded" apps/platform/src/pages/job-costing.tsx
    grep -nE "StatTile|service-call" apps/platform/src/pages/tickets.tsx

Scenery implementation loop (repo root `/Users/petrbrazdil/Repos/scenery`):

    go test ./internal/spec ./internal/compiler
    go test ./internal/generate
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go test ./...

Catalog and generated-client verification (repo root):

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json

Pilot loop (from `/Users/petrbrazdil/Repos/Micro/platform`):

    make verify-scenery
    go test ./funding/...
    cd apps/platform && bun run typecheck && bun run lint && bun test && bun run build
    make verify

Browser acceptance per 0057's recipe against
`https://micro.scenery.sh/platform/funding`; confirm mutated/filtered state via
`scenery db shell` where the UI alone is not proof.

## Validation and Acceptance

Milestone 1 is accepted when every new diagnostic has a unit test, SPEC.md
describes the stats/preset contract normatively, and `go test ./...` passes
with regenerated fixture lock revisions. Milestone 2 is accepted when the
house fixture declares a clickable tile and a preset filter, both fixture
clients are regenerated and committed, the staged catalog tsc and generated
clients tsc pass, and the conformance suite passes. Milestone 3 is accepted
under the standing parity gate: the parity inventory of `funding.tsx` is
written before conversion, every inventoried behavior demonstrably works on
the generated page in an authenticated browser session (tile toggle drives
row filtering both directions, money formatting matches, sub-line counts
match live data), `make verify` and the four frontend lanes pass, and the
hand-written page file is deleted with no remaining imports. The plan as a
whole is accepted when `scenery harness self --summary --write` (through the
worktree-local binary) passes and the docs layers in Milestone 4 are updated
in the same change set.

## Idempotence and Recovery

Spec/compiler/generator changes are ordinary code with tests; regeneration is
idempotent and fixture regeneration can be re-run at any point. The pilot's
dangerous step is deleting `funding.tsx`: it happens only after browser
acceptance, in the same commit as the router cutover, so one revert restores
the hand-written page wholly. Pilot browser mutations use disposable records
noted in Artifacts. If the pilot stalls on a contract gap, stop at the
milestone boundary, record the gap here, fix it in Milestones 1–2 terms, and
resume — the platform wave (0057) is not blocked by any state of this plan.

## Artifacts and Notes

Milestone 0 survey (2026-07-22):

- Funding has two metric bands. The hero band at
  `apps/platform/src/pages/funding.tsx:181-220` contains Total plus M1/M2/M3
  money tiles; the milestone tiles toggle one `milestoneFilter` value and
  show count sub-lines. The secondary ten-tile band at lines 464-510 mixes
  counts, money sub-lines, and success/warning/danger tones. The remainder is
  a complete-list funding workbench: status/financier/search/sort/export,
  collapsible task buckets (lines 270-306), inline money/date/status mutation,
  non-funded-code editing, project detail, threaded milestone notes, and the
  disabled EDGE submission explanation. No date preset exists in the old
  funding UI, so the pilot must not invent one merely to exercise the
  capability; the House fixture owns preset proof.
- Job Costing has four non-interactive money/count tiles at lines 267-282 and
  expandable project rows at lines 173-207/283-291. Its `BarChart3` at line 90
  is the Analytics button icon. Existing `table_page.row_detail` and
  `DataTable` expansion express the hierarchy; no chart or hierarchy block is
  justified.
- Tickets has five non-interactive count tiles at lines 308-312, one
  `DataTable` with expansion at lines 239-246/440-575, and mutation dialogs.
  Its `BarChart3` at line 164 is the Reports button icon; there is no plotted
  data.
- The 0057 retrospective confirms summary metrics as the only common blocker;
  richer workflow pieces remain typed `react_component` slots under the
  standing parity gate.

Milestone 1 grammar fixed by the survey:

    filter "created_at" {
      preset "today" { label = "Today" range = "today" }
      preset "week"  { label = "Last 7 days" range = "last_7_days" }
    }

    stats {
      source = binding.read_stats_http
      tile "ready_amount" {
        label          = "Ready"
        appearance     = "money"
        sub            = "ready_count"
        sub_appearance = "count"
        sub_label      = "projects"
        icon           = "calendar"
        filter         = "milestone"
        value          = "m2"
      }
    }

Milestone 3 browser acceptance (2026-07-22, authenticated, no mutations):

- `https://micro.scenery.sh/platform/funding` loaded 1,391 Funding rows and the
  four hero tiles. Total rendered `$22,500` / `1,618 milestones`; M1/M2/M3
  rendered money plus project-count sub-lines.
- Clicking M2 selected that tile and reduced the loaded result/secondary band
  to 155 rows; clicking Total cleared the filter and restored the full result.
- The first row rendered live milestone amount/date/status controls, NF-code
  action, project button, and comments action. Missing PTO rendered `—`.
- Project `PROJ-28863` opened its full detail dialog. Its comments action opened
  separate M1 Advance, M2 Install, and M3 PTO columns with mention-capable
  composers. The dialogs were closed without writing data.
- After deleting the hand-written page, a hard reload showed the same generated
  tiles and first live row; the final browser error-console check was empty.

Shape evidence gathered while drafting (2026-07-22): funding's hero tiles at
`apps/platform/src/pages/funding.tsx:186-220` toggle `milestoneFilter` with
`active` highlighting and `money(...)` values with count sub-lines;
job-costing's breakdown rows at `apps/platform/src/pages/job-costing.tsx:189`
use chevron expansion; no excluded page renders a real chart (the survey's
"Chart" hits are `lucide-react` `BarChart3` icons).

## Interfaces and Dependencies

Consumes: the existing `stats` singleton on `table_page`/`workspace_page`, the
generated table request-state contract (filters, typed predicates, search,
pagination), catalog `StatTile`/`StatGrid` (already supporting `onClick`,
`active`, `tone`, `sub`), and Astryx primitives only — no new runtime
dependency.

Produces: the declarative tile appearance/sub/filter contract and datetime
filter presets (this plan's core), optionally a chart/breakdown block if the
Milestone 0 decision pulls one in, and the funding conversion that unblocks
the second Micro wave.

Coordinates with: platform ExecPlan 0057 (owns the six-domain wave; its
Milestone 7 gap list feeds this plan's Milestone 0; funding/job-costing/
tickets conversions beyond the funding pilot stay platform-owned) and platform
ExecPlan 0056 (typed not-found pattern, mandatory if the pilot adds any
detail page). Harvests scenery ExecPlan 0131's deferred Milestone 6.
