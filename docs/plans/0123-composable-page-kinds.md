# 0123 Composable Page Kinds: content_page Shell, Astryx QueryTable, Shared Request State

## Purpose / Big Picture

Scenery generates React pages from `.scn` declarations. Today there are two generated page kinds and they do not compose: `split_page` renders the catalog's `SplitPage` layout (sidebar + detail), while `table_page` renders `TablePage` under `ui/pages/TablePage/`, a standalone component that owns its own page chrome (`<main>`, `<h1>` header), its own theming system (`theme.css` with `--scenery-ui-*` CSS variables and hardcoded hex fallbacks), and raw HTML controls (`<table>`, `<select>`, `<input>`, `<button>`). That violates the catalog's own contract in `ui/AGENTS.md` ("Use Astryx components and tokens with StyleX"), ignores the existing Astryx-based `ui/components/DataTable.tsx`, does not follow app theme or dark mode, and sets the wrong precedent: every future content type (form page, dashboard page) would fork another full page with its own chrome.

This plan restructures generated pages into three layers:

1. **One single-column page shell**: a new `content_page` source kind whose layout is the existing catalog `Page` component (title, header actions, scrollable centered column). It is the only generated one-column page.
2. **Content components own content**: the table experience becomes `QueryTable`, a chrome-less catalog component built from Astryx (`DataTable` for the grid, Astryx controls for filters/sort/pagination) that keeps the current query/filter/cursor `load` contract.
3. **Thin `.scn` sugar**: `table_page` remains a declaration (its columns/filters/sorts contract bound to a CRUD list is worth the ergonomics) but expands to a `content_page` whose content is the generated `QueryTable` wiring; its `toolbar` maps to the page `actions` slot.

The plan also unifies the three request-state vocabularies in the catalog (`SplitPageState`/`SplitPageProblem`, `TablePageResult`/`TablePageProblem`, and `QueryState`'s `error/isPending/isEmpty` props) into one shared module, and fixes generator correctness bugs found during review (JSX attribute escaping, missing `popstate` handling, divergent error policy, UTF-8 label corruption).

A future page kind then costs a content component plus sugar, not a new page shell. The naming decision is recorded in the Decision Log: `content_page` (not `stack_page`, `single_page`, or `detail_page`).

## Progress

- [x] Milestone 1: generator correctness fixes and shared emission helpers (completed 2026-07-17)
- [x] Milestone 2: shared request-state module in the catalog (completed 2026-07-17)
- [x] Milestone 3: `content_page` source kind end to end (completed 2026-07-17)
- [ ] Milestone 4: `QueryTable` on Astryx; `table_page` recomposes onto `content_page`; `ui/pages/` removed
- [ ] Docs, SKILL.md, local contract, cookbook, and conformance fixtures updated
- [ ] Final validation matrix green

2026-07-17: Plan created from a review session; no implementation started.
2026-07-17: Completed Milestone 1. Generated JSX attributes now use JavaScript string expressions, split-page selection follows `popstate`, both page renderers share client/load scaffolding and render unexpected failures, and `humanLabel` uppercases a decoded Unicode rune instead of a byte.
2026-07-17: Completed Milestone 2. The catalog now exports one `Problem` / `RequestState` vocabulary and `queryStateProps`; split and table state types are aliases over it, without changing generated page output.
2026-07-17: Completed Milestone 3. `content_page` now exists in the current source schema, compiler expansion and validation, generated React routing, catalog slot types, staged fixture-client compilation, and public docs. Its generated output is stable across consecutive renders.

## Surprises & Discoveries

Recorded from the motivating review (2026-07-17), before implementation:

- `ui/pages/TablePage/TablePage.tsx` uses zero Astryx components and renders its own page chrome; `ui/pages/TablePage/theme.css` defines a parallel `--scenery-ui-*` CSS-variable theme with hardcoded hex fallbacks disconnected from Astryx StyleX tokens. Evidence: read of both files; no `@astryxdesign` import exists in either.
- `TablePageProblem` (`ui/pages/TablePage/contract-types.ts`) and `SplitPageProblem` (`ui/components/SplitPage.tsx`) are identical shapes, and `QueryState` (`ui/components/QueryState.tsx`) speaks a third vocabulary, so the documented "use QueryState in slots" path requires hand-written adapters in every app.
- Datetime filter overrides are mistyped: `TablePageSlots.filters` types every entry `TablePageFilterProps<string>` while the `TablePageFilter` datetime arm declares `TablePageFilterProps<TablePageDateTimeRange>`.
- Generator emits authored strings with Go `strconv.Quote` directly into JSX attribute positions (`title=%s`, `sidebarTitle=%s`, labels). JSX attribute literals do not process backslash escapes, so a title containing a double quote produces malformed JSX.
- Generated split pages seed selection from the URL and `pushState` on change but install no `popstate` listener, so browser Back leaves rendered selection stale.
- Unexpected (non-`SceneryClientError`) load errors map to an error state in split pages but are rethrown in table pages.
- `humanLabel` in `internal/generate/generate_typescript_react.go` uppercases `parts[index][:1]`, a byte slice, corrupting labels that start with a multi-byte UTF-8 rune.

Add new discoveries here with evidence as implementation proceeds.

## Decision Log

- 2026-07-17, maintainer + agent review: name the one-column kind `content_page`. `page` is taken (core resource kind that sugar expands into); `stack_page` (SwiftUI `NavigationStack` analogy) misleads because the kind does no push/pop navigation; `single_page` collides with "single-page app"; `detail_page` overloads `split_page`'s `detail` slot. Precedent: .NET MAUI `ContentPage`; the family reads as "the content is X" (`table_page`) / "the layout is X" (`split_page`) / "the content is yours" (`content_page`).
- 2026-07-17: keep `table_page` as a `.scn` kind. Its authored contract (columns/filters/sorts bound to a CRUD list) is distinct and valuable; only its expansion changes to compose `content_page` + `QueryTable`.
- 2026-07-17: unify unexpected-error policy on the split-page behavior (map to `{ kind: "error", name: "unexpected" }`) rather than rethrowing; `TablePageResult` already has the error arm so the change is type-safe and renders instead of producing unhandled rejections.
- 2026-07-17: fix JSX escaping by emitting brace-wrapped expressions (`title={"..."}`) for every authored string in attribute position; inside braces `strconv.Quote` semantics are correct. Do not attempt JSX-literal escaping rules.
- 2026-07-17: no back-compat shims. Scenery has one rolling specification; `ui/pages/`, `theme.css`, and the `--scenery-ui-*` CSS token surface are removed, and docs that promised "catalog CSS tokens" customization are rewritten to "declared slots and Astryx tokens" in the same change.
- 2026-07-17, agent: model `RequestState` as a discriminated union over the result-arm fields, so `SplitPageState<Data>` can specialize it with `{data: Data}` and `TablePageResult<Row>` with `{items, nextCursor}`. This gives both contracts one loading/result/error vocabulary while keeping Milestone 2 output-identical.

Record further decisions with date, author, and rationale.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Terms:

- **Catalog**: `ui/` in this repo is the single editable source of `@scenery/ui`. The Scenery binary embeds it (`ui/embed.go`) and materializes it into each React-enabled TypeScript client under `react/scenery-ui/` (`internal/generate/catalog.go`; entry list `uiCatalogEntries` mirrors the embed). `envs.local.ui_catalog` (ExecPlan 0122) live-syncs catalog edits into a running client app.
- **Generated pages**: `internal/generate/generate_typescript_react.go` renders one `<name>.generated.tsx` per `table_page`/`split_page` plus `pages.generated.ts`. Generation is staged and typechecked by the managed native TypeScript checker before commit (diagnostics SCN6320/6321/6322).
- **Source kinds and expansion**: `table_page` and `split_page` are `.scn` declarations validated by schemas in `internal/spec/source_schemas.go` and expanded to ordinary page/renderer resources by `internal/compiler` (see `internal/compiler/split_page.go` and `split_page_test.go`; ExecPlan 0120 owns that mechanism). `content_page` follows the same path.

Key files (repository-relative):

- `ui/components/PageLayout.tsx` — `Page`, `PageShell`, `PageHeader` (the shell `content_page` reuses); `ui/components/SplitPage.tsx` — split contract with `sidebar`/`detail` slots; `ui/components/QueryState.tsx`; `ui/components/DataTable.tsx`; `ui/pages/TablePage/` — to be deleted; `ui/index.ts` — public surface; `ui/embed.go`.
- `internal/generate/generate_typescript_react.go`, `internal/generate/catalog.go`, golden/conformance fixtures under `internal/generate/testdata/` (`typescript_client_conformance.test.ts`, `tsconfig.catalog.json`, `tsconfig.generated-clients.json`), `internal/generate/generate_typescript_split_page_test.go`, `internal/generate/catalog_test.go`.
- `internal/spec/source_schemas.go`, `internal/spec/source_schema_metadata.go`, `internal/spec/catalog_test.go`; `internal/compiler/split_page.go` as the expansion template; `internal/compiler/ui_validate.go`.
- Docs to update together: `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, `docs/app-development-cookbook.md`, `ui/AGENTS.md`, `internal/generate/AGENTS.md`, `internal/compiler/AGENTS.md`, `docs/knowledge.json`, `docs/plans/active.md`.

Read `internal/compiler/AGENTS.md`, `internal/generate/AGENTS.md`, `internal/spec/AGENTS.md`, and `ui/AGENTS.md` before editing those subtrees.

## Milestones

Each milestone leaves the repo testable and shippable on its own.

**Milestone 1 — generator correctness and shared emission helpers.** In `internal/generate/generate_typescript_react.go`: emit authored strings in attribute positions as brace-wrapped expressions; add a `popstate` listener to generated split pages so Back/Forward re-reads the query parameter; unify unexpected-error mapping on the error-state policy; fix `humanLabel` to decode the first rune (`unicode`/`utf8`); extract `writeReactPageOpen` (exported page function + client wiring) and `writeReactLoad` (try/catch load skeleton parameterized on signature, state type, call emission, and result expression); add package-level `splitPageSlotNames` with a comment naming its two coupled ends (TS contract, source schema) and the alias-numbering constraint. Golden updates are expected for the escaping, popstate, and table-error-policy diffs; add a regression fixture with a double quote in a `table_page` title.

**Milestone 2 — shared request-state module.** Add `ui/components/request-state.ts` (name final at implementation): one `Problem` type, one `RequestState<Data>` (`loading` / `result` / `error` arms as today), and a `queryStateProps(state, resource)` helper mapping a `RequestState` onto `QueryState` props including a problem-aware `getErrorMessage`. `SplitPageState`/`SplitPageProblem` and the table result/problem types become aliases or direct re-exports of the shared shapes; `ui/index.ts` exports the module once. No behavior change in generated output.

**Milestone 3 — `content_page` end to end.** Schema: `content_page` with `title`, `path`, optional `aria_label`, optional `max_width`, `source` binding, required `content` slot, optional `actions` slot (slot children reference `react_component` resources exactly as `split_page` slots do). Compiler: expansion mirroring `internal/compiler/split_page.go` with tests mirroring `split_page_test.go`. Generator: `selectedReactContentPages` + `renderReactContentPage` composing Milestone 1 helpers; emitted page renders catalog `Page` with `title`/`maxWidth`, `actions` slot in the header, and the `content` slot receiving `RequestState`-based slot props (no selection concept). Wire into `renderTypeScriptReact` and `pages.generated.ts`. Golden test, conformance fixture, and docs (`local-contract.md` typescript_client section, `agent-guide.md`, `SKILL.md`, cookbook recipe).

**Milestone 4 — `QueryTable` and `table_page` recomposition.** Add `ui/components/QueryTable.tsx`: chrome-less, Astryx + StyleX only, composes `DataTable`, Astryx select/input controls for filters and sort, `QueryState`/`EmptyState` for states, keeps the `TablePageQuery`/`TablePageResult` load contract, fixes the datetime filter slot typing (slot map generic over the filter value type). Generator: `table_page` output becomes a `content_page` composition — page shell from `Page`, `toolbar` slot mapped to `actions`, `QueryTable` as content. Delete `ui/pages/` (component, contract-types, `theme.css`); move surviving contract types next to `QueryTable`; update `ui/index.ts`, `ui/embed.go`, and `uiCatalogEntries` in `internal/generate/catalog.go` (drop the `pages` entry or leave it tolerated-absent — it already skips missing entries; prefer dropping both ends in the same change). Update all goldens, the conformance suite, and every doc that mentions `ui/pages/`, `TablePage`, or catalog CSS tokens; consuming apps (for example `github.com/pbrazdil/onlv`, `Micro/platform`) regenerate and drop any `--scenery-ui-*` overrides in favor of Astryx tokens.

## Plan of Work

Work the milestones in order; 1 and 2 are independent of each other but both are prerequisites for 3, and 3 for 4. Within each milestone: change code, regenerate goldens deliberately (inspect the diff — every golden change must be explainable by the milestone), update the docs listed for that milestone in the same commit, and run the milestone's validation lane before moving on. Keep commits per milestone so a failed later milestone leaves earlier ones landed and green. Do not start Milestone 4 by editing generated output paths in client apps; clients only regenerate after the scenery-side change is complete.

## Concrete Steps

All commands run from the repository root unless stated.

1. Orientation: `grep -n "split-page\|split_page" internal/spec/source_schemas.go internal/compiler/split_page.go` and read `internal/generate/generate_typescript_split_page_test.go` to learn the golden layout. `scenery inspect docs -o json` for doc freshness before editing docs.
2. Milestone 1 edits in `internal/generate/generate_typescript_react.go`; run `go test ./internal/generate` and inspect golden diffs; add the quote-in-title fixture; run the conformance lane (step 6 commands).
3. Milestone 2 edits under `ui/`; run `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`; then `go test ./internal/generate` (materialization goldens include catalog bytes).
4. Milestone 3: schema in `internal/spec` (`go test ./internal/spec`), expansion in `internal/compiler` (`go test ./internal/compiler`), generator + goldens (`go test ./internal/generate`), then a fixture-app compile: add a `content_page` to the fixture app used by the split-page tests and assert the generated page typechecks via the staged checker path exercised in those tests.
5. Milestone 4: implement `QueryTable`, recompose the table generator, delete `ui/pages/`, update `ui/embed.go` + `uiCatalogEntries` together, re-run the full matrix.
6. Full validation each milestone end:
   - `go test ./...`
   - `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
   - `bun test internal/generate/testdata/typescript_client_conformance.test.ts`
   - `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json`
   - `scenery harness self --summary --write`
7. Docs pass per milestone (files listed in Milestones); after Milestone 4, sweep with `grep -rn "ui/pages\|TablePage\|scenery-ui-" docs/ SKILL.md README.md ui/AGENTS.md` and resolve every hit.
8. Observable end-to-end check: in a consuming app (a fixture app, or `Micro/platform` with `envs.local.ui_catalog` pointed at this repo's `ui/`), run `scenery up`, open a generated table page and a `content_page`, and verify: Astryx theming (dark mode follows the app), quote-bearing titles render, split-page browser Back updates the selected item, and a killed network produces the rendered error state instead of a console rejection.

## Validation and Acceptance

Acceptance is behavioral, not just compilation:

- All commands in Concrete Steps step 6 pass.
- A `table_page` with `title = "Say \"hi\""` generates valid TSX (regression test from Milestone 1).
- A generated split page updates its rendered selection on browser Back (manual check per step 8, plus a golden showing the `popstate` effect).
- A generated table page renders no raw `<select>`/`<input>`/`<button>` outside Astryx components, imports nothing from `ui/pages/`, and no `theme.css` is materialized into clients (assert in `catalog_test.go` that the materialized file set contains no `.css`).
- `content_page` renders catalog `Page` with `actions` and `content` slots; its golden is stable across two consecutive generations.
- Datetime filter overrides typecheck with `TablePageFilterProps<TablePageDateTimeRange>`.
- `scenery harness self --summary --write` reports no architecture or knowledge-contract regressions.

Per repo policy, do not run `go install ./cmd/scenery` during validation; use the self-harness worktree-local build (`.scenery/harness/bin/scenery`).

## Idempotence and Recovery

Generation is an atomic artifact-set transaction; re-running `scenery generate --target typescript_client.<name>` after a partial failure is safe. Golden tests regenerate deterministically; if a golden diff is not explainable by the current milestone, revert the golden and re-derive. Catalog deletion (Milestone 4) is recoverable through git; stale materialized catalog files in client apps are retired by `includeStaleUICatalogFiles` (ownership-marker checked), so clients recover by regenerating — no manual deletion in client repos. Each milestone is a separate commit; abandoning a later milestone leaves the repo green at the previous one.

## Artifacts and Notes

- Golden/conformance fixtures: `internal/generate/testdata/` (split-page goldens referenced from `generate_typescript_split_page_test.go`; catalog materialization assertions in `catalog_test.go`).
- The review that motivated this plan (2026-07-17) also fixed `split_page` naming to `sidebar`/`detail` and committed the AGENTS.md subagent policy; those are done and out of scope here.
- Client apps known to consume generated react pages: `github.com/pbrazdil/onlv`, `Micro/platform` (`apps/platform/src/generated/scenery/`). Their regeneration is operator work after release, not part of this repo's validation.

## Interfaces and Dependencies

- Builds on ExecPlan 0120 (table/split source kinds, staged tsgo verification, binary-owned catalog) and 0122 (`envs.local.ui_catalog` live iteration, used for step 8 verification).
- Public surface changes: new `content_page` source kind (schema + docs); `table_page` keeps its authored contract but its generated composition changes; `@scenery/ui` exports change (`QueryTable` and the shared request-state module in; `TablePage` and the `ui/pages/` subpath out); the `--scenery-ui-*` CSS token customization surface is removed. `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, and the cookbook must land in the same changes (Documentation Update Rules in AGENTS.md).
- No new Go or JS dependencies: `QueryTable` uses existing Astryx/StyleX peers; generator changes are standard library only.
