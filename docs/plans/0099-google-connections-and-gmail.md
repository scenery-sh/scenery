# Google Connections and Gmail Platform: Reliable Offline Tokens for App Integrations

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Give scenery apps a reliable way to hold long-lived Google API access on behalf of a signed-in user, so that ONLV can fetch, draft, and send Gmail without the recurring "token expired, everything broke" failure mode. scenery standard auth gains a **Google connection**: a per-user grant of Google API scopes with an encrypted stored refresh token, disciplined automatic refresh, an explicit health status, and a re-consent flow. Gmail product behavior (mailbox sync, drafting, sending) stays in the ONLV app and consumes the connection through a small Go API and generated client endpoints.

Observable end state:

- A signed-in user clicks "Connect Gmail" in the app; after Google consent, `GET /auth/google/connection` reports `status: "active"` with the granted scopes.
- App code calls `auth.GoogleAccessToken(ctx, scopes...)` and always receives a currently-valid access token, transparently refreshed and cached, safe under concurrent calls from multiple app processes.
- When Google permanently invalidates the refresh token (revocation, expiry), the connection flips to `status: "reauth_required"`, app code gets a typed `google_reauth_required` error (not a random 401 deep in a Gmail call), and the frontend can render a "Reconnect Google" button that restarts consent. Nothing silently retries forever.
- A test suite drives all of this against a fake Google token server, including concurrent refresh, refresh-token rotation, transient 5xx, and permanent `invalid_grant`.

## Progress

- [x] Milestone 1: connection storage — schema, sqlc queries, token cipher, store tests
- [x] Milestone 2: connect / callback / status / disconnect endpoints
- [x] Milestone 3: token access API + refresh engine with fake-Google tests
- [x] Milestone 4: docs — local contract, cookbook Gmail recipe, env registry, ONLV integration contract
- [ ] Milestone 5 (optional, separate decision): worker cron proactive refresh; ONLV end-to-end acceptance

(2026-07-07) Plan created. No implementation started. Depends on plan 0098 (Google social login enable/disable + fake-Google test scaffolding) landing first.
(2026-07-07) Plan 0098 completed in this changeset, so this plan is unblocked once the changeset lands.
(2026-07-08) Milestones 1-4 implemented in this worktree: standard auth now stores per-user Google connections with encrypted refresh/access token ciphertexts, exposes connect/status/disconnect endpoints only when Google OAuth is enabled, refreshes access tokens under a Postgres row lock, records `reauth_required` on permanent refresh failures, and documents the Gmail integration contract. Manual real-Google Gmail smoke is still pending.
(2026-07-08) Real-Google smoke reached Google with ONLV on `localhost:4747` and `local.clean.tech`, but Google rejected the new `/auth/google/connect/callback` redirect URI. The connect flow now reuses the already-registered `/auth/google/callback` redirect URI and dispatches by OAuth state purpose.
(2026-07-08) Real-Google ONLV connection completed after user consent: `GET /auth/google/connection` reports `active` for `p.brazdil@gmail.com` with `gmail.modify`, `auth.GoogleAccessTokenForUser` returned an access token, and the connection row has encrypted refresh/access ciphertext plus an access expiry. The first Gmail profile REST call was blocked by Google Cloud configuration because Gmail API was disabled for OAuth project `583487526019`.
(2026-07-08) After explicit user approval, Gmail API was enabled for Google Cloud project `cleantech-501710` / `583487526019`. The disposable ONLV smoke helper then called Gmail's profile endpoint successfully with the Scenery-provided access token: HTTP 200 for `p.brazdil@gmail.com`, with the stored connection still `active` and encrypted token ciphertexts present.

## Surprises & Discoveries

- (2026-07-07, plan authoring) The existing sign-in flow (`auth/standard_google.go`) requests only `openid email profile`, never `access_type=offline`, and discards Google's access token — only the ID token is used. There is no token storage anywhere; this plan adds the first one.
- (2026-07-08, implementation) Generated sqlc output `auth/db/gen/queries.sql.go` is over the self-harness architecture warning threshold after adding Google connection queries. It is generated code and remained below the blocking threshold; no manual split is useful here.
- (2026-07-08, real-Google smoke) The ONLV OAuth client accepted the existing sign-in redirect URI `https://local.clean.tech/api/auth/google/callback`, but rejected both `http://localhost:4747/api/auth/google/connect/callback` and `https://local.clean.tech/api/auth/google/connect/callback`. The platform should not require every app to register a second callback URI just to add Gmail connection consent.
- (2026-07-08, real-Google smoke) Google Cloud API enablement is a separate project-level prerequisite from OAuth consent. A valid OAuth grant with `gmail.modify` can still get Gmail REST HTTP 403 `SERVICE_DISABLED` until `gmail.googleapis.com` is enabled for that OAuth project.

## Decision Log

- (2026-07-07, Claude + Petr) **Split of responsibilities.** scenery owns the Google connection lifecycle (consent, storage, refresh, status, disconnect) because it already owns Google identities, sessions, and the Postgres auth schema, and because token-refresh reliability is a platform concern every app would otherwise reimplement badly. ONLV owns Gmail product behavior (what to sync, drafts UX, send pipeline) because it is product-specific. The boundary is `auth.GoogleAccessToken` + the connection endpoints; ONLV never sees a refresh token.
- (2026-07-07, Claude + Petr) **Connect flow shape.** Starting a connection is a typed authenticated endpoint `POST /auth/google/connect/start` that returns `{authorize_url}`, because raw browser redirects cannot carry the Authorization header; the OAuth state row is bound to the authenticated `user_id`, which is also the CSRF defense. Google redirects back to the shared `GET /auth/google/callback` endpoint, which dispatches connection states by OAuth state purpose and trusts only the state row for user binding. `GET /auth/google/connect/callback` remains registered as a compatibility alias, but apps only need the sign-in callback URI in Google Cloud.
- (2026-07-07, Claude + Petr) **Never overwrite a refresh token with nothing.** Google only returns a refresh token when `prompt=consent` (or first grant); a callback whose token response lacks `refresh_token` keeps the stored one. Conversely, when a refresh response includes a new refresh token (rotation), it is persisted before the access token is used. This asymmetry caused real-world breakage in past integrations and is a named invariant with its own test.
- (2026-07-07, Claude + Petr) **Error taxonomy is the reliability core.** Refresh failures split into: transient (network, 408/429/5xx from Google) → bounded retry with backoff inside the call, connection stays `active`; permanent (`invalid_grant`, `invalid_client`, consent revoked) → connection becomes `reauth_required` with `last_refresh_error` recorded, and callers get `errs.FailedPrecondition` with code `google_reauth_required`. No path marks a connection broken because of a transient failure, and no path retries a permanent failure.
- (2026-07-07, Claude + Petr) **Single-flight refresh across processes** uses a Postgres row lock (`SELECT ... FOR UPDATE` on the connection row inside the refresh transaction): with `scenery up` plus a `scenery worker` process, an in-process mutex is not enough. Losers of the race re-read the row and use the fresh token.
- (2026-07-07, Claude + Petr) **Tokens are encrypted at rest** with AES-256-GCM (stdlib only). The key comes from an env var named by `auth.google_oauth.token_cipher_key_env` (default `AuthTokenCipherKey`, 32 bytes base64); in local runtime with no key set, a fixed dev key is derived from the local dev JWT secret so `scenery up` works with zero setup. Env var justification (per repo env policy): this is a secret and cannot live in `.scenery.json`; it follows the existing standard-auth `*Env` mechanism.
- (2026-07-07, Claude + Petr) **Connections are per user, not per tenant.** A Gmail grant is personal. The table keys on `(user_id, provider)` with the Google `sub` recorded; a second Google account for the same user is out of scope (documented limitation).
- (2026-07-07, Claude + Petr) **Gmail send reliability lives in ONLV** as a durable-task outbox (scenery `durable` package): the send endpoint enqueues, the worker sends, and a recorded Gmail `message.id` per outbox row makes retries idempotent. scenery does not gain a Gmail-specific API surface; the cookbook recipe defines this pattern instead.
- (2026-07-08, implementation) **Google connection token errors use dedicated error codes.** `auth.GoogleAccessToken` and `auth.GoogleAccessTokenForUser` return `google_reauth_required` for reconnect prompts and `google_scope_missing` for allowed-but-ungranted scopes. Requests for scopes outside `auth.google_oauth.allowed_scopes` fail with `permission_denied`.
- (2026-07-08, acceptance) **Do not revoke the user's real Gmail grant during acceptance.** The fake-Google matrix covers `invalid_grant` / revoke behavior and proves the row flips to `reauth_required`; the real ONLV account stays connected so the user can test Gmail product work immediately.

## Outcomes & Retrospective

Implementation milestones 1-4 are complete. Real-Google consent, token retrieval, encrypted storage, and a Gmail REST profile call are proven in ONLV against `p.brazdil@gmail.com`. Milestone 5 remains explicitly optional and deferred to the downstream ONLV Gmail product plan.

## Context and Orientation

Read plan 0098 first; this plan assumes its vocabulary and its conditional-registration plumbing. Relevant code:

- `auth/standard_google.go` — existing sign-in flow; `exchangeGoogleCode`, `verifyGoogleIDToken`, and the endpoint-URL vars (test-overridable after 0098) are reused here.
- `auth/standard.go` — `StandardConfig` / `GoogleOAuthConfig` (gains `TokenCipherKeyEnv`), `registerStandardAuthEndpoints`, lazy `standardAuthService`.
- `auth/db/schema` — the auth schema is embedded from `auth/db/gen/schema.sql` and bootstrapped by `bootstrapStandardAuthSchema` with `CREATE TABLE IF NOT EXISTS`, so adding a table is additive and self-applying. Queries live in `auth/db/queries.sql`, regenerated by `scripts/gen-auth-sqlc.sh`.
- `internal/standardauthmeta`, `internal/clientgen/typescript_standard_auth.go` — endpoint lists; new connection endpoints join the Google-enabled conditional group from 0098.
- `auth/standard_postgres_test.go` — live-Postgres test scaffolding.
- `durable`, `cron` — scenery's durable task and cron packages, referenced by the ONLV outbox pattern and optional Milestone 5.

Key Google OAuth facts this design encodes (these are the root causes of the past frustration, verify against current Google docs during implementation):

- **Consent screen in "Testing" publishing status ⇒ refresh tokens expire after 7 days.** This alone explains most "it worked for a week then died" histories. The cookbook recipe must instruct: set the OAuth consent screen to "In production" (personal-use apps with restricted Gmail scopes will show the unverified-app warning and are capped at 100 users, but refresh tokens stop 7-day-expiring), or use an "Internal" app under Google Workspace.
- A refresh token is only issued when `access_type=offline` and (after the first grant) `prompt=consent`; re-running consent without it returns no refresh token.
- Refresh tokens die outside our control: user revocation via Google account settings, Google account password change (historically revokes Gmail-scope tokens), >50 outstanding refresh tokens per account per client, 6 months unused. Therefore `reauth_required` is a normal state to design for, not an error to eliminate.
- `include_granted_scopes=true` enables incremental consent; the granted scopes are whatever the token response's `scope` field says, not what was requested — store the response value.

## Milestones

1. **Connection storage.** New table `scenery.scenery_auth_google_connections`: `id` uuid PK, `user_id` FK → users, `provider_subject` (Google `sub`), `email`, `scopes` (text, space-joined), `refresh_token_ciphertext` bytea, `access_token_ciphertext` bytea, `access_token_expires_at` timestamptz, `status` text (`active` | `reauth_required` | `disconnected`), `last_refresh_at`, `last_refresh_error` text, timestamps; unique on `(user_id)` for provider google (single-connection-per-user decision). sqlc queries: get-by-user (with `FOR UPDATE` variant), upsert-on-connect, update-tokens, mark-reauth-required, disconnect. AES-256-GCM cipher helper in `auth/` (stdlib `crypto/aes`, `crypto/cipher`) with key resolution per the Decision Log. Live-Postgres store tests.
2. **Connection endpoints** (registered only when `google_oauth.enabled`, extending 0098's conditional group):
   - `POST /auth/google/connect/start` (Auth, typed): body `{scopes: [...], redirect_path}`; validates scopes against an allowlist config (`auth.google_oauth.allowed_scopes` in `.scenery.json`, so an app declares which Google APIs it may ask for); creates a state row bound to `user_id` (extend `scenery_auth_oauth_states` with nullable `user_id` and `purpose` columns — additive); returns `{authorize_url}` with `access_type=offline`, `include_granted_scopes=true`, and `prompt=consent` iff no usable stored refresh token covers the requested scopes.
   - `GET /auth/google/callback` (Public, raw): consumes state, dispatches Google connection states by `purpose`, exchanges code (PKCE), verifies ID token, requires token `sub` to match the user's existing google identity if one exists (creates it otherwise, reusing `finishGoogleSignIn` linking rules), upserts the connection (union of scopes from the response `scope` field; keep old refresh token if response has none), redirects to `PublicAppURL + redirect_path` with `?google_connected=1` or `?error=...`. `GET /auth/google/connect/callback` remains a compatibility alias.
   - `GET /auth/google/connection` (Auth, typed): `{status, email, scopes, connected_at, last_refresh_at, reauth_reason}` — never token material.
   - `POST /auth/google/connection/disconnect` (Auth, typed): best-effort revoke at `https://oauth2.googleapis.com/revoke`, then status `disconnected` and ciphertexts nulled.
3. **Token access API + refresh engine.** `auth.GoogleAccessToken(ctx context.Context, scopes ...string) (string, error)` (user from ctx auth data; also a `ForUser` variant for worker/durable contexts that carry no request auth): fast path returns cached access token when it covers the scopes and expires more than 60s from now; otherwise opens a transaction, `FOR UPDATE`-locks the row, re-checks (another process may have refreshed), refreshes at the Google token endpoint with the decrypted refresh token, applies the error taxonomy and rotation invariant from the Decision Log, persists, commits. Fake-Google tests: happy refresh; concurrent callers from two `*sql.DB` handles produce exactly one upstream refresh; rotation persisted; 500-then-200 retried within the call; `invalid_grant` flips status and later calls fail fast with `google_reauth_required` without hitting the network; scope-not-granted returns code `google_scope_missing`.
4. **Docs.** `docs/local-contract.md` (connection endpoints + JSON shapes + status enum), `docs/app-development-cookbook.md` ("Gmail integration" recipe: consent-screen publishing-status warning, scope allowlist config, connect button flow, `GoogleAccessToken` usage, per-request token fetch with one forced-refresh retry on Gmail 401, 429/quota backoff, incremental sync via `users.history.list` with stored `historyId` and full-resync fallback on 404, drafts via `drafts.create`/`drafts.update`, send via durable outbox with recorded message id, threading via `threadId` + `References`/`In-Reply-To`), `docs/environment.md` + `docs/environment.registry.json` (`AuthTokenCipherKey`), `SKILL.md` if it enumerates auth endpoints, `docs/knowledge.json` freshness.
5. **Optional (decide after 3/4 land).** `cron`-based proactive refresh in the worker role (refresh connections whose access token expires within 10 minutes; demote broken ones early so users see "reconnect" before they try to send mail), and ONLV end-to-end acceptance: ONLV repo gets its own small app-local plan (connect button, mailbox sync service, drafts, durable send outbox) referencing this contract — that plan lives in the onlv repo, not here.

## Plan of Work

Milestones land in order; 1 and 2 are independent of ONLV and keep the repo green individually. Milestone 3 is the reliability heart — most of its effort is the fake-Google test matrix, which reuses 0098's overridable endpoint vars and fake JWKS/token server, extended with a refresh-grant route and scriptable failure sequences. Milestone 4 is documentation of contracts created in 1–3 and must land in the same plan window (public JSON surface rule). Milestone 5 is explicitly deferred and gets its own go/no-go note in this plan's Decision Log when reached.

Scope exclusions, recorded so they are deliberate: multiple Google accounts per user; per-tenant/shared mailboxes; Gmail push notifications (`users.watch` + Pub/Sub — requires a Google Cloud Pub/Sub topic, out of scope for local-first dev; polling sync is the baseline); any scenery-side Gmail REST wrapper package (ONLV calls the Gmail REST API directly with the token; revisit only if a second app needs it).

## Concrete Steps

All commands run from the repo root `/Users/petrbrazdil/Repos/scenery`.

1. Milestone 1: edit the auth schema source and `auth/db/queries.sql`; run `scripts/gen-auth-sqlc.sh`; confirm `bootstrapStandardAuthSchema` applies the new table idempotently on an existing dev database (it must, via `CREATE TABLE IF NOT EXISTS`; the `oauth_states` column additions need `ADD COLUMN IF NOT EXISTS` statements appended to the embedded schema — verify the bootstrap path executes them). Add `auth/standard_google_cipher.go` (seal/open helpers + key resolution) with unit tests, and `auth/standard_google_connections.go` (store layer) with live-Postgres tests following `auth/standard_postgres_test.go`.
2. Milestone 2: implement the four endpoints in `auth/standard_google_connections.go` / extend `auth/standard_google.go`; register them in the Google-enabled group in `registerStandardAuthEndpoints`; extend `internal/standardauthmeta` + `internal/clientgen/typescript_standard_auth.go`; add `allowed_scopes` to `AuthGoogleConfig` in `internal/app/root.go` and `internal/codegen/config.go`; extend the fixture app config; update goldens.
3. Milestone 3: implement `GoogleAccessToken` (+`ForUser`) in `auth/standard_google_tokens.go`; extend the fake Google with `grant_type=refresh_token` handling and a scriptable response queue; write the test matrix listed in Milestone 3.
4. Milestone 4: doc edits as listed; re-run `scenery inspect docs --json` and fix any freshness complaints for touched docs.
5. Milestone 5 (if approved later): cron job registration in the auth package guarded by config; ONLV-side plan authored in the onlv repo.

## Validation and Acceptance

- `go test ./...` and targeted `go test ./auth -run Google -count=1` green (live Postgres required; skip behavior follows existing auth Postgres tests).
- `scenery harness self --summary --write` with the worktree-local `.scenery/harness/bin/scenery` build (no `go install`).
- `scenery check --app-root testdata/apps/standard-auth --json`; `scenery inspect endpoints --json` on the fixture shows connection endpoints iff Google enabled; `scenery generate client --lang typescript` includes them iff enabled.
- Acceptance is the Milestone 3 test matrix passing, plus a manual smoke against real Google documented in Artifacts: connect a real Gmail account in a dev app, observe `active`, revoke access in Google account settings, observe the next `GoogleAccessToken` call flip the connection to `reauth_required` with a typed error.

## Idempotence and Recovery

Schema changes are additive (`CREATE TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`) and applied by the existing advisory-locked bootstrap; re-running is safe. The refresh transaction either commits new ciphertexts or leaves the row untouched; a crash mid-refresh loses nothing (the old refresh token is still valid — Google refresh tokens are not single-use). Disconnect is safe to repeat. If the cipher key is lost, connections cannot be decrypted: the recovery path is bulk-marking rows `reauth_required` (a one-line SQL update) and letting users reconnect — document this in the cookbook rather than building key rotation now.

## Artifacts and Notes

- Manual smoke checklist and the real-Google console setup notes get recorded here during implementation (consent screen publishing status, test-user list, scopes requested, redirect URIs registered).
- Gmail scopes ONLV will request: `https://www.googleapis.com/auth/gmail.modify` covers read + drafts + send and is one restricted scope instead of three; record the final choice here when ONLV integration starts.
- Validation artifacts from 2026-07-08:
  - `SCENERY_TEST_DATABASE_URL="$(jq -r '...redacted...' ~/.scenery/agent/postgres/server.json)" go test -v ./auth -run 'TestGoogle(Connection|AccessToken|TokenCipher)|TestGoogleOAuthBrowserFlowWithFakeGoogle|TestGoogleAppRedirectUsesRequestOriginBeforeConfiguredPublicAppURL|TestGoogleRedirectURIUsesRequestHostBeforeConfiguredPathModeURL' -count=1` passed against the managed local Postgres server.
  - `go test ./...` passed.
  - `go run ./cmd/scenery inspect docs --json` reported 73 indexed docs, 0 missing, 0 stale, and 18 review-due docs.
  - `go run ./cmd/scenery check --app-root testdata/apps/standard-auth --json` returned `ok: true` with no diagnostics.
  - `go run ./cmd/scenery inspect endpoints --app-root testdata/apps/standard-auth --json` showed no Google endpoints with Google disabled; a temp copy with `auth.google_oauth.enabled: true` showed `DisconnectGoogleConnection`, `GetGoogleConnection`, `GoogleCallback`, `GoogleConnectCallback`, `GoogleConnectStart`, and `GoogleStart`.
  - `go run ./cmd/scenery generate client --app-root testdata/apps/standard-auth --lang typescript --output /tmp/standard-auth-client-disabled-0099.ts` produced no Google methods/paths; the enabled temp copy produced all six methods/paths.
  - `go run ./cmd/scenery harness self --summary --write` returned `ok: true`, `status: "pass_with_warnings"`; warnings were review-due docs, slow package timings, and the generated sqlc file over the architecture warning threshold.
- Real-Google smoke attempt from 2026-07-08:
  - Disposable app `/tmp/scenery-google-smoke-0099` ran with the worktree-local `.scenery/harness/bin/scenery` on `http://localhost:4755`.
  - `POST /users/dev-bootstrap` minted a dev auth token after adding an isolated `google_smoke_0099` managed Postgres database.
  - `POST /auth/google/connect/start` returned a Google authorize URL with `access_type=offline`, `include_granted_scopes=true`, `prompt=consent`, scope `https://www.googleapis.com/auth/gmail.modify`, and redirect URI `http://localhost:4755/api/auth/google/connect/callback`.
  - Chrome reached Google as `p.brazdil@gmail.com`, but Google blocked consent with `Error 400: redirect_uri_mismatch`; the OAuth client had not registered the new connection callback URI.
  - ONLV smoke on `http://localhost:4747` produced the same redirect mismatch for `http://localhost:4747/api/auth/google/connect/callback`.
  - ONLV smoke on `https://local.clean.tech` proved the existing sign-in redirect URI `https://local.clean.tech/api/auth/google/callback` is accepted by Google, while `https://local.clean.tech/api/auth/google/connect/callback` is rejected. The implementation now reuses `/auth/google/callback` for connection states to avoid the extra Google Cloud redirect registration.
  - After restarting ONLV with the shared-callback fix, `POST /auth/google/connect/start` returned a Google authorize URL with redirect URI `https://local.clean.tech/api/auth/google/callback`, `access_type=offline`, and `prompt=consent`. Chrome reached Google's "Google hasn't verified this app" warning for `p.brazdil@gmail.com`; the user clicked through it while the OAuth state was fresh.
  - Google redirected to `https://local.clean.tech/next/?google_connected=1`.
  - `GET /auth/google/connection` with the ONLV dev auth token returned `{"status":"active","email":"p.brazdil@gmail.com","scopes":["https://www.googleapis.com/auth/gmail.modify","https://www.googleapis.com/auth/userinfo.email","https://www.googleapis.com/auth/userinfo.profile","openid"]}`.
  - Disposable smoke helper `/tmp/scenery-google-token-smoke-0099` imported the worktree-local `scenery.sh/auth`, reused ONLV's runtime `JWTSecret` and Google OAuth client env without printing secrets, called `auth.GoogleAccessTokenForUser(ctx, "519e1a33-1f0a-4089-b588-d613ef1ce11e", "https://www.googleapis.com/auth/gmail.modify")`, and received a non-empty access token.
  - The same helper verified the connection row stayed `active`, with encrypted refresh ciphertext stored, encrypted access ciphertext stored, and an access-token expiry at `2026-07-08T12:19:50.001843+02:00`.
  - Gmail REST profile smoke reached `https://gmail.googleapis.com/gmail/v1/users/me/profile` but returned HTTP 403 `PERMISSION_DENIED`; the error details include `SERVICE_DISABLED` and the activation URL for Google Cloud project `583487526019`. This is external Google Cloud setup, not a Scenery token storage or refresh failure.
  - After explicit user approval, Gmail API was enabled in Google Cloud project `cleantech-501710` / `583487526019`.
  - Re-running `/tmp/scenery-google-token-smoke-0099` returned `google_access_token_available: true`; Gmail profile returned `ok: true`, HTTP 200, `emailAddress: "p.brazdil@gmail.com"`, `historyId_present: true`, `messagesTotal: 96310`, and `threadsTotal: 72411`.
  - The same smoke reported the stored connection still `active`, with encrypted refresh ciphertext stored, encrypted access ciphertext stored, and scopes including `https://www.googleapis.com/auth/gmail.modify`.
  - Final targeted validation after the real-Gmail smoke: `go run ./cmd/scenery inspect docs --json | jq '{docs:(.documents|length), missing:.summary.missing_count, stale:.summary.stale_count, review_due:.summary.review_due_count}'` returned `{"docs":73,"missing":0,"stale":0,"review_due":18}`; the focused Google auth test command above passed again.

## Interfaces and Dependencies

- Builds on completed plan 0098 (conditional registration, callback error-redirect convention, fake-Google scaffolding, endpoint-URL override vars).
- New public surface (all conditional on `auth.google_oauth.enabled`): `POST /auth/google/connect/start`, shared raw `GET /auth/google/callback` handling login and connection states, compatibility raw `GET /auth/google/connect/callback`, `GET /auth/google/connection`, `POST /auth/google/connection/disconnect`; Go API `auth.GoogleAccessToken` / `auth.GoogleAccessTokenForUser`; config `auth.google_oauth.allowed_scopes`, `auth.google_oauth.token_cipher_key_env`; env `AuthTokenCipherKey`; error codes `google_reauth_required`, `google_scope_missing`.
- Schema: new table `scenery.scenery_auth_google_connections`; `scenery_auth_oauth_states` gains nullable `user_id`, `purpose`.
- No new Go module dependencies (stdlib crypto, existing `golang-jwt`, `httptest` fakes).
- Downstream consumer: ONLV Gmail features (own plan in the onlv repo); the contract they code against is Milestone 2 + 3 surfaces and the cookbook recipe.
