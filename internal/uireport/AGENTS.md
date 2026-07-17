# UI Report Agent Instructions

## Purpose

`internal/uireport` owns the read-only design-system adherence report for
hand-authored React frontend source.

## Ownership

- Lexical import, JSX, StyleX token, literal, and inline-style classification.
- Safe frontend source collection and generated/dependency exclusions.
- Per-file shares, totals, score weights, and deterministic ranking.

`cmd/scenery` owns CLI grammar, human rendering, and the machine envelope.
`docs/schemas/scenery.inspect.ui.schema.json` owns the JSON payload contract.

## Local Contracts

- Keep scanning standard-library-only, string/comment/template aware, and
  independent of TypeScript checker internals.
- Count markup and style adherence as separate axes. The score is triage
  ordering, not validation or enforcement.
- Omit undefined shares when their denominator is zero.
- Never write to the inspected app. Baseline or check-time enforcement requires
  a separate activation decision.
- Exclude generated Scenery source, generated/materialized directories,
  dependencies, build output, and test files.

## Work Guidance

Pin every classification change in the golden fixtures. If a metric changes on
a real consumer, compare the new ranking and totals against ExecPlan 0124 before
accepting the drift.

## Verification

```sh
go test ./internal/uireport
go test ./cmd/scenery -run TestInspectUI
```

Live acceptance uses `scenery inspect ui` against a configured React frontend
with both human and JSON output.
