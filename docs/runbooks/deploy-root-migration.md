# SSH Deployment Root Migration

Use this procedure once per deployable environment whose target still holds a
checkout from the source-sync deploy of
[ExecPlan 0115](../plans/0115-ssh-source-sync-deploy.md) at
`~/.scenery/apps/<app-id>`. Current Scenery refuses to deploy or to write
configuration there while that checkout exists, because:

- `~/.scenery/apps/<app-id>/` is now the application's environment
  configuration store, and the old deploy synchronized source into it with
  `rsync --delete`;
- the old runtime's database and storage allocations are owned by that exact
  root path, so starting a new root would allocate empty replacements.

Current deploys install releases into the stable per-environment root
`~/.scenery/deployments/<app-id>/<env>/source` (see
`docs/local-contract.md#ssh-deployment-layout`). This runbook moves the data
from the old root to the new one with the existing logical snapshot
mechanism. It is an explicit operator action with downtime; nothing in
`scenery deploy`, `scenery up` or pruning performs it implicitly.

## Preconditions

- Explicit authorization for the environment's cutover and its downtime.
- SSH access to the target as the runtime owner, with the old runtime healthy.
- The workstation checkout sets `"id"` in `.scenery.json` to the application's
  current effective identity (`name` when no `id` existed), so the store and
  the old ownership records keep one namespace.
- A current Scenery on the workstation and the target, and the old binary kept
  outside the checkout until the procedure finishes.

## Procedure

Run the target commands on the target as the runtime owner. `OLD` is
`$HOME/.scenery/apps/<app-id>`; `NEW` is
`$HOME/.scenery/deployments/<app-id>/<env>/source`; `ARCHIVE` is a private
path outside both.

1. Record the old runtime's identity and data inventory without secrets:

   ```sh
   scenery ps -o json --app-root "$OLD"
   scenery db list -o json --app-root "$OLD"
   ```

2. Take and verify an independent logical backup of the data the application
   uses:

   ```sh
   scenery snapshot save --db --storage --output "$ARCHIVE" --app-root "$OLD" -o json
   scenery snapshot verify --input "$ARCHIVE" -o json
   ```

3. Stop the old runtime and move the old checkout out of the store directory,
   keeping it intact for rollback:

   ```sh
   scenery down --app-root "$OLD" -o json
   mkdir -p "$HOME/.scenery/legacy-deploy-roots"
   mv "$OLD" "$HOME/.scenery/legacy-deploy-roots/<app-id>"
   ```

   The old allocations stay on disk, owned by the old path. Do not prune or
   delete them until the new root is verified and their removal is separately
   authorized.

4. From the workstation, configure the environment's values on the target
   (the old `.env` values, one key at a time; secrets through the prompt or
   `--stdin`), then check the catalog:

   ```sh
   scenery config set <key> [<value>] --env <env>
   scenery config show --env <env>
   ```

5. Deploy the first release into the new root:

   ```sh
   scenery deploy --env <env>
   ```

6. Restore the data into the new root and restart it:

   ```sh
   scenery snapshot load --input "$ARCHIVE" --db --storage --mode overwrite --yes --app-root "$NEW" -o json
   scenery down --app-root "$NEW" -o json
   scenery up --detach --wait ready --env <env> --app-root "$NEW" -o json
   ```

7. Verify the served application against the inventory from step 1, including
   authenticated persistence and public routes.

## Rollback

Before step 5 completes, stop any new-root runtime, move the old checkout back
to `$OLD` and run `scenery up --detach --wait ready --env <env> --app-root
"$OLD"` with the old binary; its allocations were never touched. After step 6,
the archive from step 2 restores the old data into either root.
