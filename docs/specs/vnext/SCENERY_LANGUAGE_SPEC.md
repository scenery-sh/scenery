# Scenery Language Specification

Agent-oriented draft for Scenery vNext

Version: 0.4-draft
Target language edition: 2027  
Status: design specification, not documentation of the current implementation

## 1. Purpose

This document defines the proposed Scenery vNext declaration language and its semantic contract. It is written for:

- humans authoring Scenery applications;
- AI agents inspecting or changing them;
- compiler, formatter, editor, and CLI implementers;
- extension and provider authors;
- code generators and runtime integrations.

The source language uses a deliberately small HCL-like syntax. Source files compile into a canonical, typed, versioned resource graph. The graph—not file order, filenames, generated Go, or runtime discovery—is the semantic truth.

This specification intentionally does not preserve Scenery's legacy declaration surface. Legacy JSON files, Go comments, struct tags, and runtime builder calls are migration inputs governed by scenery.legacy-bridge/v1; they are not edition-2027 source syntax.

### 1.1 Document map

- Sections 3–7 define files, syntax, references, and types.
- Sections 8–9 define packages, modules, and services.
- Sections 10–15 define operations, executions, bindings, policies, schedules, and events.
- Sections 16–18 define data, providers, extensions, deployments, and secrets.
- Sections 19–20 define compilation and canonical IR.
- Sections 21–23 define CLI, agent transactions, and diagnostics.
- Section 24 defines the language-level Go integration contract; [SCENERY_GO_IMPLEMENTATION_V1.md](SCENERY_GO_IMPLEMENTATION_V1.md) is the normative ABI specification.
- [SCENERY_HTTP_CODEC_V1.md](SCENERY_HTTP_CODEC_V1.md) is normative for scenery.http-codec/v1 and scenery.runtime-http/v1.
- [SCENERY_TYPESCRIPT_CLIENT_V1.md](SCENERY_TYPESCRIPT_CLIENT_V1.md) is normative for scenery.typescript-client/v1.
- [SCENERY_COMPATIBILITY_CORE_V1.md](SCENERY_COMPATIBILITY_CORE_V1.md) is normative for multidimensional semantic compatibility decisions.
- [SCENERY_LEGACY_BRIDGE_V1.md](SCENERY_LEGACY_BRIDGE_V1.md) is normative for mixed legacy/native migration.
- Section 25 defines security properties.
- Section 26 defines conformance profiles and the initial implementation slice.
- Section 27 is an end-to-end House example.
- Sections 28–30 define invalid cases, profile conformance, and agent workflows.
- The appendices summarize grammar, reserved roots, naming, decisions, and open draft items.

### 1.2 Normative words

The words MUST, MUST NOT, SHOULD, SHOULD NOT, and MAY are normative.

- MUST and MUST NOT define requirements for conformance.
- SHOULD and SHOULD NOT define strong recommendations. Deviations require a documented reason.
- MAY defines an optional capability.

Examples are normative when they illustrate a rule stated with one of these words. Otherwise, examples are informative.

### 1.3 Design goals

The language MUST be:

1. Flexible: one operation can be exposed through multiple transports and execution modes.
2. Simple: each concept has one canonical spelling and one semantic meaning.
3. CLI-friendly: every meaningful object is addressable, inspectable, explainable, and diffable.
4. Agent-friendly: schemas, diagnostics, provenance, mutations, and transactions are machine-readable.
5. Deterministic: compilation does not depend on source order, process state, the network, time, or randomness.
6. Statically analyzable: the full application contract is available without starting application code.
7. Explicit at boundaries: transport, authentication, authorization, execution, persistence, and deployment behavior are visible resources.
8. Extensible without semantic ambiguity: extensions add versioned schemas rather than untyped escape hatches.

### 1.4 Non-goals

The language is not:

- a general-purpose programming language;
- a clone of Terraform or Terraform's evaluation model;
- a way to execute arbitrary Go during compilation;
- a templating language;
- a replacement for application implementation code;
- dependent on source position or filename conventions;
- required to preserve legacy syntax or behavior.

## 2. Core semantic model

Scenery models an application as a graph of typed resources.

The central application kernel is:

> operation + binding + execution + policy

- An operation defines a logical capability and its typed contract.
- A binding makes an operation reachable through HTTP, an internal call, a CLI command, an event, or another transport.
- An execution defines how the operation's implementation runs: directly, durably, or as a workflow.
- Policies govern exposure, authentication, authorization, middleware pipelines, rate limits, and related cross-cutting behavior.

These concepts MUST remain independent. For example:

- adding an HTTP route MUST NOT create a second logical operation;
- making an operation durable MUST NOT change its input or result types;
- scheduling an operation MUST NOT require a special cron-only handler;
- changing authentication MUST NOT change exposure or authorization implicitly.

### 2.1 Resource families

The core language defines the following resource families.

| Family | Meaning |
|---|---|
| application | The deployable application and its global contract |
| package | A versioned module source unit |
| module | An instantiated package with explicit inputs and exports |
| service | A runtime implementation boundary |
| go_module, go_toolchain, go_target | Reproducible Go import, toolchain, and build-target metadata |
| record, enum, union | Boundary and domain types |
| operation | A logical callable capability |
| binding | A transport-facing entry point |
| http_gateway | A logical HTTP route, trust, and exposure namespace |
| execution | A direct, durable, or workflow execution policy |
| schedule | A time trigger invoking an operation |
| event | A typed event contract |
| event_emission | A typed publication from an operation outcome |
| authentication | A mechanism for establishing identity |
| authorization | A policy deciding whether an identity may act |
| workload_identity | A runtime-minted identity for schedules, workers, and services |
| pipeline | An explicitly ordered middleware pipeline |
| middleware | A typed runtime component used by a pipeline |
| data_source | A database, object store, external API, or other data provider instance |
| execution_engine | A durable or workflow execution provider instance |
| event_bus | A typed event transport provider instance |
| secret_store | A provider instance that resolves secret references |
| entity | A logical persisted object |
| view | A typed query or projection |
| crud | A declaration expanded into ordinary operations and bindings |
| fixture | Environment-scoped seed data |
| page | A UI route and interaction contract |
| renderer | A frontend-specific rendering implementation |
| provider | A versioned runtime or infrastructure integration |
| extension | A versioned language schema extension |
| secret | A reference to sensitive data held by a secret provider |
| patch | A guarded, version-bounded override of an explicitly patchable export |
| deployment | Runtime placement, scaling, and environment policy |

No resource is allowed to acquire unrelated meanings merely for convenience. In particular:

- a record is not automatically an entity;
- an entity is not automatically a database table;
- an operation is not automatically HTTP;
- a service is not automatically public;
- a schedule is not a second implementation;
- a page is not a renderer;
- a data source is not necessarily managed by Scenery;
- an execution engine is not a data source;
- an event bus is not a data source;
- a secret store never exposes secret plaintext to the compiler.

## 3. Source units and files

### 3.1 File names

The application root MUST contain a file named scenery.scn and MAY contain additional files ending in .scn. The reserved temporary filename scenery.migration.scn belongs to scenery.legacy-bridge/v1 and is not parsed as edition source.

A package directory MUST contain scenery.package.scn and MAY contain any number of files ending in .scn.

Example:

~~~text
scenery.scn
scenery.lock.scn
house/
  scenery.package.scn
  service.scn
  types.scn
  scenes.scn
  processing.scn
~~~

The lock file is generated. Authors and agents MUST change dependency intent in source files and let Scenery update the lock file.

### 3.2 File composition

The root scenery.scn file and all other .scn files directly inside the application root form one unordered application body. scenery.lock.scn is generated metadata and is excluded. In mixed mode, scenery.migration.scn is parsed only by the legacy bridge and is also excluded from the edition source body.

The scenery.package.scn file and all other .scn files directly inside one package directory form one unordered package body.

- Filenames are organizational only.
- Source order across files has no meaning.
- Source order among sibling resources has no meaning.
- Subdirectories are separate source units and are never included implicitly.
- Duplicate package-level resource identities are errors.
- A resource MAY refer to a resource declared later or in another file in the same package.

An implementation MUST produce the same semantic graph for any permutation of files and top-level resource blocks.

### 3.3 Encoding and line endings

Source files MUST be UTF-8. A byte-order mark is forbidden. Parsers MUST accept LF and CRLF. The formatter MUST emit LF.

### 3.4 Lossless concrete syntax tree

The parser MUST retain a lossless concrete syntax tree containing tokens, trivia, comments, exact byte ranges, and original line endings.

Parsing and printing a valid file without requesting formatting or mutation MUST be byte-identical. Formatting is a separate operation.

Comment attachment is deterministic:

- a contiguous leading comment group belongs to the following attribute or block when no blank line separates them;
- a comment after a syntax node on the same line is trailing trivia for that node;
- comments separated from neighboring nodes by blank lines are detached and remain at file or containing-block scope;
- rename moves attached leading and trailing comments with the renamed node;
- deletion removes attached comments and preserves detached comments.

The formatter MUST preserve authored top-level block order even when that order has no semantic meaning. Canonical IR, not source reordering, provides deterministic semantic ordering.

A semantic mutation MUST replace the smallest complete CST node capable of expressing the change, preserve unrelated bytes and comments, and choose files and insertion points deterministically. A plan predicts exact resulting bytes and workspace_revision; apply MUST reproduce them.

Parser recovery MAY produce diagnostics and usable ranges, but a recovered syntax tree MUST NOT produce a deployable manifest.

Before edition 2027 is stable, the CST implementation MUST pass fixtures for lossless round trips, idempotent formatting, comment-preserving create/update/delete/rename, nested object and block edits, invalid-source recovery, CRLF input, predicted workspace_revision equality, and fuzzed parse-format-reparse semantic equivalence.

The choice of HCL parser or writer library is not normative. If a generic HCL writer cannot satisfy these guarantees, Scenery MUST maintain its own CST over the token stream.

### 3.5 Workspace membership

The application root MAY declare one workspace block. Implementation ownership and workspace-revision membership are separate:

~~~hcl
workspace {
  implementation_root "house" {
    path = "house"

    revision_include = [
      "**/*.go",
      "**/*.c",
      "**/*.cc",
      "**/*.h",
      "go.mod",
      "go.sum",
    ]

    revision_exclude = [
      "native/build/**",
      "test-results/**",
      "coverage/**",
    ]
  }

  revision_input "go_workspace" {
    paths = [
      "go.work",
      "go.work.sum",
    ]
    optional = true
  }

  managed_generated_roots = [
    "house/scenerycontract",
    "internal/scenerygen",
    "clients/generated",
  ]
}

go_module "application" {
  root        = "."
  import_path = "github.com/example/clean-tech"
}
~~~

All application and package .scn files, scenery.lock.scn, accepted migrations, and migration ledgers are workspace-revision inputs automatically. An implementation_root declares source ownership. Its files enter workspace_revision only when they match revision_include and do not match revision_exclude. Merely creating a log, cache, model, test result, native build output, or other unmatched file beneath the root MUST NOT change workspace_revision.

revision_input declares additional exact files. An absent required path is an error; an absent optional path contributes nothing. A path cannot be both included and excluded, and exclusions cannot remove automatic language, lockfile, migration, or accepted artifact-descriptor inputs.

The glob dialect is edition-defined and path-based: * matches zero or more non-slash characters, ? matches one non-slash character, and ** matches zero or more complete path segments. Matching uses normalized slash-separated workspace-relative paths and is case-sensitive. A conforming tool MUST NOT delegate semantics to a host shell or filesystem glob implementation.

Generators may write only beneath managed_generated_roots. workspace_revision includes a generated descriptor and exactly the files covered by that descriptor; unrelated or temporary files beneath a generated root are excluded and SHOULD produce a cleanup diagnostic. This prevents a build log from invalidating an unrelated semantic plan.

go_module is an implementation-domain, bidirectional path mapping rather than a workspace-membership rule. root is either the exact value "." or a normalized workspace-relative directory; import_path is a canonical Go module import path. For import path P, the longest slash-segment-prefix import_path wins. Reverse resolution from a generated directory uses the longest directory-segment-prefix root. Equal-specificity candidates, duplicate roots or import paths, a failed round trip, or a used local package outside every implementation_root is an error.

Mappings never change revision membership. Registry implementation packages use locked import paths and package-contract metadata and MUST NOT be remapped to local source. Ambient go env, module caches, network lookup, and incidental go.work state never define a mapping.

All declared paths are symlink-safe, cannot escape the workspace, and cannot name VCS metadata or ambient build caches. Files are enumerated in bytewise normalized-path order. The resulting set defines workspace_revision; the Go build integration separately supplies the content-addressed build-input manifest used by implementation_revision.

## 4. Lexical and syntactic rules

Scenery uses the HCL native configuration shape: attributes, blocks, quoted labels, expressions, lists, and objects. It intentionally accepts a smaller surface than general HCL. The .scn extension identifies Scenery semantics; generic HCL tooling is not assumed to understand the language.

### 4.1 Canonical example

~~~hcl
record "create_task_input" {
  field "title" {
    type       = string
    min_length = 1
    max_length = 200
  }
}

operation "create_task" {
  input = record.create_task_input

  result "created" {
    type = record.task
  }

  error "invalid_input" {
    type = std.type.problem
  }
}
~~~

### 4.2 Identifiers

Language identifiers and block type names MUST use lower_snake_case ASCII:

~~~text
[a-z][a-z0-9_]*
~~~

Resource labels:

- MUST be non-empty;
- MUST use lower_snake_case unless the block schema explicitly defines another format;
- MUST be unique within the resource kind and package scope;
- MUST describe semantic identity, not source position.

Wire names, URL paths, task names, Go symbols, database identifiers, and provider identifiers are strings and follow the rules of their respective domains. HTTP path-parameter labels use lower-snake placeholder identities. HTTP header labels use canonical lower-case field-name tokens; query-parameter labels use non-empty wire strings without controls or query delimiters; cookie labels use cookie-name tokens; multipart part labels use non-empty strings without controls. Source validation, schema metadata, formatters, semantic renderers, and HTTP validation MUST use these same label policies.

### 4.3 Attributes and blocks

An attribute has one value:

~~~hcl
timeout = "40m"
~~~

A block has a type, zero or more labels allowed by its schema, and a body:

~~~hcl
retry {
  strategy = "exponential"
  initial  = "10s"
  factor   = 2
  maximum  = "2m"
}
~~~

Unknown attributes, unknown blocks, missing required labels, extra labels, and duplicate singleton blocks MUST be errors. They MUST NOT be silently ignored.

### 4.4 Values

The source syntax supports:

- booleans: true and false;
- base-10 integers and decimals;
- double-quoted strings;
- null, only where the schema explicitly permits it;
- lists;
- objects;
- references;
- operators and function calls only in fields whose schemas permit them.

Canonical object syntax uses equals signs:

~~~hcl
inputs = {
  database            = data_source.house_database
  storage             = data_source.app_storage
  process_concurrency = 4
}
~~~

#### Contextually typed primitive literals

A quoted lexical literal is typed as string unless its schema position has one exact expected primitive type. At a position expecting bytes, uuid, date, datetime, duration, size, url, or relative_path, a quoted literal is parsed directly as that primitive. This is contextual literal typing, not conversion from an already typed string; a string value, variable, or expression is never implicitly coerced to another primitive.

The same values may be written explicitly with constructors when no single expected type exists:

~~~hcl
id       = uuid("018f47a2-6f45-7c4a-8b31-4cbbe3c99a22")
day      = date("2027-03-14")
at       = datetime("2027-03-14T10:15:30.123+01:00")
timeout  = duration("40m")
capacity = size("2GiB")
site     = url("https://example.com/api")
model    = relative_path("models/roofmapnet")
digest   = bytes_base64url("AQIDBA")
~~~

Constructors return the named primitive and accept exactly one quoted literal. Their lexical and normalization rules are:

| Type | Accepted source form | Canonical behavior |
|---|---|---|
| bytes | unpadded RFC 4648 base64url without whitespace | decode to bytes; IR re-encodes unpadded base64url |
| uuid | canonical 8-4-4-4-12 hexadecimal UUID text | require lower-case canonical text and a valid UUID variant |
| date | YYYY-MM-DD | validate the proleptic Gregorian date and preserve that form |
| datetime | RFC 3339 with Z or an explicit numeric offset and at most 9 fractional digits using `.` as the fractional separator | reject commas, excess fractional digits, whitespace, and leap seconds; normalize the instant to UTC Z and trim trailing fractional zeros |
| duration | optional leading minus followed by one or more exact ns, us, ms, s, m, h, d, or w components | convert to signed arbitrary-precision nanoseconds; reject fractions that are not an exact nanosecond |
| size | non-negative exact number followed by B, kB, MB, GB, TB, KiB, MiB, GiB, or TiB | convert to arbitrary-precision integral bytes; reject fractional bytes |
| url | absolute hierarchical RFC 3986 URI with a scheme and non-empty host | reject opaque and hostless URIs; lower-case scheme and any DNS host, remove the default port for HTTP or HTTPS, remove dot segments, uppercase percent hex, and unescape percent-encoded unreserved bytes |
| relative_path | non-empty slash-separated UTF-8 segments | reject a leading slash, backslash, empty segment, dot segment, NUL, or traversal; normalize each segment with edition-pinned Unicode 15.0 NFC |

Size conversion is exact rather than integer-lexical: `1.5KiB` is valid and canonicalizes to `1536` bytes, while `0.1B` is invalid because it produces a fractional byte.

Invalid lexical form, overflow in a field with a bounded representation, loss of precision, a disallowed sign, or a constructor whose result is not assignable to the expected type is a compile error. Formatting is deterministic: when a position has one exact expected primitive type, the formatter emits its canonical quoted contextual literal; when the expected type is absent or not singular, it emits the explicit constructor. It does not preserve the author's choice between those equivalent notations. The result MUST be idempotent.

### 4.5 Strings

Strings use double quotes. The formatter MUST use escape sequences for control characters and MUST NOT emit heredocs when a normal string is sufficient.

String interpolation and templates are forbidden. Dynamic values MUST be expressed as typed expressions in fields that explicitly allow runtime expressions.

This rule prevents hidden mini-programs inside strings.

### 4.6 Comments

The canonical comment syntax is:

~~~hcl
# Tenant-scoped storage for house artifacts.
~~~

The formatter MUST emit only hash comments. Other HCL comment forms are outside the language.

### 4.7 Expressions

There are two expression phases:

1. Compile expressions are evaluated while building the resource graph.
2. Runtime expressions are evaluated while invoking an operation, policy, view, or binding.

Every attribute schema MUST declare which phase it accepts.

Compile expressions MAY contain:

- literals;
- lists and objects;
- references to module inputs and resources;
- pure, allowlisted constructor functions;
- arithmetic or boolean operators where the expected type permits them.

Compile expressions MUST NOT read:

- environment variables;
- files not declared as content-addressed compiler inputs;
- the network;
- clocks;
- randomness;
- process state;
- application code.

Runtime expression roots are explicitly scoped. Depending on the field, permitted roots include:

- input: the operation input;
- principal: authenticated identity and claims;
- context: typed invocation metadata;
- result: successful operation variants;
- error: declared error variants;
- dispatch: execution-dispatch outcomes;
- transport: decoding, negotiation, and transport-level outcomes;
- admission: authentication, authorization, and admission-control outcomes;
- system: implementation and runtime contract failures;
- message: the consumed event envelope and payload;
- value: the value currently being validated;
- item: the current view or policy item.

Using a root outside the field's declared scope is an error.

### 4.8 Functions

Only functions registered in the active language edition and expected by the field schema are legal.

Core type constructors are:

- optional(T)
- nullable(T)
- list(T)
- set(T)
- map(T)
- tuple(T1, T2, ...)
- resource_ref(KIND)

Core expression functions include contains, length, starts_with, ends_with, lower, upper, and format. Field schemas may permit only a subset.

The primitive literal constructors in Section 4.4 are core compile functions. They are valid wherever their result type is accepted, including contexts whose expected type is a union or otherwise not singular.

Functions MUST be pure and deterministic. Unknown functions are errors. scenery.compiler-core/v1 permits only functions implemented by the trusted compiler. Extension-defined executable functions require a separately claimed sandboxed-extension profile. A source file cannot define functions.

### 4.9 Forbidden dynamic structure

The following are forbidden:

- dynamic blocks;
- source-generating loops;
- conditional creation of resources;
- arbitrary user-defined functions;
- implicit imports;
- preprocessor directives;
- textual includes;
- evaluation of Go code;
- aliases that give one concept multiple spellings.

Conditional runtime behavior belongs in policies, operation code, views, or deployment-phase values. Edition 2027 does not support conditional resource installation or graph-shaping feature flags.

## 5. Language editions and resource revisions

Every application MUST select one language edition:

~~~hcl
language {
  edition = "2027"
}
~~~

An edition fixes:

- lexical and syntactic rules;
- core block schemas;
- type-system behavior;
- default behavior;
- diagnostic guarantees;
- canonical IR lowering rules.

Core resources also have independent schema revisions in the compiled graph, such as scenery.operation/v1 and scenery.binding.http/v1.

An edition upgrade MAY change source defaults or syntax only through an explicit upgrade operation. A resource schema revision MAY evolve independently when its semantics are preserved or migrated explicitly.

Compilers MUST reject an unknown edition. They MUST NOT silently select the newest edition.

## 6. References, identity, and addresses

### 6.1 Source references

Within a package, a resource is referenced by kind and label:

~~~hcl
service.house
record.process_scene_input
operation.process_scene
execution.process_scene_durable
~~~

Package inputs use the var namespace:

~~~hcl
var.database
var.http_pipeline
~~~

An installed module export uses:

~~~hcl
module.house.service
module.house.operations
~~~

Standard-library resources use:

~~~hcl
std.type.problem
std.type.execution_receipt
std.authentication.none
~~~

References are typed. A reference to a pipeline cannot satisfy an attribute expecting an authorization policy.

Named child elements are addressable through further traversal, for example record.process_scene_input.scene_id and operation.process_scene.input. Child references have schema identity but are not standalone graph-resource addresses.

### 6.2 Stable resource addresses

Every compiled resource MUST have a stable address:

~~~text
<module-instance-path>/<kind>/<name>
~~~

Examples:

~~~text
house/service/house
house/operation/process_scene
house/execution/process_scene_durable
house/binding/process_scene_async
house/entity/scene
house/view/recent_scenes
house/schedule/nightly_roof_evaluation
~~~

The root namespace is app:

~~~text
app/data_source/house_database
app/execution_engine/durable_tasks
app/event_bus/application_events
app/secret_store/production
app/deployment/production
~~~

Nested module instances extend the path:

~~~text
operations/house/operation/process_scene
~~~

Addresses:

- MUST be unique in one compiled application;
- MUST be stable across formatting and file movement;
- MUST change when a resource or containing module instance is renamed;
- MUST NOT contain generated array indexes;
- MUST be used by the CLI, diagnostics, semantic patches, provenance, and graph APIs.

### 6.3 References are not strings

A resource reference is a typed syntax node, not a string containing an address. Refactoring tools MUST update references. They MUST NOT rewrite unrelated strings that happen to contain the same text.

### 6.4 Provenance

Every effective resource field MUST retain provenance sufficient to answer:

- which source range declared it;
- whether it came from a default, module input, extension, expansion, or patch;
- which resource or export supplied a referenced value;
- which transformations changed it.

Provenance belongs in compiler metadata, not inside the user-visible resource value.

`origin.field_provenance` is indexed by an RFC 6901 pointer into that resource's `spec`. Object names are escaped as JSON Pointer segments and arrays use numeric indexes; labels never replace array indexes. Every emitted key MUST resolve to an existing value in the same graph view. Each entry records a stable origin kind, optional declaring range and package input, supplying resource/export/profile identity, source address, and ordered transformation chain. For example:

~~~json
{
  "/spec/config/model_path": {
    "kind": "module_input",
    "declared_at": { "source_id": "...", "start": { "line": 8, "column": 4, "byte_offset": 120 }, "end": { "line": 8, "column": 42, "byte_offset": 158 } },
    "input": "var.roof_model_path",
    "provided_by": "app/module/house",
    "source_address": "house/service/house",
    "transformations": ["module_input_substitution", "contextual_relative_path"]
  }
}
~~~

## 7. Type system

### 7.1 Primitive types

The core primitive types are:

| Type | Meaning |
|---|---|
| bool | Boolean |
| int | Signed arbitrary-precision integer in contracts |
| int32 | Signed 32-bit integer |
| int64 | Signed 64-bit integer |
| uint32 | Unsigned 32-bit integer |
| uint64 | Unsigned 64-bit integer |
| decimal | Exact decimal number |
| float32 | IEEE 754 binary32 value; non-finite values require an explicit codec |
| float64 | IEEE 754 binary64 value; non-finite values require an explicit codec |
| string | Unicode string |
| bytes | Arbitrary bytes with an explicit wire codec |
| uuid | RFC-compatible UUID value |
| date | Calendar date without time or zone |
| datetime | Timestamp with an explicit offset, normalized in IR |
| duration | Non-negative or signed duration where permitted |
| size | Byte size such as 2GiB |
| url | Normalized absolute hierarchical RFC 3986 network URI |
| relative_path | Normalized relative path |
| json | JSON value, allowed only at declared untyped boundaries |

Public contracts SHOULD prefer specific types over string or json.

Duration literals are quoted strings using the units ns, us, ms, s, m, h, d, and w. Days and weeks are fixed elapsed-time units, not calendar units. Size literals are quoted strings using decimal or IEC byte units. The canonical IR normalizes both while preserving exact values.

### 7.2 Composite types

Composite types use constructors:

~~~hcl
type = optional(string)
type = nullable(uuid)
type = list(record.scene)
type = map(decimal)
~~~

Optional and nullable are different:

- optional(T) means a field may be absent.
- nullable(T) means a field must be present but its value may be null.
- optional(nullable(T)) permits absence, null, or a T value.

The compiler and generated clients MUST preserve this distinction.

A set is semantically unordered and contains unique values. Canonical semantic ordering is lexicographic byte order of each element's canonical JSON encoding from Section 20.4. When a wire profile cannot preserve or define that ordering, it MUST reject the set.

### 7.3 Records

A record is a closed, named product type:

~~~hcl
record "scene" {
  field "id" {
    type = uuid
  }

  field "name" {
    type       = string
    min_length = 1
    max_length = 200
  }

  field "created_at" {
    type = datetime
  }

  field "notes" {
    type = optional(nullable(string))
  }
}
~~~

Unknown fields MUST be rejected at closed public boundaries unless the record explicitly sets:

~~~hcl
unknown_fields = "preserve"
~~~

The value preserve SHOULD be used only for forward-compatible pass-through contracts. The alternative reject is the default. A core record cannot silently discard unknown fields.

Generated representations of a preserving record MUST expose unknown members through a dedicated map separate from declared fields. The map stores canonical json values keyed by the original wire name. Encoding rejects a collision between a declared field's effective wire name and an unknown-member key.

### 7.4 Field wire names

The field label is its semantic name. Its default wire name is the same lower_snake_case string.

A field MAY declare a different wire name:

~~~hcl
field "scene_id" {
  type      = uuid
  wire_name = "sceneId"
}
~~~

Wire renaming MUST NOT change semantic references. A wire-name collision is an error.

### 7.5 Constraints

Core constraints include:

- minimum and maximum for numbers;
- min_length and max_length for strings and collections;
- pattern for strings;
- format for standardized string formats;
- min_items and max_items;
- unique_items;
- sensitive;
- immutable;
- deprecated with a message and replacement.

Constraints are part of the type contract. They MUST be represented in the canonical IR and generated schemas.

The edition-2027 pattern dialect is RE2 syntax over Unicode scalar values. Backreferences, look-around assertions, recursion, and implementation-specific extensions are forbidden. Matching is unanchored unless the pattern contains explicit anchors. Invalid or unsupported syntax is a compile error.

Cross-field validation uses a named validation block:

~~~hcl
record "run_input" {
  field "start_at" {
    type = datetime
  }

  field "end_at" {
    type = datetime
  }

  validation "end_after_start" {
    when    = value.end_at <= value.start_at
    code    = "HOUSE_INVALID_TIME_RANGE"
    message = "end_at must be later than start_at"
    path    = record.run_input.end_at
  }
}
~~~

Validation expressions MUST be pure and phase-correct.

### 7.6 Enums

~~~hcl
enum "process_mode" {
  value "all" {}
  value "roof_only" {}
  value "facade_only" {}
}
~~~

Enum values are semantic identifiers and default wire values. A value MAY specify wire_value when an external protocol requires another representation.

Enums are closed by default. Open enums MUST be declared explicitly and generated clients MUST preserve unknown values.

Source expressions refer to a declared enum value as enum.<enum_name>.<value_name>:

~~~hcl
default = enum.process_mode.all
~~~

A string with the same spelling is not implicitly coerced to an enum. Strings appear only at declared wire boundaries.

### 7.7 Unions

Unions are tagged:

~~~hcl
union "scene_reference" {
  discriminator = "kind"

  variant "registered" {
    type = record.registered_scene_reference
  }

  variant "upload" {
    type = record.upload_scene_reference
  }
}
~~~

Untagged unions are forbidden at public boundaries because decoding and generated-client behavior would be ambiguous.

The discriminator is a wire field injected and consumed by the union codec; it is not part of each variant payload record. A payload field with the same wire name is an error. The default tag value is the variant label, and a variant MAY declare wire_tag explicitly.

A closed union rejects unknown tags. An open union MUST declare an unknown variant capable of preserving the original tag and payload. Generated values expose the semantic variant separately from payload fields.

### 7.8 Defaults

Defaults MUST be deterministic and assignable to the declared type.

Compile-time defaults MAY use literals and module inputs. Runtime-generated defaults, such as UUIDs or timestamps, belong to an entity, data source, execution, or implementation policy and MUST be represented as typed default strategies rather than fake literals.

### 7.9 Public-contract strictness

Public operation, event, page-action, and module-export types:

- MUST NOT use implicit coercion;
- MUST NOT use an unconstrained any type;
- MUST distinguish absence from null;
- MUST define a stable wire representation;
- MUST be serializable by every binding that references them.

## 8. Applications, packages, and modules

### 8.1 Application

The application root declares its identity and installs module instances:

~~~hcl
language {
  edition = "2027"
}

application "clean_tech" {
  version = "1.0.0"
}

module "house" {
  source = "./house"

  inputs = {
    database            = data_source.house_database
    storage             = data_source.app_storage
    authentication      = authentication.standard
    authorization       = authorization.member
    http_pipeline       = pipeline.http_default
    process_concurrency = 4
  }
}
~~~

There MUST be exactly one language block and one application block in the root source unit.

application.version is release provenance, not contract compatibility. It is excluded from contract_revision and included in application artifact metadata and, when a build exists, implementation_revision. Applications that need a public version field expose it explicitly through an operation, binding, or contract resource.

### 8.2 Package declaration

A reusable package declares metadata, inputs, resources, and exports:

~~~hcl
package "house" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"

  go_contract {
    import_path = "github.com/example/clean-tech/house"
  }
}

input "database" {
  type  = resource_ref("data_source")
  phase = "deployment"
  requires = [
    "sql.query/v1",
    "sql.transaction/v1",
  ]
}

input "process_concurrency" {
  type    = int
  phase   = "deployment"
  default = 4
  minimum = 1
}

export "service" {
  value = service.house
}

export "operations" {
  value = {
    process_scene = operation.process_scene
    list_scenes   = operation.list_scenes
  }
}

export "process_execution" {
  value = execution.process_scene_durable

  patchable = [
    "/spec/timeout",
  ]
}
~~~

### 8.3 Module semantics

A package behaves as a pure, typed resource factory:

~~~text
package(version, inputs) -> namespaced resources + typed exports
~~~

Therefore:

- package inputs MUST be explicit and typed;
- a package MUST NOT inspect its caller, siblings, process environment, or filesystem implicitly;
- package internals are private unless exported;
- transitive dependencies are private unless re-exported;
- the same package MAY be instantiated multiple times under different module labels;
- each instance receives an independent namespace;
- instance construction MUST be deterministic;
- module dependency cycles are errors.

### 8.4 Module inputs

Missing required inputs are errors. Unknown inputs are errors. Defaults are evaluated in package scope and cannot refer to resources created by the caller.

Every input declares a phase:

- contract: affects the application contract and cannot vary between deployments;
- implementation: selects implementation bindings or build inputs;
- deployment: may be rebound by a deployment without changing the contract.

The default phase is contract. Allowed flows are:

| Input phase | Allowed destination domains |
|---|---|
| contract | contract, implementation, deployment |
| implementation | implementation, deployment |
| deployment | deployment |

For example, a deployment input cannot determine an HTTP path, operation type, public wire name, implementation package, or expansion shape.

Sensitive inputs MUST be marked:

~~~hcl
input "api_credential" {
  type      = resource_ref("secret")
  phase     = "deployment"
  sensitive = true
}
~~~

Sensitivity is orthogonal to phase. Sensitive values MUST remain tainted through evaluation, diagnostics, plans, and logs. Secret references may flow only to sensitive implementation or deployment sinks; secret plaintext never enters language evaluation.

An implementation-phase input may select non-ABI build inputs or implementation behavior beneath an already declared implementation contract. It MUST NOT determine a Go contract import path, generated type shape, service constructor, lifecycle signature, handler signature, or outcome shape. Those are invariant package declarations under scenery.go-implementation/v1.

### 8.5 Exports

Exports are the only cross-module surface.

An export:

- MUST have a stable name;
- MUST have a statically known type;
- MAY expose a resource reference, type, primitive value, or closed object of exportable values;
- MUST NOT expose a reference to a private child of an otherwise private resource unless the schema permits it.

Removing or incompatibly changing an export is a package breaking change.

### 8.6 Versions and lockfile

Registry and remote modules MUST use an explicit version constraint. The resolver writes exact versions and content hashes to scenery.lock.scn.

A local module normally omits version. Its package declaration is authoritative. When present on any module, version is always a constraint; for a local module it is checked against the package declaration and never replaces it:

~~~hcl
module "house" {
  source  = "./house"
  version = ">= 1.0.0, < 2.0.0"
}
~~~

A registry module uses version as a constraint:

~~~hcl
module "geometry" {
  source  = "registry.scenery.dev/geo/geometry"
  version = ">= 2.0.0, < 3.0.0"
}
~~~

The lockfile MUST be:

- deterministic;
- portable;
- sufficient to reproduce dependency resolution;
- changed only by dependency commands;
- verified before compilation.

A workspace-local package is identified in workspace_revision by its exact files and MAY use a development package version. Publishing requires a valid release version. Registry dependencies in a release build resolve to immutable locked content; a local workspace member is not misrepresented as a registry artifact.

### 8.7 Customization and extension points

Patch resources require scenery.patches/v1.

Callers customize a module through, in order of preference:

1. typed inputs;
2. documented extension points;
3. exported resources;
4. an explicit patch resource.

Patches are an advanced mechanism. A patch MUST:

- target an exact resource address and schema revision;
- declare a compatible module-version range;
- carry preconditions;
- fail if its target or expected value changed;
- appear in provenance and semantic diffs.

Wild-card or best-effort patches are forbidden.

Address discoverability does not grant patch access. A caller may patch only an exported resource whose package declaration marks the relevant schema path patchable. Package-private resources cannot be patched from outside the package.

~~~hcl
patch "house_process_timeout" {
  # The package exports process_execution and marks timeout patchable.
  target         = module.house.process_execution
  module_version = ">= 1.0.0, < 2.0.0"
  schema         = "scenery.execution/v1"

  expect {
    path  = "/spec/timeout"
    value = "40m"
  }

  set {
    path  = "/spec/timeout"
    value = "45m"
  }
}
~~~

### 8.8 Package dependencies

A package MAY instantiate another package with a module block:

~~~hcl
module "geometry" {
  source  = "registry.scenery.dev/geo/geometry"
  version = ">= 2.3.0, < 3.0.0"

  inputs = {
    storage = var.storage
  }
}
~~~

Nested modules follow the same input, export, version, lock, and determinism rules as application modules. Their exports are referenced as module.geometry.<export> inside the package. Their source and transitive dependencies are recorded in the application lockfile.

A caller cannot reference a transitive module unless its parent re-exports the desired value. Exporting an operation makes its private dependency closure usable by the compiler and runtime, but does not make those private resources source-referenceable. CLI inspection MAY show private resources subject to access policy; inspectability does not change source visibility.

## 9. Services

A service is a runtime implementation boundary. It owns lifecycle and implementation bindings, not transport semantics.

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
}
~~~

### 9.1 Service requirements

A service MUST declare:

- a runtime;
- an implementation locator;
- every injected dependency;
- lifecycle hooks if it owns background resources.

A service MUST NOT discover dependencies by magic function names, global lookup, struct tags, or package scanning.

Generated runtime adapters MUST verify that the implementation exists and conforms to its declared operation handlers.

A package or root application containing one or more Go services MUST own exactly one go_contract block inside its package or application declaration. go_contract.import_path is a quoted, invariant Go import-path literal; it cannot refer to an input. Every Go service in that source unit is implemented by that package, though ordinary Go code beneath it may use any internal subpackages.

Two Scenery packages in one resolved dependency graph MUST NOT own the same go_contract.import_path or the same derived /scenerycontract path. A Scenery package that needs independently versioned Go boundary packages MUST be split into separate Scenery packages. A source unit without Go services MUST NOT declare go_contract.

### 9.2 Dependency capabilities

Dependencies are checked by capability, not only provider identity. For example, a module can require sql.transaction/v1 and accept any data_source whose locked provider implements it.

Capability requirements MUST be versioned and included in the compiled graph.

A provider's locked schema derives the capabilities a configured provider instance actually supplies. Instance authors may require or narrow capabilities, but they cannot grant capabilities by assertion.

### 9.3 Reproducible Go build context

Go services require explicit implementation-domain build metadata:

~~~hcl
go_toolchain "application" {
  version               = "1.26.3"
  goos                  = "linux"
  goarch                = "amd64"
  cgo                   = "enabled"
  architecture_features = ["amd64.v3"]

  build_tags = [
    "roofmapnet_native",
  ]

  experiments    = []
  compiler_flags = []
  linker_flags   = []
}

go_target "api" {
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]

  native_inputs = [
    relative_path("house/native"),
  ]

  test {
    additional_build_tags = ["scenery_test"]
  }
}
~~~

check, generate, build, and serve MUST use the same base toolchain and target context. A test context may add tags and test-only packages but cannot remove or replace base tags, experiments, CGO policy, architecture, or native inputs.

Toolchain version, target platform, architecture feature level, CGO policy, tags, experiments, compiler/linker flags, native tool identities, and the build-supplied content-addressed input manifest participate in implementation_revision. Ambient GOFLAGS, GOEXPERIMENT, CGO_ENABLED, GOOS, GOARCH, process environment, go.work discovery, and compiler defaults MUST NOT silently change the resolved context.

go_module, go_toolchain, and go_target addresses and fields are implementation-domain metadata. They are excluded from contract_revision unless another contract resource explicitly exposes one of their values.

SCENERY_GO_IMPLEMENTATION_V1.md defines exact validation, staged generation, type checking, and artifact behavior.

## 10. Operations

An operation is a named logical capability with typed input, success variants, declared error variants, and an implementation handler.

~~~hcl
operation "process_scene" {
  service = service.house
  input   = record.process_scene_input

  handler {
    method = "ProcessScene"
  }

  result "processed" {
    type = record.process_scene_result
  }

  error "invalid_input" {
    type = std.type.problem
  }

  error "scene_not_found" {
    type = std.type.problem
  }
}
~~~

### 10.1 Operation identity

An operation represents intent, not a route or task. The following are normally one operation:

- a synchronous internal call;
- an asynchronous HTTP enqueue endpoint;
- an HTTP endpoint that waits for durable completion;
- a CLI command;
- a schedule;
- an event consumer.

They differ through bindings and execution references.

### 10.2 Input

An operation has exactly one input type. No-input operations use std.type.unit.

Transport metadata MUST NOT be hidden in the input record. HTTP headers, path parameters, CLI flags, event metadata, and authenticated identity are mapped explicitly by bindings or context.

### 10.3 Results and errors

Every success and error variant has a stable label. Variant labels form the semantic response surface used by bindings and generated clients.

Result and error variant block order has no semantic meaning.

Undeclared errors reaching the Scenery boundary MUST be converted to system.internal with a standard problem payload and recorded as a contract violation. system.internal is not an operation error variant and bindings cannot declare it unreachable.

Panics, exceptions, and transport failures are not implicit operation variants.

### 10.4 Handlers

A handler block identifies implementation code through the containing service's runtime.

Go services claiming scenery.go-implementation/v1 follow the normative ABI in Section 24. The generated adapter owns translation between canonical Scenery values and runtime-language values.

The language-neutral handler contract is:

~~~text
invoke(context, input) -> one declared result variant
                         | one declared error variant
                         | implementation failure
~~~

An implementation failure is reserved for broken infrastructure or a handler contract violation. It is not a shortcut for an undeclared business error.

The Go ABI represents declared results and errors through a closed generated outcome. Go error carries only implementation failure.

### 10.5 Idempotency

An operation MAY declare semantic idempotency:

~~~hcl
idempotency {
  mode = "keyed"
  key  = [
    input.tenant_id,
    input.scene_id,
  ]
}
~~~

Idempotency is part of the operation contract. A composite key is canonically encoded with type boundaries; it is not joined with an ambiguous delimiter. For optional(T), absence is encoded as a distinct absent marker; nullable(T) null is encoded as a distinct null marker. Neither equals an empty, zero, or default T value. A missing optional component is therefore stable but explicit. Storage duration and deduplication enforcement belong to the referenced execution.

## 11. Executions

An execution defines how one operation implementation runs.

### 11.1 Direct execution

~~~hcl
execution "list_scenes_direct" {
  operation = operation.list_scenes
  mode      = "direct"

  timeout = "15s"
}
~~~

Direct execution runs in the caller's request or process lifecycle.

### 11.2 Durable execution

~~~hcl
execution "process_scene_durable" {
  operation = operation.process_scene
  mode      = "durable"

  engine   = var.task_engine
  revision = 1
  timeout  = "40m"
  lease    = "20m"
  attempts = 6

  retry {
    strategy = "exponential"
    initial  = "10s"
    factor   = 2
    maximum  = "2m"
  }

  concurrency {
    key   = input.tenant_id
    limit = var.process_concurrency
  }

  retention {
    success = "7d"
    failure = "30d"
  }
}
~~~

In this example, var.task_engine is a deployment-phase package input of type resource_ref("execution_engine").

Concurrency keys use the same typed component encoding as idempotency keys. Optional absence, nullable null, empty strings, numeric zero, and concrete default values remain distinct. A profile that cannot represent every component without collision MUST reject the declaration.

Durable execution MUST define:

- a typed engine that supplies execution.durable/v1;
- a positive revision;
- timeout and lease behavior;
- retry behavior;
- retention behavior;
- deduplication behavior when the operation is keyed-idempotent.

Changing serialized input, result, or resumption behavior incompatibly MUST increment the execution revision.

The durable identity is the execution resource address plus revision, scoped by the engine. A package therefore remains safe when instantiated more than once. An execution MAY declare external_name for integration with an existing engine namespace; when present, it MUST be unique in that namespace and a reusable package SHOULD receive its namespace through a typed input.

An execution with no retries still declares:

~~~hcl
retry {
  strategy = "none"
}
~~~

A keyed-idempotent operation also requires:

~~~hcl
deduplication {
  retention = "24h"
  conflict  = "return_existing"
}
~~~

### 11.3 Workflow execution

A workflow execution coordinates typed steps and resumable waits. Each step invokes an operation rather than embedding an untyped callback.

Workflow state, step results, timers, signals, and compensation behavior MUST be represented in the canonical IR. An implementation MAY initially support only direct and durable modes, but it MUST reject workflow mode rather than lowering it incorrectly.

### 11.4 Multiple executions

One operation MAY have multiple executions, for example:

- a direct local-development execution;
- a durable production execution;
- a lower-concurrency administrative execution.

Every binding and trigger MUST select one execution explicitly. Edition 2027 performs no execution inference.

## 12. Bindings

A binding exposes one operation through one transport and selects an execution.

The core protocols are:

- http;
- internal;
- cli;
- event.

Extensions MAY add protocols through versioned binding schemas.

### 12.1 Common binding fields

Every binding declares:

~~~hcl
binding "process_scene_async" {
  gateway   = http_gateway.public_api
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "http"
  delivery  = "enqueue"

  authentication = var.authentication
  authorization  = var.authorization
  pipeline       = var.http_pipeline

  # Protocol-specific and policy blocks follow.
}
~~~

Every binding MUST explicitly declare authentication, authorization, and pipeline. When a dimension intentionally does nothing, the binding references the appropriate std.*.none or std.pipeline.empty resource. A non-HTTP binding declares exposure directly. An HTTP binding references one http_gateway, inherits its exposure, and MAY declare a narrower exposure; it can never widen the gateway.

Delivery is one of:

- call: return the operation result before completing the binding;
- enqueue: return after durable acceptance;
- wait: dispatch durably and wait for the operation result;
- stream: expose a declared stream result.

The selected execution MUST support the delivery mode.

Binding outcomes are divided into disjoint sets:

1. Transport and admission: decoding, content negotiation, authentication, authorization, rate limiting, and other pre-invocation checks.
2. Dispatch: enqueued, duplicate, rejected, unavailable, and wait_timeout.
3. Completion: declared result and error variants, plus system.internal.

For durable execution, dispatch.enqueued carries a receipt at dispatch.receipt, while failed dispatch outcomes carry std.type.problem at dispatch.problem. Enqueue delivery reaches only the first two sets because the operation has not completed. Call and wait delivery may also reach completion outcomes. Stream delivery reaches the sets declared by its stream schema.

Every protocol schema MUST identify its reachable outcomes. Every reachable outcome MUST have an explicit or edition-provided mapping.

### 12.2 Internal binding

~~~hcl
binding "process_scene_internal" {
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "internal"
  delivery  = "enqueue"

  exposure       = "application"
  authentication = var.authentication
  authorization  = var.authorization
  pipeline       = std.pipeline.empty

  internal {
    visibility = "application"
    principal  = "inherit"
  }
}
~~~

Internal visibility is one of package, application, or trusted_network and constrains which caller identities may address the binding. Exposure constrains deployment/network reachability. Internal does not mean unauthenticated; identity and authorization requirements remain explicit.

The internal block MUST declare principal. In scenery.runtime-http/v1 its only accepted value is "inherit": the runtime forwards a caller principal that it created or previously attenuated. A future internal-RPC profile MAY add typed service-identity references, but it MUST NOT introduce an ambient or forgeable identity default.

Package and application internal bindings are typed callable contracts. The caller supplies:

- the complete operation input value;
- a Scenery invocation context;
- an authenticated principal according to the required principal mode.

Input completeness and type constraints are identical to every other operation invocation. There is no path/query/body mapping.

The generated callable shape depends on delivery:

| Delivery | Successful return |
|---|---|
| call | one declared result or business-error outcome |
| enqueue | std.type.execution_receipt |
| wait | one declared result or business-error outcome |
| stream | the negotiated stream ABI |

Admission, dispatch, wait-timeout, and system failures use the standard typed invocation-failure envelope. Declared business errors remain operation outcomes.

For Go, generated internal clients accept context.Context, scenery.Invocation, and the generated operation input. Call and wait return the same closed outcome used by the handler; enqueue returns a receipt. Go error is reserved for invocation infrastructure failure.

A caller cannot forge an inherited principal; scenery.Invocation values are created or attenuated by the runtime. Authorization and pipeline processing run before execution.

trusted_network visibility requires a separately claimed internal-RPC codec profile. scenery.runtime-http/v1 supports package and application visibility only.

### 12.3 CLI binding

~~~hcl
binding "process_scene_cli" {
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "cli"
  delivery  = "wait"

  exposure       = "local"
  authentication = std.authentication.local_developer
  authorization  = std.authorization.local_developer
  pipeline       = std.pipeline.empty

  cli {
    command = ["house", "process-scene"]

    context "tenant_id" {
      from = principal.tenant_id
      to   = operation.process_scene.input.tenant_id
    }

    argument "scene_id" {
      position = 0
      to       = operation.process_scene.input.scene_id
    }

    flag "mode" {
      name = "mode"
      to   = operation.process_scene.input.mode
    }

    outcome "processed" {
      when = result.processed
      exit = 0

      stdout {
        codec = "json"
        from  = result.processed
      }
    }

    outcome "invalid_input" {
      when = error.invalid_input
      exit = 2

      stderr {
        codec = "problem_json"
        from  = error.invalid_input
      }
    }

    outcome "scene_not_found" {
      when = error.scene_not_found
      exit = 1

      stderr {
        codec = "problem_json"
        from  = error.scene_not_found
      }
    }
  }
}
~~~

CLI help, shell completion, exit behavior, and JSON output MUST be derivable from the binding and operation contract. Reachable operation variants require outcome mappings; the edition supplies standard outcomes for dispatch and system failures.

### 12.3.1 HTTP gateway scope

An http_gateway is a logical route namespace and trust boundary:

~~~hcl
http_gateway "public_api" {
  exposure = "internet"
  base_path = "/"

  cors            = std.cors.application
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
~~~

The gateway owns maximum exposure, base path, CORS, trusted-proxy policy, forwarded-header policy, and other listener-wide contract behavior. Hostnames, IP addresses, ports, certificates, TLS material, and platform listener identifiers are deployment values rather than contract fields.

Every HTTP binding MUST reference exactly one gateway. Route uniqueness is evaluated by gateway address, effective method, and effective path after applying the gateway base path. The same method/path pair may exist on distinct gateways. Two active owners of the same gateway/method/path are always an error, including in mixed legacy/native mode.

### 12.4 HTTP binding

An HTTP binding declares the complete transport contract:

~~~hcl
binding "process_scene_async" {
  gateway   = http_gateway.public_api
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "http"
  delivery  = "enqueue"

  authentication = var.authentication
  authorization  = var.authorization
  pipeline       = var.http_pipeline

  http {
    method        = "POST"
    path          = "/house/process-async"
    codec_profile = std.codec.http_json_v1

    body {
      codec = "json"
      to    = operation.process_scene.input
    }

    response "accepted" {
      when   = dispatch.enqueued
      status = 202

      body {
        codec = "json"
        from  = dispatch.receipt
      }
    }

    response "invalid_request" {
      when   = transport.invalid_request
      status = 422

      body {
        codec = "problem_json"
        from  = transport.problem
      }
    }

    response "dispatch_rejected" {
      when   = dispatch.rejected
      status = 503

      body {
        codec = "problem_json"
        from  = dispatch.problem
      }
    }
  }
}
~~~

An HTTP binding MUST explicitly declare:

- method;
- path;
- versioned codec profile;
- exposure;
- authentication;
- authorization;
- pipeline;
- input mappings;
- response mappings;
- delivery mode.

None of these values may be inferred from a Go method name, filename, package path, comment, struct tag, or source order.

### 12.5 HTTP paths

Path parameters use braces:

~~~hcl
path = "/house/scenes/{scene_id}"
~~~

Every path parameter MUST have exactly one mapping:

~~~hcl
path_parameter "scene_id" {
  to = operation.get_scene.input.scene_id
}
~~~

A mapped field MUST be scalar and have a defined path codec. Extra mappings and unmapped placeholders are errors.

Canonical paths:

- begin with a slash;
- do not end with a slash except the root path;
- do not contain empty segments;
- do not contain dot segments;
- use lower kebab-case for literal segments unless an external contract requires otherwise.

Route conflicts MUST be detected semantically, including conflicts between literal and parameterized paths.

### 12.6 HTTP input mapping

HTTP input sources are:

- path_parameter;
- query_parameter;
- header;
- cookie;
- body;
- authenticated principal;
- typed request context.

Each operation input field MUST be populated exactly once, have a type default, or be optional.

A context mapping block may read an allowed principal or context expression and map it to one operation input field. It is commonly used for tenant identity, trace context, or a trusted caller identity that MUST NOT be accepted from a request body.

Binding targets use operation.<name>.input.<field> as the canonical instance-field reference. A record.<name>.<field> reference describes a reusable type field but is not a binding target.

Example:

~~~hcl
http {
  method        = "GET"
  path          = "/house/scenes/{scene_id}"
  codec_profile = std.codec.http_json_v1

  path_parameter "scene_id" {
    to = operation.get_scene.input.scene_id
  }

  query_parameter "include_runs" {
    to = operation.get_scene.input.include_runs
  }

  query_parameter "tag" {
    to       = operation.get_scene.input.tags
    encoding = "repeated"
  }
}
~~~

Transport names are strings. Targets are typed field references. String paths such as "input.scene_id" are forbidden.

### 12.7 HTTP bodies

A body declares a codec and a target or source. Core codecs are:

- json;
- problem_json;
- text;
- bytes;
- form;
- multipart;
- server_sent_events.

Codecs are type-checked. For example, bytes cannot be emitted through json without an explicit bytes wire encoding.

When a request body maps to an entire record input, it MAY declare include or except with typed field references. The selected body fields are populated by semantic field and wire-name mapping; every excluded field MUST be populated by another mapping, have a default, or be optional. include and except are mutually exclusive.

Buffered opaque bodies use the bytes codec and the Scenery bytes type:

~~~hcl
body {
  codec = "bytes"
  to    = operation.upload_archive.input
}
~~~

scenery.http-codec/v1 has no raw request, response writer, or transport-handle codec. Unbuffered or transport-coupled HTTP access requires a separate ABI profile such as scenery.go-http-coupled/v1 and is unsupported by the initial runtime profile.

### 12.8 HTTP responses

Every reachable operation result, declared error, and dispatch outcome MUST be mapped or explicitly declared unreachable for that delivery mode.

Transport processing has standard outcomes independent of operation errors:

- transport.invalid_request for decoding or input-constraint failure;
- transport.unsupported_media_type;
- transport.not_acceptable;
- transport.problem containing the corresponding standard problem payload.

Admission processing uses admission.unauthenticated, admission.forbidden, admission.rate_limited, and admission.problem. Runtime or handler contract failure uses system.internal and system.problem.

A binding MUST map every transport outcome reachable under its codecs and negotiation settings. An edition MAY provide explicit standard mappings, but explain MUST show them in the effective view.

Edition 2027 provides default problem_json mappings for unsupported media type with status 415, unacceptable response media with status 406, unauthenticated with status 401, forbidden with status 403, rate limited with status 429, dispatch unavailable or rejected with status 503, wait timeout with status 504, and system internal with status 500. A binding MAY override them. The effective view always contains the resulting response resources.

A response chooses one semantic variant:

~~~hcl
response "not_found" {
  when   = error.scene_not_found
  status = 404

  body {
    codec = "problem_json"
    from  = error.scene_not_found
  }
}
~~~

Status, headers, cookies, redirects, and content type belong to the binding. Application payloads MUST NOT carry hidden status-code fields or framework response writers.

Two response conditions that can match the same outcome are an error.

### 12.9 Streaming and upgraded protocols

Streaming, server-sent events, and WebSockets require explicit stream types and lifecycle behavior. A compiler that does not implement them MUST report an unsupported-capability diagnostic. It MUST NOT silently treat them as buffered HTTP responses.

### 12.10 HTTP codec profile v1

SCENERY_HTTP_CODEC_V1.md is normative for scenery.http-codec/v1. This section is an embedded summary for language readers; the companion specification governs exact wire bytes, gateway behavior, and conformance fixtures.

Every HTTP binding resolves exactly one versioned codec profile. The default core profile is scenery.http-codec/v1, referenced as std.codec.http_json_v1 and recorded in canonical IR.

Generated clients and runtime adapters MUST use the same resolved profile.

#### 12.10.1 Paths and query strings

scenery.http-codec/v1:

- validates UTF-8 before semantic decoding;
- percent-decodes exactly once;
- rejects malformed escapes, encoded slash or backslash in one path segment, dot segments, and decoded NUL;
- encodes query components with RFC 3986 percent encoding;
- treats plus as a literal plus in query strings, not as space;
- uses repeated query names for list values by default;
- permits only scalar set elements in query parameters and emits repeated names in the canonical semantic set order from Section 7.2 before wire encoding;
- rejects repeated input for scalar fields;
- does not support comma-delimited collections unless encoding = "comma" is explicit;
- rejects map and nested-record query values unless encoding = "json" is explicit.

JSON-valued query parameters contain one UTF-8 JSON value before percent encoding. Form bodies use application/x-www-form-urlencoded rules, where plus represents space.

Every codec preserves list order. Whenever a codec emits a set as a sequence, it MUST use the canonical semantic set order from Section 7.2; a codec-specific escaped or wire representation does not redefine semantic ordering.

#### 12.10.2 Headers and cookies

Header names are lower-case in canonical IR and case-insensitive on the wire. Leading and trailing optional whitespace is removed according to HTTP rules. Repeated header values remain distinct.

A scalar header rejects multiple values. A collection header uses repeated values unless encoding = "comma" is explicit and the field's scalar codec cannot contain an unescaped comma. Set-Cookie is never comma-joined.

Cookie names use their explicit wire names. Cookie values use UTF-8 followed by RFC 3986 percent encoding. Response cookies declare SameSite, Secure, HttpOnly, Path, Domain, Max-Age, and expiry explicitly or inherit profile defaults shown by the effective view.

#### 12.10.3 Scalar wire forms

The profile uses:

| Type | Canonical wire form |
|---|---|
| bool | true or false |
| int | base-10 string with no plus or unnecessary leading zero |
| decimal | canonical decimal string |
| uuid | lower-case hyphenated UUID |
| date | YYYY-MM-DD |
| datetime | RFC 3339, emitted in UTC with Z |
| duration | ISO 8601 elapsed-duration string using only days, hours, minutes, and seconds; years and calendar months are forbidden, weeks normalize to days, and fractions are permitted only on seconds at nanosecond precision |
| size | base-10 byte-count string |
| url | normalized absolute hierarchical network-URI string |
| bytes in JSON | RFC 4648 base64 with required padding |
| enum | declared string wire value |

JSON int fields use strings by default because int is arbitrary precision. A field MAY select json_number only when its declared constraints remain within the signed JavaScript-safe integer range. Decimal uses a JSON string unless a similarly bounded profile option is explicit.

Open enums preserve unknown strings. Union discriminators use the union's declared wire field and tag values.

JSON sets encode as arrays in canonical semantic order. Duplicate set elements are decoding errors rather than silently deduplicated.

#### 12.10.4 JSON objects

JSON is UTF-8 only. Duplicate object keys, invalid Unicode, invalid numbers, and trailing non-whitespace input are errors.

Closed records reject unknown fields. Records with unknown_fields = "preserve" retain unknown values semantically, though insignificant whitespace and object-member order need not be preserved.

An absent optional field is omitted. A present nullable field may encode JSON null. optional(nullable(T)) distinguishes omitted, null, and a concrete value in generated clients and adapters.

Decoding errors carry a stable transport diagnostic code and a structured path containing semantic fields and an RFC 6901 JSON Pointer when applicable.

#### 12.10.5 Forms and multipart

Form fields use the same scalar codecs and repeated-field rules as query parameters.

Multipart mappings declare each part's target, media type, filename policy, and whether it is buffered or streamed. Unmapped parts are rejected. Filenames are metadata, never trusted filesystem paths.

Profile defaults are:

- maximum buffered request body: 8 MiB;
- maximum decompressed request body: 16 MiB;
- maximum buffered response body: 16 MiB;
- maximum multipart body: 32 MiB;
- maximum multipart file part: 16 MiB;
- maximum non-file part: 1 MiB;
- maximum parts: 128.

A binding MAY override these limits explicitly. The effective view always shows resolved limits. Streamed parts require a streaming profile.

#### 12.10.6 Negotiation and compression

Content-Type parameters are validated. JSON accepts application/json and explicitly declared structured-suffix media types. Accept processing honors wildcards and quality values; no acceptable representation produces transport.not_acceptable.

Textual codecs use UTF-8. Unsupported charsets produce transport.unsupported_media_type.

Request compression is disabled unless a pipeline explicitly enables a supported content coding. Decompressed bytes count against the decompressed limit. Response compression runs after representation selection and MUST set the appropriate Vary metadata.

#### 12.10.7 Enforcement guarantees

Each binding facet records one guarantee:

~~~json
{
  "guarantee": "framework_enforced"
}
~~~

or:

~~~json
{
  "guarantee": "implementation_declared"
}
~~~

Typed generated adapters use framework_enforced for their mappings and codecs. scenery.http-codec/v1 contains no implementation_declared body codec. A future transport-coupled profile must mark its body semantics implementation_declared; authentication, authorization, size limits, and pipeline facets may remain framework-enforced independently.

## 13. Exposure, authentication, and authorization

These are independent dimensions.

### 13.1 Exposure

Core exposure values are:

- internet;
- private_network;
- application;
- package;
- local.

Exposure answers where a binding is reachable. It does not establish identity and does not grant permission.

### 13.2 Authentication

Authentication establishes a principal:

~~~hcl
authentication "standard" {
  provider = provider.standard_auth
  scheme   = "session"
}
~~~

An authentication resource declares:

- provider;
- scheme;
- credential source;
- principal type;
- failure behavior.

Anonymous access uses std.authentication.none explicitly.

### 13.3 Authorization

Authorization decides whether a principal may invoke a capability:

~~~hcl
authorization "member" {
  principal = std.type.authenticated_principal

  rule "tenant_member" {
    allow = (
      principal.tenant_id == context.tenant_id &&
      contains(["owner", "member"], principal.membership)
    )
  }
}
~~~

Authorization rules MUST be pure, typed, ordered only when their combination strategy says order matters, and independently testable.

The default combination strategy is deny_unless_allowed. Deny rules take precedence unless a policy explicitly selects another standard strategy.

### 13.4 Fail-closed behavior

Missing authentication, an unavailable provider, an evaluation error, or an unknown authorization result MUST deny access for protected bindings.

### 13.5 Workload identities and invocation context

A principal is a runtime value. A workload_identity is a resource describing how the runtime mints that value for a non-human caller:

~~~hcl
workload_identity "scheduler" {
  issuer         = std.identity_issuer.runtime
  principal_type = std.type.workload_principal

  claims = {
    workload = "scheduler"
  }
}
~~~

Schedules, event consumers, workers, and service-to-service callers reference a workload_identity; they do not manufacture principal objects or bypass authentication. Identity issuance and attenuation are framework-enforced and fail closed.

Edition 2027 defines std.type.invocation_context with these typed fields:

| Field | Type | Meaning |
|---|---|---|
| invocation_id | uuid | Unique invocation identity |
| tenant_id | optional(string) | Trusted tenant scope, when established |
| trace_id | optional(string) | Distributed trace identity |
| deadline | optional(datetime) | Effective invocation deadline |
| caller_binding | optional(std.type.resource_address) | Binding that admitted the invocation |
| execution_id | optional(string) | Durable/workflow execution identity |
| deployment | optional(std.type.resource_address) | Selected deployment identity |
| locale | optional(string) | Validated locale preference |

Each binding and execution profile specifies which fields it can establish. Unavailable fields are absent, never fabricated from untrusted input. context mappings may narrow or copy established values but cannot overwrite runtime-owned fields. Authorization that requires an absent context field evaluates to deny.

The compiler MUST reject an internet binding whose authentication or authorization is omitted. Public anonymous access is expressed with explicit none and public policies.

## 14. Pipelines and middleware

A pipeline is a named, explicitly ordered sequence:

~~~hcl
pipeline "http_default" {
  step "request_id" {
    use = std.middleware.request_id
  }

  step "trace" {
    use = std.middleware.trace
  }

  step "recover" {
    use = std.middleware.recover
  }
}
~~~

Order is the block order inside one pipeline and is semantically significant.

Pipeline order MUST NOT be inferred from:

- filenames;
- package initialization;
- source positions outside the pipeline;
- tags;
- middleware registration timing.

A custom middleware implementation is a typed runtime component:

~~~hcl
middleware "house_metrics" {
  runtime = "go"
  service = service.house
  symbol  = "HouseMetrics"

  phase = "after_authentication"
}
~~~

Custom runtime middleware requires a separately versioned middleware ABI profile. scenery.go-implementation/v1 does not define middleware handler signatures. The initial scenery.runtime-http/v1 implementation may use only standard-library or provider-supplied middleware whose ABI is already declared.

Every middleware declares compatible protocols, allowed phases, input effects, output effects, and whether it may terminate a request.

The compiler MUST reject:

- incompatible middleware protocols;
- a step in an illegal phase;
- duplicate exclusive middleware;
- an ordering cycle introduced by before or after constraints.

## 15. Schedules and events

### 15.1 Schedules

A schedule is a trigger that invokes an existing operation:

~~~hcl
schedule "nightly_roof_evaluation" {
  trigger {
    cron     = "0 2 * * *"
    timezone = "Europe/Prague"
  }

  invoke {
    operation     = operation.run_roof_evaluation
    execution     = execution.roof_evaluation_durable
    identity      = std.workload_identity.scheduler
    authorization = std.authorization.scheduled
    pipeline      = std.pipeline.empty

    input = {
      tenant_id = "system"
      corpus    = "production"
    }
  }

  overlap = "skip"

  catchup {
    maximum_age = "10m"
  }
}
~~~

A schedule MUST contain exactly one trigger block. That block MUST define exactly one selector:

- cron;
- every;
- at;
- calendar.

Timezone behavior, daylight-saving behavior, catch-up behavior, overlap behavior, and misfire behavior MUST be explicit or fixed by the edition.

Core overlap modes are skip, queue, replace, and allow. A schedule does not own a handler; it invokes an operation through an execution.

A schedule MUST select a workload_identity, authorization policy, and pipeline. The runtime mints the corresponding principal; a scheduler does not bypass the operation's security context.

invoke.input is type-checked like binding input. Every required operation field MUST be supplied exactly once, unknown fields are errors, and values are compile-time expressions. No tenant or other operation input is inferred from the scheduler principal.

### 15.2 Events

An event is a named typed fact:

~~~hcl
event "scene_registered" {
  payload = record.scene_registered
  version = 1
}
~~~

An event binding consumes an event and invokes an operation:

In this example, var.event_bus is a deployment-phase package input of type resource_ref("event_bus").

~~~hcl
binding "process_registered_scene" {
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "event"
  delivery  = "enqueue"

  exposure       = "application"
  authentication = std.authentication.service_identity
  authorization  = std.authorization.application
  pipeline       = std.pipeline.empty

  event {
    direction = "consume"
    bus       = var.event_bus
    channel   = "house.scene-registered"
    contract  = event.scene_registered
    guarantee = "at_least_once"

    map {
      from = message.payload
      to   = operation.process_scene.input
    }

    broker_retry {
      attempts = 5
      backoff  = "exponential"
    }

    dead_letter_channel = "house.scene-registered.dead"
  }
}
~~~

Publication is a separate event_emission resource because it maps an operation outcome to an event rather than exposing an operation:

~~~hcl
event_emission "scene_registered" {
  bus       = var.event_bus
  channel   = "house.scene-registered"
  contract  = event.scene_registered
  guarantee = "at_least_once"

  from {
    operation = operation.register_scene
    when      = result.registered
    payload   = result.registered.event
  }
}
~~~

Event bus, channel, delivery guarantee, ordering key, deduplication key, broker retry, and dead-letter behavior MUST be explicit or supplied by a typed referenced policy.

## 16. Data model

Scenery separates logical contracts, persistence, providers, queries, generated operations, fixtures, and UI.

### 16.1 Data sources

A data_source is a typed provider instance for data access:

~~~hcl
data_source "house_database" {
  provider = provider.postgres
  lifecycle = "managed"

  require_capabilities = [
    "sql.query/v1",
    "sql.transaction/v1",
    "sql.migration/v1",
  ]

  config = {
    database = "house"
    scope    = "tenant"
  }
}
~~~

Lifecycle is independent of capability. Core lifecycle values are:

- managed: Scenery provisions and migrates the data source;
- external: Scenery uses but does not provision it;
- attached: another application resource owns it;
- ephemeral: created for a bounded environment or test.

Provider-specific config is validated by the provider's versioned schema. Unknown config is an error.

The effective capability set is derived from the locked provider version and validated config. require_capabilities asserts requirements and fails compilation when the provider cannot satisfy them; it never adds capabilities.

### 16.2 Entities

An entity is a logical persisted object:

~~~hcl
record "scene_row" {
  field "id" {
    type = uuid
  }

  field "tenant_id" {
    type = string
  }

  field "name" {
    type = string
  }

  field "created_at" {
    type = datetime
  }
}

entity "scene" {
  type        = record.scene_row
  data_source = var.database

  mapping {
    relation = "scenes"
  }

  field "id" {
    column      = "id"
    primary_key = true

    default {
      strategy = "uuid_v7"
    }
  }

  field "tenant_id" {
    column     = "tenant_id"
    tenant_key = true
    immutable  = true
  }

  field "name" {
    column = "name"
  }

  field "created_at" {
    column = "created_at"

    default {
      strategy = "current_datetime"
    }
  }
}
~~~

An entity MUST reference a record type and a data source. Persistence mapping MUST NOT change the public wire contract implicitly.

Every entity field mapping MUST resolve to a field in the entity's record type. Public operation records MAY differ from internal persistence records; views or implementations perform the explicit projection.

Schema ownership, migration mode, indexes, uniqueness, foreign keys, tenant isolation, and deletion behavior are explicit entity concerns.

### 16.3 Views

A view is a typed read model or query:

~~~hcl
view "recent_scenes" {
  data_source = var.database
  input       = record.list_scenes_input
  result      = list(record.scene_summary)

  implementation {
    kind = "sql_query"
    file = "queries/scene.sql"
    name = "ListRecentScenes"
  }
}
~~~

Referenced implementation files are declared inputs and become part of the content hash. Query result columns MUST be checked against the declared result type where the provider supports verification.

### 16.4 CRUD expansion

A CRUD resource is declarative sugar that expands into normal operations and bindings:

~~~hcl
crud "scene_api" {
  entity         = entity.scene
  implementation = std.crud.entity

  actions = [
    "list",
    "get",
    "create",
    "update",
    "delete",
  ]

  execution {
    mode    = "direct"
    timeout = "15s"
  }

  http {
    path           = "/house/scenes"
    codec_profile  = std.codec.http_json_v1
    gateway        = var.gateway
    authentication = var.authentication
    authorization  = var.authorization
    pipeline       = var.http_pipeline
  }
}
~~~

CRUD is not a privileged runtime subsystem. Its implementation provider, execution template, transport, gateway, authentication, authorization, and pipeline are explicit inputs. Expansion produces ordinary typed resources implemented by the selected provider.

The expanded view MUST reveal:

- every generated operation;
- every generated binding;
- generated input and result types;
- policy inheritance;
- origin and expansion lineage;
- stable derived addresses.

Expanded resources are read-only. Customize them through the generator's typed extension points or, when explicitly allowed, patch the authored generator resource before expansion.

### 16.5 Fixtures

Fixtures are typed, environment-scoped seed data:

~~~hcl
fixture "demo_scenes" {
  entity       = entity.scene
  environments = ["development", "preview"]
  mode         = "upsert"

  values = [
    {
      id        = "01900000-0000-7000-8000-000000000001"
      tenant_id = "demo"
      name      = "Example house"
    },
  ]
}
~~~

Production fixtures require an explicit deployment policy allowing them.

### 16.6 Pages and renderers

A page defines route, data, and actions. A renderer supplies frontend-specific implementation.

~~~hcl
page "scene_detail" {
  path = "/house/scenes/{scene_id}"
  load = binding.get_scene_for_page

  action "process" {
    invoke = binding.process_scene_for_page
  }
}

renderer "scene_detail_web" {
  page    = page.scene_detail
  runtime = "web"
  module  = "./ui/SceneDetail"
}
~~~

Page loads and actions invoke typed internal bindings, which already select operation, execution, and policy. They do not call bare operations or arbitrary backend methods. One page MAY have multiple renderers.

## 17. Providers, instances, and extensions

### 17.1 Provider packages

A provider package supplies schemas and phase-specific implementations:

~~~hcl
provider "postgres" {
  source  = "registry.scenery.dev/core/postgres"
  version = ">= 2.1.0, < 3.0.0"
}
~~~

Provider packages MUST publish:

- immutable package identity and integrity;
- supported Scenery editions and conformance profiles;
- signed or integrity-verified compile descriptors;
- capability and configuration schemas;
- resource schema revisions;
- generated-code, runtime, deployment, and migration ABI requirements;
- compatibility and migration metadata.

The lockfile records the selected immutable version and digest.

### 17.2 Typed provider instances

Provider instances use specialized resource kinds:

The example assumes provider.durable, provider.kafka, and provider.vault are declared like provider.postgres.

~~~hcl
data_source "house_database" {
  provider = provider.postgres

  require_capabilities = [
    "sql.query/v1",
    "sql.transaction/v1",
  ]
}

execution_engine "durable_tasks" {
  provider = provider.durable

  require_capabilities = [
    "execution.durable/v1",
  ]
}

event_bus "application_events" {
  provider = provider.kafka

  require_capabilities = [
    "events.publish/v1",
    "events.consume/v1",
  ]
}

secret_store "production" {
  provider = provider.vault

  require_capabilities = [
    "secrets.resolve/v1",
  ]
}
~~~

The canonical IR MAY use a common provider-instance envelope, but source references remain kind-specific. An execution_engine cannot satisfy resource_ref("data_source"), and an event_bus cannot satisfy resource_ref("execution_engine").

Capabilities are derived from the locked provider descriptor and validated configuration. An instance may require or narrow capabilities but cannot grant them by assertion.

All provider-instance kinds share lifecycle metadata. Core lifecycle values are managed, external, attached, and ephemeral, but each provider descriptor declares which values are valid for each specialized kind and what provision, update, and deletion responsibilities they imply. For example, an execution_engine may be managed or external only when its provider declares those modes.

### 17.3 Compilation trust boundary

scenery check and scenery compile MUST be offline. They MUST NOT resolve, download, update, or execute dependencies implicitly.

scenery module install, scenery module upgrade, and explicit provider or extension installation commands MAY access registries. Compilation uses only:

- declared workspace-local package paths;
- the verified lockfile;
- immutable content-addressed local cache entries;
- compiler-builtin schemas and functions;
- signed or integrity-verified declarative schemas and capability metadata;
- declarative lowerings supported by the active compiler profile.

Missing locked content fails with a structured diagnostic and a suggested installation command.

Workspace-local packages are read directly from their declared relative paths, participate in workspace_revision, and are never fetched or substituted from a registry cache.

The compiler MUST NOT:

- load native provider or extension plugins;
- load shared libraries or application binaries;
- launch provider-controlled subprocesses;
- execute runtime, deployment, or migration provider code;
- grant network, environment, clock, randomness, process, thread, or ambient filesystem access.

A provider package separates its compile descriptor from runtime, deployment, and migration implementations. Compile-descriptor and declarative-lowering digests participate in contract dependency identity. Runtime and provider ABI identities participate in implementation_revision.

A future sandboxed-extension profile MAY permit deterministic, locked bytecode or WASM. Such a profile MUST expose only declared content-addressed read-only inputs, enforce CPU/instruction, memory, and output limits, and provide no ambient capabilities. It is not part of scenery.compiler-core/v1.

### 17.4 Language extensions

An extension adds versioned resource schemas, codecs, protocols, or declarative lowerings:

~~~hcl
extension "maps" {
  source  = "registry.scenery.dev/geo/maps"
  version = ">= 1.4.0, < 2.0.0"
}

resource "maps.roof_model" "production" {
  config = {
    model_path = "models/roofmapnet"
  }
}
~~~

The generic resource block is allowed only for a registered extension kind. Its config is fully schema-validated and lowers to a versioned resource kind.

Extensions MUST NOT:

- alter core syntax or reinterpret a core block;
- override core resource kinds, codecs, diagnostics, or reference roots;
- add ambient evaluation roots;
- disable type checking;
- execute native code during compilation;
- read undeclared files or environment state.

scenery.compiler-core/v1 permits only compiler-builtin functions. Executable extension functions require a separately negotiated sandboxed-extension profile.

An unavailable extension is a compilation error, not an ignored block. A descriptor or lowering failure produces a provider diagnostic and MUST NOT corrupt the compiler process.

The 0.4-draft toolchain recognizes `extension` and generic `resource` as edition-defined syntax but does not claim `scenery.declarative-extensions/v1`; it emits `SCN7001 unsupported_profile` with the unavailable profile identity in structured details rather than `unknown_resource` or `SCN1002`.

## 18. Deployment and environment configuration

Application contracts are environment-independent. A deployment supplies only values whose schemas explicitly permit deployment binding.

~~~hcl
deployment "production" {
  environment = "production"

  module {
    target = module.house

    inputs = {
      process_concurrency   = 16
      roof_eval_concurrency = 2
    }
  }

  data_source {
    target = data_source.house_database

    config = {
      database = "house_production"
      region   = "eu-central-1"
    }
  }

  service {
    target = module.house.service

    replicas = 3

    resources {
      cpu    = "2"
      memory = "4GiB"
    }
  }

  http_gateway {
    target = http_gateway.public_api

    listener {
      host = "api.onlv.dev"
      port = 443
      tls  = "required"
    }
  }
}
~~~

### 18.1 Deployment overlays

A deployment overlay MAY set:

- module inputs declared with phase = "deployment";
- provider-instance configuration fields marked deployment_bindable;
- service placement, replica, and runtime-resource fields;
- HTTP gateway host, address, port, TLS, certificate reference, and platform-listener fields;
- sensitive deployment inputs using secret references.

It MUST NOT change:

- operation, record, enum, or union contracts;
- HTTP gateway exposure/base path, binding paths, methods, codecs, binding exposure narrowing, or public policy identity;
- resource addresses or module topology;
- implementation-phase inputs;
- package, provider, or extension versions.

Overlay precedence is package default, application baseline input, then selected deployment value. Every effective deployment value retains provenance.

The contract graph retains deployment input declarations, types, constraints, and defaults as symbolic slots. Selecting a deployment creates a separate resolved deployment projection; it does not mutate source, effective, or expanded contract views.

A deployment MAY rebind a deployment-phase module input to another predeclared instance of the same specialized kind when the replacement satisfies every required capability. Direct provider swapping inside an instance is forbidden unless that provider schema marks the field deployment_bindable and declares the swap compatible.

Provider schemas validate overlays after substitution. Unknown or non-bindable fields are errors.

Every deployment overlay block has one typed target reference. The target resolves to exactly one installed module, root provider instance, or exported service, so multiple module instances cannot collide by local label. String addresses are not accepted in source. Missing targets, duplicate overlays for one field, type or constraint failures, and ambiguous targets are errors.

All installed resources remain in source, effective, and expanded contract views and are active in the initial deployment profile. There is no universal enabled attribute in edition 2027. Conditional activation is reserved for a later typed activation profile.

Concurrency, limits, provider endpoints, database names, regional placement, and model locations are ordinary deployment configuration, not secrets.

### 18.2 Secrets

Secrets are references resolved through a secret_store:

~~~hcl
secret "roof_provider_token" {
  store = secret_store.production
  key   = "house/roof-provider-token"
}
~~~

Secret plaintext MUST NOT appear in:

- source;
- the canonical manifest;
- semantic diffs;
- diagnostics;
- plans;
- generated documentation;
- logs.

The compiler tracks secret taint. A secret flowing into a non-sensitive field is an error.

### 18.3 No ambient environment access

There is no env function. Environment variables may be consumed only by a deployment or provider adapter whose schema declares the mapping.

This keeps compilation reproducible and makes required configuration discoverable.

## 19. Compilation model

Compilation has ordered semantic phases:

1. Parse source files.
2. Load the selected edition.
3. Resolve declared workspace-local packages directly and registry packages, providers, and extensions strictly from the verified lockfile and local immutable cache.
4. Build package scopes.
5. Type-check attributes, expressions, inputs, exports, and references.
6. Instantiate modules into namespaces.
7. Apply effective defaults, then, when scenery.patches/v1 is active, apply allowed exact patches against those defaulted values.
8. Expand declarative resources such as CRUD.
9. Validate the whole resource graph.
10. Produce canonical IR, provenance, diagnostics, and contract_revision.
11. Generate required scenerycontract packages into an in-memory or temporary filesystem overlay.
12. Load and type-check Go implementations against that overlay using the declared go_target.
13. Validate constructors, lifecycle hooks, handlers, outcomes, and capability interfaces.
14. Generate application adapters into the same overlay.
15. Type-check the implementation, generated contracts, adapters, and composition root together.
16. For check, discard the overlay. For generate or build, atomically materialize only verified generated artifacts.
17. Optionally generate clients, schemas, and documentation from the valid contract graph.
18. Compute workspace_revision from the final managed bytes for this command.
19. When a verified build-input manifest or artifact is available, produce implementation_revision.
20. When a deployment is selected and planned, produce deployment_revision.

No code generation step may change the semantic graph. scenery check MUST NOT mutate source or managed generated roots.

Contract compilation and implementation verification are distinct states. A valid contract graph may produce a contract manifest, schemas, and clients even when implementation verification fails. Such a command still fails overall and cannot produce a runtime bundle, but diagnostics use implementation categories and preserve the valid contract_revision. A contract-language failure produces no valid manifest.

workspace_revision is still computed for a readable workspace when parsing or semantic validation fails.

### 19.1 Graph views

Tools MUST expose three views:

| View | Contents |
|---|---|
| source | Resources and values directly authored before input substitution, defaults, inheritance, patches, and expansion; authored `var.*` and export expressions remain visible |
| effective | Module inputs, defaults, inheritance, and exact patches applied |
| expanded | All effective resources plus generated resources such as CRUD operations |

Each later view retains links to earlier origins.

### 19.2 Whole-graph validation

Validation includes at least:

- unique addresses;
- reference existence and type compatibility;
- module input and export correctness;
- operation handler conformance;
- binding input completeness;
- binding response coverage;
- route conflicts within each HTTP gateway and mixed-frontend ownership conflicts;
- execution compatibility;
- policy availability;
- pipeline ordering;
- provider-instance capability satisfaction;
- schedule validity;
- phase-flow and deployment-overlay validity;
- provider and extension availability;
- secret-flow safety;
- generated-name collisions.

### 19.3 Legacy compatibility frontend

When scenery.legacy-bridge/v1 is active, a bounded legacy-v0 frontend and the edition-2027 frontend both produce candidate canonical resources. A linker applies the explicit ownership manifest and produces one merged active graph. There is never a second legacy runtime.

The bridge guarantees one active owner per address, HTTP gateway/method/path, service lifecycle, durable external identity/revision, schedule identity, schema/migration owner, and generated client contract. Shadow candidates are comparison-only and never enter runtime generation. Legacy behavior remains legacy_exact until an explicit, revision-checked ownership activation changes it.

SCENERY_LEGACY_BRIDGE_V1.md defines source discovery, legacy lowering, opaque/advisory contracts, shadow comparison, compatibility Go adapters, activation, rollback, provenance, commands, and retirement.

## 20. Canonical intermediate representation

The compiler emits a canonical manifest independent of Scenery source syntax.

### 20.1 Manifest envelope

~~~json
{
  "api_version": "scenery.manifest.v1",
  "edition": "2027",
  "diagnostic_catalog": "scenery.diagnostics.2027.v1",
  "application": {
    "name": "clean_tech",
    "version": "1.0.0"
  },
  "profiles": [
    "scenery.compiler-core/v1"
  ],
  "contract_revision": "sha256:...",
  "resources": [],
  "source_map": {},
  "diagnostics": []
}
~~~

### 20.2 Resource envelope

Every resource uses the same envelope:

~~~json
{
  "address": "house/operation/process_scene",
  "kind": "scenery.operation/v1",
  "name": "process_scene",
  "module": "house",
  "spec": {
    "service": {
      "$ref": "house/service/house"
    },
    "input": {
      "$ref": "house/record/process_scene_input"
    },
    "results": {
      "processed": {
        "type": {
          "$ref": "house/record/process_scene_result"
        }
      }
    }
  },
  "origin": {
    "kind": "authored",
    "source_id": "src_..."
  }
}
~~~

References in IR MUST be structured objects, not ambiguous strings.

### 20.3 Revision projections

Every resource-schema field declares one revision domain: contract, implementation, deployment, or workspace_only. Sensitivity is orthogonal to revision domain; secret plaintext is excluded from every projection.

#### 20.3.1 Contract revision

contract_revision identifies the canonical application contract. Its projection contains:

- language edition and stable application identity; application.version is release provenance and is excluded;
- expanded resources whose schemas participate in the contract projection, including only their addresses, kinds, and fields marked contract;
- compile-time provider and extension schema/lowering identities that affect those contract fields;
- declared conformance-profile requirements.

It excludes implementation locators and bytes, runtime/provider ABI identities, deployment values and plans, source maps, diagnostics, origins, provenance, comments, formatting, absolute paths, and contract_revision itself.

Changing a Go handler body or symbol without changing a contract field MUST NOT change contract_revision. Changing an HTTP gateway/base path, route path, operation type, result variant, binding codec, exposure, or contract policy MUST change it.

Generated clients, public schemas, and API documentation are keyed primarily by contract_revision.

#### 20.3.2 Implementation revision

implementation_revision identifies a buildable runtime implementation. Its projection contains:

- contract_revision;
- application.version as release provenance;
- fields marked implementation, including Go package, constructor, and handler bindings;
- package_contract_abi_revision for every linked Go contract package;
- a content-addressed implementation artifact or build-input-manifest digest supplied by the build system;
- generated adapter digest and runtime ABI identity;
- locked runtime/provider ABI identities;
- resolved go_toolchain and go_target fields, including tags, CGO, experiments, architecture, flags, and native tool/input identities.

The language compiler MUST NOT discover or approximate an arbitrary Go build graph. When no build-supplied artifact or input manifest is available, implementation_revision is unavailable and represented as null in interfaces that require a field.

Deployable binaries and runtime bundles are keyed by implementation_revision.

#### 20.3.3 Workspace revision

workspace_revision identifies exact managed workspace bytes. Its projection contains normalized relative paths and exact bytes for:

- application and package .scn files;
- scenery.lock.scn;
- patch inputs;
- files matched by implementation_root revision_include/revision_exclude rules and explicit revision_input declarations;
- accepted migrations and migration ledger;
- managed generated descriptors and exactly their covered artifacts.

Entries sort by normalized path and are length-framed. Formatting, comments, Go-body edits, and generated-client byte changes alter workspace_revision even when other revisions remain unchanged.

Workspace membership follows Section 3.5. Symlinks, files outside declared roots, VCS internals, and ambient build caches are excluded.

Source-mutation preconditions always include workspace_revision.

#### 20.3.4 Deployment revision

deployment_revision identifies one resolved deployment. Its projection contains:

- implementation_revision;
- selected deployment address;
- resolved non-secret deployment-domain values;
- target platform identity;
- canonical provider-plan digests;
- stable secret references or version handles when required, never secret plaintext.

Changing replicas, region, deployment-phase concurrency, provider runtime plans, or the implementation changes deployment_revision. A deployment revision is unavailable until both implementation_revision and a provider plan exist.

#### 20.3.5 Revision-domain rules

Each projection is canonically encoded and hashed with SHA-256 after a distinct domain prefix:

~~~text
scenery.contract-revision.v1\0
scenery.implementation-revision.v1\0
scenery.workspace-revision.v1\0
scenery.deployment-revision.v1\0
~~~

Revision domains are not inferred from field names. A resource schema that omits a domain for a field is invalid.

Each revision is emitted as sha256: followed by lower-case hexadecimal SHA-256 of the domain prefix concatenated with canonical projection bytes.

Contract, implementation, and deployment projections use the canonical JSON encoding in Section 20.4. Workspace entries use an unsigned 64-bit big-endian path-byte length, UTF-8 normalized relative path bytes, unsigned 64-bit big-endian content length, then exact content bytes.

Expected behavior:

| Change | Contract | Implementation | Workspace | Deployment |
|---|---|---|---|---|
| Comment or formatting | same | same | changes | same |
| Go handler body | same | changes | changes | changes |
| HTTP path | changes | changes | changes | changes |
| Unconsumed generated projection bytes only | same | same | changes | same |
| Replica count | same | same | changes | changes |
| Provider runtime version only | same unless contract schema changes | changes | changes | changes |

The generated-projection row applies only when no deployed artifact consumes that projection. If a frontend or another target imports a generated client, its build-input manifest changes and therefore the relevant implementation_revision and deployment_revision change. Edition 2027 defines one application implementation revision; future deployment profiles MAY add per-target revisions without changing contract_revision semantics.

### 20.4 Canonical encoding

Canonical encoding MUST:

- sort resources by address;
- sort semantically unordered maps by key;
- preserve explicitly ordered sequences;
- omit presentation-only formatting;
- preserve ordinary string values as their exact valid Unicode scalar sequence;
- normalize only language identifiers and types whose specifications explicitly define normalization, such as relative_path and url;
- exclude absolute machine-local paths;
- use RFC 8785 JSON Canonicalization Scheme bytes after Scenery exact scalars have been transformed to their tagged JSON forms below.

Ordinary strings, wire values, regex patterns, opaque provider keys, SQL identifiers, query/code fragments, foreign-system identifiers, and defaults of type string MUST NOT be NFC-normalized or otherwise rewritten. Language identifiers are already restricted to ASCII. A provider field may define another canonical string type explicitly, but an ordinary string schema cannot acquire normalization implicitly.

Canonical JSON strings are valid Unicode only; unpaired surrogates and invalid UTF-8 are errors. Quotation mark, reverse solidus, and U+0000 through U+001F are escaped exactly as RFC 8785 requires, using the short JSON escapes where defined and lower-case hexadecimal in other \u escapes. Solidus is not escaped. Non-ASCII scalars, including U+2028 and U+2029, are emitted directly as UTF-8. Object property ordering follows RFC 8785's UTF-16 code-unit ordering. No insignificant whitespace is emitted.

Exact semantic scalar values use tagged objects rather than unsafe JSON numbers:

~~~json
{
  "large_integer": {
    "$scalar": "int",
    "value": "9007199254740993"
  },
  "price": {
    "$scalar": "decimal",
    "coefficient": "-12345",
    "scale": "2"
  },
  "timeout": {
    "$scalar": "duration",
    "nanoseconds": "2400000000000"
  },
  "capacity": {
    "$scalar": "size",
    "bytes": "2147483648"
  },
  "identifier": {
    "$scalar": "uuid",
    "value": "018f47a2-6f45-7c4a-8b31-4cbbe3c99a22"
  },
  "day": {
    "$scalar": "date",
    "value": "2027-03-14"
  },
  "instant": {
    "$scalar": "datetime",
    "value": "2027-03-14T09:15:30.123Z"
  },
  "site": {
    "$scalar": "url",
    "value": "https://example.com/api"
  },
  "model": {
    "$scalar": "relative_path",
    "value": "models/roofmapnet"
  },
  "digest": {
    "$scalar": "bytes",
    "base64url": "AQIDBA"
  }
}
~~~

Signed decimal strings have no leading plus, no unnecessary leading zero, and normalize negative zero to zero. Decimal removes trailing coefficient zeros while reducing scale, except zero always has coefficient "0" and scale "0". Metadata integers whose schemas are bounded within the JSON safe-integer range MAY remain JSON numbers.

Bool and string retain native JSON forms. In schema-erased values, every other primitive except json uses its schema-defined tagged form so mutation, diff, and precondition consumers cannot confuse it with a string or unsafe number. json retains its canonical JSON value under the enclosing schema; when embedded in a schema-erased value it uses {"$scalar":"json","value":...}.

Equivalent source literals MUST produce identical canonical scalar objects and hashes. The same representation is used in manifests, semantic diffs, normalized mutation values, and preconditions.

Generated language structures use canonical semantic ordering. Unordered sibling resources sort by address. Unordered named children—including record fields, enum values, union variants, operation results, operation errors, service dependencies, and named response mappings—sort by semantic lower_snake_case name before generator-specific identifier conversion. Ordered constructs such as pipeline steps and declared tuple elements retain semantic order. Wire names and source positions never determine generated declaration order.

### 20.5 Source map

Source ranges are identified by opaque source IDs in portable output. A local tool may resolve them to paths.

The source map includes:

- declaration ranges;
- attribute ranges;
- default and expansion origins;
- module instantiation chains;
- patch provenance.

Absolute local paths MUST NOT appear in distributable manifests.

Distributable source maps MUST NOT contain source excerpts or comments. A local, explicitly requested debug sidecar MAY contain them after secret redaction.

Ranges are start-inclusive and end-exclusive. Lines and columns are zero-based Unicode scalar positions, and every position also includes a zero-based UTF-8 byte offset. Columns count combining marks as their own Unicode scalars rather than display grapheme clusters. A source ID is collision-resistant, opaque, unique within one workspace revision, and maps to a normalized relative URI; it MUST NOT be derived by lossy punctuation or path-separator replacement.

### 20.6 Expansion lineage

Generated resources carry:

- generator address;
- generator schema revision;
- deterministic expansion key;
- source declaration range;
- parent resource address.

An expanded resource address MUST remain stable as unrelated generated resources are added.

## 21. Command-line contract

The CLI is a first-class language interface.

### 21.1 Command namespaces

Command ownership is exact:

| Profile | Required commands |
|---|---|
| scenery.compiler-core/v1 | scenery fmt, check, compile, schema |
| scenery.inspection-core/v1 | scenery list, get, explain |
| scenery.agent-read/v1 | scenery graph, diff, agent serve --stdio |
| scenery.agent-mutation/v1 | scenery changes plan, changes apply, changes rename |
| scenery.deployment/v1 | scenery deploy plan, deploy apply |
| scenery.legacy-bridge/v1 | scenery migrate init, status, service, compare, activate, verify, finish |
| future migration profile | scenery migration plan, migration apply |
| future registry profile | scenery module install, module upgrade |

Profile requirements in Section 26 determine which commands a conforming tool MUST implement. Top-level plan, apply, rename, and serve commands are not part of edition 2027.

### 21.2 Formatting

~~~text
scenery fmt [PATH...]
scenery fmt --check [PATH...]
~~~

Formatting is deterministic and idempotent. Running it twice MUST produce no additional changes.

The formatter preserves comments and associates them with semantic syntax nodes. It MUST NOT reorder explicitly ordered blocks such as pipeline steps.

### 21.3 Checking and compilation

~~~text
scenery check -o human
scenery check -o json
scenery compile --view expanded -o json
~~~

check performs semantic validation without generating deployable artifacts. compile emits the canonical manifest even when code generation is disabled.

With -o json, compile returns one scenery.cli.v1 envelope. data.contract_status is valid or invalid and data.implementation_status is valid, invalid, unavailable, or not_requested. A valid language contract places its manifest at data.manifest and its revision at contract_revision even when implementation verification fails. A contract-language error sets contract_revision and data.manifest to null. workspace_revision remains available when the source snapshot is readable. A requested recovery graph appears at data.partial_graph with deployable set to false; it is never a manifest.

### 21.4 Resource inspection

~~~text
scenery list operation --module house -o json
scenery get house/operation/process_scene --view effective -o json
scenery explain house/operation/process_scene --provenance -o json
scenery schema scenery.operation/v1 -o json
scenery graph house/operation/process_scene --direction both -o json
~~~

get returns the resource. explain includes effective values, defaults, inputs, expansion lineage, and provenance. graph returns a bounded dependency subgraph.

### 21.5 Semantic diff

~~~text
scenery diff --semantic BASE TARGET [--rename-receipts CHANGE_PLAN_OR_RECEIPT.json] -o json
~~~

The CLI command, change-plan responses, and the agent operation revisions.diff MUST return the same versioned scenery.semantic-diff/v1 value. It contains base and target manifest or retained-revision identities, graph view, compatibility-profile identity, ordered typed changes, explicit rename evidence, per-dimension classifications, generated consequences, and risk records. Changes sort by resource address, schema path, then operation kind.

A semantic diff reports resource changes, not line changes. It distinguishes:

- added and removed resources;
- proven address renames;
- compatibility changes by dimension;
- binding and route changes;
- execution-policy changes;
- policy changes;
- deployment-only changes;
- generated consequences.

Text diffs MAY be included as secondary information.

Diff output names a versioned compatibility profile and reports separate source, wire, storage, runtime, and deployment classifications. A classification is compatible, breaking, or unknown. A dimension without normative rules MUST be unknown; tooling may not guess that it is compatible.

A rename is reported as semantic fact only when an explicit rename receipt links the old and new addresses, names the exact base and target contract revisions, and carries the valid canonical receipt digest. Applied change receipts are retained under `.scenery/changes/applied/`; diff loads them automatically when a compared reference is that app root, while `--rename-receipts` supplies a plan or receipt explicitly. A stale, malformed, or fabricated receipt is ignored and the diff reports removal and addition. Heuristic rename candidates MAY be reported separately and MUST be marked unproven.

### 21.6 Planning and applying changes

~~~text
scenery changes plan --changes changes.json \
  --base-workspace-revision WREV \
  --base-contract-revision CREV_OR_NONE \
  --out plan.json
scenery changes apply plan.json \
  --expect-workspace-revision WREV \
  --expect-contract-revision CREV_OR_NONE \
  -o json
~~~

plan MUST NOT write source files. apply MUST use the atomic transaction and revision rules in Section 22.

### 21.7 Streams and output

Machine-readable output goes to stdout. Human progress and logs go to stderr.

Every command supporting human output MUST also support:

- -o json for one JSON document;
- -o jsonl for streams where applicable;
- --non-interactive;
- --quiet.

Prompts are forbidden in non-interactive mode.

Each -o json invocation writes exactly one scenery.cli.v1 envelope. Each -o jsonl line is a scenery.cli.event.v1 envelope containing a monotonically increasing sequence, available revision fields, event kind, and payload. A JSONL stream ends with exactly one terminal summary event.

### 21.8 Exit status

Core exit statuses are:

| Status | Meaning |
|---:|---|
| 0 | Success with no error diagnostics |
| 1 | Command completed successfully but a requested predicate was false |
| 2 | Invalid source, request, or command usage |
| 3 | Revision conflict or failed precondition |
| 4 | Required provider, extension, or capability unavailable |
| 5 | Permission or required approval denied |
| 10 | Internal tooling failure |

Warnings do not change status unless warnings-as-errors is selected.

fmt --check returns 1 when formatting differs. diff returns 0 whether or not differences exist unless --exit-code is supplied; with --exit-code it returns 1 when differences exist.

### 21.9 Machine envelope

JSON commands use a stable envelope:

~~~json
{
  "api_version": "scenery.cli.v1",
  "diagnostic_catalog": "scenery.diagnostics.2027.v1",
  "ok": true,
  "workspace_revision": "sha256:...",
  "contract_revision": "sha256:...",
  "implementation_revision": null,
  "deployment_revision": null,
  "data": {},
  "diagnostics": []
}
~~~

Unknown envelope versions MUST be rejected by clients.

ok is false exactly when effective error diagnostics exist or the requested operation failed. Severity values are error, warning, information, and hint.

The transport-neutral agent error kinds are invalid_request, revision_conflict, failed_precondition, capability_unavailable, permission_denied, and internal. The CLI maps them to statuses 2, 3, 3, 4, 5, and 10 respectively.

## 22. Agent interface

A tool claiming scenery.agent-read/v1 or scenery.agent-mutation/v1 exposes the corresponding semantic capabilities through a machine API suitable for JSON-RPC, MCP, or an equivalent local protocol. The transport may differ; method semantics may not.

### 22.1 Required discovery operations

| Operation | Purpose |
|---|---|
| capabilities | Negotiate API, edition, resource, mutation, and extension support |
| schema.get | Get a resource, value, diagnostic, or mutation schema |
| resources.list | List addresses with kind and summary filters |
| resources.get | Read selected resources in a chosen graph view |
| resources.explain | Read effective value and provenance |
| graph.get | Get a bounded dependency or dependent closure |
| revisions.diff | Compare two available semantic graph snapshots |
| diagnostics.get | Get structured diagnostics |
| context.get | Get a task-focused context bundle |

capabilities MUST return exact profile versions, editions, resource-schema revisions, codec profiles, mutation operations, and transport limits. Agents MUST NOT infer support from edition alone.

revisions.diff is the transport-neutral equivalent of scenery diff --semantic. Its request identifies BASE and TARGET by an available contract_revision, deployment_revision, immutable plan snapshot, or supplied canonical manifest, plus the requested compatibility profile and dimensions. It MAY include `rename_receipts`; an app-local agent server also loads matching applied receipts from its retained change state. Its response uses the classifications and rename-evidence rules in Section 21.5. Implementations MUST report unavailable snapshots as failed_precondition and MUST NOT silently substitute the current graph.

### 22.2 Agent-mutation operations

These operations are required only by scenery.agent-mutation/v1:

| Operation | Purpose |
|---|---|
| changes.plan | Validate semantic changes without writing |
| changes.apply | Atomically apply an accepted plan |
| resource.create | Construct a typed resource change |
| resource.delete | Delete a resource with dependency checks |
| resource.rename | Rename an address and update typed references |
| value.set | Set a typed attribute or block value |
| value.unset | Remove an optional value |
| module.configure | Change typed module inputs |
| module.upgrade | Resolve, migrate, and plan a module upgrade |

These may be separate protocol methods or typed operations inside one plan request.

changes.apply is the only required operation that writes source. The other mutation operations construct, normalize, or validate change objects for a plan.

### 22.3 Context bundles

context.get MUST support bounded, task-oriented retrieval:

~~~json
{
  "focus": [
    "house/operation/process_scene"
  ],
  "include": [
    "dependencies",
    "dependents",
    "schemas",
    "diagnostics",
    "provenance"
  ],
  "depth": 2,
  "max_resources": 100,
  "max_bytes": 200000,
  "view": "effective"
}
~~~

The response states truncation and supplies continuation tokens. Ordering is deterministic.

A continuation token is opaque, query-bound, workspace-revision-bound, contract-revision-bound, and expiring. It resumes the same deterministic snapshot and ordering. If that snapshot is unavailable, the server returns failed_precondition rather than continuing against newer state.

This lets an agent obtain the smallest sufficient semantic context instead of reading the entire repository.

### 22.4 Semantic change format

A mutation targets an address and schema path:

~~~json
{
  "op": "value.set",
  "address": "house/execution/process_scene_durable",
  "view": "source",
  "path": "/spec/retry/maximum",
  "value": {
    "$scalar": "duration",
    "nanoseconds": "180000000000"
  },
  "precondition": {
    "equals": {
      "$scalar": "duration",
      "nanoseconds": "120000000000"
    }
  }
}
~~~

Paths are RFC 6901 JSON Pointers over the explicitly named graph view and address schema fields, not source line numbers. Values are typed. An operation may bind `expected_kind` and `expected_schema_revision`; planning validates those identities before simulation, and normalized returned operations always include the resolved values. Unknown paths, stale schema expectations, and type mismatches fail during planning.

The source view is directly writable. Expanded-only resources are read-only. Setting a defaulted effective value either creates a schema-authorized source override with clear provenance or fails. A patch-derived value must be changed through its patch unless its schema explicitly allows a higher-precedence override.

Preconditions are evaluated against canonical typed values at the base workspace_revision and the base contract_revision when one exists. They support at least exists, absent, and equals. Equality uses canonical type semantics, not presentation syntax.

Semantic mutation APIs MUST NOT accept secret plaintext. They accept only typed secret references or provider handles. Redaction markers are display-only and cannot be planned or applied.

### 22.5 Plan and apply

A normal plan request MUST include base workspace_revision and contract_revision.

When compilation has no valid contract_revision, a repair request sets base contract_revision to null and is bound only to workspace_revision. It may contain diagnostic machine fixes or source-view CST/schema edits, but cannot target effective or expanded resources that require a valid contract. Planning simulates all edits as one transaction. It returns an applicable plan only when the resulting source compiles to a complete valid graph, in which case predicted contract_revision is non-null. If the edits leave any effective error, planning returns failed_precondition with the resulting diagnostics and predicted source edits for inspection, but no plan ID and nothing that changes.apply can commit. An agent may combine several fixes into one repair request; partial invalid-to-less-invalid commits are not supported.

Successful planning returns:

- plan ID;
- base workspace_revision;
- base contract_revision, possibly null for repair;
- predicted workspace_revision;
- predicted contract_revision, always non-null for an applicable plan;
- implementation_revision and deployment_revision invalidation status;
- normalized semantic operations, each with mandatory resolved `expected_kind`, `expected_schema_revision`, and `view: "source"` fields;
- revision-bound rename receipts containing old/new addresses and their digest;
- semantic diff;
- affected resources;
- diagnostics;
- concrete source edits;
- formatting effects;
- required approvals or capability changes;
- structured risk records.

Applying a plan MUST:

- require the expected base workspace revision;
- require the expected base contract revision, including expected null for repair;
- be atomic across files;
- revalidate preconditions;
- preserve comments where possible;
- format changed files;
- compile and validate the resulting graph;
- return the actual workspace_revision, contract_revision, invalidation status, and receipt.

For a repair plan, successful compilation establishes the first post-repair contract_revision. A repair apply cannot succeed with a null actual contract_revision.

The transaction includes every planned source, lockfile, migration proposal, and generated-artifact write. Tooling stages all bytes, formats source, compiles, verifies generated artifacts, and validates the complete graph before committing any file. Any failure leaves the prior workspace byte-for-byte unchanged.

On success, actual contract_revision MUST equal predicted contract_revision and actual workspace_revision MUST equal predicted workspace_revision. A mismatch is an internal failure and commits nothing.

A source-only change plan MUST NOT invent a predicted implementation_revision or deployment_revision. It marks each as unchanged, invalidated, or unavailable. A build or deployment planner computes replacements in the appropriate phase.

If either base revision changed, the agent API returns revision_conflict and the CLI exits with status 3. It MUST NOT attempt a best-effort merge.

A plan is immutable and bound to one application, normalized-operation digest, rename receipts, both base revisions, negotiated capability set, caller identity, required approvals, concrete source/provider actions, and expiry. Presentation-equivalent contextual and tagged scalar values, and source-local versus canonical references, normalize before the operation digest is computed. Applying an expired or already-applied plan fails without writing.

The plan ID MAY remain a domain-separated content digest, but that public digest is not proof that a trusted planner issued the supplied object. Before trusting expiry, approvals, operations, source edits, provider actions, or predicted revisions, apply MUST authenticate issuance by either loading the exact canonical plan retained in trusted app-local state under that ID or verifying an issuer signature/MAC with key material unavailable to callers. If the caller supplies the full plan, it MUST match the authenticated canonical plan exactly; decoding rejects unknown fields and trailing values rather than normalizing an expanded caller object into the trusted shape. Missing issuance state, a changed field, or a caller-recomputed ID fails before staging. Change, deployment, migration initialization/candidate/transition/finish, and every future approval-bearing plan family use the same rule.

An approval-bearing migration transition MUST be serializable as the exact issued plan and independently applicable after approval. `scenery migrate ... --dry-run --out <plan>` retains that object and `scenery migrate apply <plan>` applies it; apply rejects planning-only flags, including `--dry-run` and `--evidence`. Rerunning the planning command creates a distinct expiry-bound plan and MUST NOT be presented as applying the earlier plan.

If required_approvals is non-empty, apply MUST reject missing or invalid approval tokens. A token is bound to the plan digest, caller, approved risk scopes, and expiry.

### 22.6 Rename

Rename is semantic:

~~~text
scenery changes rename house/operation/process_scene process_roof_scene --dry-run -o json
~~~

It updates:

- typed references;
- exports;
- generated address lineage;
- documentation references represented as semantic links.

It does not update arbitrary strings, wire names, routes, task names, or database names unless separately requested.

The resulting plan and persisted apply receipt record the old address, new address, base and target contract revisions, and a domain-separated receipt digest so later diffs can prove the rename. Rename traverses every typed reference inside attributes, including object/list/function expressions, exports, module input maps, and the separately parsed migration manifest; it never rewrites lookalike strings. Renaming a containing module instance changes every descendant address, so planning MUST derive a revision-bound receipt for each descendant matched through stable declaration/package origin and the old/new module chains. If one physical package declaration is instantiated more than once, a mutation targeting only one instance address MUST fail instead of silently renaming every instance; the caller must edit or refactor the shared declaration explicitly. External durable names are unchanged unless separately requested.

### 22.7 Agent expectations

An agent changing a Scenery application SHOULD:

1. Negotiate capabilities.
2. Read the relevant schema.
3. Fetch a bounded effective or expanded context.
4. Include current workspace_revision and contract_revision in its plan.
5. Use semantic operations rather than textual search-and-replace.
6. Inspect semantic diff and diagnostics.
7. Apply atomically.
8. Verify returned workspace_revision and contract_revision with check.

When semantic tooling is unavailable, an agent MAY edit .scn source directly, but it MUST run scenery fmt and scenery check before claiming success.

### 22.8 Server mode

A CLI claiming scenery.agent-read/v1 MUST provide:

~~~text
scenery agent serve --stdio
~~~

Server mode keeps schemas and the graph cached, watches source revisions, and exposes the agent interface without changing its semantics.

Stdio mode inherits local process identity. Any network transport MUST authenticate and authorize callers. Every mutation transport MUST confine reads and writes to the declared workspace using symlink-safe path resolution and MUST reject path escape.

## 23. Diagnostics

Diagnostics are part of the language contract.

### 23.1 Diagnostic shape

~~~json
{
  "code": "SCN2101",
  "severity": "error",
  "message": "operation input field scene_id has no HTTP mapping",
  "address": "house/binding/get_scene",
  "path": "/spec/http/input",
  "range": {
    "source_id": "src_...",
    "start": {
      "line": 18,
      "column": 3,
      "byte_offset": 412
    },
    "end": {
      "line": 31,
      "column": 4,
      "byte_offset": 781
    }
  },
  "related": [
    {
      "address": "house/record/get_scene_input",
      "path": "/spec/fields/scene_id"
    }
  ],
  "suggestions": [
    "map {scene_id} with a path_parameter block"
  ],
  "fixes": [
    {
      "title": "Add scene_id path mapping",
      "operations": []
    }
  ]
}
~~~

### 23.2 Guarantees

Diagnostic codes:

- are scoped to the language edition and advertised diagnostic-catalog version;
- are never reused for an unrelated condition;
- are documented with machine-readable metadata;
- include a resource address and schema path whenever available.

Within one diagnostic-catalog major version, a code's meaning, structured fields, and default severity MUST NOT change incompatibly. Diagnostics are transport-independent.

A message is for humans. Agents MUST branch on code and structured fields, not exact message text.

`schema.get` with `kind = "scenery.diagnostics.2027.v1"` returns the complete checked-in catalog. Supplying one catalog code such as `SCN2101` returns that definition. Every definition contains its code, category, stable identity and meaning, default severity, structured fields, and documentation. Every emitted code MUST be declared exactly once. An internal diagnostic carries a separate opaque `report_token`; its public message is stable and sanitized rather than the raw internal cause.

### 23.3 Core categories

| Range | Category |
|---|---|
| SCN1000-SCN1099 | Syntax and unknown language elements |
| SCN1100-SCN1199 | Duplicate identity and address errors |
| SCN1200-SCN1299 | Reference and type errors |
| SCN1300-SCN1399 | Evaluation-phase and determinism errors |
| SCN2000-SCN2199 | Operation and binding contract errors |
| SCN2200-SCN2399 | Execution, schedule, and delivery errors |
| SCN2400-SCN2499 | Binding and CLI contract errors |
| SCN2500-SCN2599 | Data profile errors |
| SCN2600-SCN2699 | UI profile errors |
| SCN2700-SCN2799 | Event profile errors |
| SCN2800-SCN2899 | Deployment profile errors |
| SCN2900-SCN2999 | Patch profile errors |
| SCN3000-SCN3199 | Package, module, and export errors |
| SCN3200-SCN3399 | Provider instance, entity, and extension errors |
| SCN3400-SCN3499 | Go service configuration errors |
| SCN4000-SCN4199 | Policy, security, and secret-flow errors |
| SCN4200-SCN4299 | Runtime policy and middleware-profile errors |
| SCN5000-SCN5999 | Legacy bridge, migration, and operational-evidence errors |
| SCN6000-SCN6199 | Go implementation ABI and lowering errors |
| SCN6200-SCN6299 | Go verification, generation, and artifact-transaction errors |
| SCN6300-SCN6399 | TypeScript client and codec-generation errors |
| SCN6400-SCN6499 | Compatibility comparison errors and reserved companion diagnostics |
| SCN7000-SCN7099 | Profile availability and conformance errors |
| SCN8000-SCN8099 | CLI and agent request-protocol errors |
| SCN9000-SCN9099 | Internal compiler errors |

An internal compiler error MUST include a report token but MUST NOT expose secrets.

### 23.4 Machine fixes

A diagnostic MAY include one or more fixes. A fix consists of ordinary semantic change operations and preconditions. Applying a fix requires scenery.agent-mutation/v1 and uses the same plan/apply transaction model as any other mutation. When contract_revision is null, the fix uses repair-plan rules from Section 22.5.

## 24. Go implementation ABI and generated artifacts

SCENERY_GO_IMPLEMENTATION_V1.md is normative for scenery.go-implementation/v1. This section summarizes the integration points required by the language specification; if wording differs, the companion ABI specification governs Go generation and verification.

### 24.1 ABI identity

The normative unary Go boundary is scenery.go-implementation/v1.

A tool claiming this profile MUST implement the package topology, type mappings, constructor, lifecycle, handler, outcome, registration, and verification rules below. It MUST NOT substitute reflection conventions or runtime discovery.

### 24.2 Package topology

For implementation package github.com/example/clean-tech/house, the canonical topology is:

~~~text
github.com/example/clean-tech/house/scenerycontract
<application-module>/internal/scenerygen/<instance-path>adapter
github.com/example/clean-tech/house
~~~

- scenerycontract is module-owned, generated, and imports neither the implementation nor adapter.
- the user implementation imports its module-owned scenerycontract.
- the application-owned adapter imports scenerycontract and the implementation.
- the generated application composition root imports the adapter.
- the implementation MUST NOT import the application-owned adapter.

The path suffix is scenerycontract; its Go package name is the deterministic package name plus contract, such as housecontract.

A published registry module containing a Go implementation MUST publish its matching scenerycontract package or a prebuilt implementation artifact that declares the same contract ABI. The consuming application never generates a contract package under its own internal tree for remote implementation source.

Each module-owned scenerycontract package is keyed by package_contract_abi_revision, not by the consuming application's contract_revision. package_contract_abi_revision is SHA-256 over the canonical package Go-contract projection with the domain prefix scenery.package-contract-abi-revision.v1 followed by a zero byte. The projection contains:

- canonical scenerycontract Go import path, which is part of Go type identity;
- the scenery.go-implementation ABI major;
- generated records, enums, unions, dependency types, inputs, and outcomes reachable from service constructors, lifecycle hooks, handlers, internal generated clients, and explicitly exported Go contract APIs;
- declared capability-interface identities and accepted ABI ranges referenced by those signatures.

It excludes Scenery package semver, registry identity, unreachable private types, module-instance addresses, application bindings, paths, exposure, policies, executions, deployment values, consumer-supplied input values, source provenance, and the application contract_revision. Package identity/version remain artifact provenance in the descriptor; exact bytes remain the artifact digest. package_contract_abi_revision is an ABI-shape fingerprint, not a fifth application/workspace revision.

The projection uses the canonical JSON rules in Section 20.4. The revision is emitted as sha256: followed by lower-case hexadecimal SHA-256 of the domain prefix and projection bytes.

For a local package, the generator computes package_contract_abi_revision from package declarations before instance substitution. For a registry package, publication records it in the signed package descriptor and lockfile and ships the matching scenerycontract artifact. Consumption fails if source metadata, lock metadata, generated package metadata, or implementation artifact disagree. A package whose generated Go shape depends on a module input is invalid under scenery.go-implementation/v1.

scenery.go-implementation/v1 forbids module inputs from changing exported Go ABI shape. Multiple module instances may change paths, policies, constraints, defaults, and deployment values, but they share the package-owned Go types and handlers. A package requiring instance-specific Go shapes needs a different package version/import path or a future parameterized-ABI profile.

The application adapter path derives from the full module-instance and service addresses, so multiple instances do not collide. Alternative output roots MAY be configured, but dependency direction and resolved package paths are recorded in the implementation descriptor. An import cycle is a compilation error.

For a local implementation, the implementation import path is the owning source unit's go_contract.import_path. Its contract import path is that value plus /scenerycontract and MUST forward-map to the declared managed-generated directory. The application adapter import path MUST reverse-map from its deterministic directory beneath the managed-generated adapter root. Both import paths, resolved workspace directories, and the mapping identity are recorded in the implementation descriptor. For a registry implementation, its import paths resolve through the locked package descriptor. Generators MUST NOT derive any location from ambient Go tooling or network state.

Generated package paths derive from stable package and service identities. Generated Go identifiers are deterministic UpperCamel transformations of semantic lower_snake_case names. For example, process_scene becomes ProcessScene and scene_id becomes SceneId.

Operation-scoped declarations are prefixed by the operation name: processed becomes ProcessSceneProcessed and invalid_input becomes ProcessSceneInvalidInput. Enum constants are prefixed by enum type, such as ProcessModeAll. Union wrappers are prefixed by union type. A generated identifier collision after deterministic namespacing is an error; generators MUST NOT silently add unstable suffixes.

### 24.2.1 Staged verification

Go conformance is verified against generated contract and adapter packages in a temporary overlay before any artifact is written. The required order is the staged process in Section 19: contract graph, contract generation, implementation type check, signature validation, adapter generation, combined type check, then discard or atomic materialization. A stale on-disk generated package MUST NOT be used as a substitute for the overlay.

### 24.3 Scenery-to-Go types

Core mappings are:

| Scenery | Go |
|---|---|
| bool | bool |
| int | scenery.Int |
| int32 | int32 |
| int64 | int64 |
| uint32 | uint32 |
| uint64 | uint64 |
| decimal | scenery.Decimal |
| float32 | float32 |
| float64 | float64 |
| string | string |
| bytes | []byte |
| uuid | scenery.UUID |
| date | scenery.Date |
| datetime | scenery.DateTime |
| duration | scenery.Duration |
| size | scenery.Size |
| url | scenery.URL |
| relative_path | scenery.RelativePath |
| json | scenery.JSON |
| list(T) | []T |
| set(T) | scenery.Set[T] |
| map(T) | map[string]T |
| optional(T) | scenery.Optional[T] |
| nullable(T) | scenery.Nullable[T] |

optional(nullable(T)) therefore becomes scenery.Optional[scenery.Nullable[T]]. Pointer heuristics MUST NOT collapse absence and null.

Records become generated structs with exported fields. Enums become generated named types and constants; open enums preserve unknown wire values. Tagged unions become closed generated interfaces with one generated wrapper per variant. Tuples become generated positional structs.

Generated exact-value runtime types MUST round-trip canonical IR without precision loss. Runtime adapters own required defensive copies for mutable byte slices, lists, maps, and JSON values at trust boundaries.

### 24.4 Service constructor and dependencies

A service constructor has exactly this shape:

~~~go
func NewService(
    context.Context,
    housecontract.HouseConstructorInput,
) (*Service, error)
~~~

The returned service may use another exported named pointer type, but the constructor MUST be exported, non-generic, non-variadic, accept exactly context.Context and the generated service-qualified constructor-input value, and return exactly a pointer to one named service type plus error.

Each service receives generated `<ServiceName>Dependencies`, `<ServiceName>Config`, `<ServiceName>Clients`, and `<ServiceName>ConstructorInput` types. This permits several services to share one implementation package without merging or confusing their injection surfaces. For example:

~~~go
type HouseDependencies struct {
    Database datasource.SQL
    Storage  object.Store
}

type HouseConfig struct {
    RoofModelPath      scenery.RelativePath
    ProcessConcurrency uint32
    ProviderToken      scenery.SecretRef
}

type HouseClients struct {
    Audit AuditInternalClient
}

type HouseConstructorInput struct {
    Dependencies HouseDependencies
    Config       HouseConfig
    Clients      HouseClients
}
~~~

Dependency fields expose stable capability interfaces from runtime/provider ABI packages, never concrete provider implementations. Missing or incompatible capabilities fail before constructor invocation.

Generated service and dependency identifiers follow the deterministic collision rules in Section 24.2. Two service labels that map to the same Go identifier in one contract package are an error.

Returning a non-nil error aborts service construction. Returning a nil service with nil error is an implementation-contract failure.

### 24.5 Lifecycle

Lifecycle hooks are declared explicitly:

~~~hcl
service "house" {
  lifecycle {
    start = "Start"
    stop  = "Stop"
  }
}
~~~

Their signatures are:

~~~go
func (s *Service) Start(context.Context) error
func (s *Service) Stop(context.Context) error
~~~

Hooks are exported, non-generic, and non-variadic. Start runs exactly once after dependencies and construction succeed and before any handler call. Services start in dependency order. Stop runs at most once in reverse start order, receives a bounded shutdown context, and is attempted for every successfully started service even when another stop fails. Aggregated shutdown errors are runtime failures and are never operation outcomes.

### 24.6 Unary operation handlers

A handler has exactly this shape:

~~~go
func (s *Service) ProcessScene(
    ctx context.Context,
    input housecontract.ProcessSceneInput,
) (housecontract.ProcessSceneOutcome, error)
~~~

Handlers are exported, non-generic, and non-variadic. Cancellation and deadlines flow through context.Context. A durable retry may invoke the handler more than once.

The generated contract package defines a closed outcome:

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

Handler return rules are:

- error == nil requires exactly one non-nil valid outcome;
- error != nil requires an absent outcome;
- a declared business error is returned as an outcome wrapper, never as Go error;
- Go error is exclusively infrastructure failure or implementation-contract failure;
- panic, invalid outcome, outcome-plus-error, or nil-outcome-plus-nil-error becomes system.internal and is recorded as a contract violation.

Handlers MUST NOT receive http.Request, ResponseWriter, framework request objects, or transport metadata unless the operation explicitly opts into a transport-coupled ABI. Such an operation carries implementation_declared guarantees for the coupled facets.

### 24.7 Streaming

scenery.go-implementation/v1 is unary. A tool encountering a streaming operation without a separately supported Go streaming ABI profile MUST emit unsupported_profile. It MUST NOT invent a stream signature or buffer the stream into a unary result.

### 24.8 Adapter registration

Each generated adapter exports a deterministic registration entry point:

~~~go
func Register(registry scenery.Registry) error
~~~

The generated composition root calls Register explicitly. Registration MUST NOT use package init functions, reflection-based discovery, package scanning, method scanning, or constructor-name conventions.

The application-owned adapter embeds the full application contract_revision and, for each covered service and operation address, its package identity and package_contract_abi_revision. The module-owned scenerycontract package embeds only its package identity and package_contract_abi_revision; it cannot know a future consuming application's revision.

Registration verifies the adapter's covered resource addresses and contract_revision against the runtime manifest, verifies every mapped package_contract_abi_revision against the imported scenerycontract package and implementation descriptor, and then verifies Go ABI compatibility, provider capability ABIs, constructor type, lifecycle hooks, and handlers before accepting traffic.

### 24.9 Generated artifact descriptors

Every application-scoped generated artifact set has a detached scenery.generated.v1 descriptor containing:

- artifact kind;
- artifact content digest;
- covered resource addresses;
- contract_revision;
- generator identity and version;
- required runtime ABI range;
- complete provider identity to ABI-range map.

Every module-owned scenerycontract artifact instead has a detached scenery.package-generated.v1 descriptor containing its artifact content digest, immutable package identity and version, package_contract_abi_revision, covered package-local declaration keys, scenery.go-implementation ABI range, capability-interface ABI ranges, and exported generated package path. It MUST NOT claim an application contract_revision.

Application adapters, clients, schemas, and documentation are keyed by contract_revision. Module-owned scenerycontract packages are keyed by package_contract_abi_revision and may be reused by every compatible application instance. The application adapter descriptor records the package-contract mapping described in Section 24.8. A built runtime bundle additionally records implementation_revision and its build-input or artifact digest. A deployment artifact records deployment_revision.

Build verification checks the applicable artifact digest, application contract_revision, package_contract_abi_revision mappings, covered resources, runtime ABI, provider ABIs, and generated package topology. Runtime-bundle verification additionally checks implementation_revision. Any mismatch fails the build or startup before accepting traffic.

Artifact digests are not self-referential. The digest projection sorts normalized UTF-8 artifact paths by byte order and, for each path, hashes an unsigned 64-bit big-endian path-byte length, the path bytes, an unsigned 64-bit big-endian content-byte length, and the exact content bytes. The detached descriptor is excluded. This length framing is mandatory; delimiter-only concatenation is not conformant. implementation_revision and deployment_revision likewise exclude their own descriptor fields. The descriptor itself participates in workspace_revision and may be authenticated by a separate signature.

Generator identity and version are reproducibility provenance. A project policy MAY require an exact generator match; regeneration always records the generator actually used.

### 24.10 ABI compatibility

The Go ABI uses semantic versioning independently of the language edition.

A new major ABI is required for an incompatible change to:

- generated package dependency direction or path derivation;
- any Scenery-to-Go type mapping;
- constructor, lifecycle, handler, outcome, or Register signatures;
- absence/null representation;
- business-outcome versus Go-error semantics;
- required descriptor fields or their interpretation;
- runtime or provider capability interfaces.

Additive generated helpers and optional descriptor metadata MAY be minor changes when older runtimes safely ignore them. An application contract change requires a new contract_revision and regeneration of affected application-scoped artifacts. A package Go-contract change requires a new package_contract_abi_revision and regeneration or republication of its scenerycontract package. Neither change by itself requires a new Go ABI major when the ABI rules remain compatible.

Generated artifacts declare an accepted runtime ABI range. Providers declare capability ABI ranges independently. Compatibility is verified before build completion and again before runtime registration.

### 24.11 Generated ownership and migrations

Generated code, schemas, clients, documentation, and deployment proposals are disposable projections and never declaration sources. They live under an obvious generated path and contain a machine-readable generated marker. Agents MUST NOT edit them to change application semantics.

Migration generators emit proposals. Once a migration is accepted, committed, or applied, that migration and its ledger are immutable operational artifacts and participate in workspace_revision.

Business implementation code depends on generated contract packages or canonical runtime capability packages. It SHOULD NOT parse .scn source or inspect the manifest.

## 25. Security properties

The language and tooling MUST:

- fail closed for unknown security behavior;
- redact secret-tainted values;
- prevent undeclared compiler I/O;
- verify dependency integrity;
- surface internet exposure in semantic diffs;
- treat weaker authentication or authorization as a security-relevant change;
- require explicit anonymous access;
- preserve tenant-isolation metadata through entities, views, operations, and policies;
- avoid executing application code during inspection or compilation.

Semantic plans MUST emit stable machine-readable risk records for recognized high-impact changes, including:

- a binding becoming internet-exposed;
- authentication changing to none;
- authorization being removed or weakened;
- secret values moving to non-sensitive sinks;
- a provider-instance lifecycle becoming managed;
- destructive entity migrations;
- durable attempts or timeout increasing materially.

When tooling cannot prove that an authentication or authorization change is equal or stronger, it classifies the change as unknown_or_weaker rather than safe. Project policy maps risk records to required approvals, which changes.apply enforces as defined in Section 22.

## 26. Conformance profiles

Edition 2027 defines semantics independently of implementation completeness. A tool claims only the independently versioned profiles it actually implements.

Core profiles are:

| Profile | Scope | Dependencies |
|---|---|---|
| scenery.compiler-core/v1 | Parser, lossless CST, formatter, core types, local packages/modules, semantic graph, canonical IR, source maps, diagnostics, and core compiler CLI | none |
| scenery.go-implementation/v1 | Normative unary Go implementation ABI and generated adapters | scenery.compiler-core/v1 |
| scenery.http-codec/v1 | HTTP gateways, exact wire codecs, negotiation, limits, and transport conformance | scenery.compiler-core/v1 |
| scenery.runtime-http/v1 | Direct execution, HTTP/internal bindings, policies, pipelines, and HTTP runtime behavior | scenery.compiler-core/v1, scenery.go-implementation/v1, scenery.http-codec/v1 |
| scenery.runtime-durable/v1 | Durable execution, dispatch, retries, leases, retention, and execution engines | scenery.compiler-core/v1, scenery.go-implementation/v1 |
| scenery.events/v1 | Event contracts, event buses, consumption, and emissions | scenery.compiler-core/v1 |
| scenery.data/v1 | Data sources, entities, views, CRUD expansion, and fixtures | scenery.compiler-core/v1 |
| scenery.deployment/v1 | Deployment overlays, provider planning, and deployment revisions | scenery.compiler-core/v1, scenery.compatibility-core/v1 |
| scenery.inspection-core/v1 | Schema/list/get/explain over canonical graph views | scenery.compiler-core/v1 |
| scenery.agent-read/v1 | Machine graph/context retrieval, semantic diff, retained snapshots, and server mode | scenery.inspection-core/v1, scenery.compatibility-core/v1 |
| scenery.agent-mutation/v1 | CST-aware plans, atomic changes, rename, fixes, approvals, and receipts | scenery.agent-read/v1 |
| scenery.patches/v1 | Version-bounded exact patches of explicitly patchable exports | scenery.compiler-core/v1 |
| scenery.ui/v1 | Pages, actions, and renderers | scenery.compiler-core/v1, scenery.data/v1 |
| scenery.legacy-bridge/v1 | Temporary bounded legacy-v0 lowering, mixed ownership, shadow comparison, activation, and retirement | scenery.compiler-core/v1, scenery.compatibility-core/v1 |
| scenery.compatibility-core/v1 | Deterministic multidimensional semantic compatibility and rename evidence | scenery.compiler-core/v1 |
| scenery.typescript-client/v1 | Deterministic public unary HTTP TypeScript clients and artifact revisions | scenery.compiler-core/v1, scenery.compatibility-core/v1, scenery.http-codec/v1 |

Future profiles include declarative extensions, workflow execution, registry publication, migration execution, custom middleware ABIs, internal RPC, and sandboxed executable extensions.

### 26.1 Profile rules

Every profile publishes:

- exact profile identity and version;
- profile dependencies;
- required resource-schema revisions;
- required CLI commands and machine methods;
- runtime and generated ABI requirements;
- conformance fixtures.

Tools, generated manifests, runtime bundles, and agent capability responses advertise exact supported profiles.

An application MAY require profiles:

~~~hcl
language {
  edition = "2027"

  require_profiles = [
    "scenery.compiler-core/v1",
    "scenery.go-implementation/v1",
    "scenery.runtime-http/v1",
  ]
}
~~~

Compilation fails when the active toolchain cannot satisfy a required profile. A known resource belonging to an unsupported profile produces unsupported_profile, never unknown_resource and never silent omission.

Claiming one profile does not imply any unlisted profile. Profile dependency is explicit. scenery.runtime-http/v1 depends on scenery.http-codec/v1. scenery.agent-read/v1 depends on scenery.inspection-core/v1, and scenery.agent-mutation/v1 depends on scenery.agent-read/v1. A Go HTTP runtime claims both scenery.runtime-http/v1 and scenery.go-implementation/v1; the HTTP profile itself is language-neutral. scenery.legacy-bridge/v1 is a migration-tool profile, not an edition-2027 source-language feature.

### 26.2 Implementation milestones

The kernel spike SHOULD claim only:

- scenery.compiler-core/v1;
- scenery.go-implementation/v1;
- scenery.runtime-http/v1;
- scenery.inspection-core/v1.

That slice includes:

- language and application declarations;
- records, enums, unions, constraints, and references;
- local packages and modules;
- services and explicit dependencies;
- operations and direct executions;
- HTTP and internal bindings;
- authentication/authorization references and pipelines;
- canonical IR and contract_revision;
- source maps and structured diagnostics;
- fmt, check, compile, and schema;
- list, get, and explain;
- generated Go contract/adapter packages;
- one generated TypeScript client.

The migration-capable first product release additionally claims scenery.legacy-bridge/v1 so existing applications can move one service at a time without a flag day. The bridge is implemented against the same canonical graph and runtime plan, not as a second runtime.

The read-tooling milestone then adds scenery.agent-read/v1: graph traversal, semantic diff, retained-revision comparison, bounded context bundles, and agent server mode. Those features are not blockers for proving the CST, semantic graph, HTTP boundary, or generated Go ABI.

The kernel spike does not claim durable execution, workflows, events, entities or migrations, deployments, pages, remote registries, exact patches, sandboxed extensions, approval tokens, full agent read, or agent mutation.

Before expanding this profile, an end-to-end House spike MUST prove:

1. lossless source editing and canonical formatting;
2. one typed operation with direct execution and an HTTP binding;
3. policy references and framework-enforced codecs;
4. the generated Go constructor, dependencies, handler outcome, and adapter;
5. generated TypeScript client compatibility;
6. ordinary unit testing of the implementation without starting Scenery.

## 27. Representative House examples

This section shows how the existing ONLV house service can be represented after the language change. It is illustrative but follows the normative rules above.

The package is named house, singular. It owns the house runtime service, scene operations, processing execution, roof-evaluation operations, data contracts, and its exported public surface.

House Core is a complete fixture for the kernel spike. House Full adds durable execution, data providers, schedules, and deployment overlays and therefore requires later profiles.

### 27.1 House Core

The root scenery.scn uses exactly the kernel-spike profiles:

~~~hcl
language {
  edition = "2027"

  require_profiles = [
    "scenery.compiler-core/v1",
    "scenery.go-implementation/v1",
    "scenery.runtime-http/v1",
    "scenery.inspection-core/v1",
  ]
}

workspace {
  implementation_root "house" {
    path             = "house"
    revision_include = ["**/*.go", "go.mod", "go.sum"]
    revision_exclude = ["native/build/**", "test-results/**"]
  }

  managed_generated_roots = [
    "house/scenerycontract",
    "internal/scenerygen",
    "clients/generated",
  ]
}

go_module "application" {
  root        = "."
  import_path = "github.com/example/clean-tech"
}

go_toolchain "application" {
  version               = "1.26.3"
  goos                  = "linux"
  goarch                = "amd64"
  cgo                   = "disabled"
  architecture_features = ["amd64.v1"]
  build_tags           = []
  experiments         = []
  compiler_flags      = []
  linker_flags        = []
}

go_target "api" {
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = ["./..."]
}

application "clean_tech" {
  version = "2.0.0-dev"
}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

module "house" {
  source = "./house"

  inputs = {
    gateway = http_gateway.public_api
  }
}
~~~

The package file house/scenery.package.scn is independently reusable:

~~~hcl
package "house" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"

  go_contract {
    import_path = "github.com/example/clean-tech/house"
  }
}

input "gateway" {
  type = resource_ref("http_gateway")
}

service "house" {
  runtime = "go"

  implementation {
    constructor = "NewService"
  }
}

enum "process_mode" {
  value "all" {}
  value "roof_only" {}
}

record "process_scene_input" {
  field "scene_id" {
    type       = string
    min_length = 1
  }

  field "mode" {
    type    = enum.process_mode
    default = enum.process_mode.all
  }
}

record "process_scene_result" {
  field "run_id" {
    type = uuid
  }

  field "status" {
    type = string
  }
}

operation "process_scene" {
  service = service.house
  input   = record.process_scene_input

  handler {
    method = "ProcessScene"
  }

  result "processed" {
    type = record.process_scene_result
  }

  error "invalid_input" {
    type = std.type.problem
  }
}

execution "process_scene_direct" {
  operation = operation.process_scene
  mode      = "direct"
  timeout   = "40m"
}

binding "process_scene_http" {
  gateway   = var.gateway
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "http"
  delivery  = "call"

  authentication = std.authentication.none
  authorization  = std.authorization.none
  pipeline       = std.pipeline.empty

  http {
    method        = "POST"
    path          = "/house/process"
    codec_profile = std.codec.http_json_v1

    body {
      codec = "json"
      to    = operation.process_scene.input
    }

    response "processed" {
      when   = result.processed
      status = 200

      body {
        codec = "json"
        from  = result.processed
      }
    }

    response "invalid_request" {
      when   = transport.invalid_request
      status = 422

      body {
        codec = "problem_json"
        from  = transport.problem
      }
    }

    response "invalid_input" {
      when   = error.invalid_input
      status = 422

      body {
        codec = "problem_json"
        from  = error.invalid_input
      }
    }
  }
}

export "service" {
  value = service.house
}

export "operation" {
  value = operation.process_scene
}
~~~

This fixture must compile, generate, and type-check without durable, data, deployment, legacy, or full agent-read support.

### 27.2 House Full: application installation

The example assumes authentication.standard, authorization.member, and pipeline.http_default are declared in other files in the application source unit.

~~~hcl
language {
  edition = "2027"

  require_profiles = [
    "scenery.compiler-core/v1",
    "scenery.go-implementation/v1",
    "scenery.runtime-http/v1",
    "scenery.runtime-durable/v1",
    "scenery.data/v1",
    "scenery.deployment/v1",
    "scenery.inspection-core/v1",
  ]
}

workspace {
  implementation_root "house" {
    path = "house"
    revision_include = [
      "**/*.go",
      "**/*.c",
      "**/*.cc",
      "**/*.h",
      "go.mod",
      "go.sum",
    ]
    revision_exclude = [
      "native/build/**",
      "test-results/**",
    ]
  }

  managed_generated_roots = [
    "house/scenerycontract",
    "internal/scenerygen",
    "clients/generated",
  ]
}

go_module "application" {
  root        = "."
  import_path = "github.com/example/clean-tech"
}

go_toolchain "application" {
  version               = "1.26.3"
  goos                  = "linux"
  goarch                = "amd64"
  cgo                   = "enabled"
  architecture_features = ["amd64.v3"]
  build_tags           = ["roofmapnet_native"]
  experiments         = []
  compiler_flags      = []
  linker_flags        = []
}

go_target "api" {
  toolchain    = go_toolchain.application
  module       = go_module.application
  packages     = ["./..."]
  native_inputs = [relative_path("house/native")]
}

application "clean_tech" {
  version = "1.0.0"
}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.application
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

provider "postgres" {
  source  = "registry.scenery.dev/core/postgres"
  version = ">= 2.1.0, < 3.0.0"
}

provider "storage" {
  source  = "registry.scenery.dev/core/storage"
  version = ">= 2.0.0, < 3.0.0"
}

provider "durable" {
  source  = "registry.scenery.dev/core/durable"
  version = ">= 2.0.0, < 3.0.0"
}

data_source "house_database" {
  provider  = provider.postgres
  lifecycle = "managed"

  require_capabilities = [
    "sql.query/v1",
    "sql.transaction/v1",
    "sql.migration/v1",
  ]

  config = {
    database = "house"
    scope    = "tenant"
  }
}

data_source "app_storage" {
  provider  = provider.storage
  lifecycle = "managed"

  require_capabilities = [
    "object.read/v1",
    "object.write/v1",
    "object.delete/v1",
  ]

  config = {
    bucket = "app"
    scope  = "tenant"
    limit  = "2GiB"
  }
}

execution_engine "durable_tasks" {
  provider  = provider.durable
  lifecycle = "managed"

  require_capabilities = [
    "execution.durable/v1",
  ]
}

module "house" {
  source = "./house"

  inputs = {
    gateway         = http_gateway.public_api
    database        = data_source.house_database
    storage         = data_source.app_storage
    task_engine     = execution_engine.durable_tasks
    authentication  = authentication.standard
    authorization   = authorization.member
    http_pipeline   = pipeline.http_default
    roof_model_path = relative_path("models/roofmapnet")
  }
}

deployment "production" {
  environment = "production"

  module {
    target = module.house

    inputs = {
      process_concurrency   = 16
      roof_eval_concurrency = 2
    }
  }

  data_source {
    target = data_source.house_database

    config = {
      database = "house_production"
      region   = "eu-central-1"
    }
  }

  http_gateway {
    target = http_gateway.public_api

    listener {
      host = "api.onlv.dev"
      port = 443
      tls  = "required"
    }
  }
}

deployment "preview" {
  environment = "preview"

  module {
    target = module.house

    inputs = {
      process_concurrency   = 2
      roof_eval_concurrency = 1
    }
  }
}
~~~

The Postgres provider descriptor marks database and region deployment_bindable. Other config fields cannot be overlaid.

### 27.3 House Full: package surface

~~~hcl
package "house" {
  version         = "1.0.0"
  scenery_version = ">= 2.0.0, < 3.0.0"

  go_contract {
    import_path = "github.com/example/clean-tech/house"
  }
}

input "database" {
  type  = resource_ref("data_source")
  phase = "deployment"
  requires = [
    "sql.query/v1",
    "sql.transaction/v1",
  ]
}

input "gateway" {
  type = resource_ref("http_gateway")
}

input "storage" {
  type  = resource_ref("data_source")
  phase = "deployment"
  requires = [
    "object.read/v1",
    "object.write/v1",
  ]
}

input "task_engine" {
  type  = resource_ref("execution_engine")
  phase = "deployment"
  requires = [
    "execution.durable/v1",
  ]
}

input "authentication" {
  type = resource_ref("authentication")
}

input "authorization" {
  type = resource_ref("authorization")
}

input "http_pipeline" {
  type = resource_ref("pipeline")
}

input "roof_model_path" {
  type  = relative_path
  phase = "deployment"
}

input "process_concurrency" {
  type    = int
  phase   = "deployment"
  default = 4
  minimum = 1
}

input "roof_eval_concurrency" {
  type    = int
  phase   = "deployment"
  default = 1
  minimum = 1
}

export "service" {
  value = service.house
}

export "operations" {
  value = {
    process_scene       = operation.process_scene
    run_roof_evaluation = operation.run_roof_evaluation
  }
}
~~~

### 27.4 House Full: service and types

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
}

enum "process_mode" {
  value "all" {}
  value "roof_only" {}
}

record "process_scene_input" {
  field "tenant_id" {
    type = string
  }

  field "scene_id" {
    type       = string
    min_length = 1
  }

  field "mode" {
    type    = enum.process_mode
    default = enum.process_mode.all
  }
}

record "process_scene_result" {
  field "scene_id" {
    type = string
  }

  field "run_id" {
    type = uuid
  }

  field "status" {
    type = string
  }
}
~~~

Tenant identity would normally come from principal or context rather than an internet request body. It remains in the logical operation input here because internal calls and durable execution also require it. The HTTP binding maps it from authenticated context.

### 27.5 House Full: one operation, multiple executions

~~~hcl
operation "process_scene" {
  service = service.house
  input   = record.process_scene_input

  handler {
    method = "ProcessScene"
  }

  result "processed" {
    type = record.process_scene_result
  }

  error "invalid_input" {
    type = std.type.problem
  }

  error "scene_not_found" {
    type = std.type.problem
  }

  error "processing_failed" {
    type = std.type.problem
  }
}

execution "process_scene_direct" {
  operation = operation.process_scene
  mode      = "direct"
  timeout   = "40m"
}

execution "process_scene_durable" {
  operation = operation.process_scene
  mode      = "durable"

  engine   = var.task_engine
  revision = 1
  timeout  = "40m"
  lease    = "20m"
  attempts = 6

  retry {
    strategy = "exponential"
    initial  = "10s"
    factor   = 2
    maximum  = "2m"
  }

  concurrency {
    key   = input.tenant_id
    limit = var.process_concurrency
  }

  retention {
    success = "7d"
    failure = "30d"
  }
}
~~~

The durable identity comes from the namespaced execution address and revision rather than a name discovered from Go.

### 27.6 House Full: asynchronous HTTP binding

~~~hcl
binding "process_scene_async" {
  gateway   = var.gateway
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "http"
  delivery  = "enqueue"

  authentication = var.authentication
  authorization  = var.authorization
  pipeline       = var.http_pipeline

  http {
    method        = "POST"
    path          = "/house/process-async"
    codec_profile = std.codec.http_json_v1

    context "tenant_id" {
      from = principal.tenant_id
      to   = operation.process_scene.input.tenant_id
    }

    body {
      codec = "json"
      to    = operation.process_scene.input
      except = [
        operation.process_scene.input.tenant_id,
      ]
    }

    response "accepted" {
      when   = dispatch.enqueued
      status = 202

      body {
        codec = "json"
        from  = dispatch.receipt
      }
    }

    response "invalid_request" {
      when   = transport.invalid_request
      status = 422

      body {
        codec = "problem_json"
        from  = transport.problem
      }
    }

    response "dispatch_rejected" {
      when   = dispatch.rejected
      status = 503

      body {
        codec = "problem_json"
        from  = dispatch.problem
      }
    }
  }
}
~~~

### 27.7 House Full: direct HTTP and internal bindings

~~~hcl
binding "process_scene_http" {
  gateway   = var.gateway
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "http"
  delivery  = "call"

  authentication = var.authentication
  authorization  = var.authorization
  pipeline       = var.http_pipeline

  http {
    method        = "POST"
    path          = "/house/process"
    codec_profile = std.codec.http_json_v1

    context "tenant_id" {
      from = principal.tenant_id
      to   = operation.process_scene.input.tenant_id
    }

    body {
      codec = "json"
      to    = operation.process_scene.input
      except = [
        operation.process_scene.input.tenant_id,
      ]
    }

    response "processed" {
      when   = result.processed
      status = 200

      body {
        codec = "json"
        from  = result.processed
      }
    }

    response "invalid_request" {
      when   = transport.invalid_request
      status = 422

      body {
        codec = "problem_json"
        from  = transport.problem
      }
    }

    response "invalid_input" {
      when   = error.invalid_input
      status = 422

      body {
        codec = "problem_json"
        from  = error.invalid_input
      }
    }

    response "scene_not_found" {
      when   = error.scene_not_found
      status = 404

      body {
        codec = "problem_json"
        from  = error.scene_not_found
      }
    }

    response "processing_failed" {
      when   = error.processing_failed
      status = 500

      body {
        codec = "problem_json"
        from  = error.processing_failed
      }
    }

  }
}

binding "process_scene_internal" {
  operation = operation.process_scene
  execution = execution.process_scene_durable
  protocol  = "internal"
  delivery  = "enqueue"

  exposure       = "application"
  authentication = var.authentication
  authorization  = var.authorization
  pipeline       = std.pipeline.empty

  internal {
    visibility = "application"
    principal  = "inherit"
  }
}
~~~

Local-only debugging can use process_scene_direct through a local binding without creating a second operation:

~~~hcl
binding "process_scene_local_debug" {
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "internal"
  delivery  = "call"
  exposure  = "local"

  authentication = std.authentication.local_developer
  authorization  = std.authorization.local_developer
  pipeline       = std.pipeline.empty

  internal {
    visibility = "package"
    principal  = "inherit"
  }
}
~~~

### 27.8 House Full: roof evaluation execution

~~~hcl
record "roof_evaluation_input" {
  field "tenant_id" {
    type = string
  }

  field "corpus" {
    type       = string
    min_length = 1
  }
}

record "roof_evaluation_result" {
  field "run_id" {
    type = uuid
  }

  field "status" {
    type = string
  }
}

operation "run_roof_evaluation" {
  service = service.house
  input   = record.roof_evaluation_input

  handler {
    method = "RunRoofEvaluation"
  }

  result "completed" {
    type = record.roof_evaluation_result
  }

  error "failed" {
    type = std.type.problem
  }
}

execution "roof_evaluation_durable" {
  operation = operation.run_roof_evaluation
  mode      = "durable"

  engine   = var.task_engine
  revision = 1
  timeout  = "61m"
  lease    = "61m"
  attempts = 1

  retry {
    strategy = "none"
  }

  concurrency {
    key   = input.tenant_id
    limit = var.roof_eval_concurrency
  }

  retention {
    success = "30d"
    failure = "90d"
  }
}
~~~

The remaining current HTTP surface—scene registration, deletion, process-run ranking, debug viewer control, corpus maintenance, run inspection, gating, and acceptance—follows the same rule: define one operation for each logical capability, then bind it explicitly.

## 28. Invalid examples

### 28.1 Duplicate resource

~~~hcl
operation "process_scene" {}
operation "process_scene" {}
~~~

Expected: a duplicate-identity diagnostic in SCN1100-SCN1199.

### 28.2 Missing internet security

~~~hcl
binding "unsafe" {
  gateway   = http_gateway.public_api
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "http"
  delivery  = "call"
}
~~~

Expected: explicit authentication and authorization diagnostics in SCN4000-SCN4199.

### 28.3 Runtime value in compile field

~~~hcl
execution "bad" {
  operation = operation.process_scene
  mode      = "durable"

  engine   = var.task_engine
  revision = 1
  timeout  = "10m"
  lease    = "5m"
  attempts = input.requested_attempts

  retry {
    strategy = "none"
  }

  retention {
    success = "1d"
    failure = "7d"
  }
}
~~~

Expected: an evaluation-phase diagnostic in SCN1300-SCN1399 because attempts is compile-time configuration.

### 28.4 String reference

Inside a binding, the following attribute is invalid:

~~~hcl
operation = "house/operation/process_scene"
~~~

Expected: a reference type diagnostic. The correct source form is operation.process_scene.

### 28.5 Incomplete response coverage

If operation.process_scene declares scene_not_found but a waiting HTTP binding has no matching response, compilation fails with a binding coverage diagnostic.

### 28.6 Incomplete scheduled input

If run_roof_evaluation requires tenant_id and corpus but a schedule supplies only corpus, compilation fails with a typed input-completeness diagnostic.

## 29. Profile conformance requirements

A tool is conforming to a profile only if it passes that profile's applicable fixtures below. A read-only profile does not inherit mutation tests unless its dependency graph requires them.

### 29.1 Parser conformance

- accepts every edition-valid syntax fixture;
- rejects forbidden HCL features;
- round-trips valid source byte-for-byte without formatting;
- preserves precise source ranges;
- recovers sufficiently to report multiple independent errors;
- handles UTF-8, LF, and CRLF rules;
- never treats a recovered tree as deployable.

### 29.2 Formatter conformance

- is idempotent;
- emits canonical comments and layout;
- preserves authored top-level order and explicitly semantic nested order;
- preserves attached comments;
- does not change contract_revision, implementation_revision, or deployment_revision;
- does change the workspace revision when source bytes change.

### 29.3 Compiler conformance

- produces identical IR for file and unordered-resource permutations;
- type-checks references and expressions;
- retains provenance;
- expands resources deterministically;
- detects graph-wide conflicts;
- never executes application code;
- performs no implicit network access;
- rejects missing locked dependencies rather than fetching;
- canonicalizes exact numbers beyond 2^53, negative integers, decimals with trailing zeros, durations, and sizes without precision loss;
- preserves ordinary string code points and applies RFC 8785 canonical JSON bytes exactly;
- enumerates workspace revision inputs through explicit include/exclude rules without admitting temporary build files;
- uses staged generated overlays before Go implementation verification;
- computes stable revisions.

Revision fixtures MUST establish:

| Change | Contract | Implementation | Workspace | Deployment |
|---|---|---|---|---|
| Comment/formatting | same | same | changes | same |
| Go body | same | changes | changes | changes |
| HTTP path | changes | changes | changes | changes |
| Unconsumed generated projection bytes | same | same | changes | same |
| Replicas | same | same | changes | changes |
| Provider runtime version | same unless contract schema changes | changes | changes | changes |

### 29.4 CLI and agent conformance

- emits the documented envelopes and exit statuses;
- provides schemas for every resource and mutation;
- supports bounded context retrieval;
- plans without writing when scenery.agent-mutation/v1 is claimed;
- applies atomically with workspace_revision and contract_revision preconditions when scenery.agent-mutation/v1 is claimed;
- reports semantic diffs;
- returns stable diagnostic codes.

### 29.5 Runtime conformance

- verifies generated ABI, contract_revision, and implementation_revision where applicable;
- preserves operation variant semantics;
- enforces binding mappings and policies;
- scopes HTTP route uniqueness and listener behavior through gateways;
- honors execution behavior;
- rejects unsupported capabilities explicitly;
- does not leak secret-tainted values.

## 30. Agent quick reference

### 30.1 To understand an application

1. Read the language edition and application identity.
2. List installed modules and exports.
3. Query the expanded resource graph.
4. Start from operations, then inspect bindings, executions, and policies.
5. Inspect data_source, entity, and view resources only when data behavior matters.
6. Use provenance to distinguish authored values, defaults, inputs, patches, and generated resources.
7. When scenery.legacy-bridge/v1 is active, inspect service ownership, inactive shadow candidates, and comparison state before proposing activation.

### 30.2 To add a capability

1. Define or reuse input, result, and problem types.
2. Define one logical operation.
3. Bind it to its service handler.
4. Define or reuse compatible execution.
5. Add one or more bindings.
6. Attach explicit security policies and pipelines.
7. Export it only if other modules should use it.
8. Plan, inspect semantic diff, apply, format, and check.

### 30.3 To change an HTTP route

Change the binding, not the operation. Then inspect:

- gateway-scoped route conflicts and base paths;
- clients and pages depending on the binding;
- exposure and security;
- semantic compatibility;
- generated OpenAPI and clients.

### 30.4 To make work durable

Add or select a durable execution and point compatible bindings or schedules to it. Do not create a duplicate task-only operation unless the logical contract is genuinely different.

### 30.5 To add a schedule

Reference an existing operation and compatible execution. Declare trigger, timezone, overlap, catch-up, and input. Do not add a cron-specific handler.

### 30.6 To change module behavior

Prefer a typed input or documented extension point. Use an exact patch only when the module deliberately supports no suitable extension.

## Appendix A: Syntax sketch

This sketch describes lexical shape. Resource schemas define valid labels, attributes, blocks, and expression types.

~~~ebnf
file             = { top_level_block } ;
top_level_block  = identifier, { string_label }, "{", { body_item }, "}" ;
body_item        = attribute | block ;
attribute        = identifier, "=", expression ;
block            = identifier, { string_label }, "{", { body_item }, "}" ;
string_label     = quoted_string ;

expression       = literal
                 | reference
                 | list
                 | object
                 | function_call
                 | unary_expression
                 | binary_expression
                 | parenthesized_expression ;

reference        = identifier, { ".", identifier } ;
list             = "[", [ expression, { ",", expression }, [ "," ] ], "]" ;
object           = "{", { object_item }, "}" ;
object_item      = (identifier | quoted_string), "=", expression ;
function_call    = identifier, "(", [ expression, { ",", expression } ], ")" ;
~~~

Top-level attributes are forbidden. Schemas may further restrict expressions.

## Appendix B: Reserved reference roots

| Root | Phase | Meaning |
|---|---|---|
| var | compile | Current package input |
| module | compile | Installed module export |
| std | compile/runtime | Standard-library resource |
| resource kind name | compile | Local resource reference |
| input | runtime | Operation input |
| principal | runtime | Authenticated identity |
| context | runtime | Typed invocation context |
| result | runtime | Success variant |
| error | runtime | Declared error variant |
| dispatch | runtime | Execution dispatch outcome |
| transport | runtime | Decoding, negotiation, and transport outcome |
| admission | runtime | Authentication, authorization, and admission outcome |
| system | runtime | Implementation or runtime contract failure |
| message | runtime | Consumed event envelope and payload |
| value | runtime | Value currently being validated |
| item | runtime | Current item in a scoped expression |

Extensions cannot redefine these roots.

The temporary `legacy` compile root defined by scenery.legacy-bridge/v1 exists only in mixed migration mode and is not an edition-2027 root.

## Appendix C: Canonical naming

| Object | Canonical form | Example |
|---|---|---|
| Resource label | lower_snake_case | process_scene |
| Package name | lower_snake_case | house |
| Module instance | lower_snake_case path segment | house |
| Scenery filename | lower_snake_case.scn | roof_evaluation.scn |
| HTTP literal segment | lower-kebab-case | process-runs |
| HTTP parameter | lower_snake_case in braces | {scene_id} |
| CLI command segment | lower-kebab-case | process-scene |
| Export name | lower_snake_case | operations |
| Operation variant | lower_snake_case | scene_not_found |

External compatibility MAY require different wire names. Those are explicit strings and do not alter semantic identity.

## Appendix D: Design decisions

This draft makes the following deliberate choices:

1. Scenery uses a standalone HCL-like authoring language.
2. Source files use .scn; HCL supplies syntax shape, not language identity or Terraform semantics.
3. The canonical resource graph is independent of authoring syntax.
4. There is one canonical declaration surface.
5. Operation, binding, execution, and policy are separate.
6. Modules are pure typed resource factories with explicit exports.
7. Source order is meaningless except in blocks whose schema explicitly says order matters.
8. CRUD and similar conveniences are transparent expansions.
9. Data sources, execution engines, event buses, and secret stores are distinct provider-instance kinds.
10. Contract, implementation, workspace, and deployment revisions are separate projections.
11. Go implementation uses a versioned generated ABI rather than Go meta declarations.
12. Compilation is offline and never executes arbitrary native provider or application code.
13. Conformance is profile-based rather than all-or-nothing.
14. Agent mutation is semantic, transactional, and revision-checked.
15. Legacy directives, tags, JSON configuration, and runtime discovery are outside the language.
16. Mixed migration uses two compiler frontends, strict ownership linking, and one runtime graph.
17. HTTP routes are scoped by logical gateways whose concrete listeners are deployment values.
18. Go build context is explicit and participates in implementation revision.
19. Ordinary strings are preserved exactly; only explicitly canonical types normalize text.
20. HTTP v1 supports buffered bytes, not raw transport objects.

## Appendix E: Open draft items

The following details require resolution before edition 2027 becomes stable:

- the exact standard-library catalog;
- the complete provider capability vocabulary;
- the runtime workflow model;
- stream and WebSocket type details;
- source-compatible versus wire-compatible change classification;
- patch authorization and review policy;
- registry trust roots, signing, and revocation policy;
- provider deployment-plan schema and target-platform vocabulary;
- migration syntax for entity evolution.
- the complete legacy-v0 construct fixture catalog and bridge removal release;
- platform-specific HTTP listener and certificate schemas;
- native toolchain identity schemas for CGO and architecture-specific builds.

Until resolved, tools MUST identify these features as draft or unsupported. They MUST NOT invent silent defaults.
