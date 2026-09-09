# Shared-to-Worktree PostgreSQL Migration

This is an explicit operator procedure, not part of `scenery up`. Keep the old
source and backup until the operator separately approves their retirement.
Installing a CLI is not a data migration.

If the root already uses per-worktree PostgreSQL and only its metadata's
specification identity is stale, use the same-root
[retained-state upgrade](worktree-state-upgrade.md) instead. The different-root
procedure below applies to the former shared data format, not that case.

For two roots already on the current Scenery protocol, prefer `snapshot save`,
`snapshot verify`, and inert `snapshot load --db`. An old snapshot belongs to
its matching old CLI: do not relabel its identities, edit its archive manifest,
or add a compatibility decoder to the current binary. Use native PostgreSQL
export/import for the pre-cutover case below.

## Inventory and Authority

Before writing anything, record the exact canonical source root, app ID,
database name, source engine version/image, Docker daemon ID, immutable
container ID, volume name/creation identity, database owner, roles/grants,
extensions, and all application and `scenery` schemas. Use the matching old
CLI to inspect its own state. A familiar container name is not sufficient
authority. Never infer credentials from another container's environment.

Select an empty **different absolute target root**, containing reviewed
application source and the matching current Scenery dependency. The original
root and shared server remain unchanged. The new binary deliberately refuses
implicit allocation when an existing root has legacy ownership evidence; do
not delete old records, select a different home for that same source root, or
move volumes to bypass that guard. In-place automatic legacy transfer is not
a supported workflow. If the historical home has an incompatible specification,
select a separate current agent home for the new empty target root. Keep source
and target homes explicit in every command; this separates protocol authority,
not data migration or Docker ownership.

The commands below assume verified Docker-managed PostgreSQL 18 instances
whose reviewed administrative owner is `scenery`, as exercised by release
acceptance A9. Substitute the inventoried values, never a name-prefix match.
For other roles or engine versions, review and test the corresponding role,
extension, collation, and upgrade requirements first. Client tools must be
compatible with both engines; using each pinned container's tools avoids
guessing which host `pg_dump` is installed.

## Quiesce and Export

Set these shell variables from the reviewed inventory: `OLD_CLI`, `NEW_CLI`,
`OLD_AGENT_HOME`, `NEW_AGENT_HOME`, `SOURCE_ROOT`, `TARGET_ROOT`, `SOURCE_CONTAINER` (immutable ID), `SOURCE_DB`,
and `BACKUP_DIR` (a new private directory). They contain no credentials.
Quiesce **all writers for the selected source database**, including workers,
schedules, external clients, and manual SQL. Stopping its Scenery app alone
is sufficient only when the inventory proves it owns every writer. Do not
stop the shared server or unrelated apps.

```sh
umask 077
mkdir "$BACKUP_DIR"
SCENERY_AGENT_HOME="$OLD_AGENT_HOME" "$OLD_CLI" down --app-root "$SOURCE_ROOT" -o json
docker exec "$SOURCE_CONTAINER" pg_dump -U scenery -d "$SOURCE_DB" --format=custom --file=/tmp/migration.dump
docker exec "$SOURCE_CONTAINER" pg_restore --list /tmp/migration.dump
docker exec "$SOURCE_CONTAINER" sha256sum /tmp/migration.dump
docker cp "$SOURCE_CONTAINER:/tmp/migration.dump" "$BACKUP_DIR/migration.dump"
shasum -a 256 "$BACKUP_DIR/migration.dump"
```

Record and compare the archive SHA256 at every transfer. Store an inventory
of source row counts and application-specific invariants alongside the backup.
Include seed/setup ledgers, durable jobs, sequences, large objects, ownership,
ACLs, and extension requirements in the review; a successful `pg_dump --list`
alone is not restore or semantic proof. The target must remain inert until
validation finishes. Durable jobs may execute after startup: review them before
the explicit switch, and never run old and new workers against the same copied
pending work unintentionally.

## Provision an Inert Target and Restore

`db server start` provisions only the selected worktree server. It does not
start application workers or run application setup. Obtain the proposed
root-derived application database name from read-only `db list`.

```sh
SCENERY_AGENT_HOME="$NEW_AGENT_HOME" "$NEW_CLI" db server start --app-root "$TARGET_ROOT" -o json
SCENERY_AGENT_HOME="$NEW_AGENT_HOME" "$NEW_CLI" db server status --app-root "$TARGET_ROOT" -o json
SCENERY_AGENT_HOME="$NEW_AGENT_HOME" "$NEW_CLI" db list --app-root "$TARGET_ROOT" -o json
```

Use `.data.container` from server status to inspect and record the exact target
container ID, image, volume and daemon binding. Set `TARGET_CONTAINER` to that
verified immutable ID, and `TARGET_DB` to `.data.database.name` from `db list`.
Confirm the target database does not already contain user data. For the fresh
server in this procedure:

```sh
docker exec "$TARGET_CONTAINER" createdb -U scenery --owner=scenery "$TARGET_DB"
docker cp "$BACKUP_DIR/migration.dump" "$TARGET_CONTAINER:/tmp/migration.dump"
docker exec "$TARGET_CONTAINER" sha256sum /tmp/migration.dump
```

A database dump does not create cluster roles. Before restore, explicitly
recreate the required reviewed roles and memberships, preserving the intended
login and privilege semantics. Do not import every role or password from a
shared cluster blindly. The acceptance fixture uses `fixture_reader NOLOGIN`
and its existing `scenery` database owner; it prepares that one role explicitly:

```sh
docker exec "$TARGET_CONTAINER" psql -U scenery -d "$TARGET_DB" -v ON_ERROR_STOP=1 -c 'CREATE ROLE fixture_reader NOLOGIN;'
docker exec "$TARGET_CONTAINER" pg_restore -U scenery --dbname "$TARGET_DB" --clean --if-exists --exit-on-error --single-transaction /tmp/migration.dump
```

The `--clean` flag here is authorized only for this verified fresh target;
it is not a repair command for an arbitrary existing database. Preserve owner
and ACL restoration: do not add blanket `--no-owner` or `--no-acl` flags to
silence failures. Install compatible required extensions in the target engine
and resolve any owner, grant, extension or collation disagreement explicitly.
An error aborts the transactional restore and blocks the switch. Preserve the
backup and resolve the cause; do not start the app after a partial workaround.

## Validate, Switch, and Roll Back

Before startup, compare complete application data/invariants, sequence values,
owners, grants, extension versions and framework ledgers with the source
inventory. Verify the source remains unchanged and other apps still serve.
Rehearse this procedure on disposable resources first.

Only after explicit acceptance of the target, start its matching current CLI:

```sh
SCENERY_AGENT_HOME="$NEW_AGENT_HOME" "$NEW_CLI" up --app-root "$TARGET_ROOT" --detach --wait ready -o json
```

Verify typed API behavior and data again after forward setup has run. This is
the switch to a new root-owned runtime, not an automatic rewrite of the old
root, old machine agent, installed CLI, or public DNS/edge configuration.
Keep the selected old app quiesced; other old apps continue using their shared
server. Redirect callers intentionally only after the new endpoint is proved.

Before target writes, rollback can use the matching old CLI and untouched
source. **After target writes, switching back loses those writes unless they
are transferred and verified.** There is no automatic reverse migration.
Archive and source retirement requires separate approval and exact selection;
never remove the shared volume merely because one application migrated.

## Release Evidence

The named worktree-runtime release probe owns A9 and records the immutable
pre-cutover revision, both Linux binary SHA256 values, separate host/nested
Docker daemon IDs, actual argv, baseline agent/container/volume identity,
continuous sibling HTTP results, and native archive checksum. It first proves
SCN8003 rejection of an incompatible shared home without target database
resources, then uses distinct source/current homes for coexistence and migration.
Its fixture
checks every lending row, a seed-ledger row and timestamp, table ownership,
an explicit read grant, and the `pgcrypto` extension before restore, after
restore and after target startup. It uses a disposable Docker-in-Docker
environment with no host socket or source bind mount. This is a tested scope,
not a claim that an arbitrary application's migration has been approved or
that every possible extension/role configuration is covered.
