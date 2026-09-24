# Environment Configuration Instructions

## Purpose

`internal/appconfig` owns the typed configuration catalog derived from a
compiled application, pure resolution of one environment revision, the
authoritative per-application store and the secret-backend adapters.

## Ownership

- Catalog, keys, typed values and `Resolve`: `catalog.go`, `value.go`,
  `resolve.go`.
- Store document, history, pins, locks and pruning: `document.go`,
  `store.go`, `lock_*.go`.
- Secret versions: `secrets.go` and one platform adapter per OS
  (`secrets_keychain_darwin.go`, `secrets_systemd_linux.go`).
- The CLI, SSH receivers, runtime snapshots and deployment releases live in
  `cmd/scenery` (`config_*.go`, `dev_config*.go`, `deploy_release*.go`);
  runtime decoding lives in `runtime/deployment_config.go`.

## Local Contracts

- Selection is application + environment only. Never add scopes, worktree
  overrides, profile layers, env-variable or dotenv fallbacks.
- Resolution is exactly declared default, then the environment's value.
  Unknown stored keys are reported unused, never dropped or delivered.
- Documents hold canonical wire values and secret version references only;
  no plaintext and no secret-derived digests. Revisions are content-addressed.
- Every write changes one key under the environment lock and publishes history
  before the desired document; stale expected revisions change nothing.
- Pruning never removes the desired revision, a pinned revision or a secret
  version any retained revision references.
- Secret plaintext crosses only in-memory buffers and subprocess stdin/stdout;
  adapter errors never include tool output.
- The package imports `internal/compiler` only for key and wire helpers; keep
  it free of OS, SSH and runtime-process concerns beyond the secret adapters.

## Verification

```sh
go test ./internal/appconfig ./cmd/scenery ./runtime ./internal/compiler ./internal/generate
go test -race ./internal/appconfig
GOOS=linux go vet ./internal/appconfig
```

Real Keychain, systemd-creds, SSH and reboot behavior belong to the
`configuration`, `configuration-secrets` and `configuration-deploy` probes.

## Child Agent Index

None.
