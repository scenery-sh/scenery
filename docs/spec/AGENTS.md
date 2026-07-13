# Current specification ownership

## Purpose

This directory contains Scenery's evolving current specification. `SPEC.md` is the umbrella specification; the five companion documents override its summaries for their named boundaries.

## Ownership

Scenery maintainers own these specifications together with the compiler, runtime, generated Go ABI, TypeScript client, and evolution rules that implement them.

## Local Contracts

- Keep the six documents together and preserve their relative links.
- Treat normative MUST/MUST NOT statements as implementation contracts, not aspirational prose.
- Record unavailable future features as unsupported; do not invent defaults.
- Update the applicable implementation, tests, schemas, user docs, and active ExecPlan in the same change when a normative rule changes.

## Work Guidance

Use `SPEC.md` section 26 for the current implementation boundary. Resource use determines required behavior; source cannot select an older or partial specification. `http-path-tail.md` owns the implemented terminal path-tail contract.

## Verification

Run `scenery inspect docs -o json` after changing this directory, then run the conformance tests for every affected boundary and `scenery harness self --summary --write` for substantial changes.

## Child Agent Index

None.
