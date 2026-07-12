# vNext specification ownership

## Purpose

This directory contains the normative design set for Scenery language edition 2027 and its implementation profiles. `SCENERY_LANGUAGE_SPEC.md` is the umbrella specification; the five companion documents override its summaries for their named profiles.

## Ownership

Scenery maintainers own these specifications together with the compiler, runtime, generated Go ABI, TypeScript client, and compatibility rules that claim conformance to them.

## Local Contracts

- Keep the six documents together and preserve their relative links.
- Treat profile identities and normative MUST/MUST NOT statements as implementation contracts, not aspirational prose.
- Record unresolved draft features as unsupported; do not invent defaults.
- Update the applicable implementation, tests, schemas, user docs, and active ExecPlan in the same change when a normative rule changes.

## Work Guidance

Use `SCENERY_LANGUAGE_SPEC.md` section 26 to determine the claimed profile boundary. Profile support is explicit; no profile is implied unless listed as a dependency. `SCENERY_HTTP_PATH_TAIL_V1.md` owns the implemented additive terminal path-tail contract; apps must claim both path-tail profiles explicitly.

## Verification

Run `scenery inspect docs -o json` after changing this directory, then run the conformance tests for every affected profile and `scenery harness self --summary --write` for substantial changes.

## Child Agent Index

None.
