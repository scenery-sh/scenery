# Connection Provider Adapters

## Purpose

Implement trusted, configured authorization-code providers behind the neutral
vault interface. `oauth2` owns shared OAuth2/OIDC mechanics; `google` owns the
Google preset. `internal/oidc` owns signature/issuer/audience/expiry/nonce checks.

## Local Contracts

- Configuration IDs select trusted endpoints; requests never supply discovery
  or issuer URLs. Construction performs no network IO.
- Use PKCE S256, bounded HTTP and bounded responses; do not follow redirects
  carrying credentials to another endpoint.
- Keep OAuth2 account lookup distinct from OIDC identity verification. Refresh
  credentials and ID tokens are optional protocol fields.
- Missing refreshed scope retains the established grant; explicit empty scope
  does not. Preserve case-sensitive scope tokens and fixed resource binding.
- Classify invalid-client separately from revoked consent. Provider errors carry
  sanitized classifications only; remote bodies/descriptions never escape.
- Do not retry ambiguous refreshes. Google consent/offline/incremental-consent
  behavior stays in its preset rather than the vault or generic defaults.

## Verification

```sh
go test ./internal/authprovider/... ./internal/connectionvault
go test -race ./internal/authprovider/oauth2
```

Unit tests inject transports and static test-only keys. Real journeys belong
to the named `auth` probe with owned SQL/process/provider cleanup.
