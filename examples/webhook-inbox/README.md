# Webhook Inbox

A small independent Scenery application, with no ONLV dependency or UI:

- `POST /events` persists a durable job and returns its admission receipt (`202`).
- A background worker stores the original payload and its SHA-256 digest in
  `inbox.processed_events`.
- `GET /events/{event_id}` requires standard authentication and returns the
  processed record (`200`) or a missing record identifier (`404`). Missing means
  pending **or** unknown; this endpoint does not inspect private queue tables.
- `client/generated` is the committed fetch client. Package exports determine
  its methods; `.scenery` and generated Go projections are ignored local output.

This is loopback-only demonstration code. The admission endpoint has no provider
signature verification or application rate limit. Every authenticated demo user
can read every event. Do not publish it as a secure multi-tenant webhook service.
Local dev bootstrap proves bearer-token verification, not production signup,
email delivery, refresh sessions, or authorization isolation.

## Run The Complete Proof

From the Scenery source checkout, with Go, Docker, Bun, curl, and jq available:

```sh
go build -o .scenery/harness/bin/scenery ./cmd/scenery
bash examples/webhook-inbox/verify.sh
apps/console/node_modules/.bin/tsc -p examples/webhook-inbox/client/tsconfig.json
```

The script copies ordinary app source to a new temporary directory, points its
Go module at this checkout, regenerates/checks/builds, and starts disposable
PostgreSQL with ordinary durability settings. It admits a job with no worker,
restarts the API, checks that the job is still queued, starts a separate worker,
then exercises the typed client with missing, invalid, and valid credentials.
It also checks malformed input and duplicate delivery. Processes and the owned
container/database are removed on exit; the printed directory retains proof
logs and the standalone copy. No installed CLI or shared agent is modified.

## Develop A Standalone Copy

Copy this directory outside the Scenery checkout. Update its `go.mod` replacement
to your Scenery source checkout with `go mod edit -replace=scenery.sh=/path/to/scenery`.
Keep `app.lock.scn`: built-in providers are explicitly pinned to their descriptor
content. Use a coherent binary/source revision, and explicitly refresh the lock
when its provider content changes (unrelated spec/producer changes do not relock).

```sh
scenery fmt --check -o json
scenery provider lock -o json
scenery provider lock --check -o json
scenery generate -o json
scenery check -o json
go test ./...
scenery build --target development --output ./bin/webhook -o json
```

`generate` prepares ordinary Go contracts inside this module together with the
client. Use `generate --target contracts` for Go-only bootstrap; a fresh checkout
needs it before raw Go tooling. The exact output roots are ignored, and no
editor module or `go.work` is generated. Use the standalone copy to prove
independent module/dependency resolution.

For the usual managed development loop, create a local `.env` file containing
any app-specific configuration and run `scenery up --detach --wait ready`.
The declared `inbox` service and `database.apply.command` provide the managed
database/schema path. The explicit split-process proof instead supplies its own
`DATABASE_URL`, creates the service schema, runs `go run ./cmd/schema`, and uses
the existing `SCENERY_ROLE=api` / `SCENERY_ROLE=worker` runtime controls.

Regenerate the fetch client after contract changes:

```sh
scenery generate --target typescript_client.public_api -o json
scenery generate --check -o json
```

`inbox/package.scn` shows the complete enqueue response mapping and the separate
direct status operation. Standard HTTP auth is declared in `app.scn` and passed
as a typed module input; `.scenery.json` enables its runtime implementation.
The Go service receives SQL through `input.Dependencies.Database`, and handlers
use their operation-specific generated input/outcome types.

Inspect those names in the standalone copy, after `generate`:

```sh
go doc example.com/webhook-inbox/inbox/scenerycontract.InboxConstructorInput
go doc example.com/webhook-inbox/inbox/scenerycontract.InboxDependencies
go doc example.com/webhook-inbox/inbox/scenerycontract.ProcessInput
go doc example.com/webhook-inbox/inbox/scenerycontract.Event
go doc example.com/webhook-inbox/inbox/scenerycontract.ProcessOutcome
```

For example, `event_id` becomes `EventId`, not `EventID`. Constructor SQL is
`input.Dependencies.Database`, not a field on the constructor input itself.
`generate -o json` reports client binding counts and changed/checked artifacts;
an empty client warning points to package exports and target selection.
