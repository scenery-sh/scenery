# Specification Catalog Instructions

## Purpose

`internal/spec` owns Scenery's one current resource/source-schema and diagnostic catalog, canonical JSON encoding, and content revision calculation.

## Local Contracts

- Logical first-party resource kinds are unversioned `scenery.<kind>` names.
- `spec_revision` identifies the canonical complete catalog; each exposed schema has its own `sha256:` content revision.
- Revisions identify current content and never select an older catalog, parser, or decoder.
- Keep external codec, runtime, provider ABI, and third-party standard versions intact.
- Do not create a parallel JSON catalog; generate machine output from the Go catalog.

## Verification

```sh
go test ./internal/spec
go test ./internal/compiler ./internal/contractagent
go test ./cmd/scenery
```
