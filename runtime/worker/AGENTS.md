# Native Worker Experiment

## Purpose

Own plan 0180's native process experiment. Only the explicit experimental
renderer selects this package; ordinary app commands retain the current runtime.
This is not production promotion or a compatibility fallback.

## Local Contracts

- Retain every native constructor and operation callback. Generate typed codecs
  and preserve real dependencies; never replace unconverted work with success.
- Native pointers, arbitrary context values, SQL objects, callbacks and concrete
  errors remain local. `internal/nativeprotocol` owns explicit wire values.
- Only compiled unary binding admissions execute through the private transport.
  Streams, custom auth data and unconverted internal/durable bindings fail
  explicitly; registration alone is not a claim of behavioral equivalence.
- Standard auth restores the concrete native auth type, including actor/session
  identity. The kernel owns authentication and declarative HTTP authorization.
- Publish compiled full-target input, implementation and Go-target proof before activation. Constructors and SQL setup
  run only after activation; owner EOF drains HTTP before native shutdown.
- A failed drain does not authorize another writer. Runtime promotion also
  requires the full input/ABI, lifecycle, debugger and performance gates in 0180.

## Verification

Run `go test ./runtime/worker ./runtime/host ./internal/generate` and the root
validation union. Keep transport/admission tests in-process. Real worker/kernel
processes and PostgreSQL belong in explicit owned probes; retain the real ONLV
proof in addition to fixture tests. A structural package cut is not performance
or full runtime-equivalence evidence.
