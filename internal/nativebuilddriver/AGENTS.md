# Retained Native Compiler

## Purpose

This package owns the complete retained Go input domain, captured stock-tool
recipes, direct compile/link execution, and fail-closed compatibility results.

## Local Contracts

- Stock `cmd/go` is the only source of bootstrap compile and link recipes.
- Retained execution compares against the last committed current capture, not
  the immutable bootstrap. Successful builds advance source snapshots and
  archive mappings together in caller-owned durable state.
- Package-selection or import changes request a stock-Go graph refresh. Direct
  execution remains limited to compatible Go body edits; configuration, tool,
  retained-artifact, or unmodeled native-action drift fails closed.
- Every changed package and transitive consumer is compiled with the captured
  stock Go tool. The final link uses current archives and current linker
  metadata.
- Never infer dependency edges or synthesize incomplete compiler command lines.
- Rebind every captured regular-file argument by its recorded index, including
  import, embed and symbol-ABI configuration. Reject the complete rebuild
  frontier before invoking a tool when required native actions are unmodeled.

## Work Guidance

Keep the package independent of `internal/build` and verifier policy. Product
ownership, persistence, publication, cancellation, and telemetry belong to the
caller; benchmark adapters must use this same implementation.

## Verification

Run `go test ./internal/nativebuilddriver` and the caller's owning validation.
