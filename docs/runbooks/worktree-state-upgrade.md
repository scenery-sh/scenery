# Retained Worktree Specification Upgrade

Use this procedure only when a stopped worktree retains the current artifact
kind and payload schema but an older specification identity. This is an
explicit metadata upgrade, not data conversion or runtime compatibility.

For former shared data formats or a PostgreSQL engine change, use the
separately reviewed [PostgreSQL](worktree-postgres-migration.md) or
[storage](worktree-storage-migration.md) migration procedure instead.

## Scope and Preconditions

- Select the same canonical app root and agent home that own the data. Do not
  create another checkout, home, allocation, volume or storage namespace.
- Stop its runtime with the matching old binary. Exclude external SQL and
  filesystem writers; the upgrade checks existing live/operation locks and
  retained process fingerprints and holds storage maintenance exclusion.
- Keep that old binary and an independent verified backup outside the checkout.
  For configured managed SQL/storage, the old binary's `snapshot save --db
  --storage --output <archive>` and `snapshot verify --input <archive>` provide
  a logical backup. Select only the data classes actually used by the app.
- Record the existing cluster identity and data inventory, plus storage
  incarnation, generation, object count and bytes. Do not publish credentials
  or private metadata backups in logs, tickets or Git.
- Select a target binary coherent with the app's Scenery source dependency.
  A CLI install by itself neither upgrades retained state nor app projections.

## Preview and Apply

From the selected app root with the target binary:

```sh
scenery worktree upgrade -o json
```

This read-only command validates the worktree record, optional stopped-session
registry and all retained managed-storage generation/reference metadata,
including inactive generations. It checks selected payload files' ownership
and lengths, without rewriting or rehashing their contents. The result gives
the root, target specification, metadata/change counts and exact revision.
Missing records/locks, different schemas, invalid ownership, live processes,
non-ready allocations and pending restore/purge operations fail closed.

After reviewing that selection and authorizing the operation:

```sh
scenery worktree upgrade --yes --expect-revision <reviewed-digest> -o json
```

Both flags are required. A changed selection invalidates the approval. The
command replaces only specification/producer identity; application payloads,
credentials, database data, storage incarnations/generations, ETags, hashes,
modification times and object metadata remain unchanged. Nested historical
session records remain history, not newly adopted processes. Externally owned
storage roots and disposable toolchain receipts are outside this transaction.

Apply first persists a private exact-byte backup and pending guard. Worktree
identity is published before storage metadata, so an old binary cannot restart
against a partially upgraded namespace. The current binary also rejects
ordinary worktree/storage access while the guard exists. Success retains the
backup path reported by the CLI and a durable completion marker. The backup
contains credentials when the source does; keep it owner-only.

## Interruption and Verification

If apply is interrupted, rerun the preview using the same target specification.
A pending result returns the original transaction revision and the number of
already upgraded files. Retry that exact revision. Recovery accepts only the
recorded before/after bytes; unknown bytes, missing backups or a changed
selection require investigation. Never delete the guard, manually copy partial
metadata back, or switch to an older binary to bypass recovery.

After completion, a fresh preview reports zero changes. Replaying an already
completed old approval can be stale; inspect current state before acting again.
Do not start the old runtime after the upgrade.

Compare retained database/storage identities and inventory against the backup
baseline. Refresh stale disposable tools through `scenery system toolchain
sync --tool <name> -o json`; regenerate/check clients, run app tests/build and
harness, then start `scenery up --detach --wait ready -o json`. Verify real app
reads through its existing origin and preserve unrelated local changes.

The complete JSON fields, artifact paths and bounded metadata limits are in
the [local command contract](../local-contract.md#cli-grammar).
