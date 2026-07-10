# Scenery Go Implementation ABI v1

Normative companion specification

Profile: `scenery.go-implementation/v1`  
Document revision: `0.4-draft`  
Status: draft for Scenery language edition 2027  
Umbrella specification: [SCENERY_LANGUAGE_SPEC.md](SCENERY_LANGUAGE_SPEC.md)

## 1. Purpose

This document defines how an edition-2027 Scenery contract is implemented, generated, type-checked, built, registered, and verified in Go.

It is normative for:

- Go import and package topology;
- reproducible toolchain and build-target metadata;
- generated contract types;
- service constructors and dependencies;
- lifecycle hooks;
- unary operation handlers and closed outcomes;
- staged generation and type checking;
- application adapters and registration;
- artifact descriptors and revision checks.

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

## 2. Profile boundary

`scenery.go-implementation/v1` is a unary implementation ABI. It does not define:

- Scenery declaration syntax through Go builders;
- runtime reflection or package scanning;
- arbitrary HTTP request/response objects;
- custom middleware handler signatures;
- streaming handlers;
- legacy handler signatures.

Legacy handler compatibility is defined by `scenery.legacy-bridge/v1`. Transport-coupled Go HTTP requires a future `scenery.go-http-coupled/v1` profile.

A tool claiming this profile MUST implement the exact topology, mappings, signatures, staged verification, descriptor, and registration rules below.

## 3. Source declarations

### 3.1 One Go contract owner per Scenery package

A Scenery package or root application containing Go services owns exactly one invariant `go_contract`:

~~~hcl
package "house" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"

  go_contract {
    import_path = "github.com/example/clean-tech/house"
  }
}
~~~

`go_contract.import_path` is a quoted literal and cannot depend on a module input. Every Go service in the source unit is implemented by that Go package. Ordinary implementation code may use internal subpackages.

Two resolved Scenery packages MUST NOT own the same implementation import path or derived `/scenerycontract` import path. A Scenery package needing independently versioned Go boundary packages must be split into multiple Scenery packages.

### 3.2 Service declaration

~~~hcl
service "house" {
  runtime = "go"

  implementation {
    constructor = "NewService"
  }

  dependency "database" {
    instance = var.database
  }

  dependency "storage" {
    instance = var.storage
  }

  config {
    roof_model_path     = var.roof_model_path
    process_concurrency = var.process_concurrency
    provider_token      = var.provider_token
  }

  client "audit" {
    binding = var.audit_binding
  }
}
~~~

Constructor and operation method names are explicit string symbols. They cannot be inferred from filenames, receiver scanning, comments, package initialization, or naming conventions.

Dependencies, configuration, and internal clients are separate constructor-input categories. A service declaration MUST list each value it consumes; the implementation cannot recover omitted values through ambient process state or a service registry.

Module inputs cannot change generated Go type shape, the implementation import path, constructor signature, lifecycle signature, handler signature, or outcome surface.

## 4. Workspace and import mapping

A local Go package resolves through an explicit `go_module` resource:

~~~hcl
go_module "application" {
  root        = "."
  import_path = "github.com/example/clean-tech"
}
~~~

The mapping is bidirectional:

- forward: import path to workspace directory;
- reverse: managed generated directory to import path.

The longest slash-segment-prefix mapping wins. Equal specificity, duplicate roots/import paths, failed round trips, symlink escape, or an implementation outside every declared `implementation_root` is an error.

The implementation package is `go_contract.import_path`. The generated contract package is that path plus `/scenerycontract`. Application adapters live beneath the configured managed-generated adapter root and receive a reverse-mapped application import path.

Registry implementations use immutable import-path and contract metadata from the lockfile. A registry implementation MUST NOT be silently replaced with local source or ambient module-cache content.

## 5. Reproducible Go context

### 5.1 Toolchain

~~~hcl
go_toolchain "application" {
  version     = "1.26.3"
  experiments = []
}
~~~

The toolchain schema includes:

- exact Go toolchain version or immutable toolchain digest;
- sorted `GOEXPERIMENT` entries;
- compiler distribution identity and digest.

Values are implementation-domain fields. They cannot be discovered from process environment.

Flags and native-tool declarations MUST be portable or content-addressed. Absolute host paths, shell expansion, response files outside declared inputs, and implicit `pkg-config`/system-library discovery are forbidden unless a future toolchain provider schema models them explicitly.

### 5.2 Contract, host-development, test, and artifact targets

~~~hcl
go_target "contract_check" {
  role      = "contract"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./house/scenerycontract"]
  cgo       = "disabled"
}

go_target "development" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
  cgo       = "host"
}

go_target "test" {
  role       = "test"
  extends    = go_target.development
  packages   = ["./..."]
  build_tags = ["scenery_test"]
}

go_target "production_linux_amd64" {
  role      = "artifact"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]

  goos                  = "linux"
  goarch                = "amd64"
  cgo                   = "enabled"
  architecture_features = ["amd64.v3"]
  build_tags            = ["roofmapnet_native"]

  native_inputs = [
    relative_path("house/native"),
  ]

}
~~~

A target declares:

- toolchain and Go module;
- package patterns;
- generated composition-root package;
- native inputs and tools;
- target-specific environment values through typed, allowlisted fields;
- role: `contract`, `development`, `test`, or `artifact`;
- host resolution or fixed target platform;
- explicit inheritance where used.

A `contract` target loads only generated `scenerycontract` packages from the staged overlay and their runtime scalar dependencies. It verifies generated type/API shape without loading application implementation packages and gives clean-clone/editor/bootstrap checks a small host-executable target. It does not prove a handler, constructor, lifecycle, native library, or artifact-target implementation.

Package patterns are interpreted by the declared Go toolchain in the explicit module mapping, never by an ambient current directory.

### 5.3 Command selection and inheritance

`serve` selects a development target executable on the current host. `test` selects a host-executable test target. `build` selects one or more artifact targets. `check` may validate a contract target, verify one implementation target, or verify every target marked `verify_by_default`; contract package generation is target-independent.

`platform = "host"` resolves through the toolchain adapter and records exact resolved GOOS, GOARCH, architecture features, CGO, and native tool identities. It cannot be used as a reproducible production deployment target. Fixed artifact targets are reproducible but need not be locally executable.

An extending target inherits every field, then applies explicit schema-authorized additions or replacements shown by the effective view. Nothing changes implicitly because the command is named `test` or `serve`.

Ambient `GOFLAGS`, `GOEXPERIMENT`, `CGO_ENABLED`, `GOOS`, `GOARCH`, `GOWORK`, module-cache replacements, shell variables, and compiler defaults MUST NOT silently alter the context. An undeclared required value is an error.

Each resolved target context and build-supplied content-addressed input manifest produces a separate `implementation_revision[target]`.

## 6. Package topology

For implementation package `github.com/example/clean-tech/house`, the topology is:

~~~text
github.com/example/clean-tech/house/scenerycontract
github.com/example/clean-tech/house
<application-module>/internal/scenerygen/<instance-and-service>adapter
<application-module>/internal/scenerygen/composition
~~~

Dependency direction is:

~~~text
scenerycontract <- implementation <- application adapter <- composition root
       ^________________ application adapter __________________|
~~~

More precisely:

- `scenerycontract` imports neither implementation nor application adapter;
- implementation imports its `scenerycontract` package;
- application adapter imports implementation and `scenerycontract`;
- composition root imports adapters;
- implementation MUST NOT import an application adapter or composition root.

An import cycle is an implementation diagnostic.

The application adapter path derives from the full module-instance and service addresses. Multiple instances therefore do not collide and may share one package-owned contract.

A published registry module containing Go implementation source ships its matching `scenerycontract` package or a prebuilt artifact declaring the same package ABI revision. Consumers never regenerate a remote module contract beneath their own internal tree.

## 7. Package contract ABI revision

### 7.1 Purpose

`package_contract_abi_revision` fingerprints generated Go ABI shape independently of application contract, package release version, and exact artifact bytes.

The projection contains:

- canonical `scenerycontract` import path, because Go import path is type identity;
- `scenery.go-implementation` ABI major;
- deterministic generated identifiers;
- service-qualified dependency, configuration, internal-client, and constructor-input structures plus referenced capability ABI ranges;
- constructor and lifecycle signatures;
- operation input and closed outcome signatures;
- generated internal-client and explicitly exported contract API signatures;
- the transitive generated-type closure reachable from those signatures.

It excludes:

- Scenery package semver and registry identity;
- module-instance addresses;
- bindings, routes, policies, and executions;
- deployment values;
- implementation source or artifact bytes;
- consumer input values that do not alter ABI shape;
- unreachable private records, enums, and unions;
- source provenance;
- application `contract_revision`.

Package identity/version are artifact provenance. Exact published bytes are the artifact digest. Neither belongs in the ABI-shape hash.

### 7.2 Hashing

The projection uses the umbrella specification's canonical JSON encoding and domain prefix:

~~~text
scenery.package-contract-abi-revision.v1\0
~~~

The result is `sha256:` plus lower-case hexadecimal SHA-256.

Local generation computes the revision before module-instance substitution. Registry publication records it in the signed package descriptor and lockfile. Any mismatch among lock, generated package, implementation artifact, or application adapter is fatal.

An input that would change the projection is illegal under this profile.

## 8. Deterministic generation

Generated declarations use canonical semantic ordering:

- sibling resources by address;
- record fields by semantic field name;
- enum values by semantic value name;
- union variants by semantic variant name;
- service dependencies by dependency name;
- operation results and errors by variant name;
- named response/client methods by semantic name.

Pipeline steps, tuple elements, and other semantically ordered sequences retain semantic order.

Wire names and source positions never choose declaration order. Generator output for the same graph, ABI version, and generator version MUST be byte-identical.

Go identifiers use deterministic UpperCamel conversion. Operation-scoped wrappers are prefixed by operation name; enum constants by enum name; union wrappers by union name. A collision is an error. Generators MUST NOT append unstable numeric suffixes.

## 9. Scenery-to-Go type mapping

| Scenery | Go |
|---|---|
| `bool` | `bool` |
| `int` | `scenery.Int` |
| `int32` | `int32` |
| `int64` | `int64` |
| `uint32` | `uint32` |
| `uint64` | `uint64` |
| `decimal` | `scenery.Decimal` |
| `float32` | `float32` |
| `float64` | `float64` |
| `string` | `string` |
| `bytes` | `[]byte` |
| `uuid` | `scenery.UUID` |
| `date` | `scenery.Date` |
| `datetime` | `scenery.DateTime` |
| `duration` | `scenery.Duration` |
| `size` | `scenery.Size` |
| `url` | `scenery.URL` |
| `relative_path` | `scenery.RelativePath` |
| `json` | `scenery.JSON` |
| `list(T)` | `[]T` |
| `set(T)` | `scenery.Set[T]` |
| `map(T)` | `map[string]T` |
| `optional(T)` | `scenery.Optional[T]` |
| `nullable(T)` | `scenery.Nullable[T]` |

`optional(nullable(T))` becomes `scenery.Optional[scenery.Nullable[T]]`. Pointer heuristics MUST NOT collapse absence and null.

Records become generated structs. Enums become named types and constants; open enums preserve unknown values. Tagged unions become generated closed interfaces with one wrapper per known variant. An open union additionally generates `<UnionName>Unknown` with `Tag string` and `Payload scenery.JSON`, preserving both on re-encoding. Tuples become generated positional structs.

A record with `unknown_fields = "preserve"` receives a dedicated field:

~~~go
UnknownFields map[string]scenery.JSON
~~~

Its generated wire metadata marks this field as non-declared storage, not an ordinary contract field. Encoding rejects collisions with effective declared wire names. Runtime adapters defensively copy mutable bytes, slices, maps, sets, JSON, and unknown-field maps at trust boundaries.

## 10. Service constructor input

Each service receives one extensible, generated constructor-input value with separate typed categories:

~~~go
type HouseDependencies struct {
    Database datasource.SQL
    Storage  object.Store
}

type HouseConfig struct {
    RoofModelPath     scenery.RelativePath
    ProcessConcurrency uint32
    ProviderToken     scenery.SecretRef
}

type HouseClients struct {
    Audit AuditInternalClient
}

type HouseConstructorInput struct {
    Dependencies HouseDependencies
    Config       HouseConfig
    Clients      HouseClients
}

func NewService(
    context.Context,
    housecontract.HouseConstructorInput,
) (*Service, error)
~~~

The constructor is exported, non-generic, and non-variadic. It accepts exactly `context.Context` and the generated service-qualified constructor input, and returns exactly a pointer to one exported named service type plus `error`.

### 10.1 Dependencies

Dependency fields expose stable provider capability interfaces, never concrete providers. Missing or incompatible capabilities fail before construction.

### 10.2 Configuration

Config fields come from the service's explicit config attributes after module/deployment resolution. They retain optional/null semantics and sensitive taint. Secrets arrive only as runtime-resolvable `scenery.SecretRef` or a narrower declared secret capability, never plaintext in generated artifacts, manifests, diagnostics, or logs.

Implementations MUST NOT recover configuration from environment variables, package globals, `context.Context`, current working directory, or ambient files.

### 10.3 Internal clients

Client fields are generated from explicit service client blocks referencing internal bindings. Each interface preserves typed input/outcome, inherited principal rules, cancellation, deadline, and invocation failure semantics. The application adapter supplies an instance-bound implementation.

There is no global registry lookup. This supports native-to-native, native-to-legacy, and legacy-to-native calls when the bridge provides a fully typed compatibility binding.

### 10.4 Construction behavior

A non-nil constructor error aborts startup. A nil service with nil error is an implementation-contract violation.

Multiple services may share one implementation package; each receives distinct generated input types. Multiple module instances share package-owned types while receiving instance-specific dependencies, configuration, and clients through application adapters.

## 11. Lifecycle

Lifecycle is explicit in source:

~~~hcl
service "house" {
  lifecycle {
    start = "Start"
    stop  = "Stop"
  }
}
~~~

Signatures are:

~~~go
func (s *Service) Start(context.Context) error
func (s *Service) Stop(context.Context) error
~~~

Hooks are exported, non-generic, and non-variadic.

- Start runs once after dependencies and construction succeed and before handlers.
- Services start in dependency order.
- Stop runs at most once in reverse successful-start order.
- Stop receives a bounded shutdown context.
- Every started service receives a stop attempt even if another stop fails.
- Lifecycle errors are runtime failures, never operation outcomes.

Only one active frontend may own a service lifecycle in mixed mode.

## 12. Unary operation handlers

### 12.1 Signature

~~~go
func (s *Service) ProcessScene(
    ctx context.Context,
    input housecontract.ProcessSceneInput,
) (housecontract.ProcessSceneOutcome, error)
~~~

Handlers are exported, non-generic, and non-variadic. Cancellation and deadlines flow through `context.Context`. Durable execution may invoke a handler more than once.

Handlers do not receive `http.Request`, `ResponseWriter`, Scenery request wrappers, or transport metadata under this profile.

### 12.2 Closed outcomes

~~~go
type ProcessSceneOutcome interface {
    isProcessSceneOutcome()
}

type ProcessSceneProcessed struct {
    Value ProcessSceneResult
}
func (ProcessSceneProcessed) isProcessSceneOutcome() {}

type ProcessSceneInvalidInput struct {
    Problem Problem
}
func (ProcessSceneInvalidInput) isProcessSceneOutcome() {}

type ProcessSceneSceneNotFound struct {
    Problem Problem
}
func (ProcessSceneSceneNotFound) isProcessSceneOutcome() {}
~~~

Only generated wrappers implement the unexported marker. Every declared result and business-error variant has exactly one wrapper.

### 12.3 Return rules

- `error == nil` requires exactly one non-nil valid outcome.
- `error != nil` requires an absent outcome.
- Declared business errors are outcome wrappers, never Go errors.
- Go error means infrastructure or implementation failure only.
- Panic, invalid wrapper, outcome plus error, or nil outcome plus nil error becomes `system.internal` and a contract-violation record.

## 13. Internal generated clients

Package/application internal bindings generate typed clients. Call and wait clients accept:

- `context.Context`;
- runtime-created `scenery.Invocation`;
- generated operation input.

They return the same closed operation outcome plus Go error for invocation infrastructure failure. Enqueue returns a typed execution receipt. A caller cannot construct or forge an inherited principal through the client.

Services receive these clients only through their generated `<ServiceName>Clients` constructor field. The adapter binds the exact module instance and binding address; no process-global client registry exists.

## 14. Staged generation and verification

### 14.1 Bootstrap problem

Implementations import generated `scenerycontract`. Therefore a new or changed contract cannot be verified against stale or missing on-disk generated packages.

### 14.2 Required stages

Every check/generate/build performs:

1. Compile and validate the language-level contract graph.
2. Generate package-owned `scenerycontract` packages into an in-memory or temporary filesystem overlay.
3. Resolve the explicitly selected or verify-by-default targets without ambient overrides.
4. For a contract target, type-check only generated contract packages and runtime scalar dependencies; for every other selected target, load and type-check implementation packages against the overlay.
5. For non-contract targets, validate constructor input, dependencies, configuration, internal clients, lifecycle, handler, and outcome signatures.
6. For non-contract targets, generate application adapters and composition root into the same overlay.
7. Type-check the applicable contract-only set or the full implementation, contracts, adapters, and composition root together.
8. Verify package ABI revisions, runtime/provider capability ABIs, and covered addresses.
9. For `check`, discard the overlay.
10. For `generate` or `build`, atomically materialize only the verified artifact set.

A stale on-disk generated package MUST NOT satisfy a stage. No generated byte is written before all applicable validation succeeds.

### 14.3 Result states

Contract compilation and implementation verification are separate results:

- `contract_valid`: canonical manifest and clients may be produced;
- `contract_target_valid[target]`: the staged generated package is valid, without claiming implementation conformance;
- `implementation_valid[target]`: that target may be built;
- `implementation_invalid[target]`: command fails for that target, but a valid contract manifest remains available;
- `implementation_unavailable[target]`: no build input/toolchain was requested or available for that target.

Contract diagnostics and implementation diagnostics use distinct stable categories. `check` returns failure for either invalid state but never mutates source or generated roots.

## 15. Adapter registration

Each application adapter exports:

~~~go
func Register(registry scenery.Registry) error
~~~

The composition root calls `Register` explicitly. Registration MUST NOT use package `init`, reflection discovery, source/package scanning, or method scanning.

The application adapter embeds:

- application `contract_revision`;
- covered application resource addresses;
- package identity and `package_contract_abi_revision` for each covered service/operation;
- required runtime and provider capability ABI ranges.

The module-owned contract package embeds only package identity and package ABI revision; it cannot know a future consuming application revision.

Before traffic or workers start, registration verifies all fields plus constructor, lifecycle, and handler bindings.

## 16. Generated artifact descriptors

### 16.1 Package contract descriptor

`scenery.package-generated.v1` contains:

- artifact kind and content digest;
- package identity and release version as provenance;
- canonical generated import path;
- `package_contract_abi_revision`;
- covered package-local declaration keys;
- accepted Go runtime ABI range;
- capability-interface ABI ranges;
- generator identity and version.

It MUST NOT contain an application `contract_revision`.

### 16.2 Application descriptor

`scenery.generated.v1` contains:

- artifact kind and content digest;
- application `contract_revision`;
- covered resource addresses;
- complete import-path to package-ABI-revision map;
- runtime and provider ABI ranges;
- generator identity and version.

Each built runtime bundle records its target-specific `implementation_revision`, build-input manifest or artifact digest, resolved `go_target`, and toolchain identity. A multi-target application descriptor records the complete target-to-revision map.

### 16.3 Digest rules

Descriptors are detached. Artifact digests sort normalized paths and hash exact bytes while excluding the descriptor itself. A descriptor participates in `workspace_revision` and may be signed separately.

Generated contracts are keyed by package ABI revision. Application adapters record application contract and package ABI revisions. Clients and schemas also record their artifact-specific projection revisions, allowing unrelated internal changes to avoid invalidating public artifacts.

## 17. Revision behavior

Each `implementation_revision[target]` includes:

- application `contract_revision`;
- application release version as artifact provenance;
- every linked `package_contract_abi_revision`;
- implementation-domain source fields;
- verified build-input manifest or artifact digest;
- generated adapter digest;
- resolved toolchain and target;
- runtime/provider ABI identities.

Changing a Go body, build tag, CGO policy, architecture feature, native input, generated adapter, runtime ABI, or provider ABI changes every affected target revision. It need not change an unaffected target. Changing only an unconsumed generated client projection does not change a Go target revision.

## 18. ABI compatibility

The Go ABI is semantically versioned independently of language edition.

A new major is required for an incompatible change to:

- package topology or dependency direction;
- type mappings;
- optional/null representation;
- constructor input, dependency/config/client, lifecycle, handler, outcome, or registration signature;
- business-outcome versus Go-error semantics;
- required descriptor interpretation;
- runtime or provider capability interfaces.

A package Go-surface change creates a new package ABI revision but does not require a profile major when the mapping rules remain compatible. A package release with identical reachable Go ABI shape retains the same package ABI revision.

## 19. Generated ownership

Generated artifacts are projections, never declaration sources. They live only beneath declared managed-generated roots, contain generated markers, and are replaced atomically as complete descriptor-covered sets.

Agents and humans MUST NOT edit generated artifacts to change semantics. They edit `.scn` declarations or implementation source and regenerate.

Unknown files beneath generated roots are not adopted implicitly.

## 20. Clean clone and editor workflow

For every workspace-local Go implementation package, its module-owned `scenerycontract` package and detached descriptor MUST be deterministically materialized and committed. A clean clone can therefore run ordinary `go test ./...` and provide types to `gopls` without starting Scenery or generating into the source tree first.

`scenery check` generates the expected contract and adapters in an overlay, compares descriptor-covered on-disk contract bytes, and reports stale or missing committed contracts as implementation diagnostics. It still type-checks against the overlay and never mutates files.

`scenery generate` atomically refreshes committed contract packages and other selected generated artifacts. `scenery generate --check` exits nonzero when committed bytes differ and is the required CI clean-tree check. Application adapters may be committed or build-generated according to project policy, but their descriptor ownership and staleness rules are identical.

During unsaved or not-yet-generated `.scn` edits, a Scenery language server SHOULD expose the verified overlay to `gopls` through supported editor integration. Without overlay integration, `gopls` sees the last committed contract and the editor displays a clear stale-generation status rather than silently mixing revisions.

Generated descriptors record the source contract/package ABI revisions, so stale artifacts are always detectable. A stale generated contract is an error for check/build and a warning-level editor state only while the user is actively editing.

Registry packages ship their contract package and descriptor as part of the immutable artifact.

## 21. Testing

Unit tests may import `scenerycontract` and instantiate the service directly:

~~~go
func TestProcessScene(t *testing.T) {
    svc, err := NewService(context.Background(), housecontract.HouseConstructorInput{
        Dependencies: housecontract.HouseDependencies{},
        Config:       housecontract.HouseConfig{},
    })
    if err != nil {
        t.Fatal(err)
    }

    outcome, err := svc.ProcessScene(context.Background(), housecontract.ProcessSceneInput{
        SceneId: "scene-1",
        Mode:    housecontract.ProcessModeAll,
    })
    if err != nil {
        t.Fatal(err)
    }

    if _, ok := outcome.(housecontract.ProcessSceneProcessed); !ok {
        t.Fatalf("unexpected outcome %T", outcome)
    }
}
~~~

Tests need not start a Scenery runtime. Their build context extends but never replaces the target base context.

## 22. Legacy compatibility boundary

`adapter = "legacy_go_v0"` is not native ABI conformance. It is a temporary bridge-owned adapter described by [SCENERY_LEGACY_BRIDGE_V1.md](SCENERY_LEGACY_BRIDGE_V1.md).

A service may first migrate declarations while retaining existing handlers, then later remove the adapter and implement this native ABI. Exactly one adapter is active for each operation.

## 23. Conformance requirements

A conforming tool passes fixtures for:

- local and registry topology;
- import-path forward/reverse mapping;
- multiple module instances;
- multiple services in one Go package;
- duplicate Go contract ownership rejection;
- toolchain/tag/CGO resolution without ambient environment;
- host development/test targets alongside cross-platform artifact targets;
- contract-only staged-overlay target and its explicit non-guarantees;
- target inheritance and target-specific implementation revisions;
- generated identifier collision rejection;
- deterministic named-child ordering;
- every type mapping;
- preserving-record unknown fields;
- service-qualified dependencies;
- constructor-input, dependency/config/client, and lifecycle signatures;
- sensitive configuration taint and secret-reference injection;
- native/native and bridge-backed typed internal client injection;
- every valid and invalid handler return combination;
- staged overlay verification with missing and stale generated packages;
- check with no filesystem mutation;
- atomic artifact materialization;
- package ABI revision stability across a semver-only release;
- package ABI change for a reachable signature change;
- no package ABI change for an unreachable private type;
- descriptor and runtime registration mismatch failures;
- clean-clone `go test ./...` and gopls-visible committed contracts;
- overlay-based check with stale committed contracts;
- atomic generate and `scenery generate --check`;
- ordinary implementation unit testing.

## Appendix A: Diagnostic classes

Implementations expose distinct stable diagnostic classes for:

- toolchain/target resolution;
- import mapping;
- generated-name collision;
- package ABI mismatch;
- implementation loading/type checking;
- constructor mismatch;
- dependency capability mismatch;
- lifecycle mismatch;
- handler/outcome mismatch;
- adapter/composition mismatch;
- artifact materialization failure.

Language-contract diagnostics are not recategorized as Go implementation failures.
