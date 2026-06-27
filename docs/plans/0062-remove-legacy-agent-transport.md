# Remove Legacy Agent Transport

This plan is maintained according to `PLANS.md`.

This ExecPlan is a living document and must be updated as work proceeds.

## Purpose / Big Picture

Remove the legacy agent transport layer from scenery rather than disabling or hiding it. Agents should use scenery-native CLI JSON, dashboard routes, logs, traces, metrics, database commands, generated metadata, and harness outputs.

The desired final state:

- scenery does not start, route, configure, expose, document, test, or ship the removed transport.
- Stale config keys for the removed transport fail validation through strict `.scenery.json` decoding.
- Session manifests and local proxy routes expose API, dashboard, frontends, Grafana, legacy async runtime, sync, and other real app/dev services only.
- Self-harness fails on reintroduction of the removed transport surface.

## Progress

- [x] 2026-06-01: Removed framework config fields, generated runtime fields, local proxy routes, dashboard handlers, session routes, dashboard service cards, and protocol-specific tests.
- [x] 2026-06-01: Added strict config coverage for stale removed-transport keys.
- [x] 2026-06-01: Added a self-harness residue rule for the removed transport terms.
- [x] 2026-06-01: Finished doc residue sweep and validation.

## Surprises & Discoveries

- 2026-06-01: The transport was exposed through both local-proxy workspace routes and agent session routes, so both registries needed to change together.
- 2026-06-01: Dashboard JSON-RPC already covers the local UI needs, which made the dashboard transport handler a clean delete.

## Decision Log

- Decision: Remove the transport completely rather than keeping a disabled or compatibility route.
  Rationale: The product model should only show active scenery-native capabilities.
  Date/Author: 2026-06-01 / Petr + agent
- Decision: Reject stale config keys with the existing strict config decoder.
  Rationale: Silent ignore would preserve the old surface as implicit compatibility.
  Date/Author: 2026-06-01 / Petr + agent
- Decision: Enforce residue removal through the architecture self-harness.
  Rationale: The product should not accidentally grow the old transport back through docs, tests, or UI strings.
  Date/Author: 2026-06-01 / Petr + agent

## Outcomes & Retrospective

The legacy agent transport was removed from runtime startup, generated config, local proxy routes, agent session manifests, dashboard handlers, UI service labels, docs, schemas, and tests. Self-harness now includes a residue rule that fails if the removed transport terms are reintroduced in tracked product/source/docs. Validation passed with `go test -count=1 ./...`, `go install ./cmd/scenery`, dashboard UI typecheck/test/build, and `scenery harness self --json --write`.

## Context and Orientation

The removed transport previously touched config parsing, generated runtime configuration, local proxy routes, agent session routes, dashboard handlers, dashboard UI labels, integration tests, and agent docs. The replacement agent workflow uses direct scenery commands and dashboard JSON-RPC instead of a protocol adapter.

## Milestones

1. Delete runtime and routing support.
2. Remove docs and examples from the current contract.
3. Add validation that rejects stale config and prevents reintroduction.
4. Run the ordinary and substantial validation matrix.

## Plan of Work

Keep the removal in one slice so public JSON, docs, tests, and runtime behavior do not drift. Delete the transport code rather than leaving disabled state. Let strict config decoding reject stale keys.

## Concrete Steps

- Remove the config fields from app config structs, generated runtime literals, schema, and examples.
- Remove local proxy route generation and agent session route generation for the old transport host.
- Delete dashboard protocol handlers and protocol-specific tests.
- Remove UI service-card labeling for the old route.
- Add a self-harness text rule for reintroduction.

## Validation and Acceptance

Acceptance requires:

- `go test ./...` passes.
- `go install ./cmd/scenery` passes.
- Dashboard UI typecheck, tests, and build pass.
- `scenery harness self --json --write` passes.
- The residue search for removed-transport names returns no tracked product/source/doc hits.

## Idempotence and Recovery

If stale generated cache files contain old route metadata, regenerate or delete the cache. If a test expected the old transport, rewrite it around current scenery-native inspection or delete it when it only tested the removed surface.

## Artifacts and Notes

Expected touched areas are runtime config, local proxy, agent routing, dashboard server, dashboard UI, docs, schema, and tests. No new dependencies are expected.

## Interfaces and Dependencies

Removed interface: the legacy agent transport route, config key, dashboard handler, and advertised session URL.

Retained interfaces: `scenery inspect ... --json`, `scenery status --json`, `scenery logs`, `scenery db`, `scenery run`, `scenery check --json`, `scenery harness --json --write`, dashboard, and generated clients.
