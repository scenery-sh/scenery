# User Connections

Connections give trusted Go code a bearer token for one standard-auth user and
one configured external provider. Connecting never creates a login identity or
changes a profile, tenant, membership or session. Google sign-in remains controlled
by `auth.google_oauth.enabled`; connection providers are configured independently.
While `auth.enabled` is false, prepared connection declarations are inactive and
their credentials are absent from the configuration catalog.

## Provider declarations and environment credentials

Declare credential-free providers in `.scenery.json`. IDs are unique lowercase
configuration identifiers. One `(user_id, provider_id)` slot holds one external
account/resource; use distinct IDs for distinct accounts or APIs. Changing an
existing slot's account or resource requires disconnecting it first.

A Google preset:

```json
{"auth":{"enabled":true,"connections":[
  {"id":"google","preset":"google","allowed_scopes":["https://www.googleapis.com/auth/gmail.modify"]}
]}}
```

An OAuth2-only provider uses its configured account endpoint instead of an ID token:

```json
{"auth":{"enabled":true,"connections":[
  {"id":"files","protocol":"oauth2","issuer":"https://accounts.example.com",
   "authorize_url":"https://accounts.example.com/authorize",
   "token_url":"https://accounts.example.com/token",
   "revoke_url":"https://accounts.example.com/revoke",
   "account_url":"https://accounts.example.com/me",
   "subject_field":"account_id","email_field":"email","client_auth":"basic",
   "resource":"https://files.example.com","allowed_scopes":["files.read"]}
]}}
```

The endpoints above illustrate the provider's documented protocol; substitute its
actual trusted URLs. `protocol = "oidc"` uses an explicit `jwks_url` and verifies
RS256 signatures with keys of at least 2048 bits, issuer, audience/authorized
party, expiry and nonce. There is no request-controlled discovery. HTTPS is
required except literal loopback HTTP for local providers. `client_auth` is
`basic` (default), `post` or `none`. Resource selection is fixed by the declaration.
Google owns its OIDC/consent/offline/incremental-consent defaults and identity scopes.
Its connection grants expose an ID-token email only when `email_verified` is true;
an absent or false claim leaves email metadata empty.

Use the existing selected application/environment store:

```sh
scenery config set auth.connections.files.client_id client-id --env local
scenery config set auth.connections.files.client_secret --stdin --env local
scenery config set auth.connections.files.callback_uri https://app.example.com/api/auth/connections/files/callback --env local
scenery config set auth.token_cipher_key --stdin --env production
```

The cipher input is a base64-encoded 32-byte key. Credentials reach the runtime
through its configuration snapshot; never put them in declarations, generated
code or product tables. Callback defaults to the runtime's advertised API base
URL plus `/auth/connections/<id>/callback`, resolved after runtime metadata
initialization. Register that exact URI with the provider before enabling it. Call `/start` through that callback hostname; another
gateway hostname is rejected before creating state or setting its host-only browser
cookie. Cookies are shared across ports on the same hostname. Production requires
the cipher key; local development preserves the original JWT-derived key and its historical derivation label.

## Request and worker token calls

```go
// In an authenticated standard-auth request:
token, err := auth.ConnectionToken(ctx, "google", "https://www.googleapis.com/auth/gmail.modify")

// In trusted server worker code, after authorizing the stored user's work:
token, err := auth.ConnectionTokenForUser(ctx, userID, "files", "files.read")
```

Both return only bearer tokens. Required scopes are permission requirements,
not a downscoping request. Empty required scopes impose no extra permission
requirement. Scopes are case-sensitive opaque OAuth tokens; sorting/deduplication
creates no semantic aliases. Requests cannot select another user's connection,
even by replacing the argument context and starting another goroutine. Runtime
admission supplies an opaque invocation: authenticated requests remain bound to
their principal, and admitted background tasks can select an owner independently
of their workload principal. This includes event and declared schedule workers.
Preserve that context in helper goroutines; `context.Background()` and `auth.WithContext` cannot
grant worker authority. Workers must authorize tenant-scoped work before selecting
an owner. There is no HTTP token endpoint.

## Browser administration and status UI

The compiler projects the following routes into inspection whenever connections
are declared. Generated clients expose the three authenticated administration
operations; the public browser callback is opaque and excluded from typed calls:

| Method | Route | Result |
| --- | --- | --- |
| POST | `/auth/connections/{provider}/start` | `authorize_url`; private browser-binding cookie |
| GET | `/auth/connections/{provider}` | Credential-free `ConnectionResponse` |
| POST | `/auth/connections/{provider}/disconnect` | Credential-free disconnected status |
| GET | `/auth/connections/{provider}/callback` | Public, one-time verified callback; safe app redirect |

Start accepts `{scopes, redirect_path}`. State binds browser, live session/user,
provider, issuer, client, callback, resource and requested scopes. Authorization
uses PKCE S256. Callback replay, another browser or issuer/provider mismatch fails.
Return paths must be relative application paths. Connecting an unrelated external
account does not replace the current login identity.
An invalid, expired or consumed callback state returns HTTP 400. A callback state
storage failure returns `connection_unavailable` (HTTP 503), retaining the browser
binding so a still-pending callback can be retried after recovery. Start also
returns `connection_unavailable` for session/owner storage failures; a missing,
revoked or expired session remains `unauthenticated`.

A status component can use the generated methods without handling credentials:

```tsx
// ConnectionResponse is imported from the app's generated client types.
function ConnectionStatus({ status, reconnect }: {
  status: ConnectionResponse;
  reconnect: () => void;
}) {
  const needsConsent = status.status === "reauth_required" || status.status === "disconnected";
  return <section>
    <p>{status.email ?? status.provider}: {status.status}</p>
    {needsConsent && <button onClick={reconnect}>Reconnect</button>}
  </section>;
}

// The app's action handler owns loading/error state and exception handling.
async function reconnectGoogle() {
  const start = await client.connectionStart({
    provider: "google",
    scopes: ["https://www.googleapis.com/auth/gmail.modify"],
    redirectPath: "/settings",
  });
  if (start.kind === "result" && start.name === "success") {
    window.location.assign(start.value.authorizeUrl);
  }
}
```

Handle each typed failure and transport exception in the consuming app. Domain
responses use declared operation-error outcomes: `invalid_request` (400),
`unauthenticated` (401), `forbidden` (403), `internal` (500) and `unavailable` (503).
Their JSON problem codes preserve the specific runtime classification, including
`connection_not_configured` and `permission_denied`. Declared transport/admission
failures retain their exact namespaced codes. Keep this
domain flow in the app's component slot.
Metadata includes provider/account/resource, effective scopes, timestamps and a
sanitized reauthorization/revocation reason; never tokens or ciphertext.

## Refresh, failures and disconnect

A SQL row lock and bounded session advisory lease serialize independent processes.
A durable `refresh_pending` intent is committed before remote refresh. Successful
rotation and effective grant are persisted before a missing-scope error is returned.
Omitted refresh credentials/scopes retain their previous values; explicit empty
scope narrows the grant. Missing expiry is not invented: an uncached newly obtained
token may be issued once, then needs refresh or reauthorization.
When a grant declares an expiry but has no refresh credential, it remains usable
until its actual expiry; the one-minute early-refresh window applies to
refreshable grants. When an exhausted grant has no refresh credential, token
issuance commits `reauth_required` with reason `refresh_credential_missing` before
returning its reauthorization error, so status clients can offer reconnect. Failed
status writes or commits return storage unavailability and remain retryable.

| Error | Meaning |
| --- | --- |
| `connection_not_configured` | No such registered provider/runtime |
| `connection_not_connected` | No active connection |
| `connection_scope_missing` | Effective grant lacks a required permission |
| `connection_reauth_required` | Revoked/missing credential or ambiguous refresh outcome |
| `connection_configuration` | Key, provider credentials, token type or schema configuration failed |
| `connection_unavailable` | Storage failure or explicit temporary provider refusal |

Invalid client/key failures do not change consent. An explicit temporary refusal,
including HTTP 429 regardless of its response body, can be retried by a later
caller. No hidden retry reuses a potentially rotated
credential after a lost response, process interruption or failed final commit:
reconnect the affected slot. Remote and local commits cannot provide exactly-once
refresh. Disconnect durably prevents local issuance before remote revocation and
cannot be undone by a late refresh. Revocation results are sanitized (`succeeded`,
`unsupported`, `failed`, `configuration_changed`, `credential_unavailable`);
`pending` records an interrupted revocation attempt after local issuance stopped.
Previously issued tokens may remain usable at the provider.

Every successful disconnect expires pending browser callbacks in the same local
transaction, including repeated disconnects and a slot with no connection row.
State creation and callback consumption share the owner/provider lease. A callback
retains it through remote exchange and grant installation; consumption is committed
before remote work to prevent replay after a crash. Disconnect waits for an in-flight
callback, then removes its grant; a completed disconnect cannot be undone by it.
Expiry cleanup and pending owner/provider invalidation have dedicated SQL indexes.
The service checks the connection schema once per pool after success; failed or
canceled checks remain retryable. Schema migration requires stopping writers.

## Working examples and validation

The [backend-only fixture](../testdata/apps/basic/app.scn) builds and serves in
`--probe core-separation` without UI prerequisites. The
[SQL library fixture](../testdata/apps/worktree-postgres/README.md) exercises
create/list/borrow/return without pages in `--probe worktree`. The named `auth`
probe implements full [Google](../scripts/verify/testdata/authprobe/google_journeys.go)
and [OAuth2-only](../scripts/verify/testdata/authprobe/independent.go) examples with
request/worker calls, status, refresh and disconnect against owned fake providers.

Use `go test ./auth ./internal/connectionvault ./internal/authprovider/...` for
injected, service-free checks. `go run ./scripts/verify --probe auth --summary --write`
owns SQL/migration/process proof and cleanup. See the
[coordinated migration runbook](runbooks/auth-connections-migration.md) before
using a new binary with existing Google connections.
