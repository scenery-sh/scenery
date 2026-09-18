# Go Implementation Analysis

## Purpose

Own Go syntax, type and package loading for constructor and handler ABI checks.

## Local Contracts

- Keep `golang.org/x/tools/go/packages` inside this package. Export only
  `internal/model`-owned analysis data to consumers; do not leak loader types.
- Application declarations belong in `.scn`, not Go comments or initialization.
- A repeated analysis of one target may load only the packages whose own source
  bytes changed, and their importers only when a changed package's API
  digest changed (`retained.go`). Any other difference — module files, build
  flags, environment, a directory added or removed, an overlay — loads
  everything; the result must equal a full load, and a failed load retains
  nothing.
- Compiler tests must not import this loader. Preserve the separation between
  graph compilation and implementation analysis.

## Work Guidance

Use `ARCHITECTURE.md` for loader/model/compiler boundaries. Keep toolchain and
real-process proof in explicit integration probes, with ordinary in-process
coverage of analysis behavior.

## Verification

Run `go test ./internal/parse`, then the root validation union. Preserve the
absolute test-root timing budget and Go's result cache.
