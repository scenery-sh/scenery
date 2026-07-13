# Scenery Evolution Rules

Normative companion specification

Umbrella specification: [SPEC.md](SPEC.md)

## 1. Purpose

This document defines deterministic semantic compatibility comparison for Scenery resource graphs. It is used by `scenery diff --semantic`, package publication, generated-client migration analysis, migration shadow comparison, change plans, deployment gates, and agents.

Compatibility is not one Boolean. A change can be compatible for callers while breaking response consumers, stronger for security while requiring caller migration, and unrelated to storage. These rules report those dimensions separately.

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

## 2. Dependencies and inputs

The rules depend on:

- the exact current `spec_revision`;
- canonical source, effective, and expanded graph schemas;
- stable resource addresses and explicit rename evidence;
- exact resource-schema revisions;
- every codec, runtime, data, or deployment rule whose resources are compared.

A comparison input is an immutable base graph and target graph plus:

- graph view, normally `effective` or `expanded`;
- requested dimensions;
- evolution-rule and catalog revisions;
- optional explicit rename receipts;
- optional deployment or artifact scope.

The direction is always **base consumers and state moving to the target**. Reversing base and target is a different comparison.

## 3. Result vocabulary

Each dimension result is one of:

- `compatible`: existing affected consumers or state can continue without coordinated migration;
- `breaking`: an existing valid consumer, invocation, or invariant can fail or change meaning;
- `migration_required`: compatibility can be restored only through an explicit data, runtime, client, or operational migration;
- `unknown`: the rules cannot prove one of the other results.

`unknown` is never treated as compatible. Policy MAY permit a reviewed unknown, but the approval records its exact comparison digest and risk.

Security additionally reports one relation:

- `equal`;
- `stronger`;
- `weaker`;
- `incomparable`;
- `unknown`.

`stronger` may still be caller-breaking. `weaker`, `incomparable`, and `unknown` require a security risk record.

## 4. Dimensions

Every change is classified independently for all applicable dimensions:

| Dimension | Question |
|---|---|
| source | Does existing authored source still type-check and resolve? |
| request_wire | Can requests valid under base still be sent to target with the same meaning? |
| response_wire | Can base response consumers safely consume target responses? |
| generated_client | Can code compiled against the base generated client move without incompatible API changes? |
| internal_call | Can base internal callers invoke the target binding with the same typed contract? |
| runtime | Can live runtime work and implementation adapters continue? |
| security | Is admission equal or stronger, weaker, or incomparable? |
| storage | Can existing persisted data and ownership continue safely? |
| deployment | Can the target be rolled out using the selected deployment without coordinated migration? |

A dimension that does not apply is omitted with `applicable: false`; it is not reported as compatible.

## 5. Machine result

The canonical result kind is `scenery.semantic-diff`, carries its exact `schema_revision`, and contains ordered `changes`. Each change includes at least:

~~~json
{
  "change_id": "chg_01J...",
  "operation": "replace",
  "address": "house/binding/process_scene_http",
  "expected_kind": "scenery.binding",
  "base_schema_revision": "sha256:...",
  "target_schema_revision": "sha256:...",
  "path": "/spec/http/path",
  "base": "/house/process",
  "target": "/house/scenes/process",
  "classifications": {
    "request_wire": {
      "applicable": true,
      "result": "breaking",
      "rule": "SCN_COMPAT_ROUTE_IDENTITY_CHANGED"
    },
    "security": {
      "applicable": true,
      "result": "compatible",
      "relation": "equal",
      "rule": "SCN_COMPAT_SECURITY_UNCHANGED"
    }
  },
  "affected_artifacts": [
    "typescript_client_revision[public_api]",
    "openapi_revision[public_api]"
  ],
  "evidence": []
}
~~~

Rules have stable codes. Results also include base/target revisions, evolution-rule and catalog digests, graph view, scope, summary, required migrations, generated consequences, and risk records.

Changes sort by address, schema path, then operation. Dimension names and evidence entries use canonical semantic ordering.

## 6. Resource identity and renames

Removing one address and adding another is removal plus addition unless an explicit rename receipt proves continuous identity and both schemas permit renaming.

A generated receipt has this canonical evidence shape:

~~~json
{
  "from": "parent/geometry/operation/process",
  "to": "parent/geometry/operation/process_roof",
  "base_contract_revision": "sha256:...",
  "target_contract_revision": "sha256:...",
  "digest": "sha256:..."
}
~~~

The consumer MUST recompute the domain-separated receipt digest and require exact equality with the compared base and target `contract_revision` values before treating the evidence as a rename. Stale, malformed, or fabricated evidence is not advisory rename evidence: it is ignored, leaving the ordinary removal and addition. Change apply persists generated receipts for later local comparisons; agent and CLI callers may also supply a plan or receipt explicitly.

The digest is domain-separated and covers the old/new addresses and both revisions. Change plans and apply receipts preserve the same evidence; comparison consumes it rather than guessing continuity.

A rename never silently preserves:

- an HTTP route;
- a wire field name;
- a generated language symbol;
- a durable or schedule external identity;
- a database object;
- a provider-owned identifier.

Those remain compatible only through explicit unchanged wire names, external identities, or aliases. Heuristic similarity is informative evidence only.

## 7. Type compatibility

Rules are position-aware. `input` means data accepted by the target; `output` means data emitted by the target to a base consumer.

### 7.1 Records

| Change | Input/request | Output/response |
|---|---|---|
| add required field | breaking | breaking for closed consumers; compatible only for preserving consumers |
| add optional field | compatible | breaking for closed consumers; compatible for preserving consumers |
| remove required or optional field | breaking when a base caller may send it | breaking when a base consumer requires or reads it |
| optional to required | breaking | compatible only if target proves it always emits the field; otherwise breaking |
| required to optional | compatible for callers | breaking for consumers expecting presence |
| non-nullable to nullable | compatible for callers | breaking for consumers |
| nullable to non-nullable | breaking for callers | compatible only if null was never a promised output; otherwise breaking |
| closed to preserving unknown fields | compatible | compatible |
| preserving to closed | breaking | breaking when unknown data may round-trip |
| wire-name change | breaking | breaking |

Changing only a semantic field name while retaining the exact wire name may preserve wire compatibility but is source- and generated-client-breaking.

### 7.2 Constraints

Tightening an accepted-input constraint is breaking. Loosening an accepted-input constraint is compatible. Loosening an output guarantee is breaking; tightening an output guarantee is compatible when it does not change representation.

Changing a default is breaking when omission was valid and the default affects semantic behavior. Adding a default to a formerly required input is source-compatible only when the field simultaneously becomes optional; it does not make already persisted representations compatible automatically.

Pattern changes use the specification-defined regex dialect. A tool may classify subset/superset relations only when the catalog contains a normative proof algorithm; otherwise the result is unknown.

### 7.3 Numeric types

A type change is compatible only when every base value is accepted by the target in input position, every target value is accepted by the base in output position, and the wire representation is unchanged.

Widening `int32` to `int64` is input-compatible but output-breaking unless target output remains int32-constrained. Signed/unsigned changes, exact/float changes, JSON-number/string changes, and any potentially lossy conversion are breaking. Constraint proofs may narrow an otherwise unknown case.

### 7.4 Enums and unions

- Removing an accepted input value or variant is breaking.
- Adding an accepted input value is compatible for callers.
- Adding a possible output value to a closed enum or union is breaking for exhaustive consumers.
- Adding an output value is compatible for an open enum/union consumer that normatively preserves unknowns.
- Closing an open enum or union is breaking.
- Opening a closed enum or union is source- and generated-client-breaking when generated representation changes, even if the immediate wire set does not.
- Changing a tag or wire value is breaking.

### 7.5 Collections

Changing list, set, tuple, or map kind is breaking. Changing collection ordering, duplicate semantics, key normalization, or wire encoding is breaking. Minimum/maximum cardinality follows the input/output constraint rules.

## 8. Operations and bindings

### 8.1 Operations

- Removing an exported or bound operation is breaking.
- Adding an operation is compatible unless it creates an invalid identity conflict.
- Input and outcome type changes use Section 7.
- Adding a result or declared error is response- and generated-client-breaking for closed exhaustive consumers; it is compatible only when the applicable rules normatively preserve unknown outcomes.
- Removing a result/error is request-independent but response- and implementation-breaking for consumers or handlers using it.
- Changing handler symbols without changing ABI shape is contract-compatible but implementation-breaking.

### 8.2 HTTP

- Removing a route is breaking.
- Changing gateway, method, effective path, codec, content type, mapping, status, header, or cookie contract is breaking unless an exact rule below proves equivalence.
- Adding a route is compatible after route-conflict validation.
- Adding a response media representation is compatible when base negotiation still selects the same representation for every base `Accept` value; otherwise it is breaking or unknown.
- Tightening request/body limits is request-breaking. Raising them is compatible unless security policy reports risk.
- Changing framework-enforced behavior to implementation-declared is breaking and a guarantee downgrade.

Two path templates are equivalent only when their canonical gateway, method, literal/parameter shape, parameter mappings, and scalar codecs are identical. Parameter semantic renames still affect source/generated clients.

### 8.3 Internal, CLI, event, and schedule bindings

Removing or changing an internal callable binding incompatibly is internal-call-breaking. CLI command or flag identity changes are source and CLI-wire breaking. Event topic, key, envelope, ordering, or delivery changes are runtime/wire breaking or migration-required. Schedule expression or timezone changes are runtime behavior changes and normally migration-required when catch-up state exists.

## 9. Security

Security comparison resolves the effective gateway, exposure, authentication, principal type, authorization capabilities, policy inputs, and pipeline admission steps.

- Increasing exposure is weaker.
- Anonymous replacing required authentication is weaker.
- Removing an authorization requirement or required capability is weaker.
- Requiring authentication or a stronger authorization capability is stronger but caller-breaking.
- Changing principal type, tenant source, credential trust boundary, or policy semantics without a normative implication proof is incomparable or unknown.
- Moving secret-tainted data to a non-sensitive sink is weaker and breaking.
- Reordering security-relevant pipeline steps is unknown_or_weaker unless equivalence is proven by the catalog.

Operation binding requirements are hard invariants. A target graph that violates one is invalid rather than merely breaking.

## 10. Runtime and durable execution

- Direct timeout/retry/concurrency changes are runtime changes; a stricter timeout or lower capacity may be breaking.
- Switching direct to durable or changing delivery mode is runtime-, response-, and often client-breaking.
- A durable serialization, external identity, revision, retry semantic, lease semantic, or workflow resumption change is `migration_required` unless a versioned adapter proves compatibility.
- Removing support for queued/running revisions is breaking.
- Schedule ownership epoch, cursor, overlap, and catch-up changes require operational migration when state exists.
- Implementation-only body changes with unchanged declared behavior are contract-compatible but invalidate affected implementation targets.

## 11. Storage and providers

- Additive nullable columns or independently initialized fields may be compatible when the provider rules prove it.
- Destructive, narrowing, renaming, identity, primary-key, tenant-key, encoding, or ownership changes are migration-required or breaking.
- Changing a provider while retaining capabilities is not automatically storage-compatible; provider migration evidence is required.
- A reversible expand/contract plan reports migration_required until the compatibility window closes, then compatible.
- Crossing an irreversible migration barrier records rollback impossible.

Without applicable data/provider evolution rules, storage changes are unknown.

## 12. Deployment

Changing only replicas or placement is contract-compatible but deployment-changing. Provider runtime, region, secret-store, network boundary, listener, target implementation, and rollout-strategy changes use the deployment provider's rules; absent rules are unknown.

A change that selects a different target implementation revision invalidates the deployment even when contract-compatible. An artifact-specific change invalidates only consumers of that artifact projection.

## 13. Aggregation

Dimension aggregation uses severity:

~~~text
breaking > migration_required > unknown > compatible
~~~

This ordering is for summary display only. It MUST NOT erase individual results, and policy may treat unknown more strictly than migration_required.

Aggregation reports the exact affected dimensions and their classifications. It does not translate them into release-number advice. Public breaking or unknown results require explicit consumer handling; runtime, storage, or deployment `migration_required` results require the corresponding migration evidence.

## 14. Agent and plan integration

`revisions.diff`, `scenery diff --semantic`, migration comparison, and change plans return the same canonical result schema. `revisions.diff` accepts `rename_receipts`; `scenery diff --semantic` accepts `--rename-receipts <change-plan-or-receipt.json>` and automatically loads matching applied receipts when a compared reference is an app root.

A mutation plan records:

- evolution-rule/catalog digest;
- comparison digest;
- rules that produced each result;
- read-set hashes and expected resource schema revisions;
- required approvals and migrations;
- artifact regeneration/rebuild/redeployment consequences.

Rebase recomputes the complete comparison. It never carries approvals to a changed comparison digest automatically.

## 15. Determinism and extension

Core rule catalogs are immutable and content-addressed. Provider and future rules may extend only their declared resource schemas and dimensions. Extensions cannot redefine a core rule or convert unknown to compatible without normative rules and an evidence schema.

Two conforming implementations given the same canonical graphs, view, catalogs, and scope MUST produce byte-identical ordered classifications and comparison digest. Golden fixtures are verified by at least two independent implementations.

## 16. Conformance requirements

A conforming implementation passes fixtures for:

- every result vocabulary value and non-applicable dimension;
- directional base-to-target comparison;
- required/optional/nullable record transitions;
- closed/preserving records;
- constraint tightening and loosening by position;
- every numeric widening and wire-representation boundary;
- closed/open enum and union additions/removals;
- operation and outcome additions/removals;
- HTTP route, mapping, status, media, and limit changes;
- internal, CLI, event, and schedule changes;
- authentication, authorization, exposure, principal, and secret-taint changes;
- durable revisions and queued-work migrations;
- storage/provider unknown fallback;
- target-specific and artifact-specific invalidation;
- explicit renames and external identities;
- deterministic machine schema and summary aggregation;
- rebase invalidating stale approvals;
- unknown fallback for every unsupported case.

## Appendix A: Deliberate exclusions

The current evolution rules do not claim arbitrary program equivalence, regex-language inclusion beyond catalogued cases, SQL query equivalence, opaque provider compatibility, behavioral equivalence of Go handlers, or safe stateful rollback without operational evidence.
