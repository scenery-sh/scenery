# Harness Evidence Artifacts

## Purpose

Own shared artifact serialization, bounded output, command reproduction text,
and explicitly requested evidence writes for app and repository validation.

## Local Contracts

- Store requested run artifacts beneath the supplied root's `.scenery/harness`.
- Keep data types in `internal/harnessreport` and identities in `internal/machine`.
- Do not execute commands, select tests, schedule work, or interpret app facts.
- Preserve output bounds, write warnings, and explicit disabled-write behavior.

## Verification

Run `go test ./cmd/scenery`; app harness and validation consumers cover this leaf.
