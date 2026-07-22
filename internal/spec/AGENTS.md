# Specification Catalog Instructions

## Purpose

`internal/spec` owns Scenery's one current resource/source-schema and diagnostic catalog, canonical JSON encoding, and content revision calculation.

## Local Contracts

- Logical first-party resource kinds are unversioned `scenery.<kind>` names.
- `spec_revision` identifies resource schemas, structural source schemas,
  stable diagnostic rules, and explicit behavioral semantic revisions; each
  exposed schema has its own `sha256:` content revision.
- Exported schema/catalog accessors return deep copies. Do not expose live maps,
  slices, pointers, or nested values from the canonical catalog.
- Explanatory diagnostic prose belongs to the separate diagnostic-catalog
  digest; stable diagnostic rule identity remains in `spec_revision`.
- Revisions identify current content and never select an older catalog, parser, or decoder.
- Generation materialization and revision-scheme migration are semantic review
  gates; bump their owning digests and stable diagnostics together.
- Keep external codec, runtime, provider ABI, and third-party standard versions intact.
- Do not create a parallel JSON catalog; generate machine output from the Go catalog.
- Keep declarative UI kinds, including generic split-page composition and
  entity detail pages, and CRUD list source shapes in the singular source schema catalog; their
  compiler expansion must not introduce a second public resource-schema
  dialect or domain-specific page kind.

## Verification

```sh
go test ./internal/spec
go test ./internal/compiler ./internal/contractagent
go test ./cmd/scenery
```
