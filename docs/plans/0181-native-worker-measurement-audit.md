# Native Worker Measurement Audit

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current. It follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

The developer requested a double check of the negative result in completed
plan 0180. Preserve its samples and compare the corrected worker both with the
unchanged ordinary control and with a separately labeled control performing the
same full post-build input discovery. The scope remains a real ONLV semantic edit
through full native verification, retained binaries, first execution and
authenticated SQL response; it excludes the complete supervisor/frontend loop.

## Progress

- [x] (2026-09-11) Find asymmetric input validation in the measured implementation.
- [x] (2026-09-11) Share one full graph capture and its hashed bytes with the kernel projection.
- [x] (2026-09-11) Remove redundant rendering using already prepared projection ownership.
- [x] (2026-09-11) Prove closure and kernel identity equivalence; repeat six alternating
  AB/BA pairs for each explicitly named control.
- [x] (2026-09-11) Record exact results, validation and the corrected architectural conclusion.

## Surprises & Discoveries

The original worker performed four input-discovery/fingerprint passes per sample;
the ordinary control performed one. The worker alone rediscovered inputs after
compilation. Summed per-sample discovery medians were 661.492 ms versus 241.174 ms;
fingerprinting was 403.079 ms versus 140.330 ms. These overlap other work and are
not an additive causal prediction. The worker also requested ordinary workspace
artifacts and adapter rendering after preparation had already produced them.
The original 19% slowdown is valid for that implementation, but does not isolate
the architecture's cost.

## Decision Log

- (2026-09-11, Codex) Keep 0180 and its samples immutable; correct living indexes.
- (2026-09-11, Codex) Preserve full post-build membership and byte checks. Derive
  kernel inputs from the complete capture; prove equality with independent
  discovery. Keep the ordinary control intact and add a labeled matched control.
- (2026-09-11, Codex) Keep every authored package, declared target pattern and ABI
  check. No broad runtime migration or production promotion belongs to this audit.

## Outcomes & Retrospective

Completed as a measurement correction, not runtime promotion. The initial
architectural rejection based on the 19% slowdown is withdrawn. Equal validation
produces a small observed difference; it does not demonstrate the large saving
needed to justify the broad migration. No assertion of statistical equivalence
or an inherent worker penalty follows from six pairs.

| Control | Control median ms | Worker median ms | Worker difference |
| --- | ---: | ---: | ---: |
| ordinary | 5978.691770 | 6633.046917 | +654.355147 ms / +10.945% |
| matched | 6414.336000 | 6532.734833 | +118.398833 ms / +1.846% |

Both series retain the first slower worker sample (8151.310000 ms in the ordinary
comparison); no measured sample was excluded. Each has six AB/BA alternating
pairs, a fresh semantic edit and worker executable every time, and a new kernel
process using the exact verified retained kernel. The matched control adds the
candidate's full post-build graph/membership/byte check; it is not the ordinary
product path. Absolute times drifted from the earlier series, so percentages are
computed within each new paired series rather than across runs.

| Pair/order | Ordinary A ms | Ordinary B ms | Matched A ms | Matched B ms |
| --- | ---: | ---: | ---: | ---: |
| 1 / AB | 6399.107417 | 8151.310000 | 6538.648125 | 6468.862875 |
| 2 / BA | 6079.491584 | 6565.841125 | 6600.295792 | 6614.904292 |
| 3 / AB | 5927.104583 | 6647.039917 | 6421.954667 | 6596.606792 |
| 4 / BA | 6030.278958 | 6619.053917 | 6406.717334 | 6677.247791 |
| 5 / AB | 5854.943583 | 6841.950750 | 5925.153833 | 6188.928958 |
| 6 / BA | 5860.934792 | 6242.416625 | 6304.484125 | 6304.523666 |

In the matched series, summed Go-command medians were 1813.351 ms for the
control and 1609.017 ms for the worker (204.334 ms lower). Native checking was
1192.075/1349.354 ms and worker-only rendering 149.635 ms. These spans overlap;
do not add them into an invented critical-path saving. The corrected candidate
still prepares ordinary private composition alongside worker additions, retaining
the full target patterns. That is an experiment cost, not proof of an inherent
architectural penalty. Eliminating it or keeping a kernel process alive would
require separately validated candidates; neither saving is claimed here.

All 24 measured builds passed semantic response, standard auth, two-tenant SQL
isolation, linked identity, retained artifact and owner cleanup checks. The
independent kernel audit matched all 687 kernel input entries against the full
1681-entry target manifest. All pairs retained the same 509 native source/embed/
public-projection identities, differing only in the intended `solar/projects/api.go`
bytes. Worker reachability retains all 166 native packages (578 packages total),
kernel reachability contains no native application package (331 total). All 5063
baseline native text symbols remain in the 5205-symbol worker.

Framework source digest:
`sha256:3a201fcc1700521ecfeb36576bfa912d4c735755c5283adaa5234b1abdfe170c`.
Measurement driver SHA-256:
`4549ca88c92569e77b3bf576997c55c2141d4b013ab4a2d6e17537aba14c71d1`.

Validation passed: affected Go packages; build race tests; lint (zero issues);
all three committed generator fixture refreshes; TypeScript conformance (27 tests)
and both TypeScript checks; full default verifier (Go suite, vet, schemas, drift);
and explicit probes `native-contract` (12.943 s), `dev-process` (62.299 s),
`build-info` (0.570 s), `assistant-runtime` (1.385 s). The verifier retains 41
knowledge and 22 architecture warnings; its 6.259 s aggregate Go-suite time was
advisory. Full release, debugger, stream/custom-auth/durable parity and complete
ONLV supervisor/frontend/assistant measurements were not selected because this
audit does not promote the private candidate. The ordinary runtime behavior is
unchanged; README, SKILL and public schemas were intentionally left unchanged.

## Context and Orientation

`internal/build/native_experiment.go` compiles the private candidate;
`internal/build/build_input.go` discovers inputs. The stamped driver is
`scripts/native-worker-experiment/main.go`. Native adapters are rendered in
`internal/generate/generate_native_worker.go`. Original evidence is
`.scenery/harness/native-worker/m2-paired-report.json`; the owned app copy is
`.scenery/harness/native-worker/m2-app`. Personal ONLV data/runtime are untouched.

## Milestones

M1 corrects asymmetry without weakening evidence. M2 proves graph/bytes/behavior
and repeats both paired comparisons. M3 records the revised conclusion.

## Plan of Work

Capture direct import edges with the complete target graph. Hash inputs once per
capture and project kernel package/module entries from those exact bytes. Repeat
the full capture after the build/check join. Reuse prepared pure projections,
record worker rendering time, and add an experiment-only matched control that
checks the post-build graph before issuing its retained-artifact receipt.

## Concrete Steps

From the repository root run focused tests, build the experiment driver with the
current verifier's linked framework digest, then use copies of the owned
`measure-pairs.py` with distinct report/cache paths. Run no concurrent tests during
a series. Keep failed runs, response labels, linked identities and cleanup proof.

## Validation and Acceptance

Run from the repository root: `go test ./internal/build ./internal/generate
./cmd/scenery ./scripts/native-worker-experiment`, `go test -race ./internal/build`,
`golangci-lint run ./...`, and `go run ./scripts/verify --summary --write` (full Go
suite and refreshed agent context). Run each generator fixture refresh command:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go run ./scripts/verify --summary --write --probe native-contract --probe dev-process --probe build-info --probe assistant-runtime
```

The real SQL/auth series must preserve 166 native packages, 215 operations,
baseline native symbols and exact authored input membership. Full release,
debugger/streaming promotion gates and all-root timing remain unselected while
the prototype has no production admission. Report that condition explicitly.
No complete-loop 0179 claim follows from this bounded API measurement.

## Idempotence and Recovery

Use owned output roots and labeled disposable PostgreSQL containers. Restore
source only if bytes match the last owned write. Check exact container ownership
before removal. Preserve failures and never overwrite the original M2 report.

## Artifacts and Notes

Audit evidence uses the `audit-` prefix beneath `.scenery/harness/native-worker/`.
Exact commands run from the repository root:

```sh
python3 .scenery/harness/native-worker/audit-measure-pairs.py
python3 .scenery/harness/native-worker/audit-measure-pairs.py --matched-control
.scenery/harness/native-worker/audit-experiment-driver --app-root "$PWD/.scenery/harness/native-worker/m2-app" --output "$PWD/.scenery/harness/native-worker/audit-independent-inputs" --mode worker --binding projects/binding/projects_list_projects_http --verify-kernel-projection
```

The driver was built with `go build` and
`-ldflags=-X=scenery.sh/internal/build.linkedFrameworkDigest=` followed by the
exact framework digest above; source proof is `audit-source-verifier.json`.
Raw evidence is `audit-ordinary-paired-report.json`,
`audit-matched-paired-report.json`, `audit-input-retention-report.json`,
`audit-closure-report.json`, `audit-worker-packages.json`,
`audit-kernel-packages.json`, `audit-baseline-nm.txt`, `audit-worker-nm.txt`, and
`audit-independent-inputs/build-evidence.json`. Logs are `audit-affected.log`,
`audit-race.log`, `audit-lint.log`, `audit-default.log`, `audit-probes.log`,
`audit-fixture-{native,house,assistant}.json`, and `audit-ts-{conformance,clients,catalog}.log`.
Owned series roots are `audit-pairs-ordinary-9af5b3f5c4` and
`audit-pairs-matched-5cee3bee1b`. Both reports record complete cleanup.

## Interfaces and Dependencies

The ordinary CLI/runtime selection stays unchanged. Experiment evidence preserves
complete targets and independent content-addressed binaries. Kernel projection
fails closed on missing packages, edges or consumed input identities.
