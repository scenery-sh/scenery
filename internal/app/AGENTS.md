# App Discovery and Configuration

## Purpose

Own canonical app-root discovery, configuration decoding and environment supply.

## Local Contracts

- `.scenery.json` is the app-root marker; reject missing or invalid configuration.
- Keep this package free of PostgreSQL drivers and database IO. Deterministic
  database/schema/environment naming belongs in `internal/postgresname`;
  database IO belongs in `internal/postgresdb`.
- Compiled SQL requirements own application needs. Environments supply endpoints;
  verified retained worktree records own allocations. Do not rediscover removed
  `dev.services` or infer cleanup authority from current declarations.
- Preserve non-symlink workspace and declared managed-output containment.

## Work Guidance

Read the App Config section of `docs/local-contract.md` for config changes and
`ARCHITECTURE.md` for boundaries with compiler and retained worktree owners.

## Verification

Run `go test ./internal/app`, then the root validation union. External ownership
or lifecycle changes also require their named probe under the root policy.
