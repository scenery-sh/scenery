# Add generated access metadata, route gating, and explicit audit identity

This ExecPlan is a living document. Update `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` as work proceeds. Maintain it according to `PLANS.md`.

## Purpose / Big Picture

After this change, applications built with Scenery can declare which product application owns a generated page and which application-owned capability controls that page. The generated React route tree, side navigation, direct-route boundary, workspace tabs, app switcher, and command menu can all consume one generated route catalog instead of reconstructing ownership from URL prefixes.

Scenery will also expose an unambiguous impersonation-aware audit identity. Downstream applications will be able to distinguish the effective user whose permissions and data are being exercised from the real actor who initiated an impersonation session. This prevents audit records from attributing an administrator's action only to the impersonated subject.

The new surface remains deliberately small:

- Scenery owns route metadata propagation, route and workspace-tab presentation gating, current-route matching, and auth-session actor/effective identity.
- Each application owns app entitlements, access profiles, feature semantics, data scopes, organizations, policy persistence, exceptions, and backend authorization.
- The access resolver is synchronous and read-only. Scenery does not fetch application permissions, define roles, interpret access keys, cache policy decisions, or authorize business records.
- Backend operations remain independently authorized by application code. Hiding a route or tab is user-experience behavior, not a security boundary.

The completed behavior is observable in a fixture app:

1. A generated page declares `application_key = "microgrid"` and `access_key = "projects"`.
2. The generated route descriptor contains those exact opaque values.
3. One application-provided resolver controls side-navigation visibility and direct-route rendering.
4. A denied route never invokes its protected page component.
5. A generated workspace tab with a denied access key is absent; a direct URL selecting it resolves to the first allowed tab.
6. `createSceneryApp` exposes the complete route catalog.
7. Parameterized routes can be matched back to their descriptors.
8. A normal session reports the same effective and actor user.
9. An impersonation session reports the target as effective user and the administrator as actor.

This is a current-specification change. Do not add deprecated aliases, dual signatures, compatibility adapters, policy DSLs, permission persistence, or old runtime paths.

## Progress

- [x] (2026-08-10 10:15Z) Initial ExecPlan drafted against `scenery-sh/scenery` `main` commit `36351c31fea6e185b25ea9ee1de4d3a07c555b66`.
- [x] (2026-08-10 10:59Z) Recorded local commit `36351c31fea6e185b25ea9ee1de4d3a07c555b66`; preserved unrelated dirty files `cmd/scenery/dev_assistant_supervisor_test.go` and `cmd/scenery/dev_supervisor.go`; active plans were 0145 and 0101; task-scoped docs selected `ARCHITECTURE.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `docs/spec/typescript-client.md`, and the current generated-client schema. Pre-edit harness classes were `cli-json-contract`, `go-package`, and `release-sensitive-or-runtime`, recommending the full self-harness, `go test ./...`, and `go test ./cmd/scenery`.
- [x] (2026-08-10 10:59Z) Registered permanent plan 0151 in `docs/plans/active.md` and `docs/knowledge.json`.
- [x] (2026-08-10 11:27Z) Added exact opaque `application_key` and `access_key` metadata to every supported page macro and workspace tabs, including blank-value diagnostics, expansion propagation, and application-only tab inheritance.
- [x] (2026-08-10 11:27Z) Generated access metadata into the singular current TypeScript route and workspace contracts.
- [x] (2026-08-10 11:27Z) Added the synchronous resolver/context, denied and pending direct-route boundary, navigation and workspace-tab gating, merged route catalog export, and deterministic parameterized matcher.
- [x] (2026-08-10 11:27Z) Replaced the path-only callback signatures with descriptor-aware current signatures; regenerated Scenery fixtures and migrated Platform's generated app seam against the source revision.
- [x] (2026-08-10 11:27Z) Added impersonation-aware `AuditIdentity` and `CurrentAuditIdentity`, removed `AuditUserID`, and proved normal, impersonated, explicit-context, nil, and missing-auth behavior.
- [x] (2026-08-10 11:27Z) Updated current specification, TypeScript-client contract, local contract, agent guide, cookbook, skill, knowledge catalog, fixture declarations, provider pins, native contracts, and MCP golden output.
- [x] (2026-08-10 11:27Z) Regenerated both committed TypeScript clients plus the native contract fixture. Targeted tests, fixture checks, `go test ./...`, generated-client typechecks/conformance, and the 22-step full self-harness all passed with cached test policy.
- [x] (2026-08-10 11:27Z) Deterministic rendered-client proof under `/tmp/0151-fixture-browser` passed three tests: static/parameterized matching, zero protected-component invocations for a denied direct route, and denied/zero-access workspace behavior. Platform then passed 203 frontend tests, typecheck, lint, production build, and a source-revision `scenery check`.
- [x] (2026-08-10 11:31Z) Released `v0.3.6` from commit `b8a46807616febe39789a75f05fcf8e49f9ae48e`, verified public Go-proxy resolution to that exact tag, pinned Platform without a `replace`, regenerated both contract families from the release, and moved this plan to the completed index.

## Surprises & Discoveries

- Observation: generated route descriptors currently expose path, component, origin, search validation, parameters, navigation, and parent, but no application ownership, access key, route catalog export, or direct-route access decision.
  Evidence: `internal/generate/generate_typescript_routes.go`.

- Observation: the current generated shell accepts an `authGate`, `contentGroup`, and path-only `navigationFilter`. Navigation can be filtered, but protected route components are mounted without a route-specific access boundary.
  Evidence: `renderReactAppAdapter` in `internal/generate/generate_typescript_routes.go`.

- Observation: `workspace_page` tabs are navigable sub-destinations within one route. Route-only metadata is insufficient for access-controlled tabs such as a permissions editor inside a System workspace.
  Evidence: generated workspace pages pass a static tab list into `WorkspacePage`, while the active tab is selected from the URL query.

- Observation: Scenery already owns the complete impersonation identity in `auth.AuthData`: effective user, real actor, tenant, session, and impersonation IDs. The current `AuditUserID()` method returns the effective user despite its actor-like name.
  Evidence: `auth/standard_jwt.go`.

- Observation: Scenery's current policy-expression runtime can inspect generic auth, request, and input values, but it does not know an application's business principal, roles, scopes, organizations, or resource relations.
  Evidence: `runtime/contract_authorization.go`.

- Observation: Scenery explicitly has one rolling specification and rejects deprecated APIs and compatibility aliases.
  Evidence: root `AGENTS.md`.

- Observation: The pre-existing dirty supervisor files are unrelated to this plan and must remain unstaged and unmodified by 0151.
  Evidence: `git status --short` at commit `36351c31fea6e185b25ea9ee1de4d3a07c555b66` on 2026-08-10 10:59Z.

- Observation: Adding public schema fields intentionally changes the current specification and contract revisions, so provider-lock pins, native generated contracts, and MCP projection goldens all needed deterministic refreshes beyond the two TypeScript fixture commands.
  Evidence: the first full `go test ./...` reported only old durable-provider integrity and native MCP contract-revision values; after refreshing those owned artifacts, the complete suite passed.

- Observation: Platform already had a complete explicit application/feature route manifest. Its downstream migration therefore required replacing only the generated shell seam: one `SceneryAccessResolver`, descriptor-aware `contentGroup` and `navigationFilter`, the refreshed provider lock, and regenerated clients; its business authorization model remained app-owned.
  Evidence: `/Users/petrbrazdil/Repos/Micro/platform/apps/platform/src/router.tsx`, 203 passing frontend tests, and a successful Scenery source-revision app check.

Append findings here with commands, test names, fixture paths, or diagnostics. Never erase earlier evidence merely because implementation changed.

## Decision Log

- Decision: Add two opaque optional metadata fields named `application_key` and `access_key`.
  Rationale: `application_key` answers which product experience owns the destination; `access_key` names the application's coarse page capability. Scenery must preserve them exactly and attach no semantics.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Support the metadata on `table_page`, `content_page`, `split_page`, `workspace_page`, top-level page-presented `detail_page`, and workspace `tab` declarations.
  Rationale: all of these can be independently navigable or selectable in generated clients. Omitting tabs would leave a common access leak in workspace UIs.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: A workspace tab inherits the containing workspace's `application_key` when the tab does not declare one. `access_key` never inherits.
  Rationale: every tab belongs to the same product unless explicitly overridden, while tab capability is intentionally specific and must not accidentally reuse a broad workspace key.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Reject blank or whitespace-only metadata at compile time; trim no nonblank value and do not normalize case.
  Rationale: keys are application-owned, opaque, and case-sensitive. Silent normalization would create mismatched authorization identifiers.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Generate one shared `SceneryAccessMetadata` and `SceneryAccessTarget` model used by routes and workspace tabs.
  Rationale: one access resolver should govern every generated navigable destination without inventing separate route and tab permission systems.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: The application supplies one synchronous resolver through `createSceneryApp`.
  Rationale: the application's auth gate should load a typed access snapshot first. The resolver then performs a pure in-memory decision during navigation and rendering, avoiding permission fetches or framework caches.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Use exactly three resolver states: `allowed`, `denied`, and `pending`; a denied result may contain `reason` and `redirectTo`.
  Rationale: these are sufficient for an application snapshot that may still be loading, a hard denial, or an allowed destination. Additional policy semantics remain application-owned.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: The same resolver controls side-navigation inclusion, route component invocation, and workspace-tab inclusion.
  Rationale: separate visibility and route rules drift. A denied destination must not appear in navigation and must not invoke its protected component on direct navigation.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: A denied route with `redirectTo` uses a replace navigation. Without a redirect it renders the application's denied slot or a small Scenery fallback. A pending route renders the application's pending slot or `null`.
  Rationale: direct URLs need deterministic behavior without requiring every application to implement boilerplate.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: If a URL selects a denied workspace tab, replace the URL with the first allowed tab. If zero tabs are allowed, render the denied state for the workspace content.
  Rationale: leaving the denied tab selected would expose labels or produce an empty broken workspace.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Export the exact merged route catalog from `createSceneryApp`, including generated and authored descriptors.
  Rationale: app switchers, command menus, breadcrumbs, first-accessible-route logic, and access previews should use the same catalog as the router.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Add a deterministic matcher for authored and generated paths, including `$parameter` segments.
  Rationale: consumers need the current route descriptor without duplicating TanStack route-pattern logic or URL-prefix maps.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Replace path-only `navigationFilter` and `contentGroup` callbacks with descriptor-aware signatures in the current API; do not retain overloads.
  Rationale: Scenery carries one current specification and the repositories are being updated together. Descriptor-aware callbacks eliminate fragile path reclassification.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Add `AuditIdentity` with `EffectiveUserID`, `ActorUserID`, `TenantID`, `SessionID`, and `ImpersonationID`.
  Rationale: these are exactly the auth-session facts Scenery owns and every downstream audit implementation needs.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Remove `AuthData.AuditUserID()` and replace it with `AuthData.AuditIdentity()` plus `CurrentAuditIdentity(ctx)`.
  Rationale: the old method name is ambiguous and returns the effective subject. Scenery does not carry deprecated aliases.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: In a normal session `ActorUserID == EffectiveUserID`; in impersonation `EffectiveUserID == AuthData.UserID` and `ActorUserID == AuthData.ActorUserID`.
  Rationale: this makes downstream audit code branch-free and preserves the real administrator.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Do not add applications, roles, scopes, organizations, business-user IDs, or permissions to JWT claims or Scenery auth organizations.
  Rationale: authentication workspaces are not application products or business companies. Applications load their own typed business access snapshot.
  Date/Author: 2026-08-10 / OpenAI.

- Decision: Do not expand `auth.PermissionChecker`.
  Rationale: its opaque Boolean all-of seam is already the correct smallest interface. Resource and scope authorization belongs in application code.
  Date/Author: 2026-08-10 / OpenAI.

## Outcomes & Retrospective

Released in Scenery `v0.3.6` at commit `b8a46807616febe39789a75f05fcf8e49f9ae48e`. The current grammar accepts optional, opaque, case-sensitive `application_key` and `access_key` on all five generated page macros and workspace tabs. Blank authored values fail compilation. Expanded route-owning `scenery.page` resources preserve exact values; a tab inherits only its workspace's application key.

Generated React clients now expose `SceneryAccessMetadata`, `SceneryAccessTarget`, the `allowed`/`pending`/`denied` `SceneryAccessResult`, `SceneryAccessResolver`, descriptor-aware `contentGroup` and `navigationFilter`, `matchSceneryRoute`, and `createSceneryApp(...).routes`. One synchronous resolver controls navigation inclusion, direct-route component invocation, and workspace-tab inclusion. Redirects replace history, denied/pending page components are never invoked, denied tab URLs recover to the first allowed tab with replacement, and a zero-access workspace renders the configured denial. These remain presentation rules; backend authorization stays application-owned.

The house fixture declares workspace `application_key = "microgrid"` and `access_key = "projects"`; its `orders` tab declares `access_key = "orders"`, `summary` overrides both keys with `analytics`/`summary`, and `vendors` declares `access_key = "vendors"`. `/tmp/0151-route-output-after.txt` and `/tmp/0151-workspace-output-after.txt` record the generated output. `TestRenderReactRoutesCarriesAccessMetadataAndMatcher`, `TestRenderReactAppAdapterGatesRoutesBeforeComponentInvocation`, `TestRenderReactWorkspacePage`, and the three rendered-client tests in `/tmp/0151-route-access-runtime.txt` prove exact metadata, deterministic matching, denied direct-route non-invocation, tab filtering, replacement, and zero-access denial.

Auth now exposes `AuditIdentity` plus `CurrentAuditIdentity(ctx)` and has no `AuditUserID` alias. `TestAuditIdentityNormalSessionUsesEffectiveUserAsActor`, `TestAuditIdentityImpersonationPreservesEffectiveAndActorUsers`, and `TestCurrentAuditIdentityFailsClosedWithoutAuth` prove equal normal actor/effective identity, real-actor attribution while impersonating, exact tenant/session/impersonation preservation, explicit context support, nil method safety, and fail-closed missing auth.

Both required TypeScript fixture commands were rerun. Changed fixture outputs were the native/house TypeScript metadata and descriptors; spec-revision fallout additionally regenerated native `scenerycontract`/composition/adapter manifests, the assistant durable-provider lock, and the native MCP golden. Harness selection resolved to `cli-json-contract`, `compiler-or-generator`, `go-package`, `release-sensitive-or-runtime`, and `ui-catalog`. The exact recommended union was the full self-harness, both fixture generation commands, catalog typecheck, `go test ./...`, and focused `auth`, `cmd/scenery`, `internal/compiler`, `internal/generate`, and `internal/spec` tests. All passed; the full harness recorded 22 successful steps in `/tmp/0151-self-harness.txt`.

Platform ExecPlan 0074 is pinned to released `v0.3.6` without a local replacement. Its shell supplies one `SceneryAccessResolver` and the descriptor-aware callbacks while retaining its typed application/feature manifest and backend authorization. Contracts and clients were regenerated from the tag; `GOWORK=off go test ./...`, app typecheck, lint, 203 tests, production build, tagged `scenery check`, and public module fetch all passed.

## Context and Orientation

### Repository and baseline

Work from:

    /Users/petrbrazdil/Repos/scenery

The GitHub baseline used for this draft is:

    36351c31fea6e185b25ea9ee1de4d3a07c555b66
    Speed up assistant tests

Before editing, run:

    cd /Users/petrbrazdil/Repos/scenery
    git status --short
    git rev-parse HEAD
    git log -1 --oneline
    sed -n '1,260p' AGENTS.md
    sed -n '1,240p' PLANS.md
    sed -n '1,220p' docs/plans/active.md
    sed -n '1,220p' docs/tech-debt.md

Then read the closest instructions:

    internal/compiler/AGENTS.md
    internal/generate/AGENTS.md
    internal/spec/AGENTS.md
    docs/spec/AGENTS.md

Use documentation discovery rather than guessing:

    .scenery/harness/bin/scenery inspect docs --for-path internal/compiler/page_route.go -o json
    .scenery/harness/bin/scenery inspect docs --for-path internal/generate/generate_typescript_routes.go -o json
    .scenery/harness/bin/scenery inspect docs --for-path auth/standard_jwt.go -o json

If `0151` is already allocated in the local tree, allocate the next unused permanent four-digit ID and update every path and index reference. Never reuse an ID.

### Current route generation

`internal/compiler/page_route.go` validates and expands page-route metadata. `internal/spec/schemas.go` and related schema catalogs define accepted source fields. `internal/generate/generate_typescript_routes.go` currently generates:

- `SceneryNavigationDescriptor`
- `SceneryRouteDescriptor`
- `createGeneratedRoutes`
- `createSceneryApp`
- side-navigation sections

The generated app merges generated routes with application-authored descriptors, creates TanStack routes, and mounts the app shell.

The current generated slot signatures are path-oriented:

```ts
contentGroup?: (path: string) => string;
navigationFilter?: (routePath: string, currentPath: string) => boolean;
```

Replace these signatures; do not add alternate overloads.

### Current workspace generation

Inspect:

    internal/compiler/workspace_page.go
    internal/generate/*workspace*
    ui/components/*Workspace*
    internal/compiler/testdata/native
    internal/compiler/testdata/house

Find the exact generated tab descriptor type and the component that selects `activeTab`. Add access metadata at the smallest stable descriptor boundary. Do not put product-specific access UI in the catalog.

### Current auth identity

`auth.AuthData` is defined in `auth/standard_jwt.go`:

```go
type AuthData struct {
    UserID          AuthUserID
    TenantID        TenantID
    SessionID       string
    ActorUserID     AuthUserID
    ImpersonationID string
}
```

`UserID` is the effective user. During impersonation `ActorUserID` is the real administrator. `auth.WithContext` and `currentAuthDataFromContext` in `auth/auth.go` allow direct internal calls and tests to carry the same identity as runtime requests.

### Target public Go surface

The intended current API is:

```go
package auth

type AuditIdentity struct {
    EffectiveUserID AuthUserID
    ActorUserID     AuthUserID
    TenantID        TenantID
    SessionID       string
    ImpersonationID string
}

func (d *AuthData) AuditIdentity() AuditIdentity
func CurrentAuditIdentity(context.Context) (AuditIdentity, error)
```

Delete `AuthData.AuditUserID()`. Update all Scenery tests, examples, and first-party fixture consumers that reference it.

### Target generated TypeScript surface

Use names equivalent to the following unless existing generator conventions require a mechanically different spelling:

```ts
export type SceneryAccessMetadata = {
  readonly applicationKey?: string;
  readonly accessKey?: string;
};

export type SceneryAccessTarget =
  | {
      readonly kind: "route";
      readonly route: SceneryRouteDescriptor;
    }
  | {
      readonly kind: "workspace-tab";
      readonly route: SceneryRouteDescriptor;
      readonly name: string;
      readonly label: string;
      readonly applicationKey?: string;
      readonly accessKey?: string;
    };

export type SceneryAccessResult =
  | { readonly status: "allowed" }
  | { readonly status: "pending" }
  | {
      readonly status: "denied";
      readonly reason?: string;
      readonly redirectTo?: string;
    };

export type SceneryRouteDescriptor = {
  // Existing fields.
  readonly applicationKey?: string;
  readonly accessKey?: string;
};

export type SceneryAppSlots = {
  // Existing slots.
  readonly resolveAccess?: (
    target: SceneryAccessTarget,
    currentPath: string,
  ) => SceneryAccessResult;
  readonly accessPending?: ReactNode;
  readonly accessDenied?: (props: {
    readonly target: SceneryAccessTarget;
    readonly result: Extract<SceneryAccessResult, { readonly status: "denied" }>;
  }) => ReactNode;
  readonly navigationFilter?: (
    route: SceneryRouteDescriptor,
    currentRoute: SceneryRouteDescriptor | undefined,
    currentPath: string,
  ) => boolean;
  readonly contentGroup?: (
    currentRoute: SceneryRouteDescriptor | undefined,
    currentPath: string,
  ) => string;
};
```

`createSceneryApp` must return:

```ts
{
  router,
  App,
  routes,
}
```

Export a matcher with deterministic tests:

```ts
matchSceneryRoute(
  routes: readonly SceneryRouteDescriptor[],
  path: string,
): SceneryRouteDescriptor | undefined
```

Do not expose internal compiler resource objects or mutable route arrays.

## Milestones

### Milestone 1 — Preflight, plan registration, and characterization tests

Register the plan. Capture current generated route and workspace-tab output before changing it. Add failing characterization tests that prove the missing metadata and direct-route behavior.

Completion evidence:

- plan is indexed;
- baseline fixture generation is clean;
- new tests fail for the intended missing behavior, not for unrelated reasons;
- `/tmp/0151-before-*` contains the baseline snippets and test transcript.

### Milestone 2 — Specification and compiler metadata

Add `application_key` and `access_key` to all supported page macros and workspace tabs. Preserve exact values through expansion and validation.

Completion evidence:

- compiler tests accept valid metadata;
- blank values produce stable diagnostics;
- workspace tabs inherit only `application_key`;
- compiled expanded JSON contains the exact fields.

### Milestone 3 — Generated access contract and route catalog

Generate metadata, the access model, descriptor-aware callbacks, route catalog export, and parameterized matcher.

Completion evidence:

- generated fixtures compile;
- generated and authored routes appear in one immutable catalog;
- matcher tests cover static, nested, parameterized, root, and nonmatching paths;
- no path-only callback signature remains.

### Milestone 4 — Route, navigation, and workspace-tab gating

Add one resolver context and apply it consistently to navigation, route mounting, and workspace tabs.

Completion evidence:

- denied navigation entry absent;
- denied direct route does not invoke protected component;
- pending and denied fallbacks render deterministically;
- redirect uses replace semantics;
- denied selected workspace tab resolves to first allowed tab;
- zero allowed tabs render a denial rather than selecting a hidden tab.

### Milestone 5 — Explicit audit identity

Add the new Go API, remove the ambiguous method, and update tests/docs.

Completion evidence:

- normal session: actor equals effective;
- impersonation: actor and effective differ correctly;
- explicit `auth.WithContext` works;
- missing auth fails with `Unauthenticated`;
- repository search finds no `AuditUserID`.

### Milestone 6 — Documentation, fixtures, validation, and release

Update all current contract prose, regenerate committed clients, run the validation union and real fixture proof, then release.

Completion evidence:

- no stale generated fixtures;
- docs and schemas match implementation;
- full Go suite and self-harness pass;
- released version is consumable from Platform without a local replacement.

## Plan of Work

### 1. Register the plan

Create:

    docs/plans/0151-route-access-audit-identity.md

Update:

    docs/plans/active.md
    docs/knowledge.json

Use the actual next ID if `0151` is occupied. Add an active-index entry with owner `scenery generated clients / auth`, creation date, and a one-paragraph focus statement.

Refresh the harness context immediately:

    .scenery/harness/bin/scenery harness self --quick --summary --write

Record validation classes and recommended commands in `Progress`.

### 2. Add source-schema fields

Locate the source schemas for each page resource and workspace tab. Add:

```text
application_key: optional string
access_key: optional string
```

Validation requirements:

- omitted is valid;
- nonempty exact string is valid;
- `""`, spaces-only, tabs-only, or newlines-only are invalid;
- do not lowercase, trim, split, or validate a namespace;
- metadata does not change route uniqueness or navigation requirements.

Add stable diagnostics following the current catalog conventions. Do not invent an unregistered ad hoc message.

### 3. Propagate metadata through compiler expansion

For every supported page macro:

- source view contains the authored fields;
- expanded view contains fields on the route-owning page resource;
- generated-client model receives them;
- workspace tab expansion carries tab metadata;
- tab `application_key` falls back to the containing workspace's exact value;
- tab `access_key` remains empty when omitted.

Add compiler tests beside existing page-route and workspace tests. Use table-driven cases for every page type.

### 4. Generate route metadata and catalog

Modify `renderReactRoutes` and the app adapter generator.

Requirements:

- generated descriptors include `applicationKey` only when declared;
- generated descriptors include `accessKey` only when declared;
- authored descriptors use the same public type;
- generated and authored routes are merged once;
- return the merged `routes` array as a readonly value;
- do not create a second registry;
- preserve route order and generated provenance;
- do not add runtime mutation of route descriptors.

Implement and test `matchSceneryRoute`.

Matching requirements:

- normalize trailing slash consistently with current router behavior;
- match `/` exactly;
- match static segments exactly;
- `$name` matches one nonempty segment;
- a parameter does not consume `/`;
- more-specific static routes win over parameterized routes when both could match;
- return `undefined` for no match;
- query and hash are ignored if a full URL-like path is passed;
- basepath removal remains the application's responsibility through its existing path normalizer.

### 5. Replace path-only callbacks

Change current public signatures rather than retaining overloads.

Update:

- generator source;
- generated fixture output;
- first-party fixture applications;
- tests;
- `docs/local-contract.md`;
- `docs/agent-guide.md`;
- `docs/app-development-cookbook.md`;
- `SKILL.md` if target-app agents need the new signature.

Repository search at completion must find no old callback signature.

### 6. Add the shared access resolver context

The generated app adapter should create an internal React context containing:

- current merged route catalog;
- current route descriptor;
- current normalized path;
- resolver;
- pending slot;
- denied slot.

Keep this context private unless workspace generators need a public generated helper. If a helper is exported, expose only stable application-facing types and functions.

The resolver must not be awaited. It must not call `fetch`, inspect auth tokens, or mutate state.

### 7. Gate navigation and direct routes

Navigation logic:

1. Resolve the current route descriptor.
2. For each route with navigation metadata, invoke `resolveAccess({kind:"route", route}, currentPath)` when configured.
3. Exclude denied routes.
4. Exclude pending routes to avoid flashing unauthorized navigation.
5. Apply the application-provided descriptor-aware `navigationFilter`.
6. Preserve existing grouping, ordering, icons, active paths, and provenance.

Route logic:

1. Create the route descriptor closure.
2. Before creating the protected page component, evaluate access.
3. `allowed`: invoke the page component.
4. `pending`: render `accessPending` or `null`.
5. `denied` with `redirectTo`: render TanStack `Navigate` with replace.
6. `denied` without redirect: render `accessDenied` or a minimal semantic fallback.
7. The protected component function must not be invoked in pending or denied states.

Add a test component that increments a counter when invoked. Assert the counter remains zero for denied direct navigation.

### 8. Gate workspace tabs

Extend the generated workspace tab descriptor with access metadata.

At render:

- resolve each tab using the same resolver;
- omit denied and pending tabs from the visible tab list;
- preserve existing `available` behavior for operational unavailability; access denial is not rendered as “temporarily unavailable”;
- if the URL names a denied or unknown tab, replace it with the first allowed tab;
- if no tabs are allowed, render the denied slot against a workspace-tab target or a minimal fallback;
- do not leak denied tab labels, descriptions, counts, destinations, or unavailable reasons.

Add tests for inherited application key, tab-specific access key, active-tab replacement, history behavior, and zero allowed tabs.

### 9. Add explicit audit identity

In `auth/standard_jwt.go` or a focused new file:

```go
type AuditIdentity struct {
    EffectiveUserID AuthUserID
    ActorUserID     AuthUserID
    TenantID        TenantID
    SessionID       string
    ImpersonationID string
}
```

Implement:

```go
func (d *AuthData) AuditIdentity() AuditIdentity
func CurrentAuditIdentity(ctx context.Context) (AuditIdentity, error)
```

Rules:

- nil receiver returns zero value only for the method; top-level current function returns `Unauthenticated`;
- effective user is `UserID`;
- actor is `ActorUserID` when impersonating, otherwise `UserID`;
- preserve exact tenant, session, and impersonation IDs;
- use `currentAuthDataFromContext(ctx)` so runtime and explicit contexts behave identically;
- no database query;
- no session refresh;
- no provider-specific logic.

Delete `AuditUserID()`. Update all references in the Scenery repository and fixture apps. Do not add a deprecated forwarding method.

### 10. Update current documentation

Update exact current contracts, not historical completed plans:

    docs/local-contract.md
    docs/agent-guide.md
    docs/app-development-cookbook.md
    SKILL.md
    docs/spec/* where page macro grammar is specified
    docs/knowledge.json

Document:

- grammar fields;
- generated TypeScript types;
- synchronous resolver lifecycle;
- direct-route behavior;
- workspace-tab behavior;
- route catalog and matcher;
- frontend gating is not backend authorization;
- audit actor/effective semantics;
- application entitlements do not belong in Scenery auth organizations or JWTs.

### 11. Regenerate committed fixture clients

Run exactly:

    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json

Review the diff. Generated changes must be limited to the expected current contract. Do not hand-edit fixture output.

### 12. Release and downstream handoff

After all validation passes:

- record the exact commit;
- follow the current repository release procedure;
- create a semantic version containing this surface;
- verify the module can be fetched without a local `replace`;
- update `Outcomes & Retrospective`;
- move the plan from active to completed;
- update `docs/knowledge.json`;
- provide the released version to Platform ExecPlan 0074.

Do not mark this plan complete while Platform must depend on an untagged sibling worktree.

## Concrete Steps

From the Scenery repository:

```sh
cd /Users/petrbrazdil/Repos/scenery
git status --short
git rev-parse HEAD
git log -1 --oneline
```

Fresh-worktree preflight:

```sh
.scenery/harness/bin/scenery doctor -o json
.scenery/harness/bin/scenery harness self --quick --summary --write
```

Inspect selected validation:

```sh
python3 - <<'PY'
import json
from pathlib import Path

data = json.loads(Path(".scenery/harness/agent-context.json").read_text())
changed = data.get("changed_area", {})
print("classes:")
for item in changed.get("validation_classes", []):
    print(" -", item)
print("commands:")
for item in changed.get("recommended_commands", []):
    print(item)
PY
```

Targeted iteration commands:

```sh
go test ./internal/spec
go test ./internal/compiler
go test ./internal/generate
go test ./auth
```

Required fixture regeneration:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Validate fixture apps:

```sh
go run ./cmd/scenery check --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery check --app-root internal/compiler/testdata/house -o json
```

Search for removed current API:

```sh
rg -n 'AuditUserID|navigationFilter\?: \(routePath: string|contentGroup\?: \(path: string' .
```

Expected result: no matches except this active ExecPlan before completion. Update this plan wording or exclude its path for the final mechanical assertion.

Run the repository suite:

```sh
go test ./...
git diff --check
```

Refresh context and run the exact recommended union from the generated harness file. Because this touches compiler, generator, auth, public generated clients, and release-sensitive app behavior, the expected minimum includes:

```sh
.scenery/harness/bin/scenery harness self --summary --write
```

Record every actual command and result in `Outcomes & Retrospective`.

For the real app-facing proof, use a fixture or disposable app root containing:

```hcl
content_page "allowed" {
  path            = "/allowed"
  title           = "Allowed"
  application_key = "alpha"
  access_key      = "allowed"
}

content_page "denied" {
  path            = "/denied"
  title           = "Denied"
  application_key = "alpha"
  access_key      = "denied"
}
```

Prove:

- `/allowed` renders;
- `/denied` renders the denied result or redirects;
- denied page invocation counter remains zero;
- only Allowed appears in navigation;
- route catalog contains both exact metadata pairs.

Store screenshots or deterministic test transcripts under `/tmp/0151-*`; do not add scratch artifacts to the repository.

## Validation and Acceptance

The plan is accepted only when all of the following are true.

### Specification

- Every supported page macro accepts both metadata fields.
- Workspace tabs accept access metadata and inherit only application key.
- Blank values are rejected with stable diagnostics.
- Exact case and spelling are preserved.
- Existing declarations that omit metadata still compile and behave as unrestricted presentation destinations unless the application resolver denies them for another reason.

### Generated contract

- Generated route descriptors carry exact metadata.
- Authored routes use the same descriptor type.
- `createSceneryApp` returns one merged readonly route catalog.
- Parameterized route matching is deterministic.
- Old path-only callback signatures are removed.

### Presentation gating

- One resolver controls navigation, routes, and workspace tabs.
- Denied and pending routes do not invoke protected components.
- Direct URLs are handled.
- Workspace tab labels and counts do not leak when denied.
- Redirects replace history.
- No resolver means existing unrestricted presentation behavior.

### Authorization boundary

- Documentation explicitly says the route resolver is not backend authorization.
- Scenery does not define or persist roles, profiles, scopes, organizations, exceptions, or business permissions.
- Scenery does not add entitlement claims to auth tokens.
- No policy fetch or process-global access cache exists.

### Audit identity

- Normal-session actor and effective IDs are equal.
- Impersonation actor and effective IDs are correct.
- Tenant, session, and impersonation IDs are preserved.
- `CurrentAuditIdentity` works with runtime and `auth.WithContext`.
- Missing auth fails closed.
- `AuditUserID` no longer exists.

### Repository proof

- Targeted package tests pass.
- Both committed fixture clients are regenerated.
- Both fixture app checks pass.
- `go test ./...` passes.
- `git diff --check` passes.
- Full self-harness passes.
- The released module is consumable from Platform without a local replacement.

## Idempotence and Recovery

This change has no database migration.

Generation is deterministic and safe to rerun. If fixture generation fails, fix the owning compiler or generator and rerun both required commands; never patch generated output manually.

The route API is a singular current contract. If an implementation attempt leaves generated fixtures and source out of sync, restore the generated fixture directories from Git and regenerate from corrected source.

The audit API is additive except for removal of the ambiguous method. Update all consumers in the same coordinated release. If Platform cannot update, do not release a half-compatible local build; finish both repositories or revert the Scenery source before tagging.

If browser or fixture access gating behaves incorrectly, disable the application resolver in the fixture while debugging; this returns presentation behavior to unrestricted without changing backend authorization.

## Artifacts and Notes

Use:

    /tmp/0151-route-output-before.txt
    /tmp/0151-workspace-output-before.txt
    /tmp/0151-targeted-tests.txt
    /tmp/0151-fixture-generate-native.json
    /tmp/0151-fixture-generate-house.json
    /tmp/0151-fixture-browser/
    /tmp/0151-self-harness.txt

Do not store auth tokens, cookies, secrets, or private application data.

At each stopping point update `Progress` with:

- exact commit;
- files changed;
- tests passing;
- failing test or diagnostic;
- next unfinished milestone.

## Interfaces and Dependencies

### New source grammar

```hcl
application_key = string
access_key      = string
```

Both are optional and opaque.

### New generated TypeScript contract

```ts
SceneryAccessMetadata
SceneryAccessTarget
SceneryAccessResult
SceneryRouteDescriptor.applicationKey
SceneryRouteDescriptor.accessKey
SceneryAppSlots.resolveAccess
SceneryAppSlots.accessPending
SceneryAppSlots.accessDenied
matchSceneryRoute(...)
createSceneryApp(...).routes
```

### Changed generated TypeScript contract

```ts
SceneryAppSlots.navigationFilter
SceneryAppSlots.contentGroup
```

Both become descriptor-aware current signatures. No old overload remains.

### New Go auth contract

```go
type AuditIdentity struct {
    EffectiveUserID AuthUserID
    ActorUserID     AuthUserID
    TenantID        TenantID
    SessionID       string
    ImpersonationID string
}

func (*AuthData) AuditIdentity() AuditIdentity
func CurrentAuditIdentity(context.Context) (AuditIdentity, error)
```

### Removed Go auth contract

```go
func (*AuthData) AuditUserID() string
```

### Downstream dependency

MicroGRID Platform ExecPlan `0074-app-entitlements-access-profiles.md` depends on the released Scenery version produced by this plan. Platform remains responsible for:

- business users;
- application entitlements;
- access profiles;
- feature levels and scopes;
- individual exceptions;
- organizations and data boundaries;
- backend authorization;
- application-session snapshots;
- business audit-user resolution.
