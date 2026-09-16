# Retained Native Compiler

## Purpose

This package owns the complete retained Go input domain, captured stock-tool
recipes, direct compile/link execution, and fail-closed compatibility results.

## Local Contracts

- Stock `cmd/go` is the only source of bootstrap compile and link recipes. A
  capture names both the pattern it was built from and the import path that
  pattern resolved to; only the resolved entrypoint identifies the recorded
  `main` compile. Build flags that carry per-build identity belong in the build
  argv, never in the captured configuration that decides eligibility.
- Recorded archives, support inputs and source snapshots are retained under one
  caller-owned content-addressed state root, so recipes of one workspace adopt
  what their closures have in common and a recording directory is disposable
  once its recipe is published. Archives may be linked into the store; a source
  snapshot is copied, because a capture may name a workspace file the developer
  owns. A name and size never prove an existing store entry: adoption hashes it
  and atomically replaces content that differs.
- A recorded action is merged only when every captured input it read had the
  captured content, every input it read from a selected package directory is
  captured, and every package it compiled is selected, so a capture taken
  across an edit or a transient source file cannot pair an archive with another
  source revision.
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
