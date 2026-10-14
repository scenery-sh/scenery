# User Connection Vault

## Purpose

Own encrypted user/provider connection lifecycle independently of provider
protocols. Runtime composition supplies the store, cipher, provider registry and
clock. The public SDK reaches this implementation through `appsdk.Host`.

## Ownership

`types.go` owns injected contracts, `vault.go` token issuance and refresh,
`lifecycle.go` connection/status/disconnect, `cipher.go` version-1 AES-GCM and
`scopes.go` opaque scope validation. SQL execution belongs to its store adapter;
provider protocols belong to `internal/authprovider`.

## Local Contracts

- Never import or branch on Google/provider names, or change sign-in identity,
  profile, tenant, organization or session state when connecting.
- Preserve user ownership and one account/resource per provider configuration.
  Scope requests require permissions; they do not promise downscoping.
- Retain ciphertext version/key derivation during migration. Missing/wrong keys
  are configuration failures, never evidence of revoked consent.
- Keep cross-process serialization and row locking. Checkpoint a durable refresh
  intent before remote rotation; failed final writes leave the intent unusable.
- Persist rotation and effective scopes before reporting a reduced-grant error.
  Never ignore status/commit failures or retry an ambiguous refresh outcome.
- Disconnect stops local issuance durably before revocation. Keep credentials
  out of browser/inspection/log projections.

## Verification

```sh
go test ./internal/connectionvault
go test -race ./internal/connectionvault
```

Real SQL/process/provider/migration proof belongs to the named `auth` probe.
Unit tests use injected transports, stores and clocks without services or sleeps.
