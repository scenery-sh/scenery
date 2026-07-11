# Scenery Legacy Bridge v1

Normative migration companion specification

Profile: `scenery.legacy-bridge/v1`  
Legacy frontend: `scenery.legacy.v0`  
Document revision: `0.4-draft`  
Status: temporary migration profile  
Umbrella specification: [SCENERY_LANGUAGE_SPEC.md](SCENERY_LANGUAGE_SPEC.md)

## 1. Purpose

This specification defines finite, service-by-service migration from Scenery's legacy declaration system to edition-2027 `.scn` source without a flag day and without two independent runtimes.

Legacy sources and native sources are separate compiler frontends that feed one canonical graph:

~~~text
Legacy sources                         Native sources
────────────────────────────           ───────────────────────
.scenery.json                          scenery.scn and *.scn
//scenery:* directives                 edition-2027 resources
Go request/response tags
model/page/durable/cron builders
          │                                      │
          ▼                                      ▼
scenery.legacy.v0 frontend             edition-2027 compiler
          │                                      │
          └──────────────┬───────────────────────┘
                         ▼
              ownership/link validation
                         ▼
              canonical active graph
                         │
         ┌───────────────┼─────────────────┐
         ▼               ▼                 ▼
   runtime plan      inspect/CLI       generators
~~~

There is one semantic graph, one runtime plan, one route table, one service lifecycle graph, one worker/schedule registry, and one generated client contract.

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

## 2. Non-goals

The bridge is not:

- edition-2027 syntax;
- a permanent second declaration system;
- a second server or worker runtime;
- permission to add new legacy capabilities;
- implicit precedence between old and new declarations;
- an endpoint-by-endpoint lifecycle split within one service;
- a reason to replace SQL, sqlc, databases, or business implementation code.

## 3. Project modes

### 3.1 Legacy-only

A legacy-only project has legacy roots and no `scenery.scn`:

~~~text
.scenery.json
Go directives, tags, and builders
~~~

The legacy frontend owns every discovered resource. It preserves legacy behavior exactly, including stable-v0 build-context discovery while the project remains legacy-only. The bridge records the resolved context in an implementation compatibility descriptor but does not require native source.

### 3.2 Native-only

A native-only project has:

~~~text
scenery.scn
*.scn
~~~

Legacy discovery is disabled. No migration manifest is present.

### 3.3 Mixed migration mode

Mixed mode requires native source, at least one explicitly bounded legacy source, and the temporary manifest:

~~~text
scenery.scn
scenery.migration.scn
optional .scenery.json while shared legacy configuration remains
explicitly listed legacy Go packages
~~~

`scenery.migration.scn` is bridge tooling configuration, not edition-2027 source. It contributes to `workspace_revision` and ownership planning but is absent from the final native application.

The presence of any configured legacy root and `scenery.scn` without `scenery.migration.scn` is an error. `.scenery.json` plus `scenery.scn` is always treated as ambiguous without the manifest. There is no implicit old-wins, new-wins, source-order, or filename precedence.

## 4. Bounded migration manifest

### 4.1 Example

~~~hcl
migration {
  frontend      = "scenery.legacy.v0"
  legacy_config = ".scenery.json"

  legacy_gateway "default" {
    target = http_gateway.public_api
  }

  legacy_service "jobs" {
    package   = "./jobs"
    namespace = "jobs"
    target    = go_target.development
  }

  legacy_service "tasks" {
    package   = "./tasks"
    namespace = "tasks"
    target    = go_target.development
  }

  legacy_service "mail" {
    package   = "./mail"
    namespace = "mail"
    target    = go_target.development
  }

  shadow_service "house" {
    package       = "./house"
    module        = module.house
    legacy_target = go_target.development
    active        = "legacy"
  }
}
~~~

All paths are normalized, workspace-relative, and symlink-safe. A package must be beneath a declared implementation root.

`legacy_config` is required while `.scenery.json` exists and omitted after that shared file has been migrated. Every legacy or shadow service in mixed mode binds its legacy implementation to an explicit declared Go target. Mixed mode may continue with bounded legacy packages until the final service retires. In this post-config state, compatibility client generation and every compile/generate/change/migration planning check derive application identity, selected legacy services/bindings, and client/auth options from the already compiled application and migration snapshot; they MUST NOT rediscover the removed ambient v0 config.

### 4.2 Discovery boundary

In mixed mode the frontend reads only:

- the declared `legacy_config`;
- explicitly listed legacy or shadow package roots;
- their declared Go build-input manifest under the bridge toolchain context;
- explicitly declared shared legacy resource inputs.

It MUST NOT recursively scan the repository, import graph, module cache, generated tree, or sibling packages looking for possible legacy constructs.

An unlisted directive or builder has no legacy ownership. `scenery migrate verify` reports remaining constructs in a service before retirement, but ordinary mixed compilation does not expand its discovery boundary.

### 4.3 Explicit ownership inventory

Every service visible to the merged graph has one manifest state:

- `legacy_service`;
- `shadow_service`;
- `native_service`.

Native-only services may be listed as:

~~~hcl
native_service "house" {
  module = module.house
}
~~~

The profile MAY permit a generated explicit inventory, but it MUST NOT infer active ownership from declaration order.

## 5. Legacy frontend inputs

`scenery.legacy.v0` understands the frozen legacy surface:

- `.scenery.json` and the legacy `.config.json` fallback, including app identity, roles, frontend/proxy, built-in auth, dev services, storage, database-apply, generation, and deployment fields;
- `//scenery:api` typed and raw forms, public/auth/private exposure, methods, paths, tags, and defaults;
- request/response JSON, path, query, header, cookie, optional, sensitive, status, and header-output tags;
- `//scenery:service`, constructor/dependency discovery, initialization, and shutdown;
- `//scenery:authhandler` supported signatures;
- `//scenery:middleware`, targets, global markers, and legacy ordering;
- `//scenery:model`, `model.Entity`, table/existing-table, action generation/disable/override, field metadata, seeds, renames, and model field tags;
- `//scenery:page`, `page.Collection`, columns, displays, filters, sorts, slots, and actions;
- `durable.NewTask`, `durable.Start`, `durable.Step`, `durable.Signal`, and `durable.Schedule`, including external identities, revisions, retries, dedupe, persisted steps, signals, leases, and worker registration;
- `cron.NewJob`, interval/every configuration, overlap, catch-up, service/handler identity, and runtime registration;
- stable generated private-call and TypeScript-client surfaces described by the catalog.

The normative catalog identity is `scenery.legacy-v0-catalog/v1`. Its immutable, published descriptor enumerates every accepted JSON field, directive grammar, struct tag, builder symbol/option, generated ABI, default, ordering rule, support tier, behavioral-fixture ID, lowering rule, guarantee ceiling, and migration disposition schema. Its SHA-256 digest is recorded by `capabilities`, compilation results, comparison evidence, compatibility artifacts, and activation receipts.

There is no catch-all “other legacy construct.” A discovered symbol or field absent from the catalog is `unsupported` and prevents mixed mode unless it is outside every bounded package. Adding a new legacy feature requires another catalog/frontend identity; v0 does not grow to become an alternative language.

The compiler frontend uses parsing, Go syntax/type information, and build descriptors. In legacy-only mode it reproduces and records the stable-v0 resolved build context; entering mixed mode requires replacing that discovery with each service's explicit `legacy_target`. Core `check` and `compile` MUST NOT execute arbitrary application code. A construct that cannot be recovered statically becomes advisory or opaque as defined below.

## 6. Lowering to canonical resources

### 6.1 Required mappings

| Legacy construct | Canonical lowering |
|---|---|
| API directive | operation + direct execution + HTTP binding + compatibility policies |
| Raw API directive | operation + transport-coupled compatibility binding with advisory/opaque wire facets |
| Service directive/discovery | service + lifecycle + implementation compatibility adapter |
| Auth handler | compatibility authentication provider + principal mapping |
| Request/response tags | record fields + HTTP mappings + response mappings |
| Middleware directive | ordered compatibility pipeline/middleware references |
| Private endpoint/helper | operation + internal compatibility binding + generated legacy client adapter |
| `durable.NewTask` | operation + durable execution |
| `cron.NewJob` | schedule invoking an operation/execution |
| `model.Entity` | record/entity + explicit source mapping + optional CRUD expansion |
| `page.Collection` | page/actions + compatibility renderer metadata |
| Legacy shared database/storage | typed compatibility provider instance |
| Legacy generated TypeScript client | versioned generated-client compatibility projection |
| App root roles/frontend/dev/deploy fields | compatibility deployment, gateway, frontend, and runtime-plan resources |

Lowering produces the same resource kinds and addresses used by the native compiler. Legacy resources are not stored in a parallel endpoint/task schema.

### 6.2 Provenance

Every lowered resource carries origin and legacy construct detail:

~~~json
{
  "address": "jobs/operation/latest_offers",
  "kind": "scenery.operation/v1",
  "origin": {
    "kind": "legacy_v0",
    "frontend": "scenery.legacy.v0",
    "source_id": "src_legacy_jobs_go",
    "legacy_symbol": "jobs.LatestOffers",
    "legacy_construct": "scenery:api"
  },
  "compatibility": {
    "semantics": "legacy_exact",
    "contract": "advisory"
  }
}
~~~

Source IDs follow normal portable source-map rules. Absolute paths are not emitted in distributable manifests.

### 6.3 Stable addresses

Legacy lowering derives stable addresses using the declared namespace and legacy identity rules. The migration tool emits and then retains an address map. Renames require explicit receipts; heuristic matches never silently rewrite ownership.

## 7. Preserve legacy semantics

The compatibility frontend reproduces existing behavior, including awkward behavior:

- omitted methods use the exact legacy default;
- middleware ordering follows the exact legacy rule;
- package-derived service identity remains until explicitly migrated;
- raw endpoints retain compatibility transport behavior;
- legacy optional tags retain current decoder behavior;
- dynamic status/error conventions retain their existing adapter behavior;
- task, cron, and lifecycle naming retain legacy identity.

The bridge MUST NOT reinterpret a legacy declaration under edition-2027 defaults merely because both lower into the same kind.

Each legacy facet has a guarantee classification:

- `verified`: statically complete and framework-checkable;
- `advisory`: declared metadata exists but runtime behavior may differ;
- `opaque`: the frontend cannot derive a complete typed contract.

Each discovered construct also has one migration disposition:

- `legacy_exact`: bridge fixtures prove the promised legacy behavior;
- `native_equivalent`: a native profile can represent it without intentional change;
- `advisory`: metadata is useful but behavior is not fully enforceable;
- `opaque`: runtime ownership is known but contract details are incomplete;
- `unsupported`: the bridge cannot run or analyze the construct safely;
- `rewrite_required`: legacy ownership can continue, but native activation requires a documented manual rewrite or a future profile.

`scenery migrate status` reports both classifications, active and shadow owners, blocking profiles, stateful cutover class, generated artifacts, external identities, and rollback state for every construct. Unknown or unclassified constructs block the no-flag-day readiness claim.

Service-level guarantee classification and migration disposition aggregate the weakest construct-level value. Static graph completeness MUST NOT upgrade advisory semantics or disposition to `verified` or `native_equivalent`.

Opaque resources may remain active while legacy-owned. They cannot be declared shadow-equal, consumed by native resources as fully typed contracts, or used to generate trustworthy native clients.

Every runtime ownership key must still be known. If the frontend cannot determine a route key, service lifecycle identity, durable external identity, schedule identity, or schema/migration owner, the resource may run only in legacy-only mode until an explicit compatibility descriptor reserves that identity. Mixed mode MUST NOT guess around an opaque ownership key.

Correcting legacy semantics occurs in the native candidate and appears as an explicit comparison difference.

### 7.1 Behavioral meaning of legacy_exact

`legacy_exact` is earned by behavioral fixtures, not by graph-field similarity. HTTP fixtures cover route matching/precedence, `:param` decoding, query plus-versus-space, duplicate values, unknown and duplicate JSON members, `omitempty`, embedded fields, pointer/null behavior, `encoding.TextUnmarshaler`, custom JSON marshal/unmarshal methods, status fields, coded errors, panic/recovery, middleware order, auth/request globals, header casing/repetition, streaming, flushing, trailers, cancellation, and disconnects.

Lifecycle, durable, schedule, private-call, generated-client, environment/configuration, database, and storage constructs have equivalent fixture catalogs. A facet lacking a passing fixture is advisory, opaque, unsupported, or rewrite_required; it cannot be labelled legacy_exact.

Native codec rules never apply to a still-legacy binding merely because it lowered into a native resource kind.

## 8. Ownership and linking

### 8.1 Core rule

Every runtime-relevant identity has exactly one active owner.

The linker enforces one active owner for:

- canonical resource address;
- HTTP gateway/method/effective-path route;
- service lifecycle identity;
- durable external identity and revision;
- schedule/cron identity;
- worker/event-consumer identity;
- entity schema and migration ledger;
- generated client operation surface;
- shared authentication, authorization, pipeline, and provider instance address.

There is no conflict resolution by frontend precedence.

### 8.2 Candidate graphs

Each frontend first produces a candidate graph keyed internally by `(frontend, address)`. The ownership linker selects at most one candidate into the active graph. Non-selected shadow candidates remain comparison sidecars and are not addressable as active resources.

The final canonical active graph contains ordinary resources plus provenance and migration metadata. Runtime generation reads only this graph.

`contract_revision` hashes the active graph only. Each inactive shadow candidate has a separate candidate digest and validation result. Candidate validation uses the predicted operating graph: current active graph, minus active resources owned by candidate service S, plus the candidate resources for S. Every other active service owner remains present so verified cross-service dependencies resolve and global routes, durable external identities, schedules, event consumers, schema owners, and generated-client identities are checked before status claims the candidate is valid. Editing a shadow candidate changes `workspace_revision` and its comparison digest but does not change the active contract or implementation revision until activation. An activation plan predicts the revisions produced by selecting the candidate.

### 8.3 Dependencies

A native resource may depend on a legacy-owned resource only when that legacy resource has a complete verified canonical contract and is explicitly exported through the temporary compatibility namespace.

Advisory or opaque resources cannot satisfy a typed dependency requiring framework guarantees.

## 9. Service migration unit

The initial minimum activation unit is a service.

A service boundary includes:

- constructor and shutdown;
- injected dependencies;
- operations and routes;
- durable workers and schedules;
- middleware and admission policy;
- generated clients;
- schema/migration ownership;
- package-local operational assumptions.

The bridge does not permit some operations to use a legacy lifecycle while other operations use a native lifecycle for the same service identity. A future finer-grained profile would require an explicit independent lifecycle partition.

## 10. Migration states

### 10.1 Legacy

The legacy candidate owns every active service resource. No native candidate is required.

### 10.2 Shadow

Both candidates compile, but exactly one frontend is active:

~~~hcl
shadow_service "house" {
  package       = "./house"
  module        = module.house
  legacy_target = go_target.development
  active        = "legacy"
}
~~~

The inactive candidate:

- never registers a route;
- never constructs a service;
- never starts a worker or schedule;
- never owns a migration;
- never emits a generated public client as active;
- is available only for semantic comparison.

### 10.3 Native

Activation changes `active = "legacy"` to `active = "native"` through a revision-checked plan. The native candidate becomes the only runtime owner. The legacy candidate may remain temporarily for comparison or rollback planning.

### 10.4 Retired

Legacy directives, tags, builders, compatibility adapters, and migration mapping for the service are removed. The block becomes `native_service` or disappears when the whole migration manifest is removed.

The intended flow is:

~~~text
legacy -> shadow(active legacy) -> shadow(active native) -> native -> retired
~~~

Skipping shadow requires an explicit risk approval recorded in the activation plan.

## 11. Shadow comparison

`scenery migrate compare SERVICE -o json` compares resolved candidate subgraphs, not source text. Static classifications use [SCENERY_COMPATIBILITY_CORE_V1.md](SCENERY_COMPATIBILITY_CORE_V1.md); bridge evidence adds legacy behavioral and operational dimensions without redefining core rules.

Required dimensions are:

- operations, input types, result and error variants;
- HTTP gateway, method, path, and input mapping;
- response statuses, bodies, headers, and cookies;
- authentication and authorization;
- effective middleware pipeline and order;
- execution mode, timeout, lease, retry, concurrency, and retention;
- durable identity/revision;
- schedules and overlap/catch-up behavior;
- service constructor, dependencies, and lifecycle;
- entity/schema/migration ownership;
- generated client source and wire compatibility;
- advisory/opaque guarantee changes.

The comparison result is versioned and machine-readable:

~~~json
{
  "service": "house",
  "state": "shadow",
  "active": "legacy",
  "evidence_mode": "static_contract",
  "static_contract_complete": true,
  "static_contract_equal": false,
  "behavioral_evidence_complete": false,
  "operational_evidence_complete": false,
  "complete": false,
  "equal": false,
  "differences": [
    {
      "dimension": "wire",
      "address": "house/binding/process_scene_http",
      "path": "/spec/responses/scene_not_found/status",
      "legacy": 500,
      "native": 404,
      "classification": "intentional_or_breaking"
    }
  ]
}
~~~

`static_contract_complete` and `static_contract_equal` report canonical graph shape independently. `behavioral_evidence_complete` is true only when every promised legacy facet has verified behavioral evidence and a native-equivalent disposition. `operational_evidence_complete` is true only when no cutover class still requires external drain/fence/cursor/consumer evidence. Aggregate `complete` requires all three dimensions; aggregate `equal` additionally requires static equality. A static graph comparison may therefore be complete and equal while aggregate `complete` and `equal` remain false. A project may approve intentional static differences, but the approval is bound to the exact comparison digest.

### 11.1 Comparison evidence modes

The bridge supports three separately reported evidence modes:

1. `static_contract`: compare canonical candidate graphs, schemas, policies, and generated surfaces without executing handlers.
2. `recorded_replay`: replay a redacted, content-addressed traffic corpus in isolated legacy and native harnesses, comparing status, headers, body bytes, side-effect transcript, logs, and cancellation behavior.
3. `live_read_only`: mirror explicitly proven read-only traffic to an isolated shadow and discard its response after comparison.

Live shadowing is disabled by default. Requests with writes, external calls, nondeterminism, unknown idempotency, durable dispatch, schedule effects, event publication, secrets, or opaque behavior MUST NOT be mirrored live. A policy may permit only operations whose side-effect analysis and sandbox prove read-only behavior.

Recorded corpora contain no secret plaintext and are bound to codec/frontend versions and candidate revisions. Replay mismatch is a cutover blocker unless explicitly approved as an intentional behavior change.

## 12. Atomic activation and rollback

### 12.1 Activation plan

~~~text
scenery migrate activate house --native --dry-run --out house-activation-plan.json
scenery migrate apply house-activation-plan.json --approval-token project-approval.json
~~~

Activation uses the ordinary immutable plan/apply transaction model, including authenticated issuance before apply trusts any supplied plan field. The plan binds:

- base `workspace_revision`;
- base and predicted `contract_revision`;
- implementation/deployment invalidation state;
- legacy and native candidate digests;
- comparison digest and approved differences;
- ownership keys transferred;
- generated runtime-plan diff;
- required risk approvals;
- expiry and caller identity.

Apply atomically edits ownership configuration and generated descriptors. Any revision, candidate, comparison, diagnostic, or approval mismatch commits nothing.
The apply command consumes the exact retained plan. Re-running `migrate activate` would issue a new expiry-bound plan and cannot consume an approval token for the dry-run plan.

Native activation requires static contract completeness. Static differences require approval of the exact comparison digest. Incomplete behavioral evidence requires the revision-bound `risk_advisory_migration_evidence` approval; static shape equality alone never authorizes cutover. Operational evidence remains mandatory for each non-stateless cutover class.

The activation plan computes cutover classes from the union of legacy and native candidate resources. A durable execution, schedule, event consumer, generated client, or other stateful identity removed by the native candidate still requires its drain, fence, cursor, or consumer evidence; omission from the target graph does not erase the legacy cutover obligation.

### 12.2 Runtime effect

One runtime plan is generated after ownership linking. Activation can never start a native route/worker before the corresponding legacy owner is absent from that same plan.

### 12.3 Operational cutover classes

Every activation plan assigns each transferred resource one cutover class:

| Class | Required evidence and behavior |
|---|---|
| stateless_route | Atomic owner flip; immediate source/runtime rollback permitted while both candidates remain valid |
| stateful_direct_service | Versioned stored-state compatibility window, read/write compatibility evidence, and rollback deadline |
| durable_execution | Fence new dispatch, drain or migrate queued/running legacy revisions, or retain an old-revision worker until zero outstanding work |
| schedule | Transfer a monotonically increasing ownership epoch and preserve nominal-fire, last-fire, missed-run, and catch-up cursor |
| schema_owner | Expand/contract migration plan with explicit reversible and irreversible barriers |
| event_consumer | Broker subscription/offset handoff, fencing, replay policy, and duplicate-delivery strategy |
| generated_client | Versioned coexistence or evidence that deployed consumers accepted the new client surface |
| external_identity | Preserve or explicitly migrate task names, webhooks, aliases, and other non-Scenery identities |

Activation cannot proceed while required drains, compatibility windows, offsets, cursors, consumer gates, or migration barriers are unresolved. The receipt records each operational state and whether rollback is currently safe, conditionally safe, or impossible.

A source ownership rollback is not an operational rollback. The CLI and API MUST NOT present a generic rollback action as safe when any transferred class crossed an irreversible barrier.

### 12.4 Rollback

An activation receipt records the reverse ownership transfer. Rollback is allowed only while:

- the legacy candidate still compiles;
- no incompatible schema/migration ownership change has occurred;
- durable identity/revision remains compatible;
- every cutover-class receipt still reports rollback-safe;
- the rollback plan passes the same conflict and comparison checks.

Rollback is a new revision-checked plan, not a runtime toggle outside source control.

## 13. Contract and handler migration are separate

A service may migrate in two phases.

### 13.1 Native contract with legacy handler

~~~hcl
operation "latest_offers" {
  service = service.jobs
  input   = record.latest_offers_input

  handler {
    method  = "LatestOffers"
    adapter = "legacy_go_v0"
  }

  result "ok" {
    type = record.latest_offers_response
  }
}
~~~

The bridge generates a compatibility adapter for the frozen existing signature, for example:

~~~go
func (s *Service) LatestOffers(
    context.Context,
    *LatestOffersParams,
) (*LatestOffersResponse, error)
~~~

The adapter maps the legacy call and dynamic behavior into declared operation outcomes. Every unprovable mapping is advisory and visible.

### 13.2 Native Go ABI

Later, implementation moves to `scenery.go-implementation/v1`:

~~~go
func (s *Service) LatestOffers(
    ctx context.Context,
    input jobscontract.LatestOffersInput,
) (jobscontract.LatestOffersOutcome, error)
~~~

The source removes `adapter = "legacy_go_v0"`. Contract activation and implementation-ABI activation may be separate plans, but one operation has exactly one active adapter at a time.

If the service lifecycle is already native while this operation remains bridge-backed, the Go verifier MUST prove that the native constructor result is assignable to the legacy endpoint receiver. A bridge that would require a failing runtime type assertion is invalid before generation or startup.

## 14. Native candidate generation

~~~text
scenery migrate service jobs --generate --dry-run
~~~

Generation inspects bounded legacy declarations and Go types, including:

- JSON and transport tags;
- path arguments;
- query, header, and cookie mappings;
- pointer and optional behavior;
- response types;
- status and response-header tags;
- coded errors where statically enumerable;
- service dependencies and lifecycle symbols.

Generated `.scn` source is explicit and human-editable. It is a proposed semantic change, not an automatically active owner.

Ambiguity becomes a diagnostic, never a guess:

~~~text
SCN_MIGRATE_OPTIONAL_AMBIGUOUS
jobs.LatestOffersParams.Country uses string with query:"country".
Legacy decoding cannot distinguish absence from an empty value.
Choose required(string) or optional(string).
~~~

Other required ambiguity diagnostics include dynamic error surface, implicit status, opaque raw body, unresolved middleware order, package-derived identity, and runtime-only builder configuration.

## 15. Shared application resources

Legacy `.scenery.json` and bounded shared declarations lower into ordinary canonical resources. During mixed mode, verified exports are available through a temporary `legacy` reference root:

~~~hcl
authentication = legacy.authentication.standard
database       = legacy.data_source.jobs_database
~~~

The `legacy` root exists only under `scenery.legacy-bridge/v1`. It is not part of edition 2027 and disappears at migration finish.

No address may be defined by both frontends. Shared resources can migrate independently by switching references to native resources, such as:

~~~hcl
authentication = authentication.standard
database       = data_source.jobs_database
~~~

Global middleware and authorization are included in service comparison through their fully resolved effective policies.

## 16. Data implementations remain independent

Migration does not replace SQL, sqlc, schemas, transactions, or database initialization.

A migrated service may keep:

- existing SQLite or PostgreSQL schema;
- existing `queries.sql`;
- existing sqlc-generated Go packages;
- existing `*sql.DB` setup;
- handwritten transactions and query code.

The native declaration makes dependencies and ownership explicit. Replacing the data implementation is a separate semantic and operational change.

## 17. Merged inspection

All resource-oriented inspection reads the merged active graph:

~~~text
scenery list service -o json
scenery list operation -o json
scenery get jobs/operation/latest_offers --view effective -o json
scenery explain jobs/operation/latest_offers --provenance -o json
~~~

Resources include migration state:

~~~json
{
  "address": "jobs/operation/latest_offers",
  "origin": {
    "frontend": "legacy_v0"
  },
  "migration": {
    "state": "legacy",
    "active": "legacy",
    "native_candidate": null
  }
}
~~~

For shadow services, comparison summaries and candidate digests are available without making the inactive candidate part of the active graph.

Legacy inspection commands may remain as compatibility projections:

~~~text
scenery inspect endpoints --json
scenery inspect routes --json
scenery inspect durable --json
~~~

They MUST project the merged canonical graph and MUST NOT invoke an independent legacy runtime discovery path.

### 17.1 CLI and machine-schema compatibility

The CLI protocol is versioned independently from the language edition. An implementation claiming this bridge supports both `scenery.cli.v0` and the current resource-oriented protocol for the stable v0 command catalog.

Existing v0 spellings and flags retain their exact documented envelope, field names, exit-status taxonomy, ordering, and omission rules. In particular, an invocation such as `scenery check --json` selects the v0 protocol, while `scenery check -o json` selects the current protocol. `--api-version scenery.cli.v0` and `--api-version scenery.cli.v1` are explicit overrides. Conflicting selectors are an error.

Both protocols project the same merged active graph. A compatibility command MUST NOT run a second discovery or runtime path. If an active native resource cannot be represented truthfully in a requested v0 schema, the command emits a stable `SCN_LEGACY_CLI_UNREPRESENTABLE` diagnostic and fails; it never omits, invents, or weakens the resource silently.

Activation reports every known CI, agent, and script dependency on a v0 command schema when such consumers are declared in workspace metadata. Undeclared external consumers remain an explicit operational risk. CLI protocol retirement is independent from any one service ownership switch, but bridge `finish` still requires declared v0 consumers to migrate or move to a separately supported CLI-compatibility product.

### 17.2 Generated client compatibility

Generation has two explicit product families during mixed mode:

1. A native merged client per requested language and public-surface projection, generated from all active resources whose wire behavior is complete and verified. TypeScript follows [SCENERY_TYPESCRIPT_CLIENT_V1.md](SCENERY_TYPESCRIPT_CLIENT_V1.md). Its descriptor records active operation addresses, gateway or export scope, codec profiles, whole `contract_revision`, and the smallest applicable artifact revision.
2. A versioned legacy client for active `legacy_exact`, advisory, or custom-wire surfaces whose stable v0 client behavior cannot be represented by the native profile. It retains the v0 package, method, metadata, and error conventions and records the compatibility frontend and fixture-catalog digest. After `legacy_config` is removed, this family is rendered from the compiled application identity, active migration inventory, canonical authentication resources, structured handler-adapter metadata, and selected binding graph rather than ambient v0 root discovery. Free-form source symbols, legacy names, and origin descriptions MUST NOT enable authentication options.

The two families use distinct artifact identities and import/package names. A generated client selection manifest maps every active client operation to exactly one family and revision. No generator may expose advisory or opaque behavior as native framework-enforced behavior, and no operation may be silently emitted by both families under the same external artifact identity.

Native-equivalent, verified legacy resources MAY appear in the merged native client before service activation when their active legacy adapter enforces the declared wire contract. Otherwise they remain in the versioned legacy client until ownership changes.

Moving an operation between client families is a `generated_client` cutover. Activation requires either a compatibility-preserving surface, versioned coexistence for the declared support window, or evidence that all declared deployed consumers accepted the regenerated client. Browser caches and independently deployed frontends are stateful consumers; regenerating source in the repository is not proof of cutover.

## 18. Commands

### 18.1 Initialization

~~~text
scenery migrate init
~~~

Creates a proposed `scenery.scn` and bounded `scenery.migration.scn`. It does not activate native ownership.

### 18.2 Status

~~~text
scenery migrate status -o json
~~~

Shows mode, service states, active owners, opaque/advisory resources, conflicts, and readiness.

For every discovered construct, status includes guarantee classification, migration disposition, active and shadow owners, candidate and comparison digests, required and missing profiles, cutover class, stateful drain/fence/cursor state, external identities and aliases, generated artifacts and deployed-consumer gates, CLI protocol dependencies, rollback safety, and all blocking diagnostics. Summary readiness is false while any construct is unknown or any required field is unavailable.

### 18.3 Candidate generation

~~~text
scenery migrate service house --generate --dry-run
~~~

Produces native source edits and ambiguity diagnostics through the normal plan model.

### 18.4 Shadowing and comparison

~~~text
scenery migrate service house --shadow
scenery migrate compare house -o json
~~~

Shadowing writes explicit ownership configuration but does not change the active owner unless separately requested.

### 18.5 Activation

~~~text
scenery migrate activate house --native --dry-run --out house-activation-plan.json
scenery migrate apply house-activation-plan.json --approval-token project-approval.json
~~~

Activation is atomic and receipt-producing.

### 18.6 Verification and finish

~~~text
scenery migrate verify house
scenery migrate finish
~~~

`verify` finds remaining bounded legacy constructs, adapters, opaque dependencies, ownership, stateful identities, CLI consumers, and generated projections for one service. `finish` succeeds only when no active or shadow legacy service, legacy export, compatibility adapter, raw/custom-middleware/streaming bridge dependency, opaque or unsupported construct, `rewrite_required` disposition, old durable revision or queued work, legacy schema/event owner, legacy generated-client consumer, ownership receipt needed for rollback, or legacy root remains.

Machine output follows normal CLI envelopes and stable diagnostics.

## 19. Hard coexistence rules

The profile freezes these invariants:

1. Exactly one active owner per canonical resource address.
2. Exactly one active owner per HTTP gateway/method/effective path.
3. Exactly one active lifecycle per service identity.
4. Exactly one active durable external identity and revision.
5. Exactly one active schedule, worker, schema, and migration owner.
6. No implicit precedence between frontends.
7. Legacy discovery is explicitly bounded in mixed mode.
8. Legacy behavior is preserved rather than silently upgraded.
9. Shadow candidates never generate runtime registrations.
10. Native resources depend only on legacy exports with complete verified contracts.
11. Opaque/advisory legacy facets are never represented as framework-enforced.
12. Ownership switches are immutable, revision-checked plans with receipts.

## 20. Security and determinism

The legacy frontend:

- performs no ambient repository scanning;
- performs no network access during check/compile;
- does not execute arbitrary application code during contract compilation;
- uses the explicit legacy-service Go target in mixed mode; legacy-only compatibility records the exact stable-v0 resolved context and never reuses it as a native production target;
- records every source and compatibility snapshot digest;
- applies the same secret-redaction and source-map rules as native compilation;
- treats raw/opaque behavior as untrusted and advisory.

If exact legacy behavior requires a build-produced compatibility descriptor, that descriptor is explicit, content-addressed, and part of `implementation_revision`. It cannot silently change the contract graph during runtime startup.

## 21. Implementation sequence

### Phase 1: Graph compatibility kernel

- Define canonical graph schemas.
- Implement legacy-v0 lowering.
- Prove existing apps produce equivalent route, service, task, schedule, and policy metadata.
- Keep current runtime adapters while the graph is verified.

### Phase 2: Native compiler and linker

- Parse/type-check `.scn`.
- Compile both candidate graphs.
- Enforce ownership and conflicts.
- Expose the merged graph through inspection.

### Phase 3: Shadow migration

- Generate native service candidates.
- Compare semantics.
- Add `legacy_go_v0` compatibility adapters.
- Migrate one fixture service.

### Phase 4: Common runtime generation

- Generate one route/service/worker/schedule plan.
- Make old inspection commands projections of that plan.
- Migrate one real ONLV service.

### Phase 5: Retirement

- Remove legacy ownership service by service.
- Migrate shared configuration.
- Delete `.scenery.json` and `scenery.migration.scn`.
- Remove the frontend after the published support window.

## 22. Removal condition

The bridge is finite. No new product should start in mixed mode when native-only is possible.

The legacy frontend may be removed only after:

- all first-party fixtures are native;
- ONLV has no active or shadow legacy services;
- at least one structurally different real application has completed migration;
- shared legacy configuration is gone;
- compatibility adapters are gone;
- every raw endpoint, custom middleware ABI, streaming handler, or other unsupported native surface has either migrated to a claimed transport-coupled profile or been explicitly rewritten;
- no legacy durable work, schedule cursor, schema/migration owner, event offset, external identity alias, v0 CLI consumer, or legacy generated-client dependency remains;
- two stable Scenery releases have supported mixed mode;
- rollback/support policy for the last bridge release is published.

An application that still requires raw Go HTTP ownership, custom middleware ABI, or streaming may continue under the frozen bridge for its published support window. It cannot claim native-only completion merely because its declarative metadata was translated.

## 23. Release gates

Scenery MUST NOT advertise no-flag-day migration or bridge retirement until all of these product-level proofs pass:

1. Every stable-v0 construct has a behavioral fixture and one non-unknown migration disposition.
2. ONLV and one structurally different real application complete service-by-service migrations.
3. A redacted captured HTTP corpus reproduces every promised `legacy_exact` request and response byte-for-byte.
4. Legacy-to-native and native-to-legacy internal calls preserve authentication, principal/context mapping, typed outcomes, failures, deadlines, and cancellation.
5. A durable execution is cut over with old jobs present, drained or served by a fenced old-revision worker, rolled back where safe, and rejected where an irreversible barrier was crossed.
6. From a clean clone, `scenery check`, editor type checking, `go test ./...`, generation, and build follow the documented Go contract-package workflow.
7. Host development/test targets coexist with a different fixed production target and yield distinct reproducible implementation revisions.
8. An agent discovers a route semantically, plans a typed change, handles a revision conflict through explicit rebase, applies atomically, and explains the resulting revisions without repository text search.
9. `scenery migrate finish` rejects raw, opaque, unsupported, rewrite-required, old durable, old schema/event-owner, v0 CLI, and legacy generated-client dependencies.

Release-gate evidence is content-addressed and records tool, fixture catalog, application revision, target, and result. A passing older result does not cover changed relevant inputs.

## 24. Conformance requirements

A conforming bridge passes fixtures for:

- all three project modes;
- ambiguous root rejection;
- bounded discovery and unlisted legacy constructs;
- every supported legacy lowering;
- exact legacy default and middleware-order preservation;
- guarantee classification and every migration disposition;
- complete behavioral fixture coverage for every `legacy_exact` facet;
- stable addresses and provenance;
- duplicate active resource rejection;
- gateway-scoped route conflict rejection;
- duplicate lifecycle, durable, schedule, and migration ownership rejection;
- shadow candidates producing zero runtime registrations;
- comparison across every required dimension;
- optional/null ambiguity diagnostics;
- raw/dynamic legacy advisory behavior;
- contract migration with `legacy_go_v0` handler adapter;
- native ABI migration;
- atomic activation and revision conflict;
- rollback eligibility and rejection;
- every operational cutover class, drain/fence/cursor receipt, and irreversible rollback barrier;
- merged inspection and legacy projection commands;
- exact v0 CLI selection, output, exit status, and unrepresentable-resource failure;
- merged native and versioned legacy client generation, selection manifests, and deployed-consumer gates;
- legacy-to-native and native-to-legacy internal calls;
- native dependency rejection for opaque legacy contracts;
- finish refusal for every legacy, raw, opaque, unsupported, rewrite-required, stateful, CLI, and generated-client blocker;
- clean-clone Go and multi-target migration workflows;
- deterministic graph and plan output.

## Appendix A: Example mixed application

Native root excerpt:

~~~hcl
language {
  edition = "2027"
}

application "clean_tech" {
  version = "2.0.0"
}

module "house" {
  source = "./house"

  inputs = {
    gateway              = http_gateway.public_api
    database             = data_source.house_database
    storage              = data_source.app_storage
    task_engine          = execution_engine.durable_tasks
    authentication       = authentication.standard
    authorization        = authorization.member
    http_pipeline        = pipeline.http_default
    process_concurrency  = 4
    roof_eval_concurrency = 1
  }
}
~~~

Temporary `scenery.migration.scn`:

~~~hcl
migration {
  frontend      = "scenery.legacy.v0"
  legacy_config = ".scenery.json"

  legacy_service "jobs" {
    package = "./jobs"
    target  = go_target.development
  }

  legacy_service "tasks" {
    package = "./tasks"
    target  = go_target.development
  }

  shadow_service "house" {
    package       = "./house"
    module        = module.house
    legacy_target = go_target.development
    active        = "legacy"
  }
}
~~~

The referenced development target is declared in native application source. After an approved activation, `active` becomes `native`. After legacy House declarations are deleted, the block becomes `native_service`. After the final service and shared resource migrate, the migration file disappears.
