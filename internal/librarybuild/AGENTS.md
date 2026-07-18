# Shared Library Build Instructions

## Purpose

`internal/librarybuild` owns deterministic `c-shared` artifact production for
declared Scenery libraries and the exact portable artifact manifest.

## Local Contracts

- The supported matrix is exactly darwin/arm64 and linux/amd64.
- Artifact digests are computed before the manifest is written.
- Linux cross-builds use the pinned Go toolchain container and preserve
  absolute local module replacements as read-only mounts.
- Never unload a built Go shared library at runtime; loading and swapping are
  owned by `scenery.sh/library`.

## Verification

```sh
go test ./internal/librarybuild ./library
```
