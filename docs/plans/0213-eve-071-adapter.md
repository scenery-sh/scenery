# Eve 0.71 portable assistant adapter

## Purpose / Big Picture

Build and run Scenery assistants with Eve 0.71.0, preserving portable capsules, runtime-selected gateway connections, authenticated tool control and approval behavior.

## Progress

- [x] (2026-10-04 10:30Z) Created isolated source worktree from upstream main ab4829e14ca6023b8d821e0311c4ef346cdb5996; verified npm latest 0.71.0.
- [x] (2026-10-04 10:30Z) Updated authoritative adapter/scaffold Eve pin and regenerated the scaffold npm lock.
- [x] (2026-10-04 10:44Z) Inspected actual Eve output and shared strict generated-tree canonicalization between production capsules and prepared development caches. Added split-chunk, missing-manifest and malformed/duplicate rejection coverage.
- [x] (2026-10-04 11:06Z) Complete repository runtime verification, lint and affected assistant probes.
- [x] (2026-10-04 11:06Z) Prove the selected source through the separate ONLV fixture; record exact source identity and portable build acceptance.

Completed locally; ONLV records consumer acceptance in docs/agent/exec-plans/completed/eve-latest-scenery-adapter.md. No framework release or installation was requested.

## Surprises & Discoveries

Upstream main retains a 0.59.1 implementation pin and validates the earlier one-module manifest layout. Eve 0.71 also parks approvals inside their existing open turn, and calls MCP tools through connection_execute with nested action events. The channel handles human waits without completing a run and preserves concrete capability names without publishing a duplicate nested action. A focused protocol proof covers same-turn resume and the held queue. The new main generated fetch wrapper needed a callable type to remain compatible with Bun preconnect. Eve 0.71 retains the JSON manifest shape but moves its compiled-artifact bootstrap to .output/server/_chunks/node-server.mjs; build paths also occur in other generated chunks. A changed package declaration alone does not produce a portable accepted capsule.

## Decision Log

Preserve all current canonical-root, dynamic-connection and malformed/duplicated-manifest checks. Adapt the exact supported provider layout, without general JavaScript evaluation or executing untrusted build text in Go. Keep original source checkouts and installed executables untouched. This is a private implementation change unless evidence requires a public contract change.

## Outcomes & Retrospective

The final default verifier (20261004T110459.427840000Z) passes with zero errors and one advisory total-test timing warning (9.412s versus 5s). Lint reports zero issues. Final helper and real journey probes pass (20261004T110249.594592000Z), retaining approval, queue, overlapping history, durable control and host-replacement assertions. The generator fixtures are regenerated; TypeScript conformance and generated/catalog checks, including Bun fetch types, pass. Canonicalization now covers each generated .mjs module and requires one validated node-server manifest; native bytes and dynamic connection checks are preserved. The unchanged bootstrap still supplies the loopback Nitro host/port. Both ONLV production capsules build, both assistants reach Ready with exact capability identities, all Go consumers and NextNext checks/build pass, and native Chrome confirms the preserved design/composer; no release, commit, push or installation is authorized.

## Context and Orientation

internal/assistantadapter/eve/adapter.go owns the private Eve version, overlay and runtime templates. canonical.go validates reusable server output. internal/build/assistant_assets.go copies deterministic portable trees. testdata/project under the adapter owns embedded scaffold package bytes. Named probes provide real process/provider proof; ordinary Go tests remain service-free.

## Plan of Work

Build a genuine overlay from the latest ONLV assistant, inspect its compiler artifacts, update the private canonicalization/copy boundary and focus tests on relocation plus rejected static/missing/duplicate connections. Update any affected current runtime ownership documentation, not completed plans. Run full verification and lint once on the final source, then affected named assistant probes and the ONLV consumer acceptance.

## Concrete Steps

From this source root, use go test ./internal/assistantadapter/eve ./internal/build, go run ./scripts/verify --summary --write, golangci-lint run ./..., and go run ./scripts/verify --probe assistant-init --probe assistant-runtime --probe assistant-helper --probe assistant-journey --summary --write. Use the worktree-local prepared CLI for runtime operations; never install globally.

## Validation and Acceptance

Reject malformed or ambiguous compiled artifacts and static Scenery connections. Equivalent builds in different paths produce identical portable artifacts, load after relocation, and resolve their gateway at startup. Real Eve provider, private protocol and queued/approved journey acceptance must retain their current assertions. Complete the verifier's cumulative changed-area command union.

## Idempotence and Recovery

This detached worktree owns all source edits and ignored scratch overlays. Managed runtime probes own their disposable copies and must clean up only those processes. The original source repository remains unchanged. Keep failed evidence when a probe cannot confirm cleanup.

## Artifacts and Notes

.scenery/harness/eve-build/ contains real provider output. The initial probe attempt used stale probe IDs, then its journey exposed the separate development-cache copy path; that run was also invalidated by concurrent task edits. Final accepted archives use corrected named probes and stable source inputs. The first current-API journey revealed turn.waiting handling, which is fixed and verified. Default timing remains advisory; the report was reviewed without attributing concurrent-run timings to this patch. Verifier immutable archives and ONLV .scenery/harness/eve-latest/ identify tested source bytes and runtime ownership.

## Interfaces and Dependencies

Keep provider-neutral assistantcontrol values and public schemas unchanged. Update the exact Eve implementation/scaffold to 0.71.0; retain managed Node 24.18.0 unless a concrete incompatibility requires a change.
