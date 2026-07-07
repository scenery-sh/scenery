# Google Connections and Gmail Platform: Reliable Offline Tokens for App Integrations

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Give scenery apps a reliable way to hold long-lived Google API access on behalf of a signed-in user, so that ONLV can fetch, draft, and send Gmail without the recurring "token expired, everything broke" failure mode. scenery standard auth gains a **Google connection**: a per-user grant of Google API scopes with an encrypted stored refresh token, disciplined automatic refresh, an explicit health status, and a re-consent flow. Gmail product behavior (mailbox sync, drafting, sending) stays in the ONLV app and consumes the connection through a small Go API and generated client endpoints.

Observable end state:

- A signed-in user clicks "Connect Gmail" in the app; after Google consent, `GET /auth/google/connection` reports `status: "active"` with the granted scopes.
- App code calls `auth.GoogleAccessToken(ctx, scopes...)` and always receives a currently-valid access token, transparently refreshed and cached, safe under concurrent calls from multiple app processes.
- When Google permanently invalidates the refresh token (revocation, expiry), the connection flips to `status: "reauth_required"`, app code gets a typed `failed_precondition` error (not a random 401 deep in a Gmail call), and the frontend can render a "Reconnect Google" button that restarts consent. Nothing silently retries forever.
- A test suite drives all of this against a fake Google token server, including concurrent refresh, refresh-token rotation, transient 5xx, and permanent `invalid_grant`.

## Progress

- [ ] Milestone 1: connection storage — schema, sqlc queries, token cipher, store tests
- [ ] Milestone 2: connect / callback / status / disconnect endpoints
- [ ] Milestone 3: token access API + refresh engine with fake-Google tests
- [ ] Milestone 4: docs — local contract, cookbook Gmail recipe, env registry, ONLV integration contract
- [ ] Milestone 5 (optional, separate decision): worker cron proactive refresh; ONLV end-to-end acceptance

(2026-07-07) Plan created. No implementation started. Depends on plan 0098 (Google social login enable/disable + fake-Google test scaffolding) landing first.
(2026-07-07) Plan 0098 completed in this changeset, so this plan is unblocked once the changeset lands.

## Surprises & Discoveries

- (2026-07-07, plan authoring) The existing sign-in flow (`auth/standard_google.go`) requests only `openid email profile`, never `access_type=offline`, and discards Google's access token — only the ID token is used. There is no token storage anywhere; this plan adds the first one.

## Decision Log

- (2026-07-07, Claude + Petr) **Split of responsibilities.** scenery owns the Google connection lifecycle (consent, storage, refresh, status, disconnect) because it already owns Google identities, sessions, and the Postgres auth schema, and because token-refresh reliability is a platform concern every app would otherwise reimplement badly. ONLV owns Gmail product behavior (what to sync, drafts UX, send pipeline) because it is product-specific. The boundary is `auth.GoogleAccessToken` + the connection endpoints; ONLV never sees a refresh token.
- (2026-07-07, Claude + Petr) **Connect flow shape.** Starting a connection is a typed authenticated endpoint `POST /auth/google/connect/start` that returns `{authorize_url}`, because raw browser redirects cannot carry the Authorization header; the OAuth state row is bound to the authenticated `user_id`, which is also the CSRF defense. The callback `GET /auth/google/connect/callback` is a public raw endpoint (Google redirects the browser there) and trusts only the state row for user binding.
- (2026-07-07, Claude + Petr) **Never overwrite a refresh token with nothing.** Google only returns a refresh token when `prompt=consent` (or first grant); a callback whose token response lacks `refresh_token` keeps the stored one. Conversely, when a refresh response includes a new refresh token (rotation), it is persisted before the access token is used. This asymmetry caused real-world breakage in past integrations and is a named invariant with its own test.
- (2026-07-07, Claude + Petr) **Error taxonomy is the reliability core.** Refresh failures split into: transient (network, 408/429/5xx from Google) → bounded retry with backoff inside the call, connection stays `active`; permanent (`invalid_grant`, `invalid_client`, consent revoked) → connection becomes `reauth_required` with `last_refresh_error` recorded, and callers get `errs.FailedPrecondition` with code `google_reauth_required`. No path marks a connection broken because of a transient failure, and no path retries a permanent failure.
- (2026-07-07, Claude + Petr) **Single-flight refresh across processes** uses a Postgres row lock (`SELECT ... FOR UPDATE` on the connection row inside the refresh transaction): with `scenery up` plus a `scenery worker` process, an in-process mutex is not enough. Losers of the race re-read the row and use the fresh token.
- (2026-07-07, Claude + Petr) **Tokens are encrypted at rest** with AES-256-GCM (stdlib only). The key comes from an env var named by `auth.google_oauth.token_cipher_key_env` (default `AuthTokenCipherKey`, 32 bytes base64); in local runtime with no key set, a fixed dev key is derived from the local dev JWT secret so `scenery up` works with zero setup. Env var justification (per repo env policy): this is a secret and cannot live in `.scenery.json`; it follows the existing standard-auth `*Env` mechanism.
- (2026-07-07, Claude + Petr) **Connections are per user, not per tenant.** A Gmail grant is personal. The table keys on `(user_id, provider)` with the Google `sub` recorded; a second Google account for the same user is out of scope (documented limitation).
- (2026-07-07, Claude + Petr) **Gmail send reliability lives in ONLV** as a durable-task outbox (scenery `durable` package): the send endpoint enqueues, the worker sends, and a recorded Gmail `message.id` per outbox row makes retries idempotent. scenery does not gain a Gmail-specific API surface; the cookbook recipe defines this pattern instead.

## Outcomes & Retrospective

Not yet completed.

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
   - `GET /auth/google/connect/callback` (Public, raw): consumes state, exchanges code (PKCE), verifies ID token, requires token `sub` to match the user's existing google identity if one exists (creates it otherwise, reusing `finishGoogleSignIn` linking rules), upserts the connection (union of scopes from the response `scope` field; keep old refresh token if response has none), redirects to `PublicAppURL + redirect_path` with `?google_connected=1` or `?error=...`.
   - `GET /auth/google/connection` (Auth, typed): `{status, email, scopes, connected_at, last_refresh_at, reauth_reason}` — never token material.
   - `POST /auth/google/connection/disconnect` (Auth, typed): best-effort revoke at `https://oauth2.googleapis.com/revoke`, then status `disconnected` and ciphertexts nulled.
3. **Token access API + refresh engine.** `auth.GoogleAccessToken(ctx context.Context, scopes ...string) (string, error)` (user from ctx auth data; also a `ForUser` variant for worker/durable contexts that carry no request auth): fast path returns cached access token when it covers the scopes and expires more than 60s from now; otherwise opens a transaction, `FOR UPDATE`-locks the row, re-checks (another process may have refreshed), refreshes at the Google token endpoint with the decrypted refresh token, applies the error taxonomy and rotation invariant from the Decision Log, persists, commits. Fake-Google tests: happy refresh; concurrent callers from two `*sql.DB` handles produce exactly one upstream refresh; rotation persisted; 500-then-200 retried within the call; `invalid_grant` flips status and later calls fail fast with `google_reauth_required` without hitting the network; scope-not-granted returns `failed_precondition` with code `google_scope_missing`.
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

## Interfaces and Dependencies

- Builds on completed plan 0098 (conditional registration, callback error-redirect convention, fake-Google scaffolding, endpoint-URL override vars).
- New public surface (all conditional on `auth.google_oauth.enabled`): `POST /auth/google/connect/start`, `GET /auth/google/connect/callback`, `GET /auth/google/connection`, `POST /auth/google/connection/disconnect`; Go API `auth.GoogleAccessToken` / `auth.GoogleAccessTokenForUser`; config `auth.google_oauth.allowed_scopes`, `auth.google_oauth.token_cipher_key_env`; env `AuthTokenCipherKey`; error codes `google_reauth_required`, `google_scope_missing`.
- Schema: new table `scenery.scenery_auth_google_connections`; `scenery_auth_oauth_states` gains nullable `user_id`, `purpose`.
- No new Go module dependencies (stdlib crypto, existing `golang-jwt`, `httptest` fakes).
- Downstream consumer: ONLV Gmail features (own plan in the onlv repo); the contract they code against is Milestone 2 + 3 surfaces and the cookbook recipe.
