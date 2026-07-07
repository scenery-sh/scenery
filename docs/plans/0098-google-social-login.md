# Google Social Login: Enable/Disable Contract, Hardening, and Tests

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

A scenery app (concretely: ONLV) can turn "Sign in with Google" on or off in its app config, supply its own Google OAuth client credentials through named environment variables, and rely on a flow that is tested, documented, and diagnosable. When Google login is disabled, the app exposes no Google auth endpoints at all — not in the runtime router, not in `scenery inspect endpoints --json`, and not in the generated TypeScript client.

Observable end state:

- An app root with `"auth": {"enabled": true, "google_oauth": {"enabled": true}}` in `.scenery.json` serves `GET /auth/google/start` and `GET /auth/google/callback`, and a browser can complete a full sign-in round trip against a fake Google (in tests) or real Google (manually).
- The same app with `"google_oauth": {"enabled": false}` (or the block absent) returns 404 for both paths, and neither endpoint appears in `scenery inspect endpoints --json` output or the generated TypeScript client.
- `go test ./auth` exercises the complete browser flow against an `httptest` fake Google (authorization redirect, code exchange, JWKS verification) and a live test Postgres, including identity linking and abuse cases.

## Progress

- [x] Milestone 1: `google_oauth.enabled` honored end to end (runtime registration, app model, standardauthmeta, TypeScript client)
- [x] Milestone 2: flow hardening (JWKS cache, expired oauth-state cleanup, consistent error redirects)
- [x] Milestone 3: fake-Google test suite for the full flow and abuse cases
- [x] Milestone 4: docs, environment registry, `scenery check` diagnostic, ONLV integration notes

(2026-07-07) Plan created. No implementation started.
(2026-07-07) Milestone 1 implemented in the runtime endpoint registration, standard auth metadata, inspect responses, and TypeScript client generation. Added focused tests in `internal/standardauthmeta`, `internal/inspect`, and `internal/clientgen`; verified the disabled standard-auth fixture reports zero `/auth/google/*` endpoints and an enabled throwaway fixture reports two.
(2026-07-07) Milestone 2 implemented: Google JWKS keys are cached for one hour with a forced refresh on unknown `kid`, callback failures redirect to `/sign-in?error=...`, and expired OAuth state rows are deleted opportunistically before new state creation. Added auth unit tests for JWKS cache/refetch behavior and callback error redirect URLs.
(2026-07-07) Milestone 3 completed: added a fake-Google server scaffold and live-Postgres browser-flow test covering start redirect, fake authorization, code exchange/JWKS, callback refresh cookie, refresh validation, verified email/password account linking, refusal to link unverified email/password accounts, state replay rejection, nonce mismatch rejection, and `email_verified=false` rejection. Verified it passes against local test Postgres with `SCENERY_TEST_DATABASE_URL=postgres://test:test@127.0.0.1:5433/test?sslmode=disable`.
(2026-07-07) Milestone 4 implemented: `scenery check --json` now returns an `auth` warning when `auth.google_oauth.enabled` is true but the configured client ID/secret env vars are unresolved from process env or `.env`, with a focused `cmd/scenery` test. Updated `docs/local-contract.md`, `docs/app-development-cookbook.md`, `docs/environment.md`, and `docs/environment.registry.json` for the conditional endpoint contract, Google OAuth setup, ONLV integration note, and env registry entries.

## Surprises & Discoveries

- (2026-07-07, plan authoring) The Google sign-in flow is already substantially implemented in `auth/standard_google.go` (PKCE + state + nonce, ID-token verification against Google JWKS, identity linking, session issuance) as part of plan 0006. What is missing is the enable/disable contract, tests, JWKS caching, and documentation — this plan is completion and hardening, not greenfield.
- (2026-07-07, plan authoring) `GoogleOAuthConfig.Enabled` exists in `auth/standard.go` and is plumbed from `.scenery.json` through `internal/app/root.go` (`AuthGoogleConfig`) and `internal/codegen/config.go` (`authGoogleConfigLiteral`), but nothing reads it: `registerStandardAuthEndpoints` registers `GoogleStart`/`GoogleCallback` unconditionally and `internal/standardauthmeta/meta.go` lists them statically.

## Decision Log

- (2026-07-07, Claude + Petr) Disabled means absent, not 503. When `google_oauth.enabled` is false, the Google endpoints are not registered at runtime and are excluded from the app model, `scenery inspect endpoints --json`, and the generated TypeScript client. Rationale: a disabled capability should not leak surface area; agents and generated clients must see the true contract. When enabled but secrets are missing at runtime, `GoogleStart` keeps returning 503 with a clear message (already implemented) and `scenery check` warns ahead of time.
- (2026-07-07, Claude + Petr) Default is disabled. Google login activates only when the app config explicitly sets `auth.google_oauth.enabled: true`. Client credentials come from env vars named by `client_id_env` / `client_secret_env` (defaults `GoogleOAuthClientID` / `GoogleOAuthClientSecret`, with `GOOGLE_OAUTH_CLIENT_ID` / `GOOGLE_OAUTH_CLIENT_SECRET` fallbacks already in `applyStandardSecrets`). The env-var mechanism is pre-existing standard-auth contract, not a new knob; this plan documents it in `docs/environment.md` and `docs/environment.registry.json` instead of inventing a new configuration channel, because secrets must not live in `.scenery.json`.
- (2026-07-07, Claude + Petr) Scope of this plan is sign-in only (`openid email profile`). Offline access, refresh-token storage, and Gmail scopes are explicitly out of scope and live in plan 0099, so that the login surface stays small and shippable.

## Outcomes & Retrospective

Completed on 2026-07-07.

Shipped:

- `auth.google_oauth.enabled` now controls Google sign-in endpoint registration, inspect metadata, and generated TypeScript client methods. Disabled apps expose no `/auth/google/*` runtime routes, model entries, or client methods.
- Google callback failures now redirect back to the app sign-in page with explicit error codes; Google JWKS are cached with one forced refresh on unknown `kid`; expired OAuth state rows are cleaned up opportunistically.
- The fake-Google live-Postgres test covers the full browser flow, refresh cookie validation through `/auth/me`, verified email account linking, unverified account refusal, replay rejection, nonce mismatch, and `email_verified=false`.
- Docs, environment registry entries, ONLV setup notes, and `scenery check` missing-credential warnings are updated.

Validation:

- `go test ./auth ./cmd/scenery`
- `go test ./...`
- `SCENERY_TEST_DATABASE_URL=postgres://test:test@127.0.0.1:5433/test?sslmode=disable go test -v ./auth -run TestGoogleOAuthBrowserFlowWithFakeGoogle -count=1`
- `scenery check`, `scenery inspect endpoints`, and `scenery generate client` on disabled and enabled standard-auth fixtures
- Runtime HTTP proof: enabled fixture served `/auth/google/start` and `/auth/google/callback`; disabled fixture returned 404 for both paths.
- `scenery harness self --summary --write` passed with warnings only: due knowledge-review warnings, a generated sqlc large-file warning, and test timing warnings.

## Context and Orientation

Standard auth is scenery's built-in authentication module. An app enables it in `.scenery.json` under the `auth` key (`internal/app/root.go`, `AuthConfig`); codegen (`internal/codegen/config.go`, `generateMain`) emits a `sceneryauth.RegisterStandard(StandardConfig{...})` call into the generated main. `RegisterStandard` (`auth/standard.go`) resolves secrets from env via `applyStandardSecrets`, registers the auth handler, and calls `registerStandardAuthEndpoints`, which registers typed endpoints plus the two raw Google endpoints.

Files that matter:

- `auth/standard_google.go` — the whole Google flow: `GoogleStart` (builds the Google authorize URL with PKCE challenge, stores hashed state/nonce/verifier in `scenery.scenery_auth_oauth_states` with a 10-minute TTL), `GoogleCallback` (consumes state, exchanges the code at `https://oauth2.googleapis.com/token`, verifies the ID token against Google JWKS, enforces `email_verified`, nonce, issuer, audience), `finishGoogleSignIn` (links or creates the user: existing `google` identity → profile refresh; existing verified email user → link identity; unverified email user → `failed_precondition`; no user → create verified user), then sets the refresh cookie and redirects to `PublicAppURL` + safe redirect path.
- `auth/standard.go` — `StandardConfig`, `GoogleOAuthConfig{Enabled, ClientIDEnv, ClientSecretEnv}`, `registerStandardAuthEndpoints` (registers `GoogleStart`/`GoogleCallback` unconditionally today).
- `auth/standard_config.go` — `identityProviderGoogle`, `defaultOAuthStateTTL`.
- `auth/db/queries.sql` + `auth/db/gen/` — sqlc queries incl. `CreateOAuthState`, `ConsumeOAuthState`, `GetAuthIdentityByProviderSubject`, `CreateAuthIdentity`. Regenerate with `scripts/gen-auth-sqlc.sh`.
- `internal/app/root.go` — `AuthGoogleConfig` JSON shape (`auth.google_oauth.{enabled,client_id_env,client_secret_env}`).
- `internal/codegen/config.go` — emits the Go config literal from `.scenery.json`.
- `internal/standardauthmeta/meta.go` — static endpoint list consumed by the parser/model so standard-auth endpoints appear in `scenery inspect endpoints --json`; includes `GoogleStart`/`GoogleCallback` unconditionally.
- `internal/clientgen/typescript_standard_auth.go` — generated TypeScript client's standard-auth endpoint list; includes the Google raw endpoints unconditionally.
- `auth/standard_postgres_test.go` — pattern for live-Postgres auth tests (`createAuthLiveTestDatabase`, `resetStandardAuthStateForTest`).
- `testdata/apps/standard-auth` — fixture app used by plan 0006 acceptance; reuse for `scenery check` / clientgen acceptance here.

Terms: "raw endpoint" is a scenery endpoint with `Raw: true` that receives the plain `http.ResponseWriter`/`*http.Request` (needed for browser redirects). "App model" is the parsed representation of the app that `scenery inspect` and clientgen read.

## Milestones

1. **Enable/disable contract.** `GoogleOAuth.Enabled` gates runtime registration in `registerStandardAuthEndpoints`; `internal/standardauthmeta` exposes the endpoint list as a function of whether Google is enabled (e.g. `Endpoints(includeGoogle bool)` or a filtered accessor), and the parser/model (`internal/parse`, `internal/model`) plus `internal/clientgen/typescript_standard_auth.go` consume the app's `auth.google_oauth.enabled` value so disabled apps show no Google endpoints anywhere. Repo stays green with existing fixtures.
2. **Flow hardening.** Cache Google JWKS in-process with a TTL (~1 hour) and single-flight refresh, refetching once on unknown `kid` before failing; make all `GoogleCallback` failure paths redirect to `PublicAppURL + "/sign-in?error=..."` with distinct error codes instead of bare `http.Error` pages (a browser is the caller); delete expired rows from `scenery_auth_oauth_states` opportunistically (e.g. on `CreateOAuthState`, delete-where-expired first).
3. **Fake-Google test suite.** An `httptest.Server` standing in for Google (token endpoint + JWKS endpoint, RSA key generated in-test) plus live test Postgres. Cover: full happy-path start→callback→cookie→`/auth/me`; linking to an existing verified email/password user; refusal to link to an unverified email user; state replay rejection; nonce mismatch rejection; `email_verified=false` rejection; endpoints absent when disabled. The Google endpoint URL constants become overridable for tests (package-level vars or a test hook, mirroring how `svc.clock()` is injectable).
4. **Docs and diagnostics.** `docs/local-contract.md` (endpoint presence contract + config shape), `docs/app-development-cookbook.md` (recipe: enabling Google login, Google Cloud console setup incl. redirect URI `${APIBaseURL}/auth/google/callback`, local dev URLs), `docs/environment.md` + `docs/environment.registry.json` (register `GoogleOAuthClientID`, `GoogleOAuthClientSecret` and fallbacks), `SKILL.md` if the portable skill mentions auth endpoints. Add a `scenery check` diagnostic: `auth.google_oauth.enabled` true but client ID/secret env unresolvable → warning naming the expected env vars.

## Plan of Work

Work proceeds in milestone order; each milestone leaves `go test ./...` green and is committable on its own. Milestone 1 changes the public contract (endpoint presence), so its model/clientgen changes and doc updates in Milestone 4 must land in the same overall plan; until Milestone 4 lands, this plan file records the drift per the repo rule.

The one structural change is threading "is Google enabled" from app config into the three places that currently assume static endpoint lists: runtime registration (reads `StandardConfig`, trivial), the parser/model path (reads `appcfg.Config.Auth.GoogleOAuth.Enabled` where standard-auth endpoints get injected into the model), and clientgen (same source). Find the injection points by following `standardauthmeta.Endpoints()` callers; keep the meta package the single source of truth for the list so the three consumers cannot drift.

## Concrete Steps

All commands run from the repo root `/Users/petrbrazdil/Repos/scenery` unless stated.

1. Milestone 1:
   - In `auth/standard.go`, wrap the two `runtime.RegisterEndpoint` calls for `GoogleStart`/`GoogleCallback` in `if config.GoogleOAuth.Enabled` (registration needs access to the config; pass it into `registerStandardAuthEndpoints`).
   - Change `internal/standardauthmeta/meta.go` so callers state whether Google endpoints are included; update all callers (`grep -rn "standardauthmeta.Endpoints" --include="*.go"`).
   - Update `internal/parse` / `internal/model` / `internal/clientgen/typescript_standard_auth.go` call sites to pass the app's `auth.google_oauth.enabled`.
   - Adjust `testdata/apps/standard-auth/.scenery.json` (or add a second fixture) so both enabled and disabled shapes are covered by golden/model tests.
2. Milestone 2:
   - Add a package-level JWKS cache in `auth/standard_google.go` (`sync.Mutex` + fetched-at + keys map; TTL constant in `auth/standard_config.go`), refetch on unknown `kid`.
   - Replace `http.Error` responses in `GoogleCallback` with `http.Redirect` to `appRedirectURL("/sign-in?error=<code>")`, one code per failure class (`google_oauth`, `oauth_state`, `google_token`, `google_id_token`, `google_email_unverified`, `google_link_precondition`, `google_internal`).
   - Add a `DeleteExpiredOAuthStates` sqlc query in `auth/db/queries.sql`, run `scripts/gen-auth-sqlc.sh`, call it best-effort from `GoogleStart` before `CreateOAuthState`.
3. Milestone 3:
   - Make `googleAuthEndpoint`, `googleTokenEndpoint`, `googleJWKSURL` package vars; add a test helper that points them at an `httptest.Server` and restores them via `t.Cleanup`. The fake signs ID tokens with an in-test RSA key served from the fake JWKS route and echoes back a configurable subject/email/nonce.
   - Write `auth/standard_google_test.go` covering the cases in Milestone 3 above, using `createAuthLiveTestDatabase` and `resetStandardAuthStateForTest` from `auth/standard_postgres_test.go`. Drive the flow with `httptest.NewRecorder` + crafted requests: call `GoogleStart`, parse the redirect URL for state/challenge, then call `GoogleCallback` with the fake's issued code.
4. Milestone 4:
   - Doc and registry edits listed in Milestones; add the `scenery check` diagnostic where auth config validation already happens (follow existing check diagnostics in `cmd/scenery`).
   - Write the ONLV integration note (in the cookbook recipe): ONLV sets `auth.google_oauth.enabled: true`, puts `GoogleOAuthClientID`/`GoogleOAuthClientSecret` in its local `.env`, registers the redirect URI for its `APIBaseURL`, and points its sign-in button at `GET /auth/google/start?redirect_path=/`. ONLV's own `AGENTS.md` update happens in the onlv repo, not here.

## Validation and Acceptance

- `go test ./...` and `go test ./auth ./cmd/scenery` green.
- `scenery harness self --summary --write` using the worktree-local `.scenery/harness/bin/scenery` build (do not `go install`).
- `scenery check --app-root testdata/apps/standard-auth --json` passes; with Google enabled and env unset it emits the new warning.
- `scenery inspect endpoints --app-root testdata/apps/standard-auth --json` shows Google endpoints iff enabled in the fixture config.
- `scenery generate client --app-root testdata/apps/standard-auth --lang typescript --output <tmp>` includes/excludes `GoogleStart`/`GoogleCallback` matching the fixture config.
- Acceptance: the fake-Google flow test proves a browser round trip issues a refresh cookie whose session passes `/auth/me`; the disabled-fixture test proves 404 + absent from model and client.

## Idempotence and Recovery

All changes are ordinary code and doc edits; every milestone leaves the tree green and committable. sqlc regeneration (`scripts/gen-auth-sqlc.sh`) is deterministic and re-runnable. If Milestone 1's conditional registration breaks a fixture golden file, regenerate the golden output rather than hand-editing. Reverting any milestone commit restores the previous behavior with no data migration concerns (the only schema-adjacent change, expired-state deletion, is a new query on an existing table).

## Artifacts and Notes

- Redirect URI shape: `${APIBaseURL}/auth/google/callback` (see `googleRedirectURI` in `auth/standard_google.go` for the localhost fallback logic). Google Cloud console → APIs & Services → Credentials → OAuth 2.0 Client ID (type "Web application") must list it exactly, including scheme and port, for each environment.
- Error-code query params on `/sign-in?error=...` are frontend contract; ONLV's sign-in page should map them to human messages.

## Interfaces and Dependencies

- Public contract changed: Google standard-auth endpoints become conditional on `auth.google_oauth.enabled`. `docs/local-contract.md`, `internal/standardauthmeta`, model output, and the generated TypeScript client change together.
- New sqlc query `DeleteExpiredOAuthStates`; no schema change.
- New env registry entries: `GoogleOAuthClientID`, `GoogleOAuthClientSecret` (+ documented fallbacks `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET`). These are app-supplied secrets resolved through the existing standard-auth `*Env` config mechanism.
- No new Go module dependencies (fake Google is stdlib `httptest` + existing `golang-jwt`).
- Downstream: plan 0099 (Google connections + Gmail) builds directly on this plan's enable/disable plumbing and fake-Google test scaffolding, and must land after it.
