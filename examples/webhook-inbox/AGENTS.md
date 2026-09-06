# Webhook Inbox Example

## Purpose

Independent, loopback-only durable webhook example. Do not connect it to ONLV or
an existing application database. This is a shared inbox, not a multi-tenant app
or a production signed-webhook receiver.

## Ownership And Contracts

- App root and config: this directory and `.scenery.json`.
- Graph: `app.scn`, `app.lock.scn`, and `inbox/package.scn`.
- Go: `inbox/service.go`; database schema application: `cmd/schema`.
- No frontend. Generated fetch client: `client/generated`, declared managed root.
- `DATABASE_URL` names the isolated app database. No secret values are committed.
  Local proof uses Scenery's existing local JWT default and local dev bootstrap;
  nonlocal environments require `JWT_SECRET` and must disable dev bootstrap.
- Admission persists the durable job; only the worker creates the processed row.
  The first payload wins, including after queue deduplication expires.
- Status requires standard auth. Any authenticated demo user can read any row.
  Missing means unprocessed or unknown, not a failed queue lookup.

## Verification

From the Scenery root, build the worktree-local CLI and run
`bash examples/webhook-inbox/verify.sh`. It uses Docker PostgreSQL and Bun,
stops only its own processes/container, and retains source/logs in a temporary
directory. It does not install Scenery or start the shared development agent.

After declaring/updating builtin providers, explicitly run
`scenery provider lock -o json` and review the lock diff. Inside a standalone
copy: `scenery generate -o json`, `scenery check -o json`,
`go test ./...`, and `scenery harness -o json --write`.
Typecheck the client with `tsc -p client/tsconfig.json` and regenerate it using
`scenery generate --target typescript_client.public_api -o json`.
Never edit generated clients or commit Go/editor cache output.
