# Data Platform Fixture

This fixture demonstrates the beta dynamic data platform through ordinary onlava services.

## Local PostgreSQL

DB-backed repository tests start `postgres:17-alpine` with `testcontainers-go`
when Docker is available. Set a PostgreSQL URL only when you want to reuse an
existing database instead:

```sh
export ONLAVA_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable'
```

The same variable is accepted by the fixture as `DATABASE_URL` fallback.

## Validate

From the onlava repo root:

```sh
onlava check --app-root testdata/apps/data-platform --json
go test ./internal/objectstore ./internal/datainspect -count=1
```

## Run

```sh
onlava run --app-root testdata/apps/data-platform
```

Sample calls, assuming the app is listening on `127.0.0.1:4000`:

```sh
curl -sS -X POST http://127.0.0.1:4000/data/objects \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant","tenant_name":"Fixture Tenant","name_singular":"company","name_plural":"companies"}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/fields \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant","name":"name","type":"text"}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/fields \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant","name":"stage","type":"select","options":[{"value":"lead"},{"value":"won"}]}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/outbox-triggers \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant"}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/indexes \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant","name":"company_stage","fields":[{"field":"stage"}]}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/indexes/query \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant"}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/records \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant","values":{"name":"Acme","stage":"lead"}}'

curl -sS -X POST http://127.0.0.1:4000/data/objects/company/records/query \
  -H 'content-type: application/json' \
  -d '{"tenant_key":"fixture_tenant","query":{"select":["id","name","stage"],"filter":{"op":"eq","field":"stage","value":"lead"},"sort":[{"field":"stage"}],"limit":50}}'
```

When the query response includes `next_cursor`, pass it back as
`query.cursor` with the same object and sort shape to fetch the next page.

Open an SSE stream for matching updates:

```sh
curl -N 'http://127.0.0.1:4000/data/events?tenant_key=fixture_tenant&object=company&query_id=companies&fields=name,stage'
```

Reconnect/replay uses `after_seq`:

```sh
curl -N 'http://127.0.0.1:4000/data/events?tenant_key=fixture_tenant&object=company&query_id=companies&after_seq=0'
```

After triggers are enabled, direct SQL or DB Studio changes to the physical
record table also write outbox rows. Use inspect to find the physical table and
columns before doing manual SQL.

Inspect metadata, migrations, and outbox state without dumping user records:

```sh
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL" --tenant fixture_tenant --object company
```
