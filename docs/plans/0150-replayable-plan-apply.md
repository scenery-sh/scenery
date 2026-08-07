# Replayable Plan Apply and Trusted Agent Context

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery currently commits source and deployment plans at most once, but a
second apply of the same plan fails with `already applied`. Durable agent
runtimes such as Eve may repeat an interrupted tool step after Scenery has
committed and synced its receipt but before the runtime records the response.
That turns a successful operation into an uncertain failure.

This plan changes the contract from "single-use request" to "single commit,
replayable result." The first successful apply still performs one mutation.
Every later authenticated apply of the same canonical `plan_id` returns the
stored, validated receipt without repeating effects. At the same time, normal
agent traffic carries only a compact plan handle and summary; Scenery loads the
retained canonical plan itself. Caller identity, granted capabilities,
approval credentials, app root, and call correlation come from trusted
transport context rather than model-authored JSON.

The observable result is that an Eve tool retry receives success with the
original receipt, a model never has to echo complete resulting file bytes, and
changing `caller`, capabilities, or approval tokens in tool input is rejected
as an unknown field.

## Progress

- [x] 2026-08-07: Audited the current evolution transaction, retained-plan,
      contract-agent, deployment apply, workspace recovery, and Eve assertion
      paths. Confirmed that current tests deliberately require replay failure
      even though the synced receipt is the durable commit marker.
- [x] 2026-08-07: Activated this ExecPlan and delegated non-overlapping core,
      deployment, contract-agent, CLI, Eve, and review work.
- [x] 2026-08-07: Implemented strict, canonical-plan-bound receipt replay for
      source changes, including immutable atomic receipt publication and
      concurrent duplicate apply recovery.
- [x] 2026-08-07: Implemented strict deployment receipt replay and recovery
      validation without a second provider apply.
- [x] 2026-08-07: Shipped compact plan summaries, plan-ID apply, and receipt recovery in the
      contract-agent protocol.
- [x] 2026-08-07: Bound contract-agent identity, grants, approvals, and app root to
      server-owned session context.
- [x] 2026-08-07: Bound private MCP sessions to principal, assistant,
      conversation, and capability revision, and moved request-ID generation
      to the gateway because pinned Eve cannot expose a per-action call ID.
- [x] 2026-08-07: Updated current schemas, specification, local contract, agent guide, and
      cookbook wording.
- [x] 2026-08-07: Completed the changed-area command union, full repository
      suite, focused race suite, fixture regeneration checks, dashboard build,
      browser UI harness, and full self-harness.

## Surprises & Discoveries

- 2026-08-07: `changes.plan` retains the complete plan before the
  contract-agent enforces its 2 MB response bound. A source-heavy plan can
  therefore be durably issued but returned only as `response exceeds transport
  limit`, leaving the caller without its plan ID.
- 2026-08-07: The Eve assertion schema already carries `request_id`, trace
  context, and `idempotency_key`, while the generated connection currently
  supplies none of them. Principal derivation from `ctx.session.auth` is
  already present.
- 2026-08-07: Contract-agent `capabilities` are currently bound into the plan
  digest but do not authorize apply; `required_capabilities` is always empty.
- 2026-08-07: Eve 0.29.5 connection header callbacks receive a cached
  `SessionContext`, not the authored tool's per-action `callId`, and connection
  definitions expose no configurable `toModelOutput`. A turn/session ID would
  collide when one turn starts multiple tools, so the MCP gateway must mint a
  fresh invocation ID instead.
- 2026-08-07: Eve's application-MCP `user-approval` flow and the separate
  contract-agent mutation protocol have no approval broker between them. The
  opaque assistant `appr1_` handle is not an evolution `ApprovalToken` and must
  not be reinterpreted as one.
- 2026-08-07: Publishing a receipt directly to its final path can leave a
  partial commit marker after a crash. Receipt creation now writes and fsyncs a
  same-directory temporary file, then uses an atomic no-overwrite link while
  the workspace transaction lock is held.

## Decision Log

- Decision: Preserve the at-most-one-effect invariant while making successful
  results replayable.
  Rationale: Request retry and mutation cardinality are separate concerns. A
  synced, plan-bound receipt is authoritative evidence that the first request
  committed.
  Date/Author: 2026-08-07, Codex and Petr.
- Decision: Authenticate the retained plan and server-bound caller before
  looking up a replayable receipt; on a valid receipt, return before checking
  first-apply expiry, approvals, or live base revisions.
  Rationale: Receipt replay must neither leak cross-principal state nor require
  expired credentials that were already validated at commit time.
  Date/Author: 2026-08-07, Codex.
- Decision: Keep durable receipts immutable and place `replayed` only in an
  apply-result envelope or transport metadata.
  Rationale: Applied receipts are durable historical evidence and are consumed
  by rename/deployment state readers.
  Date/Author: 2026-08-07, Codex.
- Decision: Keep full plan artifacts for trusted review and CLI workflows, but
  return only a compact summary from ordinary agent planning and accept only
  `plan_id` for ordinary agent apply.
  Rationale: The retained canonical plan is the authority; echoing it through
  the model adds cost and disclosure without adding trust.
  Date/Author: 2026-08-07, Codex and Petr.
- Decision: Keep `internal/contractagent` separate from application MCP tools.
  Rationale: Source mutation is a control-plane capability and must not become
  an implicit capability of every application assistant.
  Date/Author: 2026-08-07, Codex.
- Decision: Generate one request ID at the Scenery MCP invocation boundary and
  bind the MCP session to its stable authenticated assertion tuple.
  Rationale: The pinned Eve connection API does not expose a safe per-action
  call ID. Server generation avoids model-controlled or same-turn collisions,
  while stable tuple binding prevents same-principal session context swaps.
  Date/Author: 2026-08-07, Codex.
- Decision: Do not bridge Eve's opaque approval handle to an evolution token
  without a server-owned plan/risk-bound broker.
  Rationale: The token types authenticate different claims and currently cross
  different protocols. A cast or model-carried bridge would create false
  authority; approval-bearing contract-agent apply remains an operator/trusted
  adapter path until that broker is deliberately designed.
  Date/Author: 2026-08-07, Codex.

## Outcomes & Retrospective

Completed 2026-08-07.

Change and deployment apply now have one durable commit and replayable results.
Source receipt publication is atomic, no-overwrite, and recovery-safe; retained
plans and source receipts require canonical bytes and complete content-bound
identity. Duplicate/concurrent applies return the original validated receipt
without executing edits or provider operations again. Corrupt or mismatched
state fails closed.

The contract-agent model surface now returns a bounded plan summary and accepts
only `plan_id` for apply/recovery. Full plans are restricted to an explicit
trusted review capability. Principal, app root, mutation grants, approval
tokens, and verifiers are session-owned rather than JSON parameters. The stdio
launcher binds a local process context, and private MCP sessions bind the stable
assertion tuple while the gateway creates a fresh request ID per invocation.

The original proposal assumed Eve could expose authored-tool `callId` and
`toModelOutput` through an MCP connection. The pinned 0.29.5 public API exposes
neither. The implementation therefore uses the protocol's compact result and a
gateway-generated request ID; it does not synthesize a colliding turn ID or use
unsupported deep imports.

One explicit follow-up remains outside this plan's connected runtime surface:
Eve application-MCP approval and contract-agent mutation are separate
protocols. A future server-owned broker must bind the canonical plan, risk
scopes, principal, conversation, and approval decision before minting and
injecting an evolution token. Until then, approval-bearing contract-agent apply
uses a trusted operator/adapter context; the opaque assistant approval handle
is never treated as an evolution token.

Validation completed with `go test ./...`, the focused race suite, both
committed TypeScript fixture regeneration commands (no changed bytes), console
lint/typecheck/build, a seven-route browser UI harness against the native
fixture, quick self-harness, and full self-harness. The literal recommended UI
harness invocation from the repository root cannot discover an app and returns
SCN9000; the app-root-bound native fixture invocation passed. Temporary fixture
`.env` and runtime state used for that proof were removed afterward.

## Context and Orientation

`internal/evolution/changes.go` defines `ChangeRequest`, the canonical retained
`ChangePlan`, `ChangeReceipt`, and source apply. `internal/evolution/issued_plan.go`
owns app-local issued-plan records under `.scenery/plans/issued/change/`.
`internal/workspacetx` uses the applied receipt path as the durable commit marker:
an interrupted unreceipted transaction rolls back, while a receipted one remains
committed.

`internal/deployplan/deployplan.go` has a parallel retained deployment plan,
crash-recovery journal, applied receipt, and current replay rejection.

`internal/contractagent/agent.go` is the model-facing JSON-RPC capability
surface used by `scenery agent serve --stdio`. It currently accepts
model-authored caller, capabilities, expected revisions, and approval tokens,
returns the full plan from `changes.plan`, and requires that full plan again in
`changes.apply`.

`internal/assistantadapter/eve/templates/scenery-connection.ts.tmpl` creates
the short-lived signed assertion used by the private MCP gateway. The current
gateway assertion type already has principal and request/idempotency metadata.
This plan only strengthens that existing boundary; it does not expose
contract-agent mutations through the application MCP catalog.

## Milestones

### Milestone 1: Replay-safe transaction cores

Add strict issued-plan and receipt loaders. Source and deployment apply must
return the original validated receipt when the same plan was already committed.
Tests prove no edit or provider action executes twice, replay survives expiry
and later state drift, caller mismatch is denied, and corrupt state fails closed.

### Milestone 2: Handle-based contract-agent protocol

Add a deterministic compact plan summary. `changes.plan` injects trusted
session identity/grants and returns that summary. `changes.apply` accepts only
`plan_id`, loads the canonical plan, and returns the receipt plus ephemeral
replay metadata. Add an explicit receipt/status read for recovery and a
full-plan read only as an explicit review operation.

### Milestone 3: Trusted identity and invocation correlation

Introduce a server-owned contract-agent execution context. The stdio launcher
binds the local principal, app root, and granted mutation capability outside
request JSON. Approval tokens/verifiers are supplied through that context, not
the model schema. The Eve adapter projects its authenticated session principal
and stable session/conversation identity into the signed assertion; the
Scenery MCP gateway binds that tuple and generates per-invocation request IDs.

### Milestone 4: Current contract and complete proof

Update every current schema and behavior document in the same change, refresh
the harness context, execute the exact changed-area command union, and finish
with the full repository suite and full self-harness.

## Plan of Work

First implement receipt loading and replay in `internal/evolution` and
`internal/deployplan`, preserving existing first-apply validation and crash
recovery. The loaders reject symlinks, unknown fields, trailing JSON, stale
artifact identities, incorrect plan IDs, and receipt fields that do not match
the canonical retained plan.

Next change `internal/contractagent` so its public request structs are separate
from evolution's internal `ChangeRequest`. Attach an execution context to the
session, inject authoritative identity and grants during planning, and call the
plan-ID evolution API during apply. Normal planning returns a compact summary;
explicit review may load the full plan. Adapt the stdio launcher in
`cmd/scenery/contract_commands.go` to construct the local execution context.

Then populate only the Eve metadata exposed safely by the pinned public API and
protect it with exact template tests. Bind stable assertion context at the MCP
session and mint request IDs per gateway invocation. Do not guess at unavailable
Eve APIs, substitute turn IDs, or pass approval credentials through the
helper/model.

Finally update schemas and current prose. Replace "single-use" with the exact
single-commit/replayable-result rule, document expiry and approval semantics for
first apply versus replay, and document the trusted-context request shapes.

## Concrete Steps

Run from `/Users/petrbrazdil/Repos/scenery`:

1. Implement focused package changes and run:

       go test ./internal/evolution
       go test ./internal/deployplan
       go test ./internal/contractagent
       go test ./internal/assistantadapter/eve
       go test ./cmd/scenery

2. Refresh the worktree-local harness context:

       .scenery/harness/bin/scenery harness self --quick --summary --write

3. Read `.scenery/harness/agent-context.json` and execute every command in the
   exact `changed_area.recommended_commands` union.

4. Run the repository and race proof:

       go test ./...
       go test -race ./internal/evolution ./internal/deployplan ./internal/contractagent

5. Run final machine-contract validation:

       .scenery/harness/bin/scenery harness self --summary --write

## Validation and Acceptance

Acceptance requires all of the following observable results:

- Applying the same committed change plan twice returns the same receipt; the
  second result is identified as replayed outside the receipt.
- Replay still succeeds after plan/token expiry and after unrelated subsequent
  workspace changes.
- A different principal cannot retrieve or replay another principal's result.
- A corrupt or plan-mismatched receipt fails without applying edits.
- A deployment replay invokes no provider `Apply` method a second time.
- Ordinary `changes.plan` JSON contains no `source_edits`, full semantic diff,
  or resulting file bytes.
- Ordinary `changes.apply` accepts `plan_id` and rejects caller, capability,
  expected-revision, full-plan, and approval-token fields as unknown.
- The approval verifier receives credentials only from server-owned context.
- Eve's signed assertion carries authenticated principal plus supported stable
  session/conversation context; the gateway mints a distinct request ID for
  each tool invocation, and model tool input contains none of those credentials.
- Every focused command, the full `go test ./...`, the named race command, the
  changed-area command union, and full self-harness complete successfully.

## Idempotence and Recovery

The plan ID is the natural idempotency key for apply. A receipt is returned only
after strict validation against the retained canonical plan. Receipt presence
with invalid contents is committed-state corruption; Scenery reports it and
never attempts the mutation again.

If a source process dies before the receipt is durable, `internal/workspacetx`
restores the prior bytes and a later first apply may proceed if the plan has not
expired. If it dies after the receipt is durable, recovery preserves the new
bytes and apply returns that receipt. Deployment recovery retains its existing
provider rollback journal; only a fully committed deployment receipt is
replayable.

All documentation and schema edits are ordinary repository changes and may be
reapplied through `apply_patch`. Focused tests use temporary app roots and must
not touch the real `.scenery` state.

## Artifacts and Notes

The current focused baseline passed on 2026-08-07:

    go test ./internal/evolution -run 'TestChangeApplyRequiresBoundApprovalAndRejectsReplay|TestChangeTransactionRecoveryKeepsReceiptedApply'
    go test ./internal/contractagent -run 'TestAgent'

The first command confirms the existing replay rejection; it is expected to be
rewritten by this plan.

## Interfaces and Dependencies

The implementation should converge on small interfaces equivalent to:

```go
type ChangeApplyResult struct {
    Receipt  ChangeReceipt `json:"receipt"`
    Replayed bool          `json:"replayed"`
}

func LoadIssuedChangePlan(root, planID string) (ChangePlan, error)
func ApplyIssuedChangePlanWithOptions(root, planID string, options ApplyOptions) (ChangeApplyResult, error)
func LoadAppliedChangeReceipt(root, planID string) (ChangeReceipt, error)
```

Exact names may change during implementation, but contract-agent code must load
the canonical retained plan rather than accept a model-supplied copy.

The contract-agent session context owns principal, granted capabilities,
approval tokens/verifier, client identity, request/call ID, trace/session ID,
and idempotency key. App root remains process-bound by the launcher. No new
environment-variable knob or external dependency is introduced.
