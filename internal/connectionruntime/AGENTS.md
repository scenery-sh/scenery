# Connection Runtime Composition

## Purpose

Own standard-auth connection browser administration and provider/vault composition
behind `appsdk.Host`. See [user connections](../../docs/auth-connections.md) and
the [cutover runbook](../../docs/runbooks/auth-connections-migration.md).

## Ownership

`service.go` binds trusted declarations, existing environment snapshots, lazy
standard-auth SQL and the historical cipher key. `endpoints.go` owns typed
administration and one-time browser/session/provider-bound callbacks.

## Local Contracts

- Never initialize providers from request URLs or perform network IO at startup.
- Reuse standard-auth database/config ownership; connecting changes no login data.
- Keep credentials out of HTTP metadata, inspection, errors, logs and traces.
- Bind state to the initiating live user/session, browser, provider, issuer,
  resource, client and callback. Use PKCE S256 and safe relative return paths.
- Token issuance stays behind the host; no HTTP token endpoint. Request/worker
  authority is enforced by the public facade and runtime invocation boundary.
- Preserve the supported cipher envelope and historical local key input.

## Verification

```sh
go test ./internal/connectionruntime ./auth ./runtime
```

The named `auth` probe owns real SQL/browser/provider/process acceptance
and cleanup. Read the cutover runbook before mutating retained data.
