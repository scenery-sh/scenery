# Runtime Host

## Purpose

Own the generated application's HTTP server, framework orchestration and native
callback integration. This package is explicitly imported by generated bootstrap
and adapters. Application-native calls use `scenery.sh/runtime` without importing
this host.

## Local Contracts

- Keep one process-local owner for request/auth state, tracing, service lifecycle,
  native internal calls and durable persistence. Do not serialize arbitrary Go
  contexts, callbacks, pointers or concrete errors between these owners.
- `internal/nativecompose` owns ABI/resource admission and transactional apply;
  this host supplies snapshot, rollback and page validation. A rejected adapter
  must not reserve resources or initialize services.
- `internal/nativeservice` owns constructors, dependencies and shutdown order.
  Assistant/MCP dependencies must use that registry, not parallel lifecycle maps.
- Preserve linked contract/implementation/build-input/target identity under the
  `scenery.sh/runtime/host` linker namespace. Generated bootstrap, runtime bundle
  generation and preflight must change together.
- A host package move is not proof of a kernel/worker process split. Plan 0180
  requires full native closure and an authenticated operation before performance
  acceptance.

## Verification

Run `go test ./runtime ./runtime/host` and the root validation union. Lifecycle,
request and callback changes require `--probe native-contract`; auth, durable
and assistant boundaries additionally use `auth`, `postgres` and
`assistant-runtime` respectively. Generated bootstrap changes also require
`dev-process` and `build-info`. Real process/service proof stays in these probes.
