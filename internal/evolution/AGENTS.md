# Scenery Evolution Instructions

## Purpose

`internal/evolution` owns the revision-checked source transaction that writes
builtin provider locks for `scenery provider lock`.

## Local Contracts

- Source mutations must remain confined to the app workspace. Transaction
  metadata and recovery are owned by `internal/workspacetx`; evolution writes
  that shared exact shape and never creates a parallel recovery reader.
- Builtin provider locking uses the compiler's validated lock snapshot.
  Preserve unrelated dependency entries; do not require a valid compiled app
  to bootstrap its missing builtin locks.
- Do not import `internal/generate`.

## Verification

```sh
go test ./internal/evolution
go test ./cmd/scenery
```
