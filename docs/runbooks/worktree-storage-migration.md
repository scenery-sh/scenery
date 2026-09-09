# Shared-to-worktree storage migration

This is an explicit operator procedure. Current Scenery never attaches, repairs,
renames, deletes or automatically migrates a legacy shared cell. A legacy cell
is not an alternate runtime backend. Preserve it until application-level data
checks and your rollback/backup policy are satisfied.

An already worktree-isolated namespace with unchanged metadata schemas does
not need legacy export/import merely because its specification identity is
stale. Use the same-root [retained-state upgrade](worktree-state-upgrade.md).

## Preconditions

- Stop **all** writers to the legacy cell, including every old worktree, worker,
  script and external filesystem writer. The exporter cannot establish that
  fact by looking at a directory; `--confirm-source-quiesced` is your explicit confirmation.
- Record the exact app ID, app name and which stores were tenant-scoped. Store
  policy is not inferred from a physical prefix: an unscoped reserved key may
  otherwise be indistinguishable from framework data.
- Preserve the old configuration, source tree and an independent backup.
- Use the current checkout's `scripts/storage-export-legacy`. It runs the
  one-shot Go converter; it does not install Scenery or allocate a target.

## Export and verify

Select the old **cell directory**, containing `objects/<store>/...`, and a new
output path outside it. Output files must not already exist. Repeat
`--tenant-store` for each known tenant-scoped store; omit it for unscoped stores.

```sh
scripts/storage-export-legacy --source /absolute/legacy-cell \
  --app-id exact-app-id --dry-run --tenant-store attachments
scripts/storage-export-legacy --source /absolute/legacy-cell \
  --output /absolute/backups/storage-logical.zip \
  --app-id exact-app-id --app-name "App name" --confirm-source-quiesced \
  --tenant-store attachments
```

The converter reads raw payloads and `__scenery/metadata/<physical-key>.json`
sidecars. Known `__scenery/tenants/<base64url-tenant>/` prefixes become explicit
tenant identities. It preserves source bytes, permissions and modification
times; reads may update access times. Malformed/missing/orphaned sidecars,
unsafe paths, symlinks, unknown reserved keys, duplicate identities, invalid
lengths and invalid metadata fail closed. It does not invent missing metadata.

Dry-run validates source objects without creating an archive or establishing
quiescence. If `--app-name` is omitted for a directory, it defaults to the app ID.

An old ZIP can be selected with `--input /absolute/old.zip`; app identity comes
from its checked legacy manifest. Directory quiescence confirmation is not
needed for an immutable archive. A legacy ZIP without object modification
timestamps cannot reconstruct those timestamps: export the original directory
instead. Old Scenery ZIPs commonly omitted these fields. Do not substitute ZIP
container or snapshot creation time for object modification time.

The output is built beside its destination, checked completely with the current
logical archive reader, then published. Failed conversion never replaces an
existing output or publishes a valid-looking partial archive. Record the
reported whole-archive SHA-256 and independently verify it:

```sh
scenery snapshot verify --input /absolute/backups/storage-logical.zip \
  --expect-sha256 <64-lowercase-hex-digest> -o json
```

## Import into an explicitly selected worktree

Intentionally remove `storage.cell_id` and `storage.share` from the target's
configuration only after the source and backup have been preserved. Keep its
store policies accurate. Select the intended canonical worktree root; never
copy retained owner, lock, reference or generation files between worktrees.

```sh
scenery snapshot load --app-root /absolute/target-worktree \
  --input /absolute/backups/storage-logical.zip \
  --expect-sha256 <64-lowercase-hex-digest> --storage \
  --mode overwrite --yes --dry-run -o json
scenery snapshot load --app-root /absolute/target-worktree \
  --input /absolute/backups/storage-logical.zip \
  --expect-sha256 <64-lowercase-hex-digest> --storage \
  --mode overwrite --yes -o json
scenery inspect storage --app-root /absolute/target-worktree --stats -o json
```

Load requires a stopped target, creates independent payloads and fresh ETags,
and selects a complete generation. An interrupted load retains a checksum-bound
recovery instruction. Resume with the same input digest, mode and selected
classes; do not erase its operation record. Explicit overwrite after a completed
managed purge creates a fresh incarnation; old handles cannot revive it.

Check logical `(store, tenant, key)` identities, object metadata/content and
application database references before resuming use. Directory storage export
does not infer or back up a database. Coordinate database migration separately
using the [PostgreSQL migration runbook](worktree-postgres-migration.md). Combined
DB/files fixtures require supported managed ownership and exclusion of external
writers; ordinary backup scripts never stop applications automatically.

## Rollback limits

Keep the old source and old binary/configuration together for source rollback.
An old binary cannot read the new worktree filesystem protocol. Restoring an old
cell does not downgrade a new namespace, and current Scenery does not provide a
legacy decoder, alias or fallback runtime path.
