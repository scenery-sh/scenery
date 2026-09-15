# Retained Native Compiler

## Purpose

This package owns the complete retained Go input domain, captured stock-tool
recipes, direct compile/link execution, and fail-closed compatibility results.

## Local Contracts

- Stock `cmd/go` is the only source of bootstrap compile and link recipes.
- Retained execution may rebuild compatible body edits only; package selection,
  imports, directives, configuration, tool identity, or retained-artifact drift
  returns `needs_rebootstrap` before publishing output.
- Every changed package and transitive consumer is compiled with the captured
  stock Go tool. The final link uses current archives and current linker
  metadata.
- Never infer dependency edges or synthesize incomplete compiler command lines.

## Work Guidance

Keep the package independent of `internal/build` and verifier policy. Product
ownership, persistence, publication, cancellation, and telemetry belong to the
caller; benchmark adapters must use this same implementation.

## Verification

Run `go test ./internal/nativebuilddriver` and the caller's owning validation.
