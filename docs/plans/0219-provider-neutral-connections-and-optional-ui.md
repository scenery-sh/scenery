# Provider-Neutral Connections And Optional UI

## Purpose / Big Picture

Implement the user's point-2 guide against baseline
`67cff8fc292a79ef93025f593ed9cbf1092a9935` (2026-10-14) in the registered
`provider-neutral-connections` feature at
`/Users/petrbrazdil/.codex/worktrees/b94c/scenery`. External connections belong to
one standard-auth user and one configured provider ID. Google becomes a preset;
connecting never creates a login identity or changes the user's profile. Backend
CRUD and ordinary TypeScript clients remain independent of optional page output.

Keep one Go module, canonical graph, configuration store, SDK host boundary and
artifact transaction. No lifecycle/cache overhaul, storage work, arbitrary OAuth
grants, sign-in redesign, global installation, production data mutation or
subagents are authorized by this implementation request. `VNEXT.md` and completed
historical plans remain unchanged.

## Progress

- [x] (2026-10-14 11:50Z) Verified clean baseline and prepared the worktree-local
  product with `go run ./scripts/verify --quick --summary --write`. All selected
  executable checks passed; 27 pre-existing knowledge freshness warnings remain
  in `.scenery/harness/runs/20261014T115032.101862000Z/self.json`.
- [x] (2026-10-14 11:53Z) Created `feat/provider-neutral-connections` and registered
  this existing worktree using `scenery feature register`.
- [x] (2026-10-14 11:55Z) Read root and applicable generator, compiler, app,
  configuration and verifier instructions, architecture, plan contract, debt
  tracker, feature workflow and cumulative validation matrix.
- [x] (2026-10-14 12:05Z) Extracted optional UI rendering and pure shared queries;
  parent retains verification/publication. Existing renderer tests and new common
  client/owned-retirement/failed-render atomicity checks pass.
- [x] (2026-10-14 12:05Z) Named `ui` and `core-separation` probes pass, including
  SDK/runtime transitive UI exclusion and a Go-only build/runtime. Source baseline
  knowledge warnings persist; the introduced generator instruction-budget warning
  was corrected by moving the detail-page contract into the living UI guide.
- [x] Establish provider/SDK connection boundaries.
- [x] (2026-10-14 13:20Z) Implemented the credential-free SDK value leaf, host
  dispatch and request/worker token accessors. Runtime composes providers and
  the existing standard-auth pool; the SDK imports no vault/provider/runtime.
- [x] (2026-10-14 14:00Z) All three exact fixture regenerations report zero changed
  bytes; Bun and both managed TypeScript checks pass. Required race checks and
  Linux config vet pass. Final UI/core probes pass; full cumulative lane remains.
- [x] Implement provider-neutral vault, injected adapters and SDK helpers.
- [x] (2026-10-14 12:18Z) Implemented provider-independent vault and injected
  cipher/store/provider/clock contracts. Deterministic tests cover caching,
  rotation with reduced/omitted scopes, optional refresh, ownership/resource/key
  failures, truthful status/commit failures, unknown outcomes, cancellation,
  unsupported token types and durable disconnect before revocation.
- [x] (2026-10-14 12:18Z) Implemented configured OAuth2 account lookup, shared
  RS256 OIDC verification and the Google preset. Injected-transport tests cover
  PKCE, optional OAuth2 fields, fixed resources, sanitized errors/no ambiguous
  retries, Google consent and issuer/audience/expiry/nonce/signature rejection.
- [x] Implement explicit schema/config migration and coordinated route/error cutover.
- [x] (2026-10-14 13:20Z) Replaced Google connection APIs/routes/errors/live SQL
  with generic contracts. Google sign-in keeps its original lifecycle. Provider
  declarations use the current config schema, environment keys and generated
  composition; TypeScript projection carries the provider path parameter.
- [x] (2026-10-14 13:20Z) Added one-shot transactional schema migration and runtime
  unknown/legacy-shape refusal. SQL generation used `sqlc v1.31.1`; no generated
  query Go was edited manually. The original legacy schema is a migration fixture.
- [x] (2026-10-14 13:20Z) Expanded named auth probe: all 19 public SQL/runtime
  journeys pass, including OAuth2-only account identity, unchanged login profile,
  browser/provider/issuer/replay isolation, populated legacy ciphertext/ID/time
  preservation, rerun, unknown schema and rollback. Evidence:
  `.scenery/harness/runs/20261014T131923.204572000Z/self.json`.
- [x] (2026-10-14 13:55Z) All 22 auth scenarios pass, including independent-process
  rotation, disconnect and interrupted refresh, exact ownership constraints and
  populated migration. Evidence: `.scenery/harness/runs/20261014T135518.225199000Z/self.json`.
- [x] (2026-10-14 14:00Z) Added living connection/cutover guides, updated owning
  docs/skill/config contract and shared path discovery/architecture restrictions.
  Credential-copy local/remote/conflict/rerun tests, request/worker authority and
  frozen legacy cipher/key vector pass. Configuration and CLI grammar probes pass.
- [x] (2026-10-14 14:17Z) Full cumulative verification, lint and every changed-area
  command pass; auth (22 cases), worktree SQL (18 functional rows), UI,
  core-separation, configuration, CLI grammar and native-contract probes pass.
  Native runtime responses attest contract/implementation/build identity and
  the generated client passes. Exact owned cleanup passes. Final documentation
  closure is followed by one cumulative archive below.

### Resume here

Completed 2026-10-14. Implementation and required acceptance are complete in
`feat/provider-neutral-connections`; local review/landing/publication remain
separate actions. No production secrets/data or shared CLI installation changed.
The source is intentionally uncommitted for local review. Follow the living
connection/cutover and generated-UI guides for future changes.

## Surprises & Discoveries

The old public `model`/`page` packages are already absent. `internal/model` is
Go analysis, not UI. `renderTypeScriptReactWithCatalog` already omits all React
output when `target.Spec["react"]` is absent; extraction must preserve this.

`auth/standard_google_connections.go:finishGoogleConnection` currently writes
sign-in identities and profiles. New connections will stop doing that while
existing identities remain intact. `standard_google_tokens.go` currently checks
the refreshed scope before storing rotation, ignores status-update failures,
and treats decrypt/configuration failures as revoked consent. These are explicit
acceptance cases, not cosmetic renames.

## Decision Log

- (2026-10-14) Retain user ownership and `UNIQUE(user_id, provider_id)`. Provider
  IDs select trusted registered configuration, never request-supplied URLs.
  Separate configuration IDs allow separate accounts/resources; no implicit
  email or row-order selection. Tenant authorization remains the app's duty;
  an authenticated request cannot choose another user's token.
- (2026-10-14) Empty required scopes mean no additional permission requirement;
  accessors do not promise downscoping. Scopes remain case-sensitive opaque
  values, sorted/deduplicated without semantic rewriting. Only bearer tokens
  are returned by the initial string API.
- (2026-10-14) Preserve ciphertext version 1 and the historical local derivation
  input `scenery-google-token:`. Renaming an input would lose existing data.
- (2026-10-14) Keep PostgreSQL row locking across refresh/disconnect. Bound lock
  and HTTP waits. Save successful rotation and effective scopes before returning
  missing-scope errors. Ambiguous remote rotation/failed commit requires truthful
  recovery; never claim exactly-once or blindly retry a possibly rotated token.
- (2026-10-14) Use authorization-code + PKCE S256 and one-time browser/user-bound
  state. OAuth2-only account identity uses a configured endpoint; OIDC validates
  issuer, audience, signature, expiry and nonce. Google retains separate sign-in.
- (2026-10-14) UI extraction precedes the independent connection cutover so output
  equivalence can be checked before auth projection changes.
- (2026-10-14) A refresh intent is durably checkpointed before the provider call.
  The SQL lease must preserve cross-process serialization across that checkpoint
  while retaining row locking. After a crash/failed final commit, internal
  `refresh_pending` refuses further token use until reconnection. This is needed
  to avoid reuse of a remotely rotated credential when local publication fails;
  an ordinary rollback cannot roll back the provider. Use a bounded session
  advisory lease plus row locks, discard a SQL session if unlocking fails, and
  prove independent-process refresh/disconnect behavior in the auth probe.
- (2026-10-14) OAuth2 providers may omit refresh credentials/expiry/ID tokens.
  Missing expiry is not invented: issue that freshly obtained token once, then
  require refresh/reauthorization rather than indefinitely caching it. Google
  consent/identity-scope defaults are preset-owned. OIDC verification requires
  RS256 with a 2048-bit-or-larger trusted JWKS key; unit tests use one static
  test-only key, never runtime key generation.

## Outcomes & Retrospective

Completed the coordinated public/SQL/config cutover and optional UI extraction.
The public SDK reaches the vault through the existing host and remains free of
concrete providers/runtime/UI. Google sign-in stays separate. The vault preserves
version-1 ciphertext and local key derivation, stores rotation before scope denial,
and uses row locks plus a durable intent/session lease for cross-process recovery.
The one-shot SQL/config migrations preserve legacy/unrelated data and reject unknown
shapes/conflicts. UI rendering shares the canonical graph and parent transaction;
common fixture client bytes remain unchanged.

Evidence (all under `.scenery/harness/runs/`):

| Check | Passing archive or result |
| --- | --- |
| Full Go/vet/schema/architecture/docs | `20261014T141220.747683000Z/self.json`; zero errors, 25 pre-existing living-document review warnings |
| Auth, migration and independent processes | `20261014T140647.952517000Z/self.json`; all 22 cases, credential-free logs/evidence, cleanup passes |
| SQL application without pages | `20261014T140648.734446000Z/self.json`; all 18 functional acceptance rows, ten borrower races per worktree, verified owned cleanup |
| UI / common clients | `20261014T135903.139624000Z/self.json`; 56 Bun tests and both managed TypeScript projects pass |
| Backend/SDK transitive isolation and Go-only runtime | `20261014T135903.136340000Z/self.json` |
| Selected environment authority | `20261014T135013.295076000Z/self.json`; local/remote credential-copy/conflict/rerun unit tests also pass |
| CLI grammar | `20261014T135256.066748000Z/self.json` |
| Native entrypoints / served identity / generated client / restart reuse | `20261014T141559.263481000Z/self.json`; host and service linked under the private link budget |
| Exact native/house/assistant regeneration | All three commands in Validation and Acceptance return `changed: []` |
| Race checks | Auth, config, vault, provider, connection runtime, generator and UI pass; Linux config vet passes |
| Lint | `golangci-lint run ./...`: zero issues |

The native response attests contract
`sha256:1eb277c110ae63667ece7cd6eee233cb5364b9eeedc3deb727c8c49075c8695d`,
implementation `sha256:416a22ec96c71ea2d823514ef0cbce243c370ad97b1b84cbf6ef96a3e7295375`
and build input `sha256:6b2203a7c91ab2400ceff0d6641551a542aa45dc7218c3b4553446226512f77d`.
It includes 231 external framework package inputs and names the answering service
process. These are disposable accepted runtime identities, not deployed production.

A strict constraint probe initially included PostgreSQL 18 NOT NULL constraint
rows twice; exact column shape owns those, and C collation makes remaining
constraint ordering deterministic. The first full lane detected a schema revision
mismatch; the repository's complete self-normalized calculation now proves both
static/checked identities. One worktree run saw active source edits and was rerun
against stable inputs. Native readiness preceded terminal build telemetry; the
probe now waits for the completed queued operation before judging linking/reuse.
Every earlier failure was followed by successful same-scope evidence.

Some simultaneous verifier/probe runs exceeded the advisory 5-second cached-suite
budget; the stable full run above took 4.750 seconds with no Go timing warning.
No performance, benchmark or release certification is claimed. Release gate,
benchmarks and real-provider/production credential migration were not selected:
they require their own requested scope. Missing release selection is not represented
as a release pass. `VNEXT.md`, completed numbered plans and unrelated application
state remain unchanged. Migration operators must quiesce writers and retain the
same key; after remote rotation, an old backup is not guaranteed to contain a valid
refresh credential. Ambiguous refresh requires reconnection rather than hidden retry.

## Plan of Work

Ownership map: `auth` owns the public SDK types/helpers and existing standard
sign-in contracts; `internal/appsdk` owns host dispatch. New
`internal/connectionvault` owns lifecycle and encrypted storage through injected
store/provider/clock/cipher boundaries, and `internal/authprovider` owns trusted
OAuth2/OIDC mechanics with a Google preset. Runtime composition installs the
connection implementation through the existing host. `auth/db/queries.sql` and
`auth/db/gen/schema.sql` are SQL generation inputs; regenerate query Go with
sqlc. `internal/app`/`internal/appconfig` own provider declarations/environment
credentials; compiler framework projections and generated clients consume them.
`internal/uigen` owns React/page/catalog rendering; `internal/generate` owns
common clients, checks, descriptor ownership and atomic `workspacetx` publishing.
`scripts/verify` owns external-boundary probes and cleanup.

Public cutover table:

| Existing surface | Replacement |
| --- | --- |
| `GoogleAccessToken(ctx, scopes...)` | `ConnectionToken(ctx, provider, scopes...)` |
| `GoogleAccessTokenForUser(ctx, user, scopes...)` | `ConnectionTokenForUser(ctx, user, provider, scopes...)` (trusted server workers) |
| Google connect/status/disconnect records/methods | Provider-neutral connection records/methods |
| `/auth/google/connect/start`, `/auth/google/connection`, disconnect | `/auth/connections/{provider}/start`, status and disconnect |
| Connection callback on Google sign-in route | `/auth/connections/{provider}/callback` |
| Google reauth/scope errors | Not-configured, not-connected, reauth, scope, unavailable and configuration errors |
| Google-only connection rows and handshakes | Provider/config/resource/account-bound connection rows and handshakes |
| Google connection config | Registered provider definitions and environment-backed client credentials |

Preserve Google sign-in routes, users, sessions, tenants, memberships and linked
identities. Do not retain old connection aliases/routes or fallback decoders.
Provider metadata exposed to browsers contains no access/refresh credentials.

Migration is an explicit, transactional rename/extension after verifying the
legacy shape, with `provider_id = "google"`, stable row IDs/subjects/ciphertext/
scopes/status/timestamps. Clean installation uses the new schema. Unknown schema
shapes fail closed; safe rerun verifies the new shape. Operators take an encrypted
backup, quiesce all token writers, migrate and verify counts/decryption in memory,
map config through the authoritative store, expire incompatible connection
handshakes only, register callback URLs, deploy one implementation, and verify
reconnect/refresh/disconnect before enabling writers. No mixed-version writes.
After new provider writes or rotation, rollback may require a forward fix and
reauthorization; a stale backup is not a guaranteed valid refresh credential.

Implement and validate each boundary with deterministic in-process tests. Add
external PostgreSQL/process/provider journeys to the existing named auth probe,
including empty/populated migration, failure/rerun/unknown schema, rotating
refresh serialization, disconnect races, callback replay/mismatch, no login
mutation and credential leakage checks. Do not hide external setup in unit tests.

## Validation and Acceptance

All commands run from the feature worktree root. This is source/compiler/
generator/runtime work, so final cumulative validation is full directly:
`go run ./scripts/verify --summary --write`. Read its reported archive's
`agent-context-summary.json` then complete the exact union in `agent-context.json`.
Pre-edit proof never certifies the final input.

Focused checks: `go test ./auth ./errs ./internal/appsdk ./internal/appconfig`,
`go test ./internal/generate ./internal/uigen ./internal/compiler ./internal/graph`,
new vault/provider package tests, `go test ./runtime/...`,
`go test -race ./auth ./internal/generate ./internal/appconfig` and
`GOOS=linux go vet ./internal/appconfig`. Always run `golangci-lint run ./...`.
Retain cached tests; no unrequested benchmarks/fresh timing audit/release gate.

Generator acceptance regenerates all three owning fixtures:
`go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`,
the same command with `internal/compiler/testdata/house`, and with
`testdata/assistant`. Run
`bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts`,
`tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json`,
and the same managed compiler with `internal/generate/testdata/tsconfig.catalog.json`.
`go run ./scripts/verify --probe ui --summary --write` owns frozen UI prerequisites.

External acceptance is `go run ./scripts/verify --probe auth --summary --write`,
extended through its existing assertion inventory and owned cleanup. Configuration
boundary changes also select `--probe configuration`. Complete any additional
named probes reported by changed-area classification; skipped probes are not pass.

Observable acceptance: a Go-only app builds/serves without UI tools or provider
initialization; CRUD SQL/authorization works without pages; a plain TypeScript
target emits no React/hooks/catalog; React output and app-owned slots survive,
UI failures publish none of the artifact set, disabling UI deletes only proven
owned files, and presentation edits do not change SQL/backend contracts. Google
and deterministic non-Google journeys use the same vault for consent/status/
cached/rotated token/worker/disconnect/reconnect. Wrong owners/providers/resources/
issuers and replay fail closed. Runtime proof records served producer identity.

## Idempotence and Recovery

Preserve unrelated work and retained runtime/data ownership. Do not mutate live
application secrets or databases while developing; named probes use disposable,
verified owners and assert cleanup. Keep one-shot schema/config migration separate
from ordinary runtime bootstrap. A failed migration rolls back entirely. Reusing
the feature worktree and frozen managed dependencies is safe; no global install.
Generation renders/verifies before one transaction and respects descriptors.
Keep this plan's Resume here and evidence current through every milestone.
