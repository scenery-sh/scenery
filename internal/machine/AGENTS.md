# Machine Envelope Instructions

## Purpose

`internal/machine` owns Scenery's singular cross-process CLI JSON and JSONL
envelope shapes plus the common identity header for cross-process artifacts.

## Local Contracts

- Logical kinds are `scenery.cli` and `scenery.cli.event`.
- Every envelope carries exact schema/spec revisions and producer identity;
  workspace/contract revisions are digest-or-null and
  implementation/deployment revisions are digest, digest-map, or null.
- Decoding accepts only the current kind and exact schema/spec revisions; do not add compatibility decoders.
- Keep the matching schemas under `docs/schemas/scenery.cli*.schema.json`
  synchronized with Go types. Their constants are complete
  `SchemaDocumentRevision` digests, not compact descriptor hashes.
- Checked artifacts use a static `ExactSchemaRevision` proved against the
  complete self-normalized JSON Schema. Private artifacts without a checked
  schema use complete structural descriptors guarded by type-shape tests.

## Verification

```sh
go test ./internal/machine
go test ./cmd/scenery
```
