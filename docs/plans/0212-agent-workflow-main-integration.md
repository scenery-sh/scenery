# Agent Workflow And Main Integration

## Purpose / Big Picture

Publish the authorized agent-context and warning cleanup work to `main` together
with the performance and automatic tracing changes already on `origin/main`.
Preserve both histories and install the exact pushed source as the shared CLI.
The observable scenario is a generated client that retains exact JSON fast paths
and reports trace identities, with normal process replacement, PostgreSQL durable
notifications and tracing exercised through disposable native applications.

## Progress

- [x] 2026-10-04: saved the reviewed 89-path local change as `97df4166`.
- [x] 2026-10-04: merged incoming `d4bc1171`, preserving 262 declarations in the
  storage file splits and exact combined generated runtime bytes.
- [x] 2026-10-04: full Go tests, vet, schemas and architecture pass; lint reports
  zero issues. Initial uncached suite has one advisory 9.712 s budget warning.
- [x] 2026-10-04: regenerated all three checked clients; both TypeScript checks
  and 66 Bun tests with 356 assertions pass.
- [x] 2026-10-04: five boundary probes pass. Observability isolated a merge
  omission in the retry projection; restored incoming response-body cancellation
  and added payload-free lifecycle values to its failure diagnostic.
- [x] 2026-10-04: all six named boundary probes and owned cleanup pass in stable
  run `20261004T052943.365827000Z`, with zero diagnostics; observability retains
  28 spans and verifies retry correlation and discarded-response cancellation.
- [ ] Complete final cached full verification, publish `main`, and install it.

### Resume here

Recorded 2026-10-04: the merge is staged but not committed. The next command is
final cached full verification below, then authorized publication and installation. Initial full evidence is
`.scenery/harness/runs/20261004T051743.566620000Z/`; detailed parity, generation,
lint and Bun evidence is `.scenery/harness/publish-main-20261004/`.
The human authorized commit, direct push to `main` and `go install`.

## Surprises & Discoveries

`origin/main...HEAD` contained three incoming and two local commits before the
new local checkpoint. Both sides split the TypeScript renderer and store files.
The incoming TypeScript types and invocation observer are combined with the local
parser and immutable field lookup; taking either whole version would lose work.
Independent worktrees also allocated IDs 0208 through 0211. Full filenames remain
the identifiers in the living completed index; historical plans stay unchanged.

The first boundary run and a focused diagnostic rerun showed correct retry trace
IDs and successful typed results, but `cancelled: false`. Taking the local HTTP
split omitted incoming `await response.body?.cancel()`. Native client byte parity
could not cover this branch because that fixture projects retry away. Comparing
the complete public renderer isolated that single missed incoming behavior; the
remaining differences are the intended local exact-JSON fast paths. Failed runs
`20261004T052051.817015000Z` and `20261004T052633.624897000Z` retain owned cleanup.

## Decision Log

- 2026-10-04: use a merge rather than rewriting published commits. Keep incoming
  storage behavior byte-for-byte at declaration level while retaining small files.
- 2026-10-04: preserve completed plan IDs and all original files. New allocation
  starts at 0212; explain the collision in the living index.
- 2026-10-04: validate real external boundaries through named probes. No benchmark,
  all-root timing audit, release certification or user application restart is selected.

## Outcomes & Retrospective

Not yet completed.

## Plan of Work

Resolve overlapping ownership, regenerate the checked clients from the prepared
worktree-local binary, and validate the combined behavior. Record the final
selected evidence before the merge commit, then fast-forward local `main` to the
integrated history. Push without force and compare the remote ref with `HEAD`.
Install only from the clean pushed checkout and verify both the executable path
and its producer commit against that ref.

## Validation and Acceptance

All commands run from the repository root. Changed classes are CLI JSON,
compiler/generator, Go packages, runtime and UI catalog. Full verification covers
the Go command union; read its compact context and full changed-area commands.

```sh
go run ./scripts/verify --summary --write
golangci-lint run ./...
.scenery/harness/bin/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
.scenery/harness/bin/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
.scenery/harness/bin/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts internal/generate/testdata/query_table_perf.test.tsx
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go run ./scripts/verify --probe postgres --probe cli-grammar --probe dev-process --probe process-model --probe native-contract --probe observability --summary --write
git diff --check
```

All selected probe assertions and owned cleanup must pass, without skips. Final
full inputs must be stable and diagnostic counts zero. `scenery logs` from the
framework changed-area context is conditional on a changed user's running app;
this integration uses disposable probes and changes no user app.

## Idempotence and Recovery

The local checkpoint preserves the original work. Do not reset another checkout,
force-push, renumber completed plans or remove unrelated runtime state. Regenerate
only descriptor-owned client output. Probe cleanup must identify owned resources;
retain failed evidence when cleanup cannot be confirmed. If `main` advances again,
fetch and integrate the new commit before publication. Installation is repeatable
from the verified clean `main` and does not restart running processes.
