# 0137 Role-Named Contract Files: app.scn, package.scn, app.lock.scn

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

Scenery's contract files currently repeat the tool name the branded `.scn`
extension already carries: `scenery.scn` (app root), `scenery.package.scn`
(per package), `scenery.lock.scn` (lock). `warranty/scenery.package.scn`
says "scenery" twice and its role only in the middle segment. Petr wants
role-named files.

The naming principle adopted: **the extension carries the tool, the
filename carries the role** (the `main.tf` / `package.json` school —
appropriate because `.scn` is branded; `Cargo.toml`/`go.mod` put the tool
in the filename only because their extensions are generic).

New names:

| Today | New | Notes |
|---|---|---|
| `scenery.scn` | `app.scn` | App-root marker; unambiguous since no other tool claims `.scn` |
| `scenery.package.scn` | `package.scn` | `warranty/package.scn` reads as what it is |
| `scenery.lock.scn` | `app.lock.scn` | Pairs with `app.scn`, names what it locks, sorts adjacent |

This is a **clean break** — no dual-name support, per the repo rule against
dead compatibility paths and because Scenery is pre-first-release with two
known consumers (the Micro platform repo and this repo's fixtures). The one
concession to migration UX: finding a legacy-named file is a precise,
actionable error ("rename `scenery.package.scn` → `package.scn`"), not a
silent ignore and not a working alias.

## Progress

- [ ] (2026-07-22) Plan authored; naming decided with Petr. Not started.
- [ ] Milestone 1: core rename (discovery, lock, compiler diagnostics,
  legacy-name rename hints).
- [ ] Milestone 2: repo sweep (cmd tools, evolution/generate, fixtures,
  docs, harness knowledge contract).
- [ ] Milestone 3: consumer migration (Micro platform repo) and live
  verification.

## Surprises & Discoveries

- (2026-07-22) Reference map at planning time: the three filenames appear in
  ~135 Go references. Load-bearing sites: `internal/scn/source.go` (~line
  531–534, the directory-walk discovery that special-cases all three names),
  `internal/compiler/lock.go` (lock path join + parse source IDs),
  `internal/compiler/compiler.go` (SCN3005 "module missing
  scenery.package.scn"), plus `cmd/scenery/{doctor,watch,contract_commands}.go`,
  `internal/evolution/changes_create.go`, and
  `internal/generate/generate_{typescript,application}.go`.

## Decision Log

- (2026-07-22, Petr) **Role-named files; drop the tool name from
  filenames.** The `.scn` extension is the brand carrier.
- (2026-07-22, Petr + agent) **`app.scn` / `package.scn` / `app.lock.scn`.**
  Rejected alternatives, recorded so they are not relitigated:
  package-named files (`warranty/warranty.scn`) — redundant with the
  directory, breaks uniform globs, renames with the package; short
  `scn.lock`-style names — lose the extension's file-type association.
- (2026-07-22, agent) **Clean break with a rename-hint error.** Discovery
  treats legacy names as a dedicated diagnostic (new SCN code) whose message
  names the exact old→new rename for that file. No alias period: an alias
  would immediately become a dead compatibility path, and pre-release is the
  cheapest moment this change will ever have.
- (2026-07-22, agent) **JSON config and generated artifact names are
  explicitly deferred.** `.scenery.json`, `scenery.toolchain.json`, and
  `scenery.typescript-client-generated.json` share the redundancy but are
  tool config / generated artifacts, not authored contracts; renaming them
  rides different machinery (env loading, toolchain, generator manifests).
  Decide separately after this lands rather than growing this plan's blast
  radius. Recorded here so the inconsistency is deliberate, not overlooked.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

- **Discovery**: `internal/scn/source.go` walks source trees; around lines
  531–534 it skips `scenery.lock.scn` and selects `scenery.package.scn`
  (packages) vs `scenery.scn` (root only). This is the single choke point
  for what counts as a contract file; the rename starts here, ideally
  hoisting the three names into named constants so the remaining ~130
  references consume constants, not string literals.
- **Lock**: `internal/compiler/lock.go` joins `<root>/scenery.lock.scn`,
  and uses the filename in parse/source IDs (positions in diagnostics).
- **Diagnostics**: `internal/compiler/compiler.go` SCN3005 message; the
  diagnostics catalog (`internal/spec/diagnostics_catalog.go`) gains the
  new legacy-name code.
- **Everything else** is a mechanical sweep over the ~135 references:
  `cmd/scenery` (doctor's guidance text, watch's file matching, contract
  commands), `internal/evolution` (change planning reads package files),
  `internal/generate` (workspace/package roots), tests, and the fixture
  apps (`find internal -name 'scenery*.scn'` — currently two `scenery.scn`
  and two `scenery.package.scn`, plus testdata app roots with locks).
- **Docs and harness**: `SKILL.md`, `docs/agent-guide.md`,
  `docs/local-contract.md`, `docs/app-development-cookbook.md`,
  `docs/spec/SPEC.md`, and the knowledge contract enforced by
  `scenery harness self` all name these files; the platform repo's
  `CLAUDE.md`/`ARCHITECTURE.md`/`AGENTS.md` files do too.
- **Consumers**: the Micro platform repo has `scenery.scn`,
  `scenery.lock.scn`, and ~27 `<domain>/scenery.package.scn` files. Watch
  paths, `make verify-scenery`, and any editor associations keyed on full
  filenames need the sweep there.
- Spec-revision note: filenames are not part of resource schemas, so **no
  builtin-lock digest churn is expected**; source IDs in diagnostics and
  workspace-revision hashing (`internal/compiler/compiler_workspace_revision.go`)
  DO include paths — expect contract-revision changes in consumers and
  regenerate their clients as usual.

## Milestones

1. **Core rename.** Introduce filename constants; switch discovery, lock
   loading, and SCN3005 to the new names; add the legacy-name diagnostic
   (walks that meet `scenery.scn`/`scenery.package.scn`/`scenery.lock.scn`
   emit "rename X → Y" with the file's path). Unit tests for discovery of
   new names and for each legacy hint. `scenery doctor` surfaces the same
   hint.
2. **Repo sweep.** Update every remaining Go reference through the
   constants; `git mv` the fixture apps' files; regenerate fixture clients;
   update all repo docs and the harness knowledge contract; full
   `go test ./...` plus `scenery harness self` green. Verify no literal
   `scenery.package.scn` remains outside historical plans/changelogs:
   `git grep -l 'scenery\.package\.scn' -- ':!docs/plans'` returns empty.
3. **Consumer migration.** In the Micro platform repo: `git mv scenery.scn
   app.scn && git mv scenery.lock.scn app.lock.scn` plus the ~27 package
   files (one `find -execdir git mv` sweep); update its
   `CLAUDE.md`/`ARCHITECTURE.md`/docs references and any Makefile/watch
   globs; install the new binary, `scenery validate`, regenerate the
   client, run the four `apps/platform` lanes and `make verify`; restart
   the dev session and browser-check one generated route. The legacy-hint
   diagnostic is proven by running the new binary once *before* the rename
   and observing the exact guidance.

## Plan of Work

Milestone 1 is deliberately small and test-first: constants + discovery +
lock + the new diagnostic, proven by unit tests before the sweep. The sweep
(Milestone 2) is mechanical grep-driven work — do it in one pass with the
`git grep` acceptance gate rather than file-by-file judgment. Milestone 3
follows the standard consumer-migration recipe (0131 `Concrete Steps`),
noting that the contract revision will change because source paths feed
workspace-revision hashing — regenerate, don't hand-edit. Coordinate
timing: land after the current five-plan tree commits, so the rename
doesn't collide with in-flight `.scn` edits.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/scn ./internal/compiler ./internal/generate
    go test ./...
    go install ./cmd/scenery
    scenery harness self -o json

From `/Users/petrbrazdil/Repos/Micro/platform` (Milestone 3):

    # before renaming: prove the hint
    scenery validate   # expect the legacy-name diagnostic naming each file
    git mv scenery.scn app.scn
    git mv scenery.lock.scn app.lock.scn
    find . -maxdepth 2 -name scenery.package.scn -execdir git mv scenery.package.scn package.scn \;
    scenery validate
    scenery generate --target typescript_client.public_api -o json
    (cd apps/platform && bun run typecheck && bun run lint && bun test && bun run build)
    make verify

## Validation and Acceptance

- Discovery/lock/diagnostic unit tests for the new names and every legacy
  hint pass; full Go suite green; harness self green with the knowledge
  contract updated.
- `git grep -l 'scenery\.package\.scn\|scenery\.lock\.scn' -- ':!docs/plans'`
  empty in the scenery repo; the equivalent grep (excluding
  `docs/agent/exec-plans`) empty in the platform repo.
- Platform validates, regenerates with `--check` clean, all lanes green,
  dev session serves, one generated route browser-checked.
- Running the new binary against a legacy-named tree produces the exact
  rename guidance for each of the three names (captured in Artifacts).

## Idempotence and Recovery

Renames are `git mv` — atomic and revertable per repo. The two repos must
move together with the binary: sequence is scenery lands + `go install`,
then platform renames in one commit. If the platform migration stalls
mid-rename, `git checkout` restores the old names and the old binary still
works (the scenery change is not deployed anywhere else). Fixture
regeneration is deterministic; re-run on any interruption.

## Artifacts and Notes

- Naming decision table and rejected alternatives are in `Purpose` /
  `Decision Log`. Capture here during Milestone 3: the legacy-hint
  diagnostic output verbatim, and the platform contract-revision
  before/after.

## Interfaces and Dependencies

- No contract-schema surface changes; no new dependencies. Source-path
  dependent hashes (workspace revision, diagnostic source IDs) change as a
  consequence — consumers regenerate.
- Deferred (Decision Log): `.scenery.json`, `scenery.toolchain.json`,
  `scenery.typescript-client-generated.json` renames.
- Sequencing: after the current in-flight tree (0132–0136 execution)
  commits; conflicts with any concurrent `.scn` edit are rename-level and
  cheap to avoid by ordering.
