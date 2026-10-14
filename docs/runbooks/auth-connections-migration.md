# Google Connection Cutover

This is a coordinated schema, API and configuration migration. Installing a
binary does not migrate credentials. Do not run old and new token writers together.
Read [connection ownership](../auth-connections.md) and verify the exact application,
environment, database and every refreshing process before mutation.

Legacy standard-auth installations created the old connection table even when
Google connections were unused. If that table is empty and no connection provider
is needed, apply the SQL cutover and keep `auth.connections` absent; provider setup
and credential copying in steps 2–3 are unnecessary. Never invent credentials to
migrate an unused table.

## Prepare and apply

1. Take an encrypted backup; record row counts and ownership invariants without
   including credential values. Quiesce app instances and trusted workers. Retain
   the existing `auth.token_cipher_key` (or supported local JWT-derived key).
2. Update the app declaration to include provider `id = "google", preset = "google"`
   under `auth.connections`, moving the old `google_oauth.allowed_scopes` list to
   that provider. Keep `google_oauth.enabled` only for sign-in. Register the new
   `/auth/connections/google/callback` URI with Google before route cutover.
3. Use the selected environment's authoritative configuration store. The invoking
   CLI and any selected SSH receiver must run matching current Scenery versions;
   upgrading the receiver does not itself migrate data or enable token writers:

   ```sh
   scenery config show --env <environment> -o json
   scenery config migrate-connections --env <environment> --expect-revision <revision> -o json
   scenery config set auth.connections.google.callback_uri <registered-uri> --env <environment>
   ```

   This one-shot command copies legacy client ID/secret into the `google` slot
   through the existing local/SSH authority and secret backend. It preserves
   sign-in keys and the cipher key, checks destination conflicts and safely resumes
   a partial copy. Each key has its own published revision. It never exports secret
   plaintext or performs runtime fallback reads. Copy/migrate other provider slots
   explicitly using normal `config set` commands.
4. Apply the exact checked SQL file to the verified stopped database with the
   operator's existing secure connection selection:

   ```sh
   psql --single-transaction --set ON_ERROR_STOP=1 --file auth/db/migrations/0001-provider-neutral-connections.sql
   ```

   Do not pass a password in argv or evidence. The script verifies the legacy
   shape, transactionally renames/extends it, binds old rows to `google` and retains
   IDs, subjects, scopes, statuses, expiry, timestamps and ciphertext. It expires
   only old connection handshakes, preserving login states and all unrelated auth
   rows. Clean installations use the current schema; unknown/ambiguous families
   fail closed. A rerun verifies the current family and installs the expiry and
   pending owner/provider callback-state indexes on an already current schema.
5. Verify counts, unique user/provider ownership, schema version and ciphertext
   equality. Decrypt only in memory under the unchanged key. Start one canonical
   implementation, then prove status, cached/rotated token, reconnect and disconnect
   before enabling the remaining writers. Never put tokens into logs, traces,
   inspection output or migration reports.

## Public surface mapping

| Retired | Current |
| --- | --- |
| `auth.GoogleAccessToken(ctx, scopes...)` | `auth.ConnectionToken(ctx, "google", scopes...)` |
| `auth.GoogleAccessTokenForUser(ctx, user, scopes...)` | `auth.ConnectionTokenForUser(ctx, user, "google", scopes...)` |
| `/auth/google/connect/start` | `/auth/connections/google/start` |
| `/auth/google/connection` | `/auth/connections/google` |
| `/auth/google/connection/disconnect` | `/auth/connections/google/disconnect` |
| Connection use of `/auth/google/callback` | `/auth/connections/google/callback` |
| `google_reauth_required`, `google_scope_missing` | `connection_reauth_required`, `connection_scope_missing` |
| `auth.google_oauth.allowed_scopes` | `auth.connections[].allowed_scopes` |
| `auth.google_client_id`, `auth.google_client_secret` for connections | `auth.connections.google.client_id`, `.client_secret` |

Google sign-in retains its old start/callback and sign-in credential keys. No old
connection helper, route, decoder or configuration fallback is kept.

## Failure and rollback boundary

SQL failure rolls back the transaction. A credential-copy failure may leave its
first published key; inspect the returned/current revision and rerun. Conflicting
existing destination values require an explicit operator decision and are never
overwritten. Missing legacy credentials, stale revisions and destination conflicts
are CLI precondition failures (exit 3); an unavailable secret backend is a
capability failure (exit 4), never an internal-error classification. Unknown schema
requires reconciliation, not adoption or empty reset.

Before any new provider writes or refresh rotation, an operator may restore the
verified old backup and old configuration/binary as a coordinated rollback while
all writers remain stopped. After remote rotation or new-provider writes, that
backup may contain invalid credentials or omit new accounts. Prefer forward repair
and provider reauthorization; do not claim that binary rollback or restoring old
ciphertext restores provider validity. Interrupted/ambiguous refreshes surface
reauthorization and do not retry a potentially rotated credential.

The named auth probe verifies empty/populated migration, failure rollback, rerun,
unknown-shape refusal, unchanged unrelated data and original-key decryption, plus
independent-process rotation/disconnect/crash recovery. This implementation does
not mutate production databases or environment credentials by itself.
