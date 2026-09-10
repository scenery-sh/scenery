# scenery Local Contract

This file is large; read only the sections covering the surface you are
changing. Each section is self-contained.

- [Current Scenery contract](#current-scenery-contract) — role-named source files, the implemented command surface, and the revision model.
- [Status](#status) — what is implemented now versus explicitly out of scope.
- [App Config](#app-config) — the `.scenery.json` schema: envs, watch, frontends, deploy targets, and capability supply/setup.
- [CLI Grammar](#cli-grammar) — the full implemented command grammar with flags, output modes, and exit semantics.
- [Assistant model](#assistant-model) — provider-neutral MCP capabilities, public conversation routes, helper isolation, and inspection boundaries.
- [Artifact Locations](#artifact-locations) — generated artifact paths and repo-local cache locations.
- [JSON Schemas](#json-schemas) — machine-readable envelope and payload schemas under `docs/schemas/`.
- [Examples](#examples) — representative `-o json` outputs per inspect and observability command.

## Current Scenery contract

Local HTTP package directories contribute their parent folders to the route:
`group1/maps` with endpoint `/maps/list` and gateway `base_path = "/v1"`
produces `/v1/group1/maps/list`, before any runtime mount such as `/api`.
Source `http.path` remains authored; effective/expanded `http.path` includes
the group with `directory_group_prefix` provenance. Moving a package changes
the route and requires regenerated clients; no old-path aliases are created.
See [HTTP route identity](spec/http.md#33-route-identity) for literal-name,
CRUD, nested-directory and registry-package rules.

An app containing `app.scn` uses the compiler described by [the evolving current specification](spec/SPEC.md). Package contracts are named `package.scn` and the optional generated dependency lock is `app.lock.scn`. Retired pre-cutover filenames are rejected with `SCN1021` and an exact rename instruction; they are not aliases. Go comments and package-initialization builders are not application-model syntax.

The implemented command surface is:

```text
scenery fmt [--check] [--app-root <path>] [-o human|json]
scenery check [--app-root <path>] -o human|json
scenery compile [--view source|effective|expanded] [--app-root <path>] -o human|json
scenery schema <kind> [-o human|json]
scenery list <kind> [--module <name>] [--view source|effective|expanded] [-o human|json]
scenery get <address> [--view source|effective|expanded] [-o human|json]
scenery explain <address> [--view source|effective|expanded] [-o human|json]
scenery diff --semantic <base-manifest-or-revision> <target-manifest-or-revision> [--view source|effective|expanded] [--rename-receipts <change-plan-or-receipt.json>] [--exit-code] [-o human|json]
scenery graph <address> [--direction dependencies|dependents|both] [--depth <n>] [--max-resources <n>] [-o human|json]
scenery agent serve [--app-root <path>]
scenery changes plan --changes <file> --base-workspace-revision <rev> --base-contract-revision <rev|null> --out <plan> [-o human|json]
scenery changes apply <plan> --expect-workspace-revision <rev> --expect-contract-revision <rev|null> [--approval-token <file>] [-o human|json]
scenery changes rename <address> <new-name> [--dry-run] [--approval-token <file>] [-o human|json]
scenery generate [--target contracts|typescript_client.<name>] [--check] [--app-root <path>] [-o human|json]
scenery build [--development] [--verify-generation] [--target <go-target>] [--output <binary>] [-o human|json]
scenery build --lib <name|address|artifact> [--version <vN.N.N>] [--platform all|host|darwin/arm64|linux/amd64|<csv>] [--output <directory>] [-o human|json]
scenery build --desktop [--env <name>] [--app-root <path>] [-o human|json]
scenery snapshot save --output <file.zip> [--db] [--storage] [--app-root <path>] [-o human|json]
scenery snapshot verify --input <file.zip> [-o human|json]
scenery snapshot load --input <file.zip> [--db] [--storage] --mode overwrite|merge [--on-conflict fail|skip|overwrite] [--yes] [--dry-run] [--app-root <path>] [-o human|json]
scenery deploy plan <deployment> --out <plan> [-o human|json]
scenery deploy apply <plan> --expect-workspace-revision <rev> --expect-contract-revision <rev> [--approval-token <file>] [-o human|json]
scenery telemetry [--app <id-or-name>]... [--command <coarse-command>]... [--measurement completion|startup]... [--since <duration>] [--limit <n>] [-o human|json]
```

`-o json` selects the singular `scenery.cli` envelope. It always carries `kind`, digest `schema_revision` and `spec_revision`, `producer`, `ok`, nullable graph revision fields, `data`, and ordered `diagnostics`; command-specific schemas describe `data`. `workspace_revision` and `contract_revision` are a canonical digest or null. `implementation_revision` and `deployment_revision` are a canonical digest, a target-to-digest object, or null; other JSON shapes fail decoding. `-o jsonl` emits `scenery.cli.event` envelopes with the same identity fields, monotonically increasing sequence numbers, an `event` discriminator, and one terminal summary event. Decoders accept only the exact current schema revision, which is the complete self-normalized digest of the matching checked JSON Schema. Exit status is 0 for success, 1 for a false diff/check predicate, 2 for invalid input, 3 for revision conflict or failed precondition, 4 for unavailable capability, 5 for denied permission/approval, and 10 for internal failure.

CLI invocations best-effort append JSON objects to `~/.scenery/telemetry.jsonl`, with owner-only file permissions. Ordinary records contain UTC `at`, coarse `command`, `duration_ms`, `exit_code`, `version`, and `mode`; omission of `measurement` means command completion. A newly owned `scenery up` runtime instead writes one `measurement: "startup"` record immediately after its existing internal readiness boundary: shared startup dependencies, the application listener, and configured frontend readiness have succeeded. Its later supervisor exit is not recorded, the detached launcher defers to its owner child, and already-running acquisition is not startup. Startup failure that exits before readiness is recorded as a failed startup measurement. When app discovery succeeds records additionally contain configured `app.id` and `app.name`. Mode is `long_running` for `up`, `worker`, `console`, and `logs --follow`, and `oneshot` otherwise. Command classification retains at most the known command and subcommand and never stores flags, filesystem paths, SQL, tokens, storage keys, or task arguments. App attribution is resolved after the measured duration, so discovery overhead is excluded. Telemetry encoding, discovery, directory, open, or write failures are ignored and never change command output or exit status.

`scenery telemetry [--app <id-or-name>]... [--command <coarse-command>]... [--measurement completion|startup]... [--since <duration>] [--limit <n>] [-o human|json]` streams that file and retains at most the requested recent records (default 100, maximum 10,000) while calculating overall, per-app, per-command, and per-measurement timing summaries. App, command, and measurement filters are repeatable OR filters; supplying different filter classes combines them with AND. App filters exactly match configured ID or name. Every timing summary reports all-history count/average/min/max plus exact p50/p95 over the latest at most 10,000 matching records, with `percentile_sample_count` making that bound explicit. `--command up --measurement startup` therefore excludes historical or ordinary completion/lifetime timings from startup percentiles. Historical records without app identity remain visible as unattributed when no app filter is selected. JSON data uses kind `scenery.telemetry` and the exact checked schema in `docs/schemas/scenery.telemetry.schema.json`.

The checked-in diagnostic registry is publicly inspectable with `schema.get` using either the manifest's digest `diagnostic_catalog` identity or one `SCNxxxx` code. Request failures use `SCN8001` through `SCN8005`; only internal failures use `SCN9000` through `SCN9099`. Every internal failure carries an opaque `report_token` and a sanitized stable message, never its raw cause.

The current specification includes compilation, Go generation, HTTP and typed path tails, durable execution, events, data, deployment, inspection, agent mutation, patches, UI, semantic evolution, and TypeScript clients. Its `spec_revision` covers resource schemas, structural application/workspace/package/module/input/export schemas, stable diagnostic rules, and explicit revisions for source composition, defaults, expansion, reference resolution, contract projection, evolution, Go generation, and TypeScript generation. Applications cannot select a language version or feature set; resource use determines required behavior. Unavailable future behavior fails explicitly; `extension` and generic `resource` declarations emit `SCN7001 feature_unavailable`, while genuinely unknown syntax remains `SCN1002`.

The current HTTP contract implements terminal `{name...}` plus an exactly
matching `path_tail` mapping to `string`, `relative_path`, or
`optional(relative_path)`. A tail captures zero or more non-empty segments, so
`/drive/{path...}` matches `/drive` and `/drive/a/b` but not `/drive/` or
`/drive//b`. Runtime decoding splits before one-time percent decoding, rejects
separator/traversal/backslash/NUL/double-decode hazards, and never falls back
to a broader tail after selecting a more specific route. Generated TypeScript
clients encode each semantic segment independently. Unsupported wildcard or
independently raw facets fail validation.

Source schemas are enforced recursively against authored blocks before lowering. Unknown nested attributes or blocks, wrong label counts, repeated singleton blocks, and duplicate named children are errors. Workspace revision globs implement only `*`, `?`, and whole-segment `**`; character classes, escapes, embedded `**`, and host glob semantics are rejected.

`scenery schema` and agent `schema.get` expose the same recursive authored definitions used for source validation and semantic creation: attribute versus block shape, labels and their domain patterns, cardinality, ordering, value/reference type, expression phase, revision domain, defaults/constraints, sensitivity, and patchability. HTTP header/query/cookie/multipart wire labels therefore use their transport policies instead of a hard-coded semantic-name pattern. Agent capabilities return `resource_create_kinds`; only kinds whose full recursive metadata is complete appear there. `resource.create` renders attributes and nested blocks from those definitions, preserves ordered children, canonically sorts unordered children, resolves nested local module instances to their declared package source unit, and returns `capability_unavailable` for any unadvertised kind rather than emitting guessed source.

A Go service `config` block accepts dynamic lower-snake attributes. Each value must reference a typed package input, but the config key may be an explicit alias: `model_path = var.roof_model_path` generates the `model_path` field while deriving its type, contract/implementation/deployment phase, constraints, and sensitivity from `roof_model_path`. The compiler validates the resolved module value and secret-reference flow before generation.

Local module sources and generated roots are workspace trust boundaries. Before ordinary compilation reads source, Scenery recovers an abandoned source/generated transaction or rejects a live owner; only the current owner may compile during publication. `fmt` and generation reject traversal and symlinks. Application-imported Go contracts/facades are ordinary packages inside declared existing Go modules, under managed roots and ignored by default. `generate`, `test`, `build` and `up` prepare them; compilation/inspection, `check` and `generate --check` never repair application output. Check reports Go freshness and independently verifies native ABI against current expected bytes. Private composition remains in the external build cache. No generated nested module, root workfile or import replacement is created. User workfiles remain ordinary user inputs. Generation publishes changed bytes only through one recoverable artifact set, preserving unknown files and refusing unverified ownership or hand edits. Exact generated paths are excluded from authored revisions; consumed generated bytes remain build inputs. TypeScript targets retain `materialization = "source"` (default, declared managed root) or `"cache"` (`.scenery/gen/typescript/<name>`); build/test/up refresh cache targets. Pending artifacts from the prior revision scheme fail with `revision_scheme_changed`; immutable rename receipts require projection-matching `RevisionRebind` evidence rather than mutation.

External manifest references accept exactly either a current `scenery.manifest` document or a current `scenery.cli` compile envelope. Both paths validate exact identity and producer, diagnostic catalog, known unversioned resource schemas, unique canonical address order, and a recomputed `contract_revision`; unknown fields, trailing values, old envelopes, and heuristic `data.manifest` wrappers fail closed.

The compiler retains lossless CST/source maps and exposes distinct source, effective, and expanded graphs. Source preserves authored `var.*`/export expressions and omits defaults; effective resolves module inputs, applies effective defaults, then applies inheritance/exact patches; expanded adds generated resources. Every graph field carries `origin.field_provenance` keyed by an RFC 6901 pointer that resolves inside that resource's `spec` in the same view; arrays use numeric indexes, never labels. Entries include declaring range/input, supplier, source address, and transformation chain for defaults, inputs/exports, patches, expansions, and provider descriptors. Generated Go config-schema fields point to the exact referenced package-input declaration/attribute, including through config aliases. Portable source IDs are lower-case unpadded base32 SHA-256 identities over a domain-separated, length-framed normalized relative URI; they never sanitize punctuation or path separators into a collision-prone filename. Source-map ranges use zero-based Unicode-scalar lines and columns plus zero-based UTF-8 byte offsets, including for combining marks and CRLF sources. Canonical `contract_revision` excludes implementation and deployment inputs; compilation therefore reports `implementation_revision` as null. `scenery build` selects an exact declared Go target, hashes its complete non-standard package/module/embed/native-input graph, and combines that build-input digest with the resolved target to produce the target-specific `implementation_revision`. The resolved target records the selected Go command and compiler paths and SHA-256 identities; host CGO additionally records the resolved C and C++ compiler paths and identities while ambient compiler, linker, include, library, and pkg-config settings are scrubbed. A fixed non-host target with CGO enabled fails until a native-toolchain schema is available. The runtime bundle is written to `.scenery/build/runtime/<target>.json` and copied beside an explicit build output as `<binary>.scenery.runtime-bundle.json`; its schemas are `scenery.go-build-input-manifest` and `scenery.runtime-bundle`.

Resolved `deployment_revision` and artifact/schema revisions are reported independently. Semantic diff, agent reads, and mutation plans use the same canonical graph and compatibility classifications. Change planning validates optional requested kind/schema identities, canonicalizes typed scalars/references, and returns every normalized operation with mandatory resolved kind, schema revision, and `view: "source"` before hashing it. A containing-module rename derives receipts for every descendant through stable source/package lineage; each receipt records old/new addresses, base/target contract revisions, and a digest in both plan and apply receipt. Diff recomputes the digest and revision bindings, loads matching applied receipts from an app root, or accepts `--rename-receipts`; invalid evidence remains remove-plus-add. A declaration shared by multiple module instances cannot be renamed through one instance address because that would mutate all instances ambiguously. Change and deployment plans are immutable, revision-bound, caller-bound, expiring, single-commit transactions with replayable authenticated receipts. Planning retains the exact canonical issued plan at `.scenery/plans/issued/<family>/<plan-digest>.json` with owner-only permissions. Apply requires an exact canonical retained plan before trusting expiry, approvals, operations, source edits, or provider actions; a caller-recomputed content hash is not proof of plan issuance.

The model-facing agent flow deliberately carries only handles and bounded
summaries. `changes.plan` accepts exactly `base_workspace_revision`,
`base_contract_revision` (or null for repair), and `operations`; its response
contains `plan_id`, base/predicted revisions, implementation/deployment status,
a semantic summary, bounded affected resources plus
`affected_resource_count`/`affected_resources_truncated`, risk records plus
`risk_count`/`risk_records_truncated`, required approval scopes, and
required capability names, and `expires_at`. The complete canonical plan, semantic diff,
diagnostics, and source edits remain in trusted app-local state for the
approval/review UI and the explicit `plans.get({plan_id})` operation. The
model-facing `changes.apply` request is exactly `{ "plan_id": "..." }`.
`changes.receipt.get({plan_id})` is the explicit recovery read. It never
requires the model to echo source bytes, caller
identity, claimed capabilities, or approval credentials.
Its response is `{plan_id, status: "applied", receipt}`; apply returns
`{receipt, replayed}`.

Apply authenticates the server-owned execution context and loads the exact
retained plan before any model-controlled value is trusted. When a durable
receipt for the same plan ID exists, Scenery strictly decodes and validates it
against that plan and returns it as success (the response may include
`replayed: true`) before checking first-apply expiry, approvals, base
revisions, or provider actions. A malformed or mismatched receipt fails closed
and is never followed by a second application. The persisted receipt is
immutable. Approval handlers mint or attach plan-bound approval tokens after
user approval; tokens never enter model-visible parameters. The same replay
rule applies to deployment provider actions.

Eve's generated application-MCP `user-approval` response is a provider tool
decision, not an evolution approval token. The current application MCP gateway
does not expose contract-agent mutation methods and no broker converts its
opaque `appr1_` handle into a signed plan token. Approval-bearing
contract-agent applies therefore require a trusted adapter/operator to attach
the token through execution context until a dedicated plan/risk-bound broker
is implemented.

Risk-bearing apply commands accept repeatable `--approval-token <file>` values. Each file conforms to `docs/schemas/scenery.approval-token.schema.json`. Scenery verifies its detached Ed25519 signature against the app-local, non-symlink trust store `.scenery/approval-trust.json`, whose exact shape is `docs/schemas/scenery.approval-trust.schema.json`. Key values are raw 32-byte Ed25519 public keys encoded with standard padded or unpadded base64. A signature has the form `ed25519:<key-id>:<base64-signature>`.

The signed bytes are canonical JSON of exactly `plan_id`, `caller`, the sorted unique `risk_scopes`, and UTC `expires_at`; `signature` is excluded. A trusted approval service uses the public `scenery.ApprovalTokenPayload` function to produce those bytes, signs them with Ed25519, and writes the token file. Tokens are accepted only for the exact plan, caller, requested scopes, and unexpired timestamp. Trust stores and private signing keys are operational state and must not be committed; only public keys belong in the trust store.

Source, lockfile, and generated-artifact sets use one recoverable per-workspace transaction. Scenery readers honor its process-fingerprinted lock; a durable journal restores the prior byte-for-byte state after interruption unless the synced receipt proves commit.

Go generation stages contract packages, provider/application adapters, composition, ABI/provider locks, and descriptor coverage before atomically materializing verified bytes. `std.type.unit` is the exact no-input/no-body type and encodes as `{}` in Go (`scenery.Unit`) and TypeScript (`Unit`); user types merely ending in `unit`, `problem`, or `execution_receipt` are not standard types. Exact sizes accept fractional lexical quantities only when unit conversion produces an integral byte count (`1.5KiB` is `1536`; `0.1B` is invalid). `relative_path` rejects NUL and normalizes each segment with the specification-defined Unicode 17.0 NFC rules. `url` is a normalized hierarchical network URL and rejects opaque or hostless URIs. TypeScript generation provides exact scalar codecs, immutable decoded values, typed outcomes/errors, record constraints and cross-field validation, retry semantics, and metadata. Data, durable, schedule, event, HTTP, CLI, page/renderer, and internal-call runtime adapters register through the same generated composition root.

The HTTP effective graph fixes the current defaults at 64 KiB request headers, 8 MiB buffered request bodies, 16 MiB decompressed requests, 32 MiB multipart bodies, 16 MiB file parts, 1 MiB non-file parts, 128 parts, and 16 MiB buffered responses. Typed responses may split one outcome across body, header, and cookie mappings; generated Go adapters encode every declared scalar and generated TypeScript clients reconstruct the original camel-cased typed payload. Distinct same-status completion mappings are decoded independently and exactly one must validate; the compiler proves disjointness from observable media types and structural wire shapes, never nominal type or destination names, and rejects mappings where overlap cannot be excluded. Multipart clients encode only declared parts and enforce their exact names, kinds, accepted media, byte limits, filename retention, and multiplicity. Optional absent metadata stays absent. Effective response-cookie defaults are path `/`, empty domain, session expiry (`max_age=0`, no `expires`), `secure=true`, `http_only=true`, and `same_site=lax`. Fetch cannot preserve repeated request-header field lines, so a TypeScript target selecting a repeated list/set request header is rejected with `SCN6316`; use explicit comma encoding only when the scalar codec permits it. Scalar response headers consume the standard Fetch combined field value, preserving commas in dates and cache directives. Repeated list/set response headers require a Fetch `Headers.getAll(name)` extension, and response cookies require `Headers.getSetCookie()`; a runtime that cannot preserve the declared repetitions fails with `unsupported_runtime` instead of silently collapsing values. `std.authorization.none` is a valid explicit deny-all policy; it does not make a binding anonymous. `dispatch.wait_timeout` is the canonical wait outcome. Stream delivery and `server_sent_events` declarations fail with `feature_unavailable` until streaming support is implemented in the current contract. Generated TypeScript sets encode and validate canonical JSON element order by UTF-8 bytes across JSON, query, form, and header mappings. Declared transport, admission, and dispatch failures are returned as closed typed failure outcomes; only undeclared/system failures throw, and clients never add an implicit retry. Public `system.internal` responses always use the stable message `contract implementation failure`; the wrapped implementation cause remains available to internal error handling but is never serialized to the caller.

Native `protocol = "cli"` bindings execute directly as `scenery <declared command...>` from the app root. Command and flag names are lower-kebab-case, command paths are unique, and their first segment cannot collide with a built-in Scenery command. `--help`, `scenery completion <words...>`, human output, `-o json`, and exit codes are derived from the binding outcome map. Argument and flag values are decoded with the operation's declared type; required fields must be mapped exactly once. Scenery builds the declared development target, mints the local-developer principal from the OS user, injects only runtime-trusted context fields, runs authorization, and invokes call, wait, or enqueue delivery through the generated composition. Caller input cannot overwrite a context-mapped field.

Fixtures are typed contract resources, not arbitrary SQL. Deployment projection includes only fixtures whose `environments` contain the selected deployment environment. `scenery db seed --env <environment>` uses the same selection and deterministically projects validated PostgreSQL `INSERT`/`ON CONFLICT` statements under `.scenery/fixtures/`; the ordinary seed ledger and destructive-SQL checks still apply.

Generated durable executions may set `external_name` to preserve an existing durable-store task namespace; the name must be unique per engine. The execution `revision` is the persisted input/handler ABI revision. Reusing an external name with an incompatible serialized input requires a new revision and an explicit drain or migration of active jobs; startup reconciliation fails closed when active jobs remain at a different revision.

This document freezes the local developer and agent-facing contract for Scenery.

The goal is to make scenery deterministic and inspectable:
- app shape is explicit
- CLI grammar is explicit
- machine-readable JSON outputs have versioned schemas
- inspect commands are the API; generated files are cache
- app roots, dev runtimes, and capabilities are the user-facing model; substrate paths, ports, backing services, and internal session IDs are debug details

If implementation and this document disagree, treat that as a bug.

## Status

Implemented now:

- `.scenery.json` app config
- `scenery up -o jsonl`
- `scenery worker`
- `scenery worker durable`
- `scenery worker durable jobs ... -o json`
- `scenery worker durable token create -o json`
- `scenery version -o json`
- `scenery help [<command>] -o json`
- `scenery system toolchain list|sync|verify|path`
- `scenery doctor -o json`
- `scenery check -o json`
- `scenery generate`
- `scenery generate sqlc`
- `scenery db shell`
- `scenery db apply`
- `scenery db seed`
- `scenery db setup`
- `scenery db reset`
- `scenery db drop`
- `scenery snapshot save|verify|load`
- `scenery worktree create|list|remove|upgrade`
- `scenery task list|inspect|run|graph`
- `scenery task run <name>`
- `scenery task run <domain>:<name>`
- `scenery validate list|inspect|graph|changed`
- `scenery validate <profile> -o json`
- `scenery harness -o json`
- `scenery harness ui -o json`
- `scenery traces clear -o json`
- `scenery inspect app -o json`
- `scenery inspect routes -o json`
- `scenery inspect services -o json`
- `scenery inspect endpoints -o json`
- `scenery inspect build -o json`
- `scenery inspect paths -o json`
- `scenery inspect generators -o json`
- `scenery inspect durable -o json`
- `scenery inspect storage -o json`
- `scenery inspect validation -o json`
- `scenery inspect ui [-o human|json]`
- `scenery storage ls|stat|put|get|rm|cleanup -o json`
- `scenery traces list -o json`
- `scenery metrics list -o json`
- `scenery inspect docs --for-path <path> -o json`
- `scenery assistant init|sync|status -o json`
- `scenery inspect assistants [--implementation] -o json`
- `scenery logs -o jsonl`

Reserved by contract, implementation pending:
- repo-local runtime and state manifests beyond the command JSON surfaces above

Dev-only or beta surface:
- `scenery up`
- Postgres-only data platform: compiled SQL requirements, managed app database naming, logical schemas, `scenery` schema, and DB lifecycle commands
- `scenery db shell`
- `scenery db apply`
- `scenery db seed`
- `scenery db setup`
- `scenery db reset`
- `scenery db drop`
- `scenery snapshot save|verify|load`
- `scenery worktree create|list|remove|upgrade`
- `scenery generate`
- `scenery task list|inspect|run|graph`
- `scenery task run <name>`
- `scenery task run <domain>:<name>`
- `scenery validate`
- `scenery inspect validation -o json`
- `scenery traces list|metrics -o json`
- `scenery inspect generators -o json`
- `scenery inspect durable -o json`
- `scenery inspect storage -o json`
- `scenery storage ls|stat|put|get|rm|cleanup -o json`
- `scenery system toolchain list|sync|verify|path`
- `scenery doctor -o json`
- `scenery system edge install|trust|status|restart|uninstall|dns|privileged -o json`
- `scenery worker`
- `scenery worker durable`
- `scenery worker durable jobs ... -o json`
- `scenery worker durable token create -o json`
- `scenery traces clear -o json`
- `scenery harness ui -o json`
- dashboard and API Explorer
- local HTTPS edge and frontend routing
- trust-store installation
- native local observability capabilities, backed today by Victoria substrate and managed binary downloads
- schedule UI
- native durable declarations, startup DB reconciliation into the app Postgres database's `scenery` schema, queued job starts, interval schedules, retrying local Go handler execution, durable step/signal helpers, authenticated durable worker lease/heartbeat/complete/fail HTTP endpoints, durable job admin, and `scenery inspect durable -o json` while the Postgres durable execution runtime is implemented under ExecPlan 0097
- `scenery.sh/storage`, app config storage declarations, `scenery inspect storage -o json`, and `scenery storage ... -o json` while the storage runtime boundary and generated browser routes mature
- native `.scn` data sources, entities, views, CRUD, fixtures, pages, and renderers

## App Config

The app config filename is `.scenery.json`.

Schema:
- [scenery.config.schema.json](schemas/scenery.config.schema.json)

Current shape:

```json
{
  "name": "myapp",
  "id": "myapp-dev",
  "root": "app",
  "frontends": {
    "app": {
      "root": "apps/app"
    }
  },
  "envs": {
    "local": {
      "default": true,
      "libraries": {
        "geometry": { "linkage": "source" }
      },
      "frontends": { "app": { "serve": "development" } }
    },
    "production": {
      "domain": "app.example.com",
      "libraries": {
        "geometry": {
          "linkage": "shared",
          "manifest": "dist/libraries/geometry/v1.2.3/geometry.scenery-library.json"
        }
      },
      "frontends": { "app": { "serve": "production" } },
      "deploy": {}
    }
  },
  "watch": {
    "ignore": ["reference/"]
  },
  "generators": {
    "sqlc": {
      "provider": "sqlc",
      "config": "sqlc.yaml",
      "schemas": [
        {
          "sqlc_schema": "auth/db/gen/schema.sql",
          "atlas_source": "auth/db/schema.hcl"
        }
      ]
    }
  },
  "database": {
    "apply": {
      "command": "./scripts/db-safe-apply.sh"
    }
  },
  "storage": {
    "default": "app",
    "stores": {
      "app": {
        "kind": "local",
        "access": "auth",
        "tenant_scoped": true,
        "max_object_bytes": 104857600
      }
    }
  },
  "validation": {
    "default": "quick",
    "profiles": {
      "quick": {
        "description": "Fast agent handoff gate.",
        "cost": "low",
        "steps": ["harness:core", "test:go"]
      },
      "frontend": {
        "description": "Frontend validation.",
        "cost": "medium",
        "paths": ["apps/web/**"],
        "steps": ["task:web:ui-harness"],
        "artifacts": ["test-results/ui-harness/diff-report.md"]
      },
      "full": {
        "description": "Full local quality gate.",
        "cost": "high",
        "steps": ["profile:quick", "profile:frontend"]
      }
    }
  },
  "auth": {
    "enabled": true,
    "auto_bootstrap_database": true,
    "google_oauth": {
      "enabled": false,
      "allowed_scopes": ["https://www.googleapis.com/auth/gmail.modify"]
    },
    "dev_bootstrap": {
      "enabled": true,
      "default_user_email": "owner@example.test",
      "default_user_id": "dev-user",
      "default_tenant_id": "00000000-0000-0000-0000-000000000001"
    }
  },
  "observability": {
    "logs": {
      "include_endpoints": [],
      "exclude_endpoints": []
    },
    "tracing": {
      "include_endpoints": [],
      "exclude_endpoints": []
    }
  }
}
```

`envs.<name>.libraries.<library>` selects the generated facade backend without
changing application imports. `source` directly calls the declared Go handler.
`shared` requires an app-root-relative `manifest` that stays beneath the app
root. Scenery injects
`SCENERY_LIBRARY_<NORMALIZED_NAME>_LINKAGE` and, for shared mode,
`SCENERY_LIBRARY_<NORMALIZED_NAME>_MANIFEST` into the app process; these are
derived runtime inputs, not user configuration knobs.

A package-local source file declares the contract:

```hcl
library "geometry" {
  runtime = "go"
  package = "example.com/app/pkg/geometry"
  version = "v1.2.3"
  artifact { name = "geometry" }
}

operation "render" {
  library = library.geometry
  input   = record.render_input
  handler { method = "Render" }
  result "ok" { type = record.render_result }
}
```

Libraries are Go-only, must belong to a module rooted beneath `pkg/`, require a
canonical semantic version and lower-snake artifact name, and own at least one
operation. Inputs and every success/error outcome are direct records so the
generated source and C-ABI backends share one exact wire contract. The handler
is an exported package function with the generated input/outcome signature; a
library operation cannot also belong to a service.

Rules:
- App root discovery walks from the start directory upward until it finds `.scenery.json`.
- JSON outputs such as `scenery inspect app -o json`, build manifests, harness results, and generator records report the actual config file path/input used.
- `name` or `id` must be non-empty.
- If `name` is empty, scenery falls back to `id`.
- App identity for runtime environment, dashboard routes, local logs, browser harness routes, and local observability is `id` when present, otherwise `name`. `name` remains the display name and source/build package identity.
- `frontends` is optional.
- A configured frontend may declare `"tauri": { "root": "apps/desktop" }`
  to make it the web surface of a Tauri 2 desktop shell. `tauri.root` is
  app-root-relative, must remain beneath the app root, and defaults to the
  frontend root when empty. The resolved directory must contain
  `src-tauri/tauri.conf.json`; Scenery uses only an app-local
  `node_modules/.bin/tauri` supplied by `@tauri-apps/cli`.
- `build.go_flags` is an optional array of literal Go argv entries used for Scenery-owned app compilation. Values are not shell-split; write one argument per item, for example `["-tags=roofmapnet_native"]`. Scenery passes these flags to generated app `go build` invocations and generated-workspace `scenery test` `go test` invocations, while process `GOFLAGS` still applies for local one-off overrides. The normalized flag list participates in the build fingerprint/cache key.
- `watch.ignore` is an optional array of app-root-relative exclusion patterns for `scenery up`. Directory patterns such as `reference/` skip that subtree during watcher setup and rebuild fingerprint scans while leaving Git tracking untouched. `watch.ignore` is exclusion-only; use `.gitignore` for Git behavior.
- Runtime watch snapshots exclude `_test.go` and their test-only embed inputs; ordinary test execution still reads the current test sources. Files explicitly embedded by runtime code remain runtime inputs regardless of their filename. Metadata-only touches or identical-content rewrites do not restart the backend: mtimes decide whether to refresh a content hash, while content, path, permissions and embed ownership decide rebuild identity.
- Backend replacement preflights the candidate's exact spec, runtime ABI, linked contract/implementation/build-input/target identity and supplied storage descriptor before stopping the current app. The generated entrypoint's private `--scenery-runtime-preflight` handshake uses inherited file descriptor 3, separate from stdout/stderr, and exits before SQL/auth initialization, composition registration, listeners or workers. Go package `init` functions run earlier and must not perform application writes. Replacement never overlaps app generations. A failed candidate start restores the retained executable/environment only after candidate termination is confirmed; failed shutdown or shutdown cancellation forbids recovery. A retained/restored generation keeps its serving PID and metadata while the build error remains reported.
- `auth` is optional. When `auth.enabled` is true, scenery registers the built-in standard auth handler and standard auth endpoints. Google OAuth endpoints are registered only when `auth.google_oauth.enabled` is true.
- `observability` is optional.
- Unknown fields are rejected. Runtime diagnostics include the config file path and JSON field path, for example `/repo/app/.scenery.json: unknown .scenery.json field "frontends.app.extra"`. Removed standard-auth naming fields such as `auth.refresh_cookie_name`, the auth `*_env` selectors, and the Google OAuth `*_env` selectors remain rejected; standard auth reads the canonical environment names documented in `docs/environment.md` directly.
- The removed `proxy` app config has no compatibility behavior. Use `frontends` for frontend roots and dev runtime routes for local URLs.
- `.scenery.json` requires an `envs` map and exactly one `local` entry with `default: true`. `scenery up` resolves the default; `scenery up --env <name>` resolves a named entry. Each env may declare `mode`, `domain`, `expose`, `port`, `port_start`, `port_end`, per-frontend `serve`, and an optional `deploy` block. Top-level `deploy`, `dev.routing`, and `frontends.<name>.serve` are unknown fields and fail with their exact JSON paths.
- `envs.local.ui_catalog` (only `envs.local` may set it) points generation at a live `@scenery/ui` catalog source directory, resolved against the app root, instead of the binary-embedded copy. `scenery up` then watches that directory and re-materializes `react/scenery-ui/` in place on change — no rebuild or app restart; staged TypeScript verification redirects the consumer's `@scenery/ui` aliases to the sibling replacement tree, preserves its other resolved path aliases, and gates every sync before commit. A failed sync keeps the previous catalog serving. An absent directory warns and falls back to the embedded catalog so committed relative paths never break other machines; a directory without `index.ts` and `package.json` fails as a misconfiguration. Only the embed's entry set (`package.json`, `global.d.ts`, `index.ts`, `tokens.stylex.ts`, and `components/`) materializes.
- The selected env controls browser routing. Path mode (default) assigns one stable localhost base URL, optionally constrained by its port fields. Its `domain` adds `https://<sanitized-branch>-<domain>` (bare domain on `main`) and its `expose` narrows that origin. Narrowing is resolved against the complete route table before filtering: if the best route for a path is omitted, that path returns 404 instead of falling through to an exposed root frontend. The env name is stored on the session. If host ownership, edge readiness, or the HTTPS probe fails, the runtime keeps serving localhost and never falls through to a different env's domain.
- Agent dev-runtime manifests include `route_namespace`, the app-derived local browser namespace used by routed URLs. `route_namespace.workspace` comes from app identity. `route_namespace.base_domain` defaults to `local.dev`.
- Agent dev-runtime sessions include `environment` plus the existing route manifest. Global `frontends` owns invariant roots/upstreams; `envs.<name>.frontends.<frontend>.serve` selects `development` (HMR), `production` (built `dist/` static serving), or `disabled` for that env. Disabled optional frontends have no process, readiness gate or route; the root frontend cannot be disabled. Omitting a selection keeps its existing environment default. Deployable environments require every configured frontend to select `production`.
- An explicit environment `port` belongs to the primary Git checkout. A linked Git worktree excludes that port and selects its own stable port within `port_start`/`port_end`, reusing its retained available allocation on restart. It does not edit tracked configuration or rebind the original checkout's origin.
- `storage` declares stores in app config. `kind: "local"` (also the empty-kind default) uses immutable payload files and one bounded atomic reference per `(store, tenant, key)`, with checked file/directory synchronization. Managed namespaces belong to the canonical app root/worktree; a branch switch retains data, another root receives independent data, and equal app/store names grant no cross-root sharing. `default` selects a store; stores accept `kind`, `access`, `tenant_scoped`, and `max_object_bytes`. Access defaults to `auth`; `private` stores are not externally reachable. `storage.cell_id` and `storage.share` are rejected with an explicit migration diagnostic. Unknown fields/kinds fail validation. Legacy cells are never attached automatically; use the [migration runbook](runbooks/worktree-storage-migration.md).
- App processes, workers and app-local tasks receive only the strict current `SCENERY_STORAGE_CONFIG` runtime artifact when storage is configured. Its managed descriptor binds canonical root, retained worktree key and incarnation; private proxy calls echo and revalidate that binding. A headless runtime may use an explicitly provided absolute private external root with its own validated format/ownership, or a validated managed binding. Declared storage with missing, empty, stale or invalid runtime config fails closed. Explicit external roots never gain managed purge authority. No cell-ID environment selector or config fallback remains. App code uses `scenery.sh/storage`, never hidden paths or sockets.
- Stores with `tenant_scoped: true` use an explicit tenant field in the logical tuple, never a caller-visible physical prefix. Standard-auth external routes derive tenant identity solely from verified auth state and ignore caller-supplied tenant headers. Private/internal calls require auth context or `storage.WithTenantID`; CLI calls require `--tenant` for a tenant-scoped store and reject it for an unscoped store. Tenant IDs and keys are case-sensitive UTF-8; ambiguous or conflicting tenant sources fail closed.
- Content type and case-sensitive string-map metadata are stored atomically with the payload reference. Content hashes describe bytes; opaque ETags identify each committed mutation, including metadata-only rewrites. HTTP transports carry the complete metadata map as bounded base64url JSON in `X-Scenery-Storage-Metadata`, not canonicalized per-key headers. `If-None-Match: *` is create-only, and an exact single strong `If-Match` is conditional overwrite/delete. Conflicts return HTTP 412 / CLI exit 3 without publishing a candidate. Missing unconditional delete is idempotent. Invalid validators, ranges and metadata fail before mutation.
- Reserved storage HTTP routes are app data-plane runtime routes mounted only when `SCENERY_STORAGE_CONFIG` is present. They are production-supported under the same operator-proxy storage runtime contract as `scenery.sh/storage`. `GET /__scenery/storage/<store>?prefix=<prefix>&delimiter=/&cursor=<cursor>&limit=<n>` lists objects. `PUT /__scenery/storage/<store>/<key>` uploads a streamed object and returns the object metadata as JSON. `GET` and `HEAD /__scenery/storage/<store>/<key>` download object bytes with `Content-Length`, `Content-Type`, `ETag`, `Last-Modified`, `Accept-Ranges`, and byte-range support. `DELETE /__scenery/storage/<store>/<key>` deletes one object, and `DELETE /__scenery/storage/<store>/<prefix>?recursive=1` deletes by prefix. Public routes enforce the store access policy: `auth` requires the app auth handler and `private` returns permission denied on the external HTTP surface. The same reserved storage routes are also registered on the runtime private route table for Scenery-internal, non-external storage work.
- SQL requirements come from the compiled application, never a second config list. `dev` and `dev.services` are rejected. Registered services' typed `data_source` dependencies supply canonical identity, provider/capabilities, declaration provenance, lifecycle, and the logical `config.database` name. Different module instances remain distinct requirements; explicitly shared sources retain all consumers. Logical names normalize to PostgreSQL schemas; reserved names, overlong names, and distinct names colliding on one schema fail compilation before provisioning. Standard auth contributes the reserved `scenery` binding when `auth.enabled`; registered durable executions contribute it through their engine. An explicit `SCENERY_DURABLE_ENDPOINT` supplies durable-only requirements remotely, but does not remove an auth requirement.
- `scenery inspect app -o json` returns `sql_requirements` (always an array) from the current successful compilation. Each item includes `kind`, canonical `address` (or framework `config_path`), `provider`, `capabilities`, `name`, `schema`, `lifecycle`, `consumers`, and `origin`. Inspection does not connect to or provision SQL. Invalid source remains a compilation failure; retained allocation evidence cannot stand in for a valid current graph.
- An explicit app/setup `DATABASE_URL` selects external PostgreSQL supply: Scenery does not create/delete its server or database, and equal URLs intentionally share data. Without it, local provisioning requires every selected SQL requirement to declare `lifecycle = "managed"`; external/attached/ephemeral requirements do not authorize allocation. `scenery up` uses the existing dedicated container and volume per canonical app root/worktree, one app database, logical schemas and `scenery`. A no-SQL app starts without PostgreSQL; `db list -o json` succeeds with `database: null` and performs no allocation. Managed names derive from app ID and the canonical app root.
- App processes and setup receive `DATABASE_URL`, per-binding `<SERVICE>_DATABASE_URL`, and `SCENERY_DATABASE_JSON` describing resolved SQL supply (`managed` or `external`). Generated entrypoints configure those existing bindings before constructors, without database IO; explicit per-binding URLs remain supported in standalone generated runtimes. `db.Get()` selects the single supplied application binding (excluding framework `scenery` when another binding exists); ambiguous calls require a name. `db.Get(name)` consumes supplied bindings, or explicit endpoint supply for a named standalone caller, and never discovers `.scenery.json`. Workers with local SQL requirements require explicit `DATABASE_URL`.
- Retained ownership, not current requirements, controls database stop, cleanup and snapshot recovery. Snapshot schemas come from the selected actual database catalog, not current declarations; invalid or removed `.scn` does not strand owned data. An archive is never proof of target ownership. Existing stopped-owner, verified-allocation and explicit overwrite-approval requirements still apply.
- `scenery up` prepares declared local DB setup before the app process starts. When app config declares `database.apply`, service-local seed files, typed fixtures, or `database.seed.commands`, the supervisor runs the same split lifecycle as `scenery db setup`: apply first, then seed. It passes the same managed database URL env values that the app child receives, so setup targets the dev-runtime database. Successful setup is fingerprinted from `database.apply` config plus every seed SQL, fixture, command definition, and declared command-input hash; ordinary rebuilds skip setup until those inputs change. Apps can set `database.seed.enabled: false` to opt out of every seed kind.
- Native TypeScript clients are declared with `typescript_client` resources in `app.scn`; `materialization = "source"` writes the managed `output_root`, while `"cache"` writes `.scenery/gen/typescript/<name>`. Generate either with `scenery generate --target typescript_client.<name>`. Standard Google OAuth contributes framework-owned connection start, connection status, and disconnect resources to inspection and client generation when it is enabled; these describe the existing runtime handlers without adding a second runtime composition path. An optional singleton `react { tsconfig = "path/to/tsconfig.json" }` block adds a managed `react/` subtree: one adapter per declared `content_page`, `table_page`, `split_page`, `workspace_page`, or `detail_page`; typed `routes.generated.ts`; the TanStack-only `app.generated.tsx` route-tree/shell adapter; `index.ts`; and the binary-owned `@scenery/ui` catalog under `react/scenery-ui/`. Generated search validators read each authored query wire name (including snake-case names) and expose its camel-case TypeScript property. Dynamic authored path segments become TanStack route segments and are passed to generated detail components as typed string params. `createSceneryApp` combines generated pages with one app-owned `SceneryRouteDescriptor` array and fixed auth/top-bar/content/link/icon slots. Its optional generated `client` option is passed to every generated page, so one app-owned `PublicApiClient` can supply bearer authentication, custom fetch behavior, or a non-default API base without replacing generated routes. The generated adapter owns the root/shell route tree, `Outlet`, active navigation, intent preloading, and catalog `ClientAppShell`; TanStack Router remains a consuming-app peer and no catalog file imports it. Generated loaders otherwise use the browser-facing `/api/` route on the current origin, accept an optional generated-client `client` prop for app-owned fetch/auth behavior, preserve authored order, and run through the consuming app's TanStack Query client. Stable page-address query keys provide caching, deduplication, retry, and invalidation; typed client failures remain renderable data, while exhausted transport or decoding exceptions map back into the same page error state. Persistent storage is an app-owned QueryClient policy and is not enabled for arbitrary generated results. Reusable catalog components and blessed Astryx primitives are exported from `react/scenery-ui/index.ts`; semantic StyleX variables are the `t` var group in the generated-ownership-marked `react/scenery-ui/tokens.stylex.ts`. Both surfaces keep Astryx, StyleX, React, TanStack Query, and TanStack Router as peers, and the consuming React tree provides one `QueryClientProvider`. A consuming app aliases `@scenery/ui` to the materialized `index.ts` in TypeScript and its bundler. Apps using semantic tokens also alias `@scenery/ui/tokens.stylex` to the materialized defining module in TypeScript, the bundler, and the StyleX compiler plugin's own `aliases` option; a TypeScript-or-bundler-only alias is insufficient because StyleX resolves defining modules independently. Direct Astryx imports remain the escape hatch for unblessed UI. The descriptor records `ui_catalog_roots`. Before any artifact commit, Scenery stages the whole target beside its final root and runs the exact checksummed managed TypeScript 7 `tsc` binary with the declared config; `SCN6320` identifies an incompatible declared override, `SCN6321` an unrelated reachable application error, and `SCN6322` missing checker/config/dependency readiness. Generation never invokes Node, bun, or a `PATH` TypeScript compiler.

- Every generated page macro may declare optional opaque, case-sensitive `application_key` and `access_key` strings; blank or whitespace-only values are invalid and nonblank values are preserved exactly. A `workspace_page.tab` may declare the same fields, inherits only its workspace's `application_key`, and never inherits `access_key`. Generated `SceneryRouteDescriptor` values expose `applicationKey` and `accessKey`; `createSceneryApp` returns `{router, App, routes}` and exports `matchSceneryRoute(routes, path)`, whose `$parameter` segments match one nonempty segment, normalize trailing slashes, ignore query/hash suffixes, and lose to an exact static route. `slots.resolveAccess(target, currentPath)` is one synchronous, read-only presentation decision with `allowed`, `pending`, or `denied`; the same result hides navigation and workspace tabs and gates direct routes before their component is invoked. An app-owned `slots.useNavigationRevision()` hook may subscribe the generated shell to external access state; each revision render recomputes the same filtered navigation and side-nav toggle without adding a second navigation source. A denied tab URL is replaced with the first allowed tab; zero allowed tabs render the pending/denied slot. `navigationFilter(route, currentRoute, currentPath)` and `contentGroup(currentRoute, currentPath)` receive descriptors, with no path-only signature. This frontend access surface is not backend authorization: applications still authorize every operation and own entitlements, policy loading, and business scopes.

- A CRUD `list` block is an explicit public capability allowlist: `filters` accepts string or enum fields as repeated exact-match query values and datetime fields as `<field>_from`/`<field>_to`; optional `search` names string fields searched together with escaped, case-insensitive substring matching. `sorts`, optional `default_sort = { field, direction }`, and `max_page_size` control one-column keyset pagination. The generated list result is `{items, next_cursor?}`. Cursors bind canonical filters, search, injected tenant scope, sort field, and direction and use the entity primary key as a stable tie-breaker; a mismatched cursor returns the typed `invalid_cursor` request error, and an excessive limit is clamped.

- `status_map` is reusable presentation metadata: each uniquely named `status` supplies a non-empty label and one current Astryx badge variant. Badge columns and string/enum filters may reference it; generated clients emit typed `StatusMap` constants and compile-time-check the complete variant vocabulary against the catalog `BadgeVariant` type.
- `form_dialog` binds one mutation HTTP binding whose operation input is a record. Fields derive from that record; optional `field` blocks override labels, placeholders, `text`/`textarea`/`select` control choice, and status-map labels. Current generated controls support string and closed-enum inputs. Typed outcomes and transport failures render inline, success closes the dialog, and the owning table's list and stats queries are invalidated.
- Table stats tiles support `plain`, `money`, `count`, and `percent` appearances, an optional formatted `sub` field and label, and semantic icons. A table tile can name a declared filter or typed predicate and exactly one typed `value` or `clear = true`; selection and toggle/clear behavior share the table request state. Date/datetime filters can declare labeled `today`, `last_7_days`, and `month_to_date` presets, which calculate local-calendar inclusive bounds and send the existing paired typed inputs.
- `react_component` declares only a symlink-safe workspace-relative `module` and named export. `table_page` requires `path`, `title`, at least one visible `column`, and either a CRUD `source` with list/HTTP projections or a call-delivery HTTP binding source. A binding source also requires `items`, naming the sole result record's `list(record)` field. It may return that complete list without pagination, or declare `pagination { page, page_size, total }`, mapping two distinct integer operation inputs and one integer result field for numeric page navigation. `metadata = ["summary", "types"]` projects named auxiliary result fields (never `items` or pagination `total`) into typed response-aware slot context. `query { search, sort, direction }` explicitly maps supported query controls to operation inputs; `search_hidden = true` delegates the visible search input to the app toolbar while retaining the same mapped query, debounce, and page reset. Each `filter` may use `input` to map its row field to a different optional/defaulted scalar or list input. A labeled `predicate "input" { value = <typed literal> }` supplies a fixed invisible operation input; mappings and predicates cannot collide. CRUD sources retain their generated cursor contract and accept only generated same-named list filter mappings. The page also accepts `description`, optional `loading_label` and `error_title` request-state copy, positive `page_size` (bounded by a positive CRUD maximum when present), `row_link`, labeled `filter`/`sort`/`group` children, singleton `stats`, `row_detail`, `row_action`, `export`, `toolbar`, `footer`, and `empty` blocks, plus repeated `action` blocks opening `form_dialog` resources. Group declarations are valid only for binding-backed complete-list pages; cursor-paginated CRUD and numeric-page binding tables reject grouping. Each group names a row field, may provide a label, leading section `order`, and one page-wide `default = true`, and produces a runtime Group selector with None plus collapsible counted sections. A stats source is a unit-input call-delivery HTTP binding with one flat numeric/string result record; declared tiles render above the table. Columns may use `appearance = "auto"|"text"|"number"|"datetime"|"badge"`, badge columns may reference a `status_map`, column `hidden = true` keeps an export-only field out of the grid, and `export = false` keeps a display-only/custom field out of CSV. Filters and sorts must be allowlisted by the selected source contract; generated string selectors require a status map for finite options. `filter.hidden = true` preserves the typed query mapping for an app toolbar but omits that filter from built-in selectors, the popover, and chips; it cannot also be pinned. A toolbar defaults to `placement = "header"`; `placement = "content"` renders it immediately above the table. Filters and `empty` receive `TablePageResultContext`; `footer` receives it below the table; toolbar context is optional before the first result. The context contains rows, optional total/truncation metadata, optional typed projected result metadata, filtered state, `isPlaceholderData`, `isRefreshing`, the current query, and controls to set/clear one declared enum-filter value, set search text, or refresh. Filter and search changes reset pagination and row UI; refresh preserves query inputs. Loaded-row CSV is UTF-8 with an Excel-compatible BOM, uses RFC 4180 line endings, leaves empty cells empty unless overridden, hardens spreadsheet formulas, and expands `{date}` from the local calendar. `row_detail.component` receives the exact row type and defaults to `presentation = "inline"`; `presentation = "panel"` opens a resizable right panel. Mutually exclusive `row_action.component` receives `{row, onClose}` and owns selected-row workflow. `QueryTable` owns controlled query state, cursor or numeric pagination, result count, export, complete-list grouping, selection, and request state. Expansion produces the ordinary page plus a built-in web renderer. `scenery schema scenery.table-page -o json` is the authored grammar; `scenery compile --view expanded -o json` exposes the derived resources.

Generated table pages default to `scroll = "table"`, keeping controls fixed while the grid scrolls. `scroll = "page"` instead delegates vertical scrolling to the page so stats, controls, and rows move as one surface.

- A custom string-filter component may own dynamic options without declaring a fake `status_map`; the finite-option requirement applies to catalog-generated string selectors.

- Every generated page macro may declare repeated `search "<name>" { type = <type> }` blocks. Search values are optional and support `string`, `bool`, and closed enums; `SCN2619` rejects duplicate names, unavailable/open enums, unsupported shapes, or invalid navigation metadata. Optional `nav_group`, `nav_order`, `nav_label`, `nav_icon`, and `nav_active_paths` fields place the route in generated navigation; `title` is the label fallback and a page without `nav_group` stays out of navigation. Generated validators normalize invalid or absent URL values to `undefined`.

- `content_page` is the single-column generated shell. It requires `path`, `title`, and one app-owned `content` `react_component` slot; `source`, `actions`, `aria_label`, and positive pixel `max_width` are optional. With `source`, the call-delivery HTTP operation must have unit input, exactly one result, and an inherited internal binding; both slots receive typed raw `{state}` props using the shared `RequestState` vocabulary. Without `source`, no load binding or query is generated and the static slots receive no request-state props. The generated adapter renders catalog `Page` with `actions` in its header. Expansion produces the ordinary page plus the built-in content renderer. `scenery schema scenery.content-page -o json` exposes the authored grammar.

- `detail_page` is the generated one-record surface. It requires a dynamic absolute `path`, a call-delivery HTTP `source` whose operation has record input, exactly one record result, and at least one declared business error mapped by that HTTP binding to status 404; this makes entity absence a typed client completion instead of `system.internal`. It also requires a title and at least one field section. Each route segment maps by name to a supported scalar operation input or uses `param "route_name" { input = "field_name" }`; mappings are unique. Fields address top-level result fields, support text/number/datetime/badge presentation and `status_map`, and may set `hide_empty = true` to omit only null or empty-string values. `presentation = "page" | "dialog" | "both"` defaults to the routed page; both wrappers share one generated content component. A related `table` maps one route parameter into one type-compatible operation input not otherwise claimed by that binding-backed `table_page`, and renders it without nested page chrome. Declared `action` blocks reference same-module `form_dialog`s whose inputs can be seeded from matching result fields. The optional typed app-owned `actions` slot receives `{data, params, onMutated, onClose?}` for richer workflows; `onMutated` invalidates the record and related-table query keys. Expansion produces the ordinary parameterized page plus the built-in detail renderer. `SCN2629`-`SCN2633` report invalid detail contracts. `scenery schema scenery.detail-page -o json` exposes the authored grammar.

- `split_page` is domain-neutral sidebar/detail composition. It requires `path`, `title`, a call-delivery HTTP `source` whose operation has unit input and one result, and `sidebar` plus `detail` `react_component` slots. Optional `sidebar_actions` and `detail_header` slots receive the same typed raw `{state, selection, onSelectionChange}` props; `selection` is `string | null`, passing `null` to `onSelectionChange` removes the configured query parameter, and generated pages re-read that parameter on `popstate` so Back/Forward navigation updates rendered selection. `sidebar_label` names the sidebar landmark and `query_parameter` controls URL-backed selection. The catalog `SplitPage` defaults its section and sidebar labels from a string `sidebarTitle`; a non-string title requires explicit labels. Its grid fills its own box without requiring a positioned ancestor, and both header rows use one shared height/chrome primitive. Authored strings emitted directly into generated JSX attributes use brace-wrapped JavaScript expressions, so quotes and backslashes remain valid literal content; labels and enum values inside object/array props remain ordinary JavaScript literals. The source operation must also have an inherited internal binding for page loading. Expansion produces the ordinary page plus the built-in split renderer. Scenery owns layout, transport, request state, and selection wiring; each app-owned slot is responsible for rendering loading/error/ready branches and should use the catalog `QueryState` component for consistency. `scenery schema scenery.split-page -o json` exposes the authored grammar.

- `generators.sqlc` is a beta lifecycle config for SQLC generation. `provider` may be empty or `sqlc`; `config` defaults to `sqlc.yaml`; schema files listed in `sqlc.yaml` are treated as inputs. Explicit `atlas_source` schemas are refreshed only when an explicit `dev_url` is configured; `postgres://`, `postgresql://`, and `docker://` Atlas dev URLs pass through to Atlas unchanged. SQLC schema blocks whose schema path belongs to a configured database service must use a Postgres SQLC engine (`postgresql`/`postgres`). SQLC generation is a generated-source lifecycle and must not apply database schema or seed data.
- `database.apply` is a beta DB lifecycle escape hatch with an explicit shell `command`, optional `cwd`, and string `env` overlay. The accepted split lifecycle moves database mutation to `scenery db apply`; SQLC refresh stays under `scenery generate sqlc`.
- `database.migrations` selects ordered app-authored SQL with `{ "service": "projects", "directory": "projects/db/migrations" }` per distinct compiled application SQL binding. It is mutually exclusive with `database.apply.command`. Files are workspace-contained, non-symlink regular files named `0001_description.sql`, contiguous from 0001, with at most 256 files per service, 4 MiB per file and 16 MiB per service. Missing, unsafe, unordered, or invalid source inputs fail before database allocation. Scenery owns transaction and session control; migration files contain SQL transformations, not `BEGIN`, `COMMIT`, `ROLLBACK`, session-control statements or psql commands.
- Service-local `SERVICE/db/seed.sql` files are immutable initial data. They are not Atlas schema input or SQLC input. The accepted lifecycle applies seed data through `scenery db seed`. The implementation fails closed on changed previously-applied seed files and obviously destructive seed SQL rather than adding force or reseed escape hatches. Large validated reference datasets can instead use a named `database.seed.commands` entry with a target service and explicit regular, non-symlink workspace input files. Its fingerprint includes the normalized command definition and complete input contents. The command runs after SQL and typed-fixture seeds with `DATABASE_URL` pinned to the target service; success replaces its prior command hash, unchanged inputs skip, and failure retains the prior hash. Commands must make their own data mutation atomic or idempotent because process success and ledger recording cannot share one database transaction.
- Code tasks are beta app-local targets under `<domain>/tasks/`. Targets use `<domain>:<name>`, and both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`. `scenery task list`, `scenery task inspect`, and `scenery task run <domain>:<name> [-- task args...]` discover and execute them without requiring the app model to parse cleanly.
- `validation` is a beta app-owned quality-gate layer. It has `default` and `profiles`; each profile can define `description`, `cost` (`low`, `medium`, or `high`), `paths`, `steps`, string `env`, and advisory `artifacts`. Profile names use the configured-task name rule and cannot contain `:`.
- Validation profile steps are not shell. They accept `profile:<name>`, `task:<domain>:<name>`, `harness:core`, `harness:ui`, `harness`, `check`, `test`, `test:go`, `generate`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`.
- Profiles may declare `commands: [{"command": "go", "args": ["test", "./..."]}]`. These literal executable/argument vectors run sequentially after the profile's referenced `steps`, at the app root with the profile environment, without shell parsing. Empty executables and NUL bytes are rejected; empty arguments are preserved. A profile requires at least one step or command. Inspection exposes commands, dry runs resolve exact argv, and execution captures each command separately with ordinary fail-fast evidence.
- `scenery db branch`, `scenery db path`, and `scenery db snapshot` are removed. Worktree isolation uses per-worktree managed Postgres database names, and `scenery worktree create` only creates the Git worktree. Portable save/load is tracked by active plan 0100.
- Declaring `storage.stores` is sufficient for managed `scenery up`; storage has no separately managed process, database, index or catalog. The retained root owns `<agent-home>/worktrees/<worktree-key>/storage/` with stable maintenance/mutation lock files, an incarnation owner and generation directories. Normal callers never select those internals. Uploads stream to private immutable versions; atomic references publish complete metadata and bytes. Reads retain shared maintenance ownership through stream close. Capture/restore/reclamation acquire exclusive maintenance, and offline lifecycle operations also hold the verified stopped worktree's live and operation locks. Unknown/corrupt material is not inferred to be absent. Clones are independent files, never hardlinks or live cache references.
- Standard auth uses the `scenery.sh/auth` top surface and stores DB-backed auth state in the app Postgres database's `scenery` schema.
- Standard auth owns its framework tenant tables, including `scenery.scenery_auth_tenants`. Apps do not need an app-local `tenants` service, package, or table for standard auth; app-local tenant services are product-domain APIs and schema only.
- Standard auth registers `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization/invite/impersonation endpoints, and local `/users/dev-bootstrap`. When `auth.google_oauth.enabled` is true, it also registers raw `GET /auth/google/start`, raw `GET /auth/google/callback`, typed `POST /auth/google/connect/start`, typed `GET /auth/google/connection`, and typed `POST /auth/google/connection/disconnect`.
- Standard auth endpoints appear in `scenery inspect routes|services|endpoints -o json` and in generated TypeScript clients. Disabled Google OAuth endpoints are absent from inspect output and generated clients. When Google OAuth is enabled but `GOOGLE_OAUTH_CLIENT_ID` or `GOOGLE_OAUTH_CLIENT_SECRET` is missing, `scenery check -o json` returns an `auth` warning. `auth.google_oauth.allowed_scopes` declares the Google API scopes an app may request through the connection flow. `POST /auth/google/connect/start` returns a Google authorize URL whose `redirect_uri` is the shared `/auth/google/callback`; the callback dispatches connection states by OAuth state purpose so apps can reuse the sign-in redirect URI registered in Google Cloud. `AUTH_TOKEN_CIPHER_KEY` is the canonical base64 32-byte AES-GCM key used to encrypt stored Google refresh/access tokens; local development derives a dev key from the local JWT secret when this env is absent.
- `auth.auto_bootstrap_database` applies the first standard-auth schema bootstrap at runtime. It is useful for local fixtures; production deployments should manage schema changes deliberately.
- Generated binaries accept `SCENERY_ROLE=all|api|worker`. `scenery up` uses the default combined role. `scenery worker` uses `worker`.
- Native durable executions and schedules are declared in package `.scn` files and register through the generated application composition. Runtime startup requires `DATABASE_URL` and reconciles those declarations into the app Postgres database's `scenery` schema. Generated `all` and `worker` roles run the local durable worker loop; the `api` role does not execute durable jobs. `durable.Step` persists local handler step results by job/key and reuses succeeded results, while `durable.Signal` appends a JSON signal row and event for a run. Remote-worker endpoints require bearer tokens stored only as hashes and fence heartbeat/complete/fail with `worker_id` plus `lease_id`. `scenery inspect durable -o json` emits `scenery.inspect.durable` with native declarations, service schemas, and redacted app database metadata.

## CLI Grammar

Current implemented grammar, grouped by surface:

### Runtime and sessions

```text
scenery up [--env <name>] [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [--desktop] [--verbose] [-o jsonl] [--detach] [--wait ready|registered]
scenery logs --follow [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [-o jsonl|-o json]
scenery logs query [--app-root <path>] --query <logsql> [--since <duration>] [--start <time>] [--end <time>] [--limit <n>] [--timeout <duration>] [--fields <csv>] [-o json|-o jsonl]
scenery logs tail [--app-root <path>] --query <logsql> [--since <duration>] [--timeout <duration>] [--fields <csv>] [-o jsonl]
scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>]
scenery ps [-o json] [--app-root <path>] [--watch]
scenery down [--app-root <path>] [--db] [--state] [--all] [-o json]
scenery prune --older-than <duration> [--app-root <path>] [--db] [--state] [--all] [-o json]
scenery worker [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker durable --endpoint <url> --token <token> [--service <name>]... [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker durable jobs list|inspect|cancel|retry [job-id] --service <name> [--app-root <path>] -o json
scenery worker durable token create --service <name> [--name <name>] [--id <id>] [--app-root <path>] -o json
scenery logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--follow] [-o jsonl|-o json]
```

### System, toolchain, and doctor

```text
scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [-o json]
scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [-o json]
scenery system agent cleanup [--remove-state] [-o json]
scenery system edge install|trust|status|restart|uninstall|dns|privileged [-o json]
scenery help <command> [-o human|json]
scenery help all
scenery help -o json
scenery version [-o json]
scenery system toolchain list [-o json] [--include-source-locks] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain sync [-o json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain verify [-o json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
scenery system toolchain path [-o json] --tool <name> [--platform <goos/goarch>]
scenery doctor [--app-root <path>] [-o json]
```

### Deploy

```text
scenery deploy <ssh-target> [--app-root <path>]
scenery deploy --env <name> [--app-root <path>]
scenery deploy enable [--app-root <path>] [-o json]
scenery deploy disable [--app-root <path>] [-o json]
scenery deploy publish [--app-root <path>] [-o json]
scenery deploy status [-o json]
scenery deploy setup [--acme-email <email>] [--acme-ca production|staging] [-o json]
scenery deploy resume [-o json]
scenery deploy teardown [-o json]
```

### Build, check, and generate

```text
scenery build [--development] [--verify-generation] [--app-root <path>] [--target <go-target>] [--output <path>] [-o human|json]
scenery build --lib <name|address|artifact> [--version <vN.N.N>] [--platform all|host|darwin/arm64|linux/amd64|<csv>] [--app-root <path>] [--output <directory>] [-o human|json]
scenery build --desktop [--env <name>] [--app-root <path>] [-o human|json]
scenery check [--app-root <path>] [-o json]
scenery generate [--target contracts|typescript_client.<name>] [--check] [--app-root <path>] [-o human|json]
scenery generate sqlc [--app-root <path>] [--dry-run] [-o json]
scenery provider lock [--check] [--app-root <path>] [-o human|json]
scenery test [--app-root <path>] [go test flags/packages...]
```

`build --development` builds a candidate with the same source-linked assistant
assets as `up`, without starting or replacing a runtime. It defaults to the
development Go target and cannot combine with `--lib` or `--desktop`. Ordinary
`build` continues to embed production assets. Use the development variant when
comparing candidate build-input identity with a served development generation.

`build --development --verify-generation` additionally requires a prepared desired
framework and returns `candidate_identity` in `scenery.build.result`. It validates
the copied runtime bundle's exact schema/spec/producer, ordered checksummed input
manifest, target and selected framework source/executable digests. It does not
start, stop or probe a runtime. Other builds return `candidate_identity: null`.
The build also materializes `<binary>.scenery.verify.ts` and returns
`verification_module_path` plus `verification_module_digest` (both empty without
the flag). This producer-embedded TypeScript helper exports `responseIdentity`,
`assertSameBuild`, `assertServedResponse`, `assertSession` and `assertRestart`.
Consumers must confine the returned path to their output and verify its digest
before importing the verified bytes. Helpers compare each response's linked
identity and process, bind the config response to a separately verified session,
and require a same-build restart to retain the root while replacing API and owner
PIDs. HTTP/session acquisition and application assertions remain consumer-owned;
a candidate is not live evidence, and Go identity does not cover frontend source.

Plain `generate` has no `--dry-run`; use `--check` to detect drift without
writing application artifacts. Default generation publishes ordinary Go and
selected TypeScript outputs as one artifact set. `--target contracts` is a
Go-only bootstrap requiring a valid graph, not valid app implementation or live
infrastructure. Targeted TypeScript generation stays targeted. Retired
`--materialize`, `--prune-materialized-go` and `--merge-editor-workspace` switches
are invalid requests with the current replacement command, not compatibility modes.
Invalid flags and target selections return exit 2 / `SCN8001`, with an actionable
message. `build` without a target selects the `artifact` role; if none exists it
lists declared targets and suggests an explicit `--target`.

Generation's `scenery.cli` envelope retains `data.target` and `data.generation`
and `data.clients` (selected target address, HTTP `bindings` count and an
optional warning `message`). There is no `data.editor_workspace` field. Empty
HTTP/assistant selection is successful but warns about package exports and
gateway/include selectors. Fresh checkouts require explicit generation before
ordinary Go tools. A published Go module must include its required generated
source; ordinary application commits need not track it.

`provider lock` is an explicit, offline source transaction. It reads root
provider declarations and creates or refreshes their bundled provider entries
in `app.lock.scn`; compilation never invokes it. It preserves unrelated module,
extension and external-provider entries, refuses malformed/symlink locks and
unknown unpinned providers, and performs no registry downloads. JSON `data`
contains `path`, `changed`, `checked` and the selected `providers` lock entries.
`--check` writes nothing, reports required changes and exits 1 on drift; a
current lock exits 0. Builtin integrity binds descriptor schema, source,
capabilities, config, instance kinds and provider ABIs, but excludes producer
and the unrelated global `spec_revision`. After a provider semantic change,
explicitly relock, review the lock diff, regenerate and validate the app.

Schema lookup uses exact catalog identities, for example
`scenery schema scenery.execution -o json`. An unqualified known kind remains
an invalid request and suggests its qualified spelling; it is not an alias.

### Assistants

```text
scenery assistant init <name> --mcp-server <name> --client <name> [--dry-run] [--app-root <path>] -o json
scenery assistant sync <name> [--app-root <path>] -o json
scenery assistant status <name> [--app-root <path>] -o json
```

`assistant init` creates a provider-neutral assistant declaration and the
minimal authored scaffold through the revision-bound workspace transaction.
`--dry-run` plans the same edits without changing source or issued-plan state;
an existing authored file is always preserved. `assistant sync` validates the
exact package and lock bytes, resolves Scenery's managed Node/npm toolchain,
and installs into the content-addressed `.scenery/assistant-cache/<lock-digest>`
directory. Repeating sync with unchanged bytes reuses that cache; it never
modifies the assistant's authored package files. `assistant status` is the
read-only provider-neutral runtime snapshot; implementation details require
the explicit `scenery inspect assistants --implementation` surface.

## Assistant model

Assistants are application resources in the current `.scn` graph. A package
operation becomes an MCP capability through an ordinary binding with
`protocol = "mcp"`, `delivery = "call"`, and exactly one `mcp` child. A root
`mcp_server` composes local bindings and optional `mcp_connection` resources.
Connections currently support Streamable HTTP, `none`, `bearer`, or one named
header backed by a typed Scenery secret; Scenery owns the remote client,
namespace/filter projection, readiness, and credential handling. The helper
receives only the private provider-neutral Scenery MCP surface.

An `assistant` binds one MCP server to an authored implementation and a
Scenery-owned public conversation surface. The supported source shape is:

```hcl
assistant "support" {
  mcp_server = mcp_server.support

  implementation {
    adapter      = "eve"
    source       = "./assistants/support"
    package      = "./assistants/support/package.json"
    package_lock = "./assistants/support/package-lock.json"
  }

  surface {
    gateway        = http_gateway.public_api
    path           = "/assistants/support"
    authentication = std.authentication.none
    authorization  = std.authorization.public
    pipeline       = std.pipeline.empty
    session_access = "initiator"
    client         = typescript_client.public_api
  }
}
```

The `implementation.adapter` value is developer/operator data. The current
managed adapter is `eve`, but that name is not part of the supported public
contract. It may appear in authored source, implementation graph views, the
private runtime descriptor, private logs, and the explicit
`inspect assistants --implementation` view only. Public routes, generated
browser code, OpenAPI, public schemas, response headers/cookies/bodies, public
events/errors, and default inspection omit provider identity and known provider
signatures.

The public surface is five routes beneath `<surface.path>/v1/conversations`:

| Route | Method | Purpose |
| --- | --- | --- |
| `/` | `POST` | create a conversation and first run |
| `/:conversation_id/turns` | `POST` | submit a follow-up user turn |
| `/:conversation_id/events?after=<cursor>` | `GET` | stream normalized NDJSON events after a cursor |
| `/:conversation_id/approvals/:approval_id` | `POST` | approve or deny one capability proposal |
| `/:conversation_id/runs/:run_id/cancel` | `POST` | cancel one active run |

Create and turn requests carry a bounded user message. Responses use opaque
`conv1_`, `run_`, and `appr1_` handles. Event streams use
`application/x-ndjson`, monotonic sequences, and the provider-neutral event
types in `scenery.assistant.public-event`; `after` is strictly exclusive, so
clients can reconnect without duplicate events. Public approval values are
`approve` and `deny`; helper control uses the private `allow` and `deny`
vocabulary. Public errors use the closed codes in
`scenery.assistant.public-error` and stable, redacted messages.

Anonymous public surfaces issue the HttpOnly, SameSite=Lax
`scenery_assistant_initiator` cookie and bind every conversation, approval, and
cancel operation to that initiator. Authenticated surfaces use the admitted
principal instead. Authorization is evaluated independently for every MCP tool
call; model visibility and an approval decision never grant application access.

The helper is always a managed child process. Its versioned control protocol
(`scenery.assistant.control.*`) and MCP listener are private loopback services;
they require per-process authentication and exact runtime/capability revision
handshakes. A helper outage is reported as typed assistant unavailability while
the Go app remains alive. `scenery up` and `scenery build` use managed Node/npm
and exact assistant package locks without rewriting authored package files.

Use `scenery assistant status <name> -o json` or default
`scenery inspect assistants -o json` for provider-neutral readiness, policy,
restart, failure-code, and expected/actual revision state. Add
`--implementation` only when debugging the developer/operator boundary; that
payload may include adapter name, authored paths, lock digest, private listener
addresses, and child PID. The machine schema is
`scenery.inspect.assistants`.

Mutation-capable assistant adapters receive a server-owned execution context,
not model-selected identity. The context binds the authenticated principal,
app root, and granted capabilities, plus client/protocol and correlation
metadata when the transport exposes it safely. Any request attempting to
override those values fails before plan lookup. The pinned Eve MCP connection
API does not expose per-action `callId` or a configurable `toModelOutput` hook
to connection definitions. Scenery therefore returns a compact plan from the
protocol itself and mints a fresh gateway request ID per invocation; it never
uses a turn- or session-level value as a substitute call ID. A separately
authored Eve tool may use `toModelOutput` while sending richer review data to a
hook or approval UI.

### Databases, snapshots, and storage

```text
scenery db list [--app-root <path>] [-o json]
scenery db shell [--app-root <path>] [service] [psql args...]
scenery db apply [--app-root <path>] [-o json]
scenery db migrate [service] [--status | --adopt-initial] [--app-root <path>] [-o json]
scenery db seed [--app-root <path>] [--env <name>] [--dry-run] [-o json]
scenery db setup [--app-root <path>] [-o json]
scenery db reset [service] [--app-root <path>] [--yes]
scenery db drop [--app-root <path>] [--yes]
scenery db server status|start|stop|logs [--app-root <path>] [-o json]
scenery snapshot save --output <file.zip> [--db] [--storage] [--app-root <path>] [-o human|json]
scenery snapshot verify --input <file.zip> [--expect-sha256 <digest>] [-o human|json]
scenery snapshot load --input <file.zip> [--expect-sha256 <digest>] [--db] [--storage] --mode overwrite|merge [--on-conflict fail|skip|overwrite] [--yes] [--dry-run] [--app-root <path>] [-o human|json]
scenery inspect storage [--stats] [--app-root <path>] [-o json]
scenery storage ls <store> [--tenant <tenant>] [--prefix <prefix>] [--delimiter /] [--cursor <cursor>] [--limit <n>] [--app-root <path>] -o json
scenery storage stat <store> <key> [--tenant <tenant>] [--app-root <path>] -o json
scenery storage put <store> <key> <file|-> [--tenant <tenant>] [--content-type <type>] [--metadata <json-file>] [--if-absent | --if-match <etag>] [--app-root <path>] -o json
scenery storage get <store> <key> [--tenant <tenant>] --output <file> [--app-root <path>] -o json
scenery storage rm <store> <key> [--tenant <tenant>] [--if-match <etag>] [--app-root <path>] -o json
scenery storage rm <store> <prefix> [--tenant <tenant>] --recursive [--dry-run | --yes --expect-revision <digest>] [--app-root <path>] -o json
scenery storage cleanup [--purge] [--dry-run | --yes --expect-revision <digest>] [--app-root <path>] -o json
```

`db seed --dry-run` only reads the existing seed ledger and plans execution.
It never provisions/starts managed PostgreSQL or creates a ledger schema/table.
A managed database must already be running; otherwise the command returns an
explicit-start precondition. An absent ledger means no prior applied seeds.
External URLs retain their declared ownership and are not provisioned by Scenery.

### Tasks and validation

```text
scenery task list [--app-root <path>] [-o json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [-o json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery task graph -o json [--app-root <path>]
scenery validate [<profile>] [--app-root <path>] [-o json] [--write] [--dry-run]
scenery validate list [--app-root <path>] [-o json]
scenery validate inspect <profile> [--app-root <path>] [-o json]
scenery validate graph [<profile>] [--app-root <path>] -o json
scenery validate changed [--base <ref>] [--app-root <path>] [-o json] [--write] [--dry-run]
```

### Harness, inspection, and observability

Repository verification is separate from the application CLI. From the Scenery
repository root, use `go run ./scripts/verify [--repo-root <path>] [--summary]
[-o human|json] [--write] [--quick|--race|--release|--probe <id>...|--benchmark worktree-cost] [--fresh-tests]`.
`scenery harness self` is not a command and fails with `invalid_request`
(`SCN8001`, exit 2); it is not forwarded. App/UI harnesses and bounded report
inspection remain product commands.

```text
scenery harness [--app-root <path>] [-o json] [--write] [--with-validation[=<profile>]]
scenery harness ui -o json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
scenery inspect app|routes|services|endpoints|build|paths|generators|durable|storage|observability|validation|assistants -o json [--app-root <path>]
scenery inspect assistants [--implementation] -o json [--app-root <path>]
scenery inspect ui [--frontend <name>] [--app-root <path>] [-o human|json]
scenery inspect docs -o json [--repo-root <path>] [--for-path <path>|--tag <tag>|--status active|reference|completed|deprecated|--review-due|--all]
scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] -o json [--app-root <path>] [--repo-root <path>]
scenery traces list -o json [--app-root <path>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
scenery metrics list -o json [--app-root <path>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
scenery metrics query -o json [--app-root <path>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]
scenery metrics labels -o json [--app-root <path>] [--match <selector>] [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
scenery metrics series -o json [--app-root <path>] --match <selector> [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
scenery traces clear -o json [--app-root <path>]
scenery telemetry [--app <id-or-name>]... [--command <coarse-command>]... [--measurement completion|startup]... [--since <duration>] [--limit <n>] [-o human|json]
```

Implemented beta/dev helper grammar:

```text
scenery db list [--app-root <path>] [-o json]
scenery db shell [--app-root <path>] [service] [psql args...]
scenery db server status|start|stop|logs [--app-root <path>] [-o json]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [-o json]
scenery worktree list [--app-root <path>] [-o json]
scenery worktree remove <name> [--app-root <path>] [-o json]
scenery worktree upgrade [--app-root <path>] [--yes --expect-revision <digest>] [-o json]
scenery framework use [--source <checkout>] [--app-root <path>] [-o human|json]
scenery framework inspect [--runtime] [--app-root <path>] [-o human|json]
```

`framework use` prepares the `scenery.sh` version selected by the application's
root `go.mod`, including an explicit module download when necessary. It records
the complete relevant Go/native/embed source inputs and builds a matching,
content-stamped CLI inside `.scenery/framework/`. Normal pinned requirements
remain unchanged. A local replacement, or explicit `--source <checkout>`, is
co-development: the command rewrites only the Scenery replacement to its
app-local immutable source snapshot, preserving other module selections and
refusing to overwrite a concurrently changed `go.mod`. No runtime is started or
stopped. Source mode remains an intentional local dependency edit; do not commit
machine-local cache contents or a dependency on an unavailable local snapshot.
The bootstrap only prepares candidate bytes. The selected executable recomputes
and publishes its own final receipt and nested build-input identity, then emits
its own CLI envelope, including across specification changes.

Both commands emit the bounded `scenery.framework` result with source/executable
digests and paths, a preparation receipt at `.scenery/build/framework.json`,
whether the module changed, and the exact next startup command. `inspect` checks
the receipt, current module and actual source/executable bytes without running
them. A partial or changed selection fails closed. Generated workspaces preserve
the authored framework version and replacement; they only make local replacement
paths absolute relative to the authored module. Build input manifests include
`framework/scenery.sh/source` and `producer/scenery-cli/executable`, so the linked
runtime build-input digest also binds the selected source content and CLI bytes.
The supervisor verifies its compiled source stamp and the app's actual framework
before each build; changing another checkout cannot silently select a new runtime.
Repository validation stamps its worktree-local CLI automatically. An unbound
source-built executable must prepare a matching producer before starting an app.

The worktree lifetime owner separately publishes
`.scenery/build/runtime-framework.json`. This is a root-bound producer locator,
not process or resource authority. It survives shutdown to identify the last
producer of retained state; only a new lifetime owner replaces it. App launchers
route lifecycle inspection and shutdown through that executable, whose ordinary
commands still require exact current retained/health identities and verified
process ownership. Desired-source changes do not authorize old-state decoding.
`framework inspect --runtime`, executed by the recorded runtime producer, verifies
the locator and executable without consulting edited module/source inputs. It
emits mode `runtime`, not HTTP health or current-source agreement. Generation,
build and new startup still enforce desired producer/source agreement.

`scenery db list -o json` reports the app Postgres database as `scenery.db.list`; the record includes the database name, redacted URL, source (`managed` or `external`), optional size, and the compiled service bindings. `scenery db shell [service]` opens the matching `psql` inside the identity-verified managed PostgreSQL container (external databases use host `psql`); a service argument pins `search_path` to `<service_schema>,scenery`. Put CLI selectors such as `--app-root` before the service; all following arguments are passed directly to `psql`. `scenery db reset [service]` resets one service schema with `ResetSchema` and clears the current app's discovered seed-ledger identities for that service so the following setup reconstructs its initial data; without a service it resets the managed app database and requires `--yes`. `scenery db drop` drops the managed app database. Destructive reset/drop operations require a stopped worktree, hold its exclusive operation lock, and refuse external DSNs. `scenery db server status|start|stop|logs [--app-root <path>]` selects only that worktree's retained cluster. Status is read-only and reports its scope, retained resource identity, and any incomplete restore; stop retains the container, volume, and credentials. `scenery db apply` applies configured migrations or the mutually exclusive `database.apply.command`; it does not run seeds or SQLC generation. Standalone apply/seed holds worktree ownership through all SQL and child commands; `db setup` holds it continuously across both phases.

`scenery snapshot save` writes one current logical ZIP for the selected database and/or configured stores. Storage capture requires a stopped source and retains live, operation and exclusive maintenance ownership throughout database/files capture. Combined capture rejects external databases and starts only an already-owned managed database when needed. Arbitrary external SQL/filesystem writers must be excluded by the operator. The bounded root manifest references checksummed streamed per-store JSONL records; payloads contain logical identities, sizes, hashes, metadata and modification times, never internal owner/reference/lock paths. Managed Postgres tools run in the existing managed container; SQL-only external saves retain host-tool behavior.

`scenery snapshot verify` checks current schema identity, paths, duplicate entries/tuples, inventories, lengths and checksums without discovering or modifying a target. Its `scenery.snapshot.verify` JSON data always includes `sha256`, the verified archive digest as 64 lowercase hexadecimal characters without a prefix. `verify` and `load` accept `--expect-sha256 <64-lowercase-hex-digest>`. Load retains the same opened input through apply; verify and dry-run allocate no target or materialization cache and never clear recovery. `scripts/snapshot-backup.sh` composes stopped-source save, verify, optional rclone copy and retention; it never stops applications or installs a scheduler. Failed capture, verification or replication skips retention.

`scenery snapshot load` requires a stopped target and verifies app identity, store/tenant policy and every payload before target changes. Storage overwrite stages a complete generation; storage-only merge stages the effective result with `--on-conflict fail|skip|overwrite`. Each imported object receives a fresh ETag; metadata and modification time remain logical source values. A private leased materialization supplies independent native clones or verified streaming copies, and is then evicted. Result counters report actual methods when staging runs. Combined DB/storage merge is rejected. A bounded preparation record binds archive SHA-256, incarnation, classes, mode, conflict policy and staged generation before generation creation. It blocks ordinary access, capture and purge until exact matching resume. SQL-only overwrite and merge also reject a pending storage lifecycle operation under live and operation ownership, before SQL access, even after storage generation publication or removal of storage declarations. Combined overwrite stages files, repeats managed database overwrite if its completion is uncertain, then atomically switches the owner generation and retires the marker. Never retry a combined recovery as SQL-only or storage-only, or delete its marker. A verified overwrite after completed managed purge stages a fresh incarnation while the old owner remains retired. Purge failure progress reports `retired: null` when retirement was published but its durability is unknown, and requires the same approved purge retry; `reclaimed: false` never implies that no reclamation occurred. SQL-only merge retains single-transaction data-only behavior.

`scenery down` stops the selected worktree's verified runtime children and managed PostgreSQL container, retaining database data and credentials. It is idempotent when no live runtime exists and never allocates an absent cluster. `scenery down --db` additionally drops only the retained app database, not the cluster volume, and refuses external DSNs. `scenery down --state` removes only the selected app root's disposable session state. Durable ownership records remain outside the checkout.

`scenery worktree create <name> -o json` runs `git worktree add -b <name>` next to the current app root and emits `scenery.worktree.create`. `scenery worktree list -o json` emits `scenery.worktree.list` from `git worktree list --porcelain`. `scenery worktree remove <name> -o json` resolves the target from Git and removes only the stopped checkout; it has no database-deletion option. Ordinary Git removal also retains the worktree database. `scenery ps -o json` discovers retained stopped/orphaned roots independently of Git. Explicit `scenery prune --older-than <duration> --app-root <absolute-path> --db` removes the selected inactive worktree's entire verified cluster, container, and volume. It refuses live, incompatible, ambiguous, or external targets and retains authority after a failed cleanup so it can be retried.

`scenery worktree upgrade -o json` previews an explicit same-schema upgrade of
the selected stopped root's worktree record, stopped session registry and all
retained managed-storage owner/generation/reference metadata. Preview is
non-allocating. `--yes --expect-revision <digest>` applies only the exact
root/spec/metadata-bound selection; both flags are required together. Identical
artifact kind/schema and valid current payload/ownership are mandatory. Engine
or data-format changes, live recorded processes, incomplete allocations and
pending restore/purge operations are rejected. External storage roots are not
selected. SQL data, credentials, storage incarnations/generations, ETags and
immutable payload files are unchanged. Object payload existence, ownership and
length are checked; preview does not rehash all content.

The `scenery.worktree.upgrade` data contains `app_root`, `worktree_key`,
`target_spec_revision`, `revision`, `metadata_files`, `changed_files`,
`updated_files`, `pending`, `applied`, and an optional applied `backup` path.
It never contains metadata bytes, credentials or object contents. Apply holds
existing live/operation and storage maintenance locks. Exact before/after bytes
are backed up at `<worktree-state>/spec-upgrades/<revision-hex>/metadata.json`
(0600 beneath 0700 directories). A durable `spec-upgrade.json` guard blocks
ordinary worktree and managed-storage reads during partial publication. Retry
the pending preview's original revision with the same target specification;
only matching before/after bytes can resume. Completion retains the backup and
its `completed` marker. A new preview of fully current state reports zero
changes; an obsolete completed approval requires a fresh preview. Records are
bounded to 64 KiB, storage selection to 64 MiB of before/after metadata, and the
encoded transaction backup to 256 MiB. See the
[retained-state upgrade runbook](runbooks/worktree-state-upgrade.md).

`db list` derives service bindings from the current compiled SQL requirements,
then observes resolved supply; it does not allocate a cluster. An app without
SQL requirements returns `database: null` without opening a database connection.

DB lifecycle split:
- `scenery db apply` applies configured migrations, or the mutually exclusive app-owned setup command. It does not run seed files or SQLC generation.
- `scenery db migrate [service]` resolves the same compiled SQL target and stopped-owner lifecycle as apply. All pending migrations of one service execute in one transaction under a schema-scoped PostgreSQL advisory lock. SQL and success rows in `<service_schema>._scenery_migrations` commit together. The ledger records app identity, format revision 1, contiguous revision, source path, SHA-256 and completion time. Files already applied must retain their exact paths and bytes; append a new migration instead of modifying history. Cancellation, SQL failure or process death rolls back the pending chain; an uncertain commit is not reported as successful. Inspect status before retrying.
- `scenery db migrate [service] --status` is read-only: it neither allocates a cluster nor creates a schema or ledger. A stopped database must first be explicitly started with `db server start`. The `scenery.db.migrate` result reports per-service `pending`, `current`, or `blocked` status and each revision's checksum and `pending`/`applied` state. An empty schema initializes atomically. Any populated schema without a ledger, empty ledger, different app owner, unsupported ledger format, checksum mismatch or database history ahead of source is blocked, never silently adopted. Reset is a separate explicitly destructive operation; resetting a service also removes its schema-local migration ledger.
- Explicit `db migrate [service] --adopt-initial` requires the selected migration entry's `initial_verification` SQL file. The app must author a read-only predicate for the complete known initial schema. Scenery requires one `SELECT` or `WITH` query returning exactly one row with one true boolean column; it does not coerce text or numbers to boolean. The query is trusted application SQL, not a sandbox: the app owns the absence of mutating functions or CTEs. Its path and checksum appear in adoption evidence. Scenery verifies it before creating the ledger and records revision 1, then applies the remaining chain in the same transaction; false, malformed, failed or missing verification leaves the schema and ledger unchanged. This is an app-authored baseline decision, not schema inference or permission to reset existing data.
- `scenery db seed` applies service-local SQL, typed fixtures, and file-backed command imports to the declared service database. SQL such as `SERVICE/db/seed.sql` executes with `search_path=<service_schema>,scenery`; command imports declare a matching `database.seed.commands[].service` and receive that service URL as `DATABASE_URL` plus the ordinary per-service URL variables. It runs after schema exists and does not participate in Atlas or SQLC generation. Seed ledgers use `scenery.seed_runs`, keyed by app ID and seed identity. Unchanged seeds are skipped. Changed previously-applied SQL seeds fail closed with status `changed`; changed command inputs rerun and replace their prior hash only after command success. SQL validation fails closed before opening the database when it contains destructive setup patterns such as `DROP`, `TRUNCATE`, `DELETE FROM ...` without `WHERE`, `WHERE true`, or `WHERE 1 = 1`; diagnostics include the seed path, line, message, and statement context. Command input validation rejects empty lists, duplicates, missing files, directories, absolute/traversing paths, and symlinks before database access.
- `scenery db setup` runs `db apply`, then `db seed`. It reports both phases in JSON mode and stops before seed if apply fails.
- `scenery generate sqlc` remains the SQLC generated-source command. It may refresh generated schema SQL from schema definitions and run `sqlc generate`; it must not mutate a database or consume seed files.
- `scenery up` runs the setup lifecycle before starting the app when DB setup inputs exist, and reruns it on rebuild only when the `database.apply` config or a discovered SQL, fixture, command definition, or command input hash changes. Setup failures are reported through the existing compile/setup failure path and dev event stream, and the previous successful fingerprint is not advanced so the next rebuild can retry.
- Migration source checksums also participate in the setup fingerprint. Initial startup may apply pending migrations before any app workers exist. A rebuild with a still-live app performs migration status only; pending schema evolution preserves the old process and asks for `down`, `db migrate`, then `up`. It never changes a schema underneath the generation retained for candidate-start rollback.

Managed Postgres recovery:
- Existing container port bindings are checked before reuse or start, including stopped containers. A conflicting binding is SCN8003 / exit 3 with container name and expected/configured ports. No start, removal, volume deletion, credential rewrite, or state adoption is performed on that path. A matching port is a consistency check, not proof of exclusive ownership.
- Agent home isolates files/control-plane state, not the globally named container/volume in the same Docker daemon. Recovery guidance requires inspecting the Docker context/bindings and using matching agent state/credentials or an externally provisioned `DATABASE_URL`; container deletion or adoption requires explicit ownership verification and operator coordination.
- Missing/invalid required environment values, invalid startup routing configuration and Postgres readiness timeout are SCN8003 / exit 3. Unavailable Docker or a required disabled local agent is SCN8004 / exit 4. URL parser errors, Docker arguments/stderr and raw database connection failures are not copied into public diagnostics because they may contain credentials. Unknown internal failures retain the sanitized SCN9000 path.
- Invalid `.scenery.json` syntax, unknown fields, and configuration validation failures are SCN8003 / exit 3 in machine output, preserving the actionable configuration message. They are not internal failures.
- Pre-cutover ownership evidence that blocks managed PostgreSQL allocation is also SCN8003 / exit 3, preserving its explicit data-migration guidance without replacing or retiring the old claim.

Doctor rules:
- Assistant token-key checks resolve the current session from the canonical worktree's read-only registry and inspect that session's supervisor-owned key. They do not accept an obsolete root-level key or scan other sessions; invalid ownership remains an error. Explicit production secret configuration continues to be supported, and key contents are never emitted.
- `scenery doctor` is a fast, read-only local environment diagnostic. It does not install tools, download managed artifacts, start services, run builds, connect to databases, or mutate `.scenery/`.
- `scenery doctor -o json` emits `scenery.doctor.result`; reported check errors produce exit 3 in both human and JSON modes. `ok` and the summary counts describe the performed preflight checks, not application/runtime readiness. Warnings, skipped checks and unprobed services remain explicit limitations.
- Assistant asset checks validate retained descriptor structure, identity and any self-digest, but only compare archive bytes for descriptors matching the current contract/runtime. Workspace descriptors are addressed by the SHA-256 of their generated descriptor JSON, independently of the capsule archive digest. Older content-addressed sidecars are historical builds, not corrupt current artifacts; if only historical descriptors exist, the current asset check is explicitly skipped. Live assistant revision checks remain separate.
- Local storage needs no managed toolchain artifact, so `scenery doctor -o json` has no storage-specific readiness check; the local filesystem and standard disk/memory checks cover it.
- Check statuses are `ok`, `warn`, `error`, and `skipped`. Check severities are `required`, `optional`, and `informational`.
- Required failures currently cover baseline host readiness such as missing/old Go, very low memory, very low disk space, or an explicitly invalid `--app-root`.
- Doctor reports local state size through informational `storage.scenery_home` checks. `storage.scenery_home` walks the resolved Scenery agent home (`~/.scenery` by default or `SCENERY_AGENT_HOME` when set).
- Optional missing tools such as `bun`, `atlas`, `sqlc`, `git`, and Postgres client tools warn by default. `psql` and `pg_dump` are relevant only when app config declares Postgres services. App configuration can make messages more specific, but the initial doctor contract does not make optional tools fatal. Doctor reports Docker through `docker.context` and `docker.engine` checks instead of a generic host `tool.docker` line. `docker.context` reports the selected Docker context from `docker context show`. `docker.engine` warns when the Docker CLI is missing or the engine is unreachable, and when reachable it probes with `docker info --format '{{json .}}'` and reports engine details such as server version, OS/type, architecture, CPU/memory, root dir, storage driver, cgroup version, kernel version, and engine name when available. Raw failed or malformed Docker output is omitted.
- When Postgres services are configured, `db.postgres_server` checks the managed path's Docker prerequisite only. `observed.runtime_verified` is false and `observed.proof` is `none` on failure or `docker_engine_reachability_only` on success. It does not read dotenv sources, validate external database access, connect to Postgres, or verify container ownership/credentials. Guidance uses the canonical app-level `DATABASE_URL`, never removed per-service env selectors. `scenery up` performs actual managed startup and readiness checks.
- `--app-root` tunes app-sensitive diagnostics from the app config. If omitted, doctor tries current-directory app discovery and silently continues with environment-only checks only when no app marker is found. An unreadable or invalid discovered configuration always produces an `app.root` error and exit 3, including implicit current-directory discovery.
- When the deploy registry exists, `scenery doctor -o json` includes a `deploy` section summarizing `scenery deploy status` diagnostics. Deploy doctor checks may perform explicit reachability/DNS probes only because `doctor` is an operator-invoked diagnostic command.

Deploy rules:
- `envs.<name>.deploy.ssh` is an ordered, duplicate-free allowlist; one target may belong to only one env. `scenery deploy <ssh-target>` reverse-resolves that env, while `scenery deploy --env <name>` requires exactly one target. Remote down/up/publish commands carry the env name. Rsync excludes every `.env*` file and preserves remote dotenv state.
- Top-level `root` names the configured frontend that owns `/` across local path mode, branded dev domains, agent-proxied deploy targets, and published static edges. A single configured frontend is the default root. The root frontend has no `/<name>/` mount; it is the lowest-precedence SPA catch-all behind Scenery runtime/dashboard paths, `/api/`, and non-root frontend prefixes. Apps with zero or multiple frontends and no explicit root retain the services index at local `/`.
- `envs.<name>.domain` is that env's public FQDN. New registry targets, status records, publications, and immutable artifact paths record the environment name.
- `scenery deploy enable|disable -o json` records intent in the machine deploy registry at `<agent home>/agent/deploy.json` and emits the current `scenery.deploy.target` payload with an exact digest revision. Enabling rejects a domain already enabled for another app root.
- `scenery deploy publish --env <name> [-o json]` builds that env's production frontends and publishes under `<agent home>/agent/deploy-artifacts/<app-id>/<env>/<frontend>/<release>/`. Each publication records the exact frontend `base_path`. Candidate Caddy routing is validated, activated, and publicly probed before the new registry is committed; rollback restores artifact symlinks, routing, and registry as one environment-scoped publication.
- Published production frontends make the managed edge serve those routes directly from disk: Caddy rejects `/runtime`, `/dashboard`, `/console`, and `/__scenery`, proxies `/api/*` to the agent with the trusted public-edge headers, serves each non-root published frontend under `/<name>/`, and serves a current base-`/` root frontend only at `/` (GET/HEAD only, concrete files first, SPA `index.html` fallback only for extensionless paths, gzip/zstd, immutable caching for `/assets/*`, revalidation elsewhere, ETag/range semantics from Caddy's file server). A migrated root artifact built with base `/<name>` temporarily retains both `/` and `/<name>/` so its absolute asset references remain valid until republished. Root frontends also contain their former named control paths, such as `/<name>/api` and `/<name>/__scenery`, and agent-proxied and static routes reject the same protected top-level paths. Without a root frontend the agent proxy remains the catch-all. A registry target without publication metadata, or one whose `current` artifact does not resolve to a complete release, keeps the full agent-proxy behavior for those routes.
- The deploy registry and agent/session/edge ownership files use exact current artifact identities. First access migrates their former identity-only shapes in place after writing an exact owner-only `.legacy.bak`; the fsynced replacement is followed by `.legacy.migrated`. When the JSON schema is unchanged across a later Scenery specification revision, loading rebinds only the artifact identity and preserves the payload; schema changes still fail closed. Targets, app roots, process ownership, resolver state, and managed Postgres credentials are preserved.
- The privileged helper reads its target metadata through a frozen tolerant handoff contract: it decodes only the stable payload fields (`edge_kind`, `target_addr`, `http_target_addr`, `pid`, `owner_uid`, `owner_gid`, `process_start`, `executable`), ignores artifact identity revisions and unknown fields, and never rewrites the file, so an installed helper keeps forwarding after scenery upgrades rewrite target metadata. Helper installs stamp `--helper-contract` (the handoff contract revision) into the LaunchDaemon; drift detection compares that stamp, not the metadata file, against the current binary. When the helper cannot validate its target it drops connections fail-closed and appends rate-limited explanations to `/Library/Application Support/Scenery/edge-helper/edge-helper.log`.
- On Linux hosts running systemd, `scenery deploy setup` must run as root and converges the single-user public edge: it renders and Caddy-validates the registry Caddyfile, installs and starts the `scenery-agent.service` supervisor (stopping any unsupervised agent first), the `scenery-edge.service` unit running the managed Caddy binary against the Scenery-rendered Caddyfile bound directly to public 80/443, and the `scenery-deploy-resume.service` boot oneshot. Re-running setup converges; `scenery deploy teardown` (root) stops and removes only the Scenery-owned units, never published artifacts or ACME state. While the edge unit is installed, every Scenery edge restart/reload path converges through systemd instead of spawning an unsupervised Caddy. `service_manager` in setup/teardown/status payloads reports `launchd` or `systemd`.
- `scenery deploy setup` on macOS must run as the normal user, and asks sudo only when the privileged helper actually needs (re)installing: a running, publicly bound helper stamped with the current handoff contract is kept, so setup re-runs repair launchd supervision unattended (`helper_reinstalled` reports which path ran). Setup replaces a drifted helper stamped with the current handoff contract, configures it for wildcard TCP 80/443, records ACME email/CA, installs and bootstraps the `dev.scenery.agent` supervisor LaunchAgent (KeepAlive; launchd continuously owns the agent, taking over from any unsupervised agent), restarts the user-owned edge, then installs and bootstraps the login resume LaunchAgent. Installing a LaunchAgent means the job is loaded into the `gui/<uid>` launchd domain, not merely that a plist file exists; bootstrapping the resume job fires one idempotent `scenery deploy resume`. Re-running setup never deletes the deploy registry, targets, or issued Caddy certificates. `scenery deploy teardown` reinstalls the helper in loopback-only mode, boots out and removes both LaunchAgents (`agent_supervisor_removed`, `launch_agent_removed`), restarts the edge (now unsupervised), and keeps the registry plus Caddy certificates.
- `scenery deploy resume` ensures the agent and public edge, then starts missing enabled app roots with `scenery up --detach --app-root <root>` while leaving already-running roots alone. A bounded health check reacquires a healthy fingerprinted Caddy/helper/agent chain without replacing it (`edge_restarted: false`); only an unavailable chain is restarted (`edge_restarted: true`). Public recovery does not depend on the optional `local.dev` wildcard resolver; ordinary local edge readiness still requires that resolver. Resume appends JSON lines to `<agent home>/deploy-resume.log`. When the installed helper's handoff contract drifts from the current binary, resume reports `helper_drift` with the `scenery deploy setup` repair action because resume itself never escalates to sudo.
- `scenery deploy status -o json` emits `scenery.deploy.status`. It reports helper state/version/contract, wildcard listener truth for 80/443, edge/agent state, launchd supervision truth (`agent_supervisor` with installed/loaded/running/pid, and the resume `launch_agent` with installed/loaded/state/last_exit_code — plist presence alone is never "installed enough" and a completed nonzero run forces `ready: false`), ACME settings, target live-session/cert state, per-target published-frontend truth (`frontends` with `name`, `route`, `base_path`, serving `mode` of `caddy_static` or `agent_proxy`, `artifact_path`, `release_id`, `entry_document`), and structured diagnostics for LAN/public reachability, DNS A/AAAA mismatch, Cloudflare-proxied DNS, power sleep, macOS firewall, and helper contract drift. Public domains try ACME issuance first and fall back to Caddy's internal CA for an origin certificate when a TLS-terminating proxy such as Cloudflare prevents public challenge completion; direct domains still prefer publicly trusted ACME certificates. PID or listener presence is never readiness by itself: for each enabled target, `deploy.tls_handshake.<domain>` requires a completed TLS handshake with that domain's SNI through public port 443; when port 443 accepts TCP but drops TLS while Caddy answers TLS directly, status reports `ready: false` with the `scenery deploy setup` repair action. Public IP discovery and DNS lookups happen only inside `scenery deploy status` or deploy-aware `scenery doctor`.
- Public deploy routing is strict: public edge requests require the trusted edge token plus `X-Scenery-Public-Edge: 1`, exact host match against an enabled registry target, and a live session for that target app root. The agent validates candidate public-route owner fingerprints on session restoration/registration, publishes an immutable in-memory route snapshot on deploy or session changes, and monitors owners for exit; the HTTP request path performs only a snapshot lookup. Public dispatch recognizes only a frontend as the root service, serves it at `/` and unmatched SPA paths, serves `/api/` and configured non-root frontend prefixes, returns 503 for enabled-but-down apps or unavailable backends, and does not expose Scenery runtime/dashboard/control paths. Removed `envs.<name>.deploy.root: "api"` configuration must be deleted; `/api/` remains automatic while `/` uses the configured root frontend or the default agent page.

Inspect rules:
- `scenery inspect` requires a subject.
- Inspect subjects require `-o json` except `ui` and `storage`, which also
  provide a compact human report by default.
- `--app-root` is optional. When omitted, scenery walks upward from the current working directory to find the app config.
- Stable inspect subjects are `app`, `routes`, `services`, `endpoints`, `build`, `paths`, `ui`, and `docs`.
- `generators`, `durable`, `storage`, `traces`, `metrics`, and `observability` are beta diagnostic subjects. `generators` reports configured graph inputs/outputs; `durable` reports task declarations, schemas and redacted database metadata. `inspect storage` reports declared stores, retained canonical scope, readiness, optional running-console URL and public recovery instructions. Incarnation/generation are null for a never-initialized namespace. Counts/totals are absent (unknown) unless `--stats` explicitly runs a full metadata scan. Discovery never allocates; retained roots remain inspectable after source removal. `traces`, `metrics`, and `observability` read Scenery-managed data; Victoria is substrate, not the integration API.
- `scenery storage ls|stat|put|get|rm|cleanup -o json` is the worktree storage CLI; discovery is `inspect storage`. Every response echoes canonical scope. `ls` scans reference metadata with bounded keyset pages (default 100, maximum 1000; serialized page at most 64 KiB, including reserved cursor space for each committed object descriptor), counts objects and common prefixes together, and does not hash payloads. Cursors bind root/incarnation/generation, store/tenant, prefix and delimiter; they are not multi-request snapshots. `get` requires a named `--output`; payload never enters JSON stdout. `put` accepts `<file|->`, `--content-type`, case-sensitive `--metadata <json-file>` and mutually exclusive `--if-absent`/`--if-match`. Recursive `rm` previews by default and requires `--yes --expect-revision` for the unchanged selector; stale previews do not mutate. It is not bulk atomic: partial/uncertain progress requires a fresh preview. `cleanup` similarly previews and reclaims only proven unreferenced material; `--purge` additionally requires a stopped verified managed owner and durably retires its incarnation before reclamation. Stable locks/retired identity survive. The runtime's authenticated `/__scenery/storage/<store>/...` routes use the same SDK semantics; private stores are absent from public storage HTTP routes. This revision emits no generated TypeScript storage helper.
- `scenery inspect observability -o json` emits `scenery.inspect.observability` with backend readiness for logs, metrics, and traces; native dialect names; examples; and the exact enforced query scope for the selected app/session.
- The `scenery.inspect.traces`, `scenery.inspect.metrics`, `scenery.inspect.observability`, `scenery.logs.query`, `scenery.logs.tail.entry`, `scenery.metrics.query`, `scenery.metrics.labels`, and `scenery.metrics.series` schemas are useful for agents, but their source-selection, retention, rollup, percentile, and clear/delete semantics are not stable API yet.
- `--since` accepts Go duration strings such as `15m`, `1h`, or `24h`.
- `--min-duration-ms` filters root traces by duration in milliseconds.
- `--status` accepts `ok` or `error`.
- `metrics` defaults to `--since 24h` and `--limit 10000` so agents get useful local summaries without scanning unbounded history.
- User-facing dev lifecycle and observability commands scope to the app root. Internal session IDs remain in JSON records, manifests, routes, and state paths for compatibility, but users should not select or create runtime sessions directly.
- `logs query` defaults to the app root's live runtime, `--since 15m`, `--limit 200`, `--timeout 3s`, and JSON envelope output. `--limit` is capped at 2000 and reports a JSON warning when clamped. It accepts native VictoriaLogs LogsQL through `--query`; `--logql` is rejected rather than silently treating Loki LogQL as LogsQL. Finite queries use an HTTP context deadline derived from `--timeout`.
- `logs tail` streams scoped `scenery.logs.tail.entry` JSONL log entries from the VictoriaLogs live-tail endpoint, maps `--since` to VictoriaLogs `start_offset`, rejects `--start` and `--end`, and exits through normal context cancellation or interrupt handling.
- `metrics query` defaults to range mode for the app root's live runtime with `--since 15m`, `--step 5s`, `--timeout 3s`, `--limit 100`, and JSON output. `--limit` is capped at 10000 and reports a JSON warning when clamped. `--instant` switches to the instant Prometheus API endpoint. Finite queries use an HTTP context deadline derived from `--timeout`.
- `metrics labels` and `metrics series` default to the app root's live runtime with `--since 1h`, `--timeout 3s`, and `--limit 1000`; catalog limits are capped at 10000 and report a JSON warning when clamped. `metrics labels` accepts optional `--match`, and `metrics series` requires `--match`.
- Query commands are scoped by default. Scenery applies LogsQL scope through VictoriaLogs `extra_filters` and metrics scope through repeated VictoriaMetrics `extra_label` query parameters, and every JSON envelope echoes `scope.enforced=true`.
- `docs` inspects the scenery repo knowledge base, not a target scenery app. It accepts `--repo-root` and otherwise walks upward to the `module scenery.sh` repo root.

Toolchain rules:
- `scenery.toolchain.json` is the root checked-in manifest for Scenery-owned development executables, Docker images, plugins, and source lock references.
- The manifest uses kind `scenery.toolchain` with an exact digest `schema_revision`; `scenery system toolchain ... -o json` emits kind `scenery.toolchain.status` with its own digest schema revision inside the current CLI envelope.
- Binary artifacts may use `platforms` for downloaded archives or `source_build: {kind: "go", package: "./cmd/..."}` for source-built Scenery binaries. Source-built artifacts are compiled with `go build` into the managed toolchain store and report `source: "source-build"` in toolchain status.
- `--tool <name>` selectors must match a manifest artifact exactly. Unknown selectors fail closed with `unknown toolchain artifact "<name>"` instead of returning an empty successful status.
- `scenery version -o json` includes `toolchain_manifest.kind`, `schema_revision`, `sha256`, `artifact_count`, and `source_lock_count` for the bundled manifest.
- CLI installation and updates use a selected source checkout: build the dashboard with `./scripts/build-dashboard-ui-embed.sh`, then install from source. There is no release-download updater. Keep separate absolute binary paths for different checkouts and verify their producer commits with `scenery version -o json`. Installing the executable does not update app module dependencies, regenerate clients, migrate durable state, or sync managed tools.
- Managed tool updates are explicit through `scenery system toolchain sync`. After changing the CLI used for deployment, inspect `scenery system edge status -o json`; when the installed privileged helper's handoff contract differs, run `scenery deploy setup` explicitly. Version drift alone does not require reinstalling the helper.
- The default local store is `.scenery/toolchain/` under the app/repo root. Machine-level edge tools use `~/.scenery/toolchain/` under the local agent home. `SCENERY_TOOLCHAIN_DIR` overrides both store roots.
- `SCENERY_TOOLCHAIN_DOWNLOAD=0` disables automatic managed binary downloads. Per-tool download disable variables such as `SCENERY_DEV_VICTORIA_DOWNLOAD=0` still apply to their startup paths.
- Managed Caddy resolves from the managed store or manifest-driven download. Managed Victoria binaries resolve from explicit env overrides, the managed store, or manifest-driven download. They do not use implicit system `PATH` binaries.
- `scenery system toolchain verify --strict --images` fails for tag-only image refs. Tag-only image refs marked `stability: "unstable"` are accepted only outside strict verification during the migration to digest-pinned images.
- Go modules and UI package-manager files are source locks. Commands such as `go`, `bun`, `npm`, `node`, and `tsx` used to run source/package-manager workflows are not hidden Scenery-managed toolchain downloads.

Command split:

- Detached ready requires published supervisor/API process records with PID and
  fingerprints that pass live ownership verification. Missing API publication
  remains pending. Contradictory ownership is SCN8003 / exit 3; permanent
  control/specification failures return immediately instead of consuming the
  readiness deadline. `--wait registered` does not promise process publication.
- `scenery up` starts the app root's one live dev runtime: app process, file watching, and rebuild/restart supervision. The file watcher treats `.gitignore`-ignored paths and app config `watch.ignore` paths as outside the watch surface and does not descend into ignored directories. `watch.ignore` also excludes those paths from the rebuild/change fingerprint used by the dev loop, but it does not affect Git tracking. A second live code copy requires a separate Git worktree. Re-running `scenery up` while a verified live owner already runs the same app root is an idempotent success, not an error, and never starts a second supervisor: the human foreground form reports the existing runtime's owner PID, routed URLs, and the log/stop commands, then attaches to the running runtime's structured logs. The attached follower never takes ownership: Ctrl+C detaches with exit code `0` and leaves the runtime running (stopping stays explicit through `scenery down`), and the follower exits on its own once the app root no longer has a live verified owner. `-o jsonl` does not attach; it emits a `run.already_running` event and returns `0`.
- After a failed build, changes to declared generated artifacts wake the watcher so regenerating stale clients retries the current contract automatically. Successful builds ignore generated content writes to prevent self-triggered rebuild loops; authored changes made during a build remain pending.
- `scenery up --detach` starts the same worktree-owning supervisor and embedded private control plane in a background child process. By default (`--wait ready`) it waits up to two minutes until the child session is registered, its status is `running`, the API and configured frontend backends accept connections, every advertised route completes without an infrastructure 5xx response, and one script or stylesheet asset discovered in each frontend HTML shell loads successfully, then prints the app action summary, status/log/stop commands, and registered routes. Application-level 401 or 404 responses prove routing; discovered frontend assets must return below 400. `--wait registered` has a 30-second budget and returns when the child registers as the root's runtime owner, without promising serving readiness. Timeout errors report the actual child PID and last route/asset failure. Supervisor stdout/stderr is retained beneath the worktree's private control directory. A compatible live owner with the same selected environment is reused: the requested readiness check still applies, `scenery.dev.detach.already_running` is true, and `log_path` is omitted because no child was started. A different environment or incompatible identity fails without replacing the owner.
- Detached startup observes supervisor exit independently of session polling. A private inherited pipe carries one terminal `scenery.cli.event` summary with the current schema/spec/producer identity and a structured diagnostic; stdout/stderr is not parsed as startup authority. Terminal startup failure preserves the original diagnostic fields, exit classification, and internal report token after session cleanup. The public diagnostic adds `details.detached_startup` with `reason`, `owner_pid`, `wait`, and `log_path`, plus a log suggestion when a new child was started. Reasons distinguish `child_failure`, `child_exit` (no result), `protocol_error`, `timeout`, and other `wait_failure`. Exit without a result, malformed protocol, and actual deadline expiration are SCN8003 / exit 3; unknown internal failures remain sanitized SCN9000 / exit 10. The launcher reaps failed children. `--wait registered` intentionally does not promise later readiness or observe failures after it returns. Structured `build.error`, `run.failed`, and failed run summaries include `data.diagnostic` and `data.exit_code`; log text remains non-authoritative context.
- `scenery logs --follow` follows the app root's live runtime logs by default with the same app-root, limit, stream, source, kind, level, grep, since, and JSONL options, and it does not mutate runtime state.
- `scenery logs`, plain `scenery logs --follow`, and `scenery console` read structured dev events from the Victoria-backed substrate for the selected app root's live runtime.
- If the backing dev-event substrate is unavailable, structured dev-event read commands fail loudly instead of falling back to the deprecated local process-output cache.
- `scenery console` opens the source-aware terminal console when stdin/stdout are real TTYs. In CI, dumb terminals, or redirected output it falls back to normal log following with the same filters.
- Structured dev logs carry source identity. Current source ids include `api`, `worker`, `build`, `supervisor`, `victoria`, and `frontend:<name>`.
- `scenery system agent restart` affects only the explicitly managed machine control plane/router used by edge and deploy. When `dev.scenery.agent` supervises its socket, restart uses `launchctl kickstart -k` for a loaded job or bootstraps an unloaded plist; JSON reports `supervised`. `localagent.StartProcess` follows that supervisor rather than spawning a competing owner. The same explicit socket/router/trust options apply; supervised restarts preserve the plist invocation. Registered resources are not signaled merely because this control plane restarts. Established edge WebSockets must reconnect and Caddy provides bounded upstream dial retry. Ordinary worktree supervisors neither depend on this process nor run a machine-agent recovery watchdog; their localhost serving and private capabilities remain independent.
- `scenery system agent cleanup` is the explicit pre-rebrand cleanup path. It detects same-user Caddy and agent processes only when their command names the exact `~/.onlava` managed config or control socket, captures and verifies each live process fingerprint before signaling it, and leaves unverifiable processes untouched. It reports whether `~/.onlava` state remains and removes that directory only with `--remove-state`; state removal is refused while a matching process could not be verified. `-o json` reports `scenery.agent.cleanup`.
- The local agent holds `<agent-home>/run/agent.lock` for its lifetime and never substitutes a random router port when its configured address is occupied. Agent startup may stop only same-user stale agent processes whose recorded process fingerprints still verify and that do not answer health on their own `--socket`: a live agent that shares the router address but serves another agent home's control socket is never reaped (test, harness, and worktree agent starts must not take down the machine's real agent); the starting agent falls back to another router port instead. On Unix the managed Caddy child inherits `<agent-home>/run/edge.lock` for its lifetime; edge operations are serialized and may reap only verified same-user Caddy processes using Scenery's current or pre-rebrand edge configuration. `scenery doctor` warns about duplicate owners and foreign listeners on the configured router and edge TCP/UDP ports.
- Commands that ensure the local agent require its current health kind, schema revision, spec revision, and producer identity. Different CLI builds may share an agent with that exact contract; build age never triggers replacement. Incompatible or unreadable health fails without restarting the agent or changing sessions. Use a matching CLI, or a separate `SCENERY_AGENT_HOME` and explicitly distinct router address; machine-global DNS/privileged edge setup is not isolated by changing agent home. Explicit `scenery system agent restart` remains operator-controlled.
- `scenery system edge dns install` resolves the managed `dnsmasq` toolchain artifact, syncing/building it automatically unless managed downloads are disabled, starts user-owned dnsmasq for the configured wildcard dev domain plus other Scenery-managed resolver domains already present on the machine, and on macOS invokes a privileged helper only when `/etc/resolver/<domain>` is missing or mismatched. `scenery system edge privileged install` installs the macOS root-owned loopback helper that listens on `127.0.0.1:443` and `[::1]:443` and forwards raw TCP only to a validated user-owned Caddy target recorded under the helper's configured agent run directory. Run it as the normal user; it invokes `sudo` only for the minimal helper install. `scenery system edge privileged uninstall` removes that helper. `scenery system edge install` and `scenery system edge restart` refuse root, start user-owned Caddy on an unprivileged high loopback port, ensure the local agent router is running as an unprivileged HTTP upstream on its internal loopback address, disable Caddy response buffering for streaming routes while preserving upstream cache headers, and write both edge state and helper target metadata under the current agent run directory; when a previously installed macOS privileged helper is bound to another agent home, Scenery also publishes the active Caddy target to that helper's configured metadata path because port `443` is machine-global. If wildcard DNS or the privileged helper is missing or unhealthy, install prepares Caddy but fails with the actionable setup command because browser-ready default-port HTTPS requires both. They resolve Caddy from the managed `caddy` toolchain artifact, syncing it automatically unless managed downloads are disabled. `scenery system edge trust` resolves the same managed Caddy artifact, starts a temporary admin-only Caddy process with `local_certs`, runs Caddy's trust flow against that temporary admin endpoint, and does not require the port-443 edge to be running. `scenery system edge status -o json` reports `scenery.edge.status`, including the privileged helper target metadata path, PID, stamped handoff `contract_revision`, and a loopback TLS probe through port 443: a launchd-running helper that accepts TCP but drops connections before TLS reaches Caddy — while Caddy answers TLS directly — is reported `unhealthy` with the reinstall command instead of `running`. `scenery system edge uninstall` stops user-owned Caddy, removes helper target metadata only when it still points at that Caddy, leaves DNS and the privileged helper alone, and reports `scenery system edge privileged uninstall` as the helper removal command.
- `scenery down` stops only the selected worktree's verified runtime children and managed PostgreSQL container, preserving retained SQL and worktree storage data. `--db` also drops its existing managed app database without deleting the cluster volume or allocating an absent database; external DSNs are refused. `--state` removes the selected disposable `.scenery/sessions/<id>` state, and `--all` enables both. JSON reports `scenery.down`. Neither flag deletes worktree storage data; namespace retirement requires `scenery storage cleanup --purge --yes --expect-revision <digest>` after a fresh preview.
- `scenery prune --older-than <duration>` prunes eligible inactive worktree session records. Durations accept Go notation such as `336h` and day shorthand such as `14d`. Default prune deletes neither session files nor databases; `--state` adds disposable session files. `--db` requires one exact absolute `--app-root` and removes its entire verified retained cluster, container, and volume, not just the app database; `--all` enables both scopes. A live owner or active restore excludes cleanup. Explicit whole-cluster deletion may abandon an interrupted restore only after acquiring both ownership and operation locks; failed deletion retains the recovery authority. Incompatible, ambiguous, and external targets fail closed. Victoria history and retained storage namespaces remain outside prune. `scenery.prune.resources` identifies each removed root/resource/container/volume and the `worktree-cluster` scope.
- Starting `scenery up` for an app root requires exclusive ownership of that app root's live dev runtime. If another verified live owner already controls the same app root, the new invocation does not start a second supervisor and does not steal ownership: it reports the existing runtime and exits `0` (see the `scenery up` and `--detach` entries above). Ownership races that reach session registration still fail closed with an "already running" error. If the recorded owner is dead or its fingerprint no longer matches, the new owner may claim the runtime and clean recorded app, worker, and managed frontend child processes from the stale owner, plus Scenery-owned runtime processes whose injected app root/internal session environment matches. It must not clean other app roots, other worktrees, or unrelated user processes.
- Session owner checks treat `owner_pid` as the effective owner. `owner.pid` is the fingerprint for that same PID, not an independent owner field. If the stored owner fingerprint object points at a different stale PID, Scenery refreshes it on the next registration and must not delete or prune the session while the effective `owner_pid` is still live. Dev supervisors unregister sessions with an owner-conditional delete that includes the recorded owner fingerprint; if an older owner exits after ownership moved, or if the same PID now has a different recorded fingerprint, the delete is ignored and the newer session record remains registered.
- `scenery help -o json` returns `scenery.help`, the complete machine-readable command manifest for agents and contract checks. `scenery help <command> -o json` returns the same payload identity with exactly one command descriptor; unknown topics fail instead of falling back to unrelated human help. The `build` descriptor records exact usage variants, flags and required combinations, side-effect class, app-root requirement, mode-specific output schema identities, stability, common exit categories, and related inspection or validation commands. Human root help remains intentionally orienting; use `scenery help all` for the grouped reference and `scenery help <command>` for human-readable flags and subcommands.
- `scenery ps` discovers retained worktree roots without ensuring any agent or database. Its `scenery.agent.status` JSON contains `worktrees`, each with its exact root/key, ownership status, and sessions. It distinguishes live, stopped, orphaned, absent, unavailable, and incompatible-or-invalid state; a corrupt retained record is reported without repair. Live sessions use their private current control endpoint and expose effective process health. Duplicate startup prevention uses the lifetime lock and verified owner identity, never shell text. Root-scoped database status separately reports retained SQL identity and incomplete restore state without allocating resources.
- Each worktree owner starts its own dashboard backend and advertises `/console/` on its localhost base URL. Release binaries serve the embedded UI produced from `apps/console/` before Go compilation; startup does not build UI assets, though `SCENERY_DEV_DASHBOARD_UI_DIR` may select an explicit existing build. The private Unix control API remains protected by filesystem permissions. Explicit machine edge/deploy dashboards do not become owners of worktree runtime state.
- Dashboard HTTP responses carry `X-Scenery-Dashboard-Bundle-Hash`, plus `X-Scenery-Dashboard-Bundle-Stale: true` and `X-Scenery-Dashboard-Bundle-Warning` when the running binary's embedded bundle differs from `apps/console/dist` in a scenery repo checkout; dashboard HTML includes matching meta tags, and `devdash.AppStatus` exposes the same object as optional `dashboardBundle`. Staleness detection is a no-op outside a scenery repo checkout. The self-harness `dashboard ui fresh` step uses the same hash comparison.
- Console is the only runnable dashboard source. Its metadata transport is the same-origin `/__scenery` WebSocket RPC. Storage bytes stream through the same listener's `/__storage` endpoint (both paths are beneath `/console` when routed). GraphQL and the former compatibility RPCs for trace events, transaction wrappers, editors, onboarding, and telemetry are not supported dashboard surfaces; use the current typed RPCs such as `traces/list`, `db/query`, `stored-requests/*`, and `storage/*`.
- Storage RPC selects a registered `app_id`, never a caller-supplied filesystem root. `storage/inspect` is non-allocating; optional `stats` requests a complete metadata scan. `storage/list`, `storage/stat`, `storage/delete`, `storage/delete-preview`, and `storage/delete-selection` require exact `worktree_key`, `incarnation`, `generation`, `store`, and explicit tenant where scoped. These local operator capabilities retain the CLI scope and failure contracts. Mutations do not retry automatically; single deletion requires `if_match`, bulk deletion requires a fresh `selection_revision`, and partial/uncertain failures include structured details in RPC error data.
- Dashboard file GET/HEAD/PUT require `X-Scenery-Storage-Request: 1` and reject a foreign browser Origin. Query selectors use the same pinned scope plus `key`. PUT is create-only (`If-None-Match: *`) or conditional (`If-Match`); GET/HEAD honor the displayed `If-Match`. Transfers hold the namespace maintenance lease through the stream. This is a local operator endpoint, not a public application storage route or a tenant authentication mechanism.
- The Storage page clears listing, cursor, selected object, preview, and pending transfers on app/worktree/store/tenant/prefix changes. Scope refresh discards old generation state. Totals are explicitly calculated snapshots, not live counters. `inspect storage` includes a selected Storage-page URL only for a verified live runtime.
- Browser dashboard WebSocket upgrades must be same-origin.
- The direct agent router serves HTTP by default. Path-mode local dev uses the per-runtime localhost listener; only the selected env's successfully validated `domain` causes public app paths to redirect. Edge unreadiness leaves localhost content in place. Host mode (`envs.<name>.mode = "host"`) uses the managed `local.dev` edge/DNS path. Route and alias ownership, trusted edge headers, TLS issuance, and duplicate-session behavior remain exact-registry and fingerprint verified.
- Reverse proxies rebuild forwarding headers after removing hop-by-hop headers. The agent preserves an incoming `X-Forwarded-For` chain only from an authenticated loopback edge request; other requests derive it from the connecting peer. Local backend proxies discard incoming forwarding chains. Backend URL, original request host, and route-prefix behavior are preserved.
- Dev-runtime manifests include the owning worktree's dashboard route; there is no agent-disabled fallback runtime or global dashboard owner for ordinary `up`.
- `scenery up` exposes worktree-local observability through optional VictoriaMetrics, VictoriaLogs, and VictoriaTraces. Managed lifecycle checks verified owners, component reachability and exits, and serializes bounded recovery. The embedded private dashboard owns compact JSON runtime metadata and content-addressed app-model blobs; its runtime components submit mutations through the internal control endpoint. Read-only inspection does not create, migrate, or flush dashboard state. App summaries expose `sessionStatus` and `sessionStatusReason` instead of treating stale/degraded ownership as running. Trace/report histories are exported to Victoria rather than duplicated in `devdash.json`. Optional observability failure is visible but does not gate required serving readiness.
- The state home defaults to `~/.scenery` unless `SCENERY_AGENT_HOME` is set. `SCENERY_DEV_CACHE_DIR` does not relocate durable worktree ownership, credentials, or its private dashboard.
- Managed frontend services start on runtime-private hidden loopback ports and are restarted by the dev supervisor if their process exits unexpectedly. A manual `SCENERY_FRONTEND_<NAME>_ADDR` override is accepted, but configured frontend upstreams are ignored unless that frontend sets `"allow_shared_upstream": true`.
- `scenery up --desktop` waits for configured frontend readiness and launches
  each `frontends.<name>.tauri` shell with a `devUrl` overlay targeting that
  hidden backend. The desktop PID is registered as `desktop-<name>`, its output
  is captured with the session logs, and session shutdown stops it. Closing a
  desktop window is a normal exit: the app and frontend stay running and the
  desktop process is not restarted.
- Dev app children are launched through an internal runtime executable path under `.scenery/sessions/<session_id>/run/app/` so stale same-runtime app processes can be identified without broad process-name matching.
- Use default agent-routed app URLs, and run `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when trusted local HTTPS on the default port is needed.
- `scenery up --port <n>` and `scenery up --listen <addr>` force a manual TCP app backend. The default agent path uses a runtime-private Unix socket and should be preferred for worktree-safe development.
- `scenery worker` never starts the managed shared Postgres server. If an app declares a Postgres service, each service's configured database URL env must be set to a valid Postgres URL before startup; otherwise startup fails closed and points back to `scenery up` as the dev-substrate path.
- `scenery task list|inspect|run|graph` is the canonical code-task surface. Targets must use `<domain>:<name>` and resolve under `<app-root>/<domain>/tasks/...`; both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`.
- Scenery task flags must appear before the target. Code task arguments must appear after `--`, for example `scenery task run --env production billing:reconcile -- --dry-run`. Configured tasks do not accept `--env`, `--lang`, or extra runtime arguments.
- Supported code task layouts are `<domain>/tasks/<name>.task.go`, `<domain>/tasks/<name>.task.ts`, `<domain>/tasks/<name>/main.go`, and `<domain>/tasks/<name>/index.ts`. Single-file Go tasks must start with `//go:build ignore` so normal app package loading cannot accidentally include them. If multiple candidates match a target, scenery fails unless `--lang go|typescript` selects a single language.
- Code tasks execute with cwd set to the app root. The selected/default env is always injected as `SCENERY_ENV` and `SCENERY_RUNTIME_ENV`.
- `scenery inspect validation -o json` is read-only and returns the current `scenery.inspect.validation` payload with app metadata, default profile, profile records, advisory artifacts, and diagnostics.
- `scenery validate list|inspect|graph -o json` returns `scenery.validation.list`, `scenery.validation.inspect`, and `scenery.validation.graph`. `scenery validate <profile> --dry-run -o json` returns `scenery.validation.plan` and must not execute shell, task, code-task, harness, database, or generation steps.
- `scenery validate [<profile>] -o json --write` runs the resolved profile sequentially, fails fast, keeps stdout as one JSON document, captures child output as bounded evidence tails and artifacts, returns `scenery.validation.result`, and writes `.scenery/harness/validation/latest.json` plus `.scenery/harness/validation/<profile>-latest.json`.
- `scenery validate changed --base <ref>` unions branch changes from `<base>...HEAD`, tracked changes against `HEAD`, and non-ignored untracked files. NUL-delimited Git output preserves literal names; renames include both paths. Paths outside the selected app root are excluded, and the app-relative result is sorted and deduplicated. Selection includes the default profile, adds profiles whose `paths` globs match, resolves nested `profile:` steps, deduplicates profiles, and reports its reasoning in JSON.
- Changed selection reports `selection.coverage` for every path and `coverage_complete`. Dry-run checks are `planned`, never `checked`; executed required steps must all pass before a path is `checked`. A default profile counts only for its explicitly matched paths. Unmatched paths remain `unverified`, and execution returns `ok: false` even if every selected step passed. `validation.exemptions` contains `paths` and a nonblank `reason`; it marks only otherwise unmatched paths `exempt` and never masks a matching owner lane. Prefix a glob with `./` to anchor it to the app root; unprefixed basename globs retain basename matching.
- Profiles with `manual: true` require a description and run only by explicit profile selection. Changed matching records them in each path's `manual_profiles` and leaves the obligation `unverified` without running the lane. Defaults and automatic profiles cannot reference manual profiles. Other selected checks still run, and unresolved paths/lanes remain in result `next_actions`. Separate manual runs are distinct evidence, not implicitly imported into a changed result.
- Native schedule declarations run through the in-process scheduler. The API role reconciles schedules, while `scenery worker` executes scheduled operations without starting the public HTTP server.
- `scenery worker` builds once and starts the app runtime in worker-only mode with no public HTTP server. It runs scheduled operations and local durable workers; generated binaries use `SCENERY_ROLE=worker`.
- `scenery worker durable --endpoint <url> --token <token>` builds once and starts the app runtime as a remote durable worker. The generated binary receives `SCENERY_ROLE=worker`, `SCENERY_DURABLE_ENDPOINT`, `SCENERY_DURABLE_TOKEN`, and optional `SCENERY_DURABLE_SERVICES`, then polls remote durable lease endpoints and executes registered Go handlers.
- `scenery worker durable jobs list|inspect|cancel|retry ... -o json` reads or mutates jobs for one service in the app database's shared durable store and emits `scenery.durable.jobs`; `inspect` includes job events.
- `scenery worker durable token create --service <name> -o json` creates or rotates a remote durable worker bearer token for one service in the app database's shared durable store, stores only the token hash, and prints the raw secret once in `scenery.durable.worker_token.create`.
- `scenery build` produces the deployable binary and remains the preferred deployment artifact path.
- `scenery harness ui -o json` is an optional browser-backed dashboard check. It starts a temporary `scenery up` process unless `--dashboard-url` points at an existing dashboard, visits core dashboard routes, runs route-specific semantic journeys, checks stable `data-scenery-ui` markers, captures screenshots, writes compact DOM snapshots, and writes console/network artifacts under `.scenery/harness/ui/`.

Runtime safety:

- Generated binaries do not expose dev/admin endpoints by default.
- Dev/admin endpoints such as `/__scenery/config`, `/platform.Stats`, and `/debug/pprof/*` are enabled only for the development child process launched by `scenery up` or when `SCENERY_DEV_ENDPOINTS=1` is set explicitly.
- Complete linked development runtimes add `X-Scenery-Contract-Revision`, `X-Scenery-Implementation-Revision`, `X-Scenery-Build-Input-Digest`, `X-Scenery-Go-Target`, and `X-Scenery-Process-ID` to HTTP responses, including errors. These identify the serving process's linked bundle, never a newer candidate file. They are absent when dev endpoints are disabled or linked identity is incomplete. Compose the build-input digest with its verified runtime-bundle manifest to identify framework source and CLI executable inputs; a disk bundle alone is not served evidence.
- Runtime CORS reflection is enabled in dev endpoint mode. Outside dev mode, CORS origins must be explicitly allowlisted with `SCENERY_CORS_ALLOW_ORIGINS`.
- Build workspaces skip local secret and machine artifacts such as `.env`, `.env.*`, `.git`, `.scenery`, `node_modules`, `.DS_Store`, `__MACOSX`, and `coverage`.

Local observability:

- The user-facing observability surface is `scenery logs`, `scenery logs query`, `scenery logs tail`, `scenery traces list -o json`, `scenery metrics list -o json`, `scenery metrics query`, `scenery metrics labels`, `scenery metrics series`, `scenery inspect observability -o json`, and the dashboard. The current backing substrate exports local observability to Victoria sidecars:
  - VictoriaMetrics: `/opentelemetry/v1/metrics`
  - VictoriaLogs: `/insert/opentelemetry/v1/logs`
  - VictoriaTraces: `/insert/opentelemetry/v1/traces`
- Dashboard trace reads and `scenery traces list|metrics -o json` use scenery-managed observability data. Victoria is the current substrate when local sidecars are available; `devdash.json` is not a fallback trace or report-log history store.
- Every app HTTP response includes `X-Trace-Id` and lists it on `Access-Control-Expose-Headers` so browser clients can read it. Typed and raw handlers that start a request trace reuse that same id. The dashboard API explorer copies the header into `trace_id`.
- `scenery.StartSpan` records stable application-owned child spans as type
  `WORK`. Passing its returned context to nested spans, database queries, and
  HTTP requests preserves their parent relationship in trace waterfalls.
- Ordinary `scenery up` owns its optional Victoria stack in the worktree's private state root and registry. It never reuses another worktree's stack or provisions a global PostgreSQL server for dashboard state. Sidecar startup does not gate required app serving readiness. Reuse and replacement require verified component ownership; stdout/stderr, `last_exit`, and per-component exit details remain observable. The live supervisor probes the stack, serializes whole-stack recovery, backs off failed attempts, and stops recovery on cancellation. Failures remain visible through foreground errors, detached supervisor events, and dashboard notifications even while Victoria is unavailable. Stopping one worktree does not signal another's sidecars. Standalone Victoria utilities use only their explicitly selected state and are not a fallback owner for an ordinary dev runtime.
- `SCENERY_DEV_VICTORIA=0` disables Victoria sidecars. `SCENERY_DEV_VICTORIA_DOWNLOAD=0` disables automatic Victoria binary downloads. When enabled, missing Victoria binaries are downloaded into `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`.
- Victoria binary names, versions, ports, storage layout, download behavior, and Victoria query semantics are beta substrate details. They are documented so local development is debuggable, but they are hidden during ordinary app work and are not part of the stable runtime contract.
- Default Caddy, Victoria sidecar, and managed image versions are pinned in `scenery.toolchain.json`; environment variables override explicit startup controls for local testing where documented. Caddy edge is managed-toolchain only.
- Agent sessions inject `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, `SCENERY_RUNTIME_APP_ID`, `SCENERY_APP_ROOT_HASH`, `SCENERY_BRANCH`, and `SCENERY_WORKTREE` into the app process. Local development reports carry that identity and the reporter PID into Victoria trace, metric, and log exports.
- Dev report endpoints reject missing-session, stale-session, and invalid-token reports before store work. Rejections are exported as structured warning log events with `kind=dev-report-rejected`, and app-side report clients back off after repeated deadline/unauthorized/stale-report failures so old processes cannot hot-loop the dashboard.
- The emitted VictoriaMetrics request duration contract is `scenery_request_duration_seconds` with labels `scenery_app`, `scenery_trace_type`, `scenery_is_root`, `scenery_is_error`, `scenery_service`, optional `scenery_session_id`, optional `scenery_app_root_hash`, optional `scenery_branch`, optional `scenery_worktree`, optional `scenery_endpoint`, and optional `scenery_message_id`.
- The emitted VictoriaTraces and VictoriaLogs attribute contract includes `scenery.application_id`, optional `scenery.session_id`, optional `scenery.app_root_hash`, optional `scenery.branch`, and optional `scenery.worktree`.
- `scenery up` writes local ignore markers under `.scenery/` and the Victoria state roots so downloaded binaries, local databases, logs, generated build outputs, and other machine-local state are not accidentally committed by target apps.

Secrets and environment:

- The human env-var reference is [Environment Reference](environment.md). The machine-readable env contract is [environment.registry.json](environment.registry.json); it is strict current source with `kind: scenery.environment.registry` plus the exact digest `schema_revision`, and `go run ./scripts/verify` fails on identity drift or unregistered production env usage.
- Do not add a new scenery-owned production env var as a convenience escape hatch. Prefer app config, explicit CLI flags, or checked-in manifests; if env is truly required, add a registry entry with rationale, docs, and tests in the same change.
- Process environment always wins over values loaded from local files.
- The stable runtime path reads `.env` from the app root for local secret population when a value is not already present in the process environment.
- Environment dotenv order is `.env`, `.env.<env>`, `.env.local`, `.env.<env>.local`; parent process values win. `local` degenerates to `.env`, `.env.local`. Every dotenv file is optional in every environment: an absent file contributes no values, and process-only configuration needs no placeholder file. Existing files must be readable and valid dotenv; directories, read errors, and malformed content fail. Missing required values and invalid resolved values still fail their existing validation. Scenery does not create dotenv files automatically. Every `.env*` file should be ignored; `.env.example` may commit names only.
- `scenery up` passes local file values into the child process before Go package initialization so package-level declarations can read them through `os.Getenv`.
- Missing declared secrets warn in local development mode.
- `scenery worker` can use process environment without a `.env` file in every environment; operator-run generated binaries likewise use process environment directly. Production startup still fails if any declared secret is missing.
- `.env`, `.env.*`, and secret-bearing local files are not copied into build workspaces.

Standard auth:

- Apps may enable the built-in standard auth module from app config; auth handlers are native contract/runtime resources.
- Auth-protected app code can use `auth.UserID()`, `auth.Data()`, or `auth.CurrentAuthData()` from `scenery.sh/auth`.
- Audited app code uses `auth.CurrentAuditIdentity(ctx)`. Its `EffectiveUserID` is the user whose permissions and data are exercised, while `ActorUserID` is the real initiator; they are equal for normal sessions and differ during impersonation. The value also carries exact tenant, session, and impersonation IDs. `(*AuthData).AuditIdentity()` is nil-safe; missing current auth returns `unauthenticated`. Scenery does not place application entitlements, roles, business organizations, or business-user IDs in this identity or in JWT claims.
- Access tokens are HMAC JWTs with required expiration and `tenant_id` claims.
- Malformed, incorrectly signed, expired, or incomplete access tokens return
  `unauthenticated`; protected contract HTTP bindings map this to their declared
  `admission.unauthenticated` response. Missing signing-key configuration remains
  a server error, not a rejected user credential.
- Standard auth tenant state is framework-owned and lives in `scenery_auth.tenants`; an app-local `tenants` service or table is only an app-domain concern.
- Refresh sessions are stored in PostgreSQL and rotate by hashing refresh tokens. Standard auth reads, issues, and clears only `scenery_refresh`; cookie naming is not configurable.
- Google connections are per standard-auth user and live in `scenery.scenery_auth_google_connections`. The raw Google refresh token is encrypted at rest and never returned to clients. `GET /auth/google/connection` returns `{status, email, scopes, connected_at, last_refresh_at, reauth_reason}` with status `active`, `reauth_required`, or `disconnected`. App Go code calls `auth.GoogleAccessToken(ctx, scopes...)` for request-authenticated work or `auth.GoogleAccessTokenForUser(ctx, userID, scopes...)` from workers; requested scopes must be present in `auth.google_oauth.allowed_scopes`. Expired access tokens refresh under a Postgres row lock; permanent Google revocation marks the connection `reauth_required` and returns `google_reauth_required`, while missing grants return `google_scope_missing`.
- Email delivery is a pluggable `auth.EmailSender`; the default sender is a no-op.
- `/users/dev-bootstrap` is local-only. Without `dev_bootstrap.default_user_email`, it can mint a development token without opening PostgreSQL. With `default_user_email`, it opens standard auth lazily and creates the configured default tenant, verified user, and owner membership on first use when missing.
- DB-backed auth endpoints require the canonical `DATABASE_URL` environment variable.

Implemented `up -o jsonl` rules:

```text
scenery up -o jsonl
```

- output is JSONL
- each line is a `scenery.cli.event` envelope whose `data` conforms to `scenery.run.event`; one terminal summary ends the stream
- human-readable console output is suppressed in this mode
- child stdout/stderr are emitted as structured `process.output` events instead of raw terminal writes

Implemented `check -o json` rules:

```text
scenery check -o json
```

- output is one `scenery.cli` envelope
- success returns `ok: true`; failure returns `ok: false` with catalogued `SCNxxxx` diagnostics
- the `data` payload contains the contract and implementation status, a compact `manifest_summary` (application identity plus resource counts by kind), the `partial_graph` on failure, and HTTP/OpenAPI revisions. `check` does not embed the full manifest; the complete graph stays on `scenery compile --view <view> -o json` and the `list`/`get`/`explain` surfaces
- native implementation verification reports `valid` on success and `invalid` on error; when no native implementation check applies, the status is `not_requested`
- native verification is hermetic (`GOPROXY=off`). When an authored package needs dependencies absent from the local module cache, `SCN6202` reports one bounded package summary, preserves the complete package list in diagnostic `details.missing_packages`, and suggests `go mod download` before retrying instead of emitting cascaded Go type errors

Implemented `harness -o json` rules:

```text
scenery harness -o json
scenery harness -o json --write
```

- output is a single JSON document
- output conforms to `scenery.harness.result`
- it composes `scenery check -o json` and the stable `scenery inspect ... -o json` surfaces
- success returns `ok: true`
- failure returns `ok: false`, per-step errors, diagnostics, and `next_actions`
- failed and expensive steps include `evidence` conforming to `scenery.harness.artifact`
- `--write` persists the same result to `.scenery/harness/latest.json`
- `--write` persists large evidence payloads under `.scenery/harness/artifacts/<run-id>/`
- `--with-validation` and `--with-validation=<profile>` run app validation after the core harness and add a small `validation` pointer with `profile`, `ok`, and `result_path`; the validation result itself stays in `.scenery/harness/validation/latest.json`

Implemented repository-verifier JSON rules (`go run ./scripts/verify`):

```text
go run ./scripts/verify --summary
go run ./scripts/verify --summary -o json
go run ./scripts/verify -o json
go run ./scripts/verify --summary --write
go run ./scripts/verify -o json --write
```

- `--summary` selects concise human output
- `--summary -o json` outputs one `scenery.cli` envelope whose `data` conforms to `scenery.harness.self.summary`
- `-o json` outputs one `scenery.cli` envelope whose `data` conforms to `scenery.harness.self`
- summary output is the agent-facing default and must reference artifacts instead of embedding full drift inventories, successful stdout/stderr tails, complete timing package lists, or full large-file lists
- Human output uses the same `pass`, `pass_with_warnings`, `pass_with_debt` or `fail` classification as JSON summaries, names the selected mode, and includes bounded actionable warning/failure details. `ok`/`can_proceed` refer only to selected checks; they do not certify skipped checks or unselected release probes. A failed self-harness exits 3 after reporting its diagnostics instead of becoming an internal error.
- green summary output should stay under 12 KB; failed summary output should stay under 32 KB while preserving the first actionable failure and artifact references
- it validates the scenery repo itself instead of a target app
- every mode runs docs knowledge validation, `scenery inspect docs --all -o json`, architecture/drift/schema checks, worktree-local `go build -o .scenery/harness/bin/scenery ./cmd/scenery`, and local binary freshness checks. Default adds the complete cached Go suite and vet; quick uses affected packages; race adds the race shortlist. These modes do not run live runtime, database, UI, or fixture probes.
- `--probe <id>` selects only named external probes after common checks, without repeating the full Go suite. Repeated selectors form a union in catalog order. The sole functional catalog and each ID's boundary are listed in [Harness Engineering](harness-engineering.md#explicit-probe-catalog); release selects every entry exactly once and the full race suite.
- `--benchmark worktree-cost` selects only the existing A18 resource measurement with authored fixture preparation, real Victoria binaries, 1/5/10-worktree cohorts, three repetitions and verified owned cleanup. Functional `--probe worktree` and release run A1–A17 without A18. Benchmarks and all-root timing audits require an explicit human measurement request.
- Worktree A9 requires current allocation against an incompatible historical agent home to fail with SCN8003 before creating target database resources. Its old/new runtime coexistence and explicit native migration then use separate homes in one owned nested Docker daemon, preserving the historical agent, server/volume, source data, roles/grants/extensions and continuously serving sibling. A separate home never authorizes adoption of an old root or data; this proof uses a new empty target root and an explicitly verified export/restore.
- report `mode` is one of `default`, `quick`, `race`, `release`, `probe`, `benchmark`; step commands identify focused reproduction. Unknown/duplicate IDs, conflicting modes, and `--fresh-tests` with probe/benchmark modes fail before builds or provisioning.
- the `toolchain preflight` step probes required PATH tools and additionally verifies the go.mod-declared Go toolchain resolves with `GOPROXY=off`; a pruned module cache fails preflight with the exact `GOTOOLCHAIN=go<version> go version` restore command instead of surfacing as SCN6202 fixture failures deep in the suite
- the `ui` probe's `console dependencies` step runs `bun install --frozen-lockfile` in `apps/console` before dashboard and TypeScript lanes; it honors `bun.lock` and fails on lockfile drift instead of rewriting it. When bun is missing from PATH or the install fails, its dependent lanes are skipped, and the failed step reports `skipped_lanes` with one actionable diagnostic.
- every mode validates committed examples for current JSON schemas. The `ui` probe runs Bun TypeScript conformance and typechecks committed native and House clients; `fixtures` runs the fixture generation/compilation matrix. Both are also included in release.
- default, quick, race, release, focused, and changed-area-selected final Go validation uses Go's native test result cache. The full self-harness command is `go test -json ./...`; quick mode uses cached `go test` for affected packages.
- `--fresh-tests` is the explicit fresh measurement or nondeterminism-investigation lane. It discovers the complete `./...` graph, reuses linked test binaries by Go build ID, executes every test body with `-test.count=1`, preserves packages without tests in JSON evidence, uses package parallelism six, and builds at most four missing binaries concurrently. Both concurrency values are pinned from repeated A/B measurement.
- linked binaries, the workspace manifest, and package timing estimates for the explicit fresh lane are disposable under `.scenery/harness/test-binaries/`. The manifest covers toolchain/build environment and tracked/untracked workspace contents. Disposable test binaries disable VCS stamping so committing unchanged contents does not relink the repository.
- `.scenery/harness/test-timing-latest.json` identifies the timing lane. Cached and fresh runs use a five-second advisory budget and target; release runs keep the 30-second enforced budget and five-second optimization target.
- only the explicit `--fresh-tests` lane performs isolated timing confirmation. Packages over their budget are rerun once through one serial `-p 1` confirmation process. Every exact top-level `TestX` root is classed `fast` by default; its 60ms target selects confirmation candidates and its hard budget is 100ms. A candidate runs in 20 separate serial linked-test processes through `go tool test2json`, each using `-test.count=1 -test.parallel=1 -test.run=^ExactRoot$`. Each selected package is linked once into a disposable directory for that confirmation run and removed afterward. No new binary cache is created; the existing Go build cache and `internal/testsuite` cache/scheduling remain unchanged. Cached, incomplete, skipped, malformed or duplicate evidence cannot produce a confirmation. Mandatory release confirmation fails closed. `budgets.confirmation_percentile` is 95, whose nearest-rank value is the second-highest of 20 samples. A p95 exactly equal to 100ms violates the budget. Timing the top-level root includes its subtests and sums every active segment before and after `t.Parallel`; only time paused in the scheduler queue is excluded. Package timing separately retains `TestMain` and package setup cost. The default package budget remains ten seconds, with an explicit fifteen-second baseline for `scenery.sh/cmd/scenery`.
- `budgets.integration_exceptions` remains in the report for schema stability and MUST be an empty array. Any configured entry is a timing-policy failure: every exact top-level Go test root is subject to the repeated isolated 100ms p95 budget, without process, package, prefix, regexp, subtest, `TestMain`, setup, or shared-fixture exemptions.
- release mode owns explicit external-boundary probes outside the Go-test lane. They cover local-agent restart; assistant initialization and production; CLI exit/telemetry; deploy SSH; desktop, dev follower, managed-process, named-lock, session-cleanup, and frontend lifecycle; worktree Git lifecycle and changed-file inspection; edge and Victoria processes, including published static frontend HTTP caching, SPA fallback, HEAD/ranges, method limits, API proxying, blocked paths, and raw traversal containment; native-contract and generated-package compilation; snapshot backup and code tasks; TypeScript checking; fresh test-binary caching; toolchain source builds; build-info freshness; Go package inspection; and the generated native/Bun application. Each boundary retains a focused in-process Go test for ordinary coverage. These probes do not weaken or consume the 100ms top-level-test budget.
- `--probe auth` (also mandatory in release as `standard auth lifecycle`) builds `scripts/verify/testdata/authprobe`, checks its exact 15-case inventory, and runs each case in a fresh native process and disposable database. Named assertions cover schema bootstrap, dev identity/tenant membership, Google sign-in/linking/connections and token refresh (including concurrent PostgreSQL row locking), impersonation, refresh replay, and user lifecycle. Auth endpoints use real authenticated HTTP; Google uses an owned loopback fake. The step provisions and retires its own PostgreSQL allocation through the existing worktree lifecycle. Missing Docker, malformed/incomplete case evidence, failed assertions, or cleanup failure fail the step; no `SCENERY_TEST_DATABASE_URL` opt-in or skipped case is accepted.
- `budgets.confirmation_scope` selects which fast candidates are confirmed. `--fresh-tests` alone uses `regressions`: a candidate is re-run when the previous `.scenery/harness/test-timing-latest.json` did not record it, when it is now at least 25% and 10ms worse, whenever its current observation is at or above the hard budget, or while its prior confirmed p95 remains at or above the hard budget. Confirmed violations are advisory warnings in this lane. `--release --fresh-tests` uses `all`, confirms every candidate, and makes confirmed fast-test violations errors. Package regressions retain their separate 25% plus 0.5s confirmation threshold. Every skipped candidate is recorded in `deferred_confirmations` with its baseline, and a single informational diagnostic reports the skip count. A missing, unreadable, or schema-stale baseline confirms everything.
- `total_seconds` covers the selected cached or fresh full-suite command only. On explicit fresh runs, `confirmation_seconds` records the extra isolated confirmation work; observed candidates and confirmed slow tests are stored separately.
- explicit fresh runs record `test_binaries`: `prepare_seconds` for all pre-execution wall time, `list_seconds` for package discovery, `build_parallelism`, `aggregate_build_seconds` for the sum of the possibly overlapping per-binary elapsed durations, `built_count`, `test_package_count` for packages that produce a test binary, and a `builds` array of `{package, build_id, seconds}` ranked slowest first. `aggregate_build_seconds` is attribution only, not wall or CPU time. Pre-execution work never appears in Go's per-package elapsed times, so this breakdown is the cold-run attribution surface.
- `budgets.test_binary_count` is the maximum number of packages that produce a test binary. Fresh runs compare it to `test_binaries.test_package_count` on every run, including warm ones, because adding a package is a cold-link cost even when this run reused cached binaries. `budgets.cold_prepare_seconds` is the maximum full-prepare wall time at the recorded, pinned build parallelism and applies only when `built_count` equals `test_package_count`. Both are advisory in default/fresh and errors in `--release`. The current line is 60 binaries and 30 seconds of cold preparation at build parallelism four.
- `--probe parallel-runtime` and release exercise parallel managed Postgres dev sessions and tear temporary state down. Default, quick and race do not start these sessions.
- Without Docker, database probes report explicit skips and warnings. The parallel-runtime step's `postgres_verified` is false when it only proves session/routing behavior; no database-isolation claim follows from a passing control-plane check.
- `--probe postgres` and release run the full service proof in a disposable throwaway-tuned container (tmpfs data directory, fsync off) with the local agent disabled. Its summary reports `proof`, ordered `segments` with `duration_ms`, and `cleanup_ms`. It covers container start, isolated databases and schemas, durable round trip, auth bootstrap, service-schema `db reset` and snapshot save/load. Missing Docker fails this full proof.
- `--probe storage` and release exercise configured storage through an app task, CLI import/export, and a live local-backend route. Restart proof writes an object through the route, stops with `scenery down`, restarts and reads the same fsync'd object through the route.
- agents must not run `go install ./cmd/scenery` unless a human explicitly requests updating the shared installed `scenery` binary; multiple worktrees may otherwise overwrite each other's CLI, and the knowledge-contract step fails when current repository-validation instructions contain an unqualified recommendation to do so
- architecture checks fail on unapproved direct dependencies, forbidden framework imports, CLI package boundary violations, missing generated/vendored ignore markers, and non-generated source/code files over 2500 lines; Markdown docs are not subject to line-count size checks
- architecture checks warn on non-generated source/code files over 1000 lines, cgo imports, `.DS_Store` artifacts, and compatibility imports outside known migration paths; unchanged warnings outside the changed area are debt summary in compact output, not agent attention
- local harness/report artifacts matching `.scenery/**`, `coverage/**`, `test-results/**`, `*.harness*.json`, or `scenery-harness-self-*.json` are reported as ignored local artifacts and do not drive changed-area recommended commands
- `scenery harness ui -o json` is not part of the default self-harness path. It needs a local Chrome/Chromium-compatible browser and is intended for explicit dashboard route validation. The route journeys cover dashboard home app selector/status, API Explorer endpoint/form behavior, service catalog metadata, traces empty/table/detail behavior, DB list or unavailable states, schedule status/empty states, and durable/worker status cards.
- `--write` persists the full archive to `.scenery/harness/self-latest.json`, the compact summary to `.scenery/harness/self-summary-latest.json`, and topic artifacts such as `.scenery/harness/test-timing-latest.json`
- failed and expensive steps include `evidence` conforming to `scenery.harness.artifact`; Go test JSONL evidence is written as `.scenery/harness/artifacts/<run-id>/go-test.jsonl` when `--write` is present
- changed-area output classifies paths as `documentation-only`, `go-package`, `cli-json-contract`, `compiler-or-generator`, `ui-catalog`, `dashboard`, `release-sensitive-or-runtime`, or `repository-fallback`; multiple classes are cumulative and `recommended_commands` is their exact deduplicated union, with the full self-harness replacing the quick self-harness when both match
- documentation-only paths select full knowledge/index inspection plus quick self-harness; Go packages select every affected-package test plus `go test ./...`; CLI JSON contracts select command tests, schema validation, and `docs/local-contract.md`; compiler/generator and UI catalog paths select committed consumer regeneration; dashboard paths select lint, typecheck, build, and browser acceptance; release-sensitive/runtime paths select the full worktree-local self-harness and its applicable real-process proof
- `--write` refreshes `.scenery/harness/agent-context.json` as the one-file agent handoff. It includes current failing steps, first files to read, exact rerun commands, `validation_classification`, the changed-area command union, relevant active ExecPlans, recent failed harness artifacts, docs freshness, and separate risk classifications: `runtime`, `CLI contract`, `dashboard`, `schema`, `release`, and `onlv-impacting`.

Default agent loop:

```text
scenery doctor -o json
go run ./scripts/verify --quick --summary --write
cat .scenery/harness/agent-context.json
# implement
# run changed_area.recommended_commands
```

For a changed external boundary, select its named `--probe <id>` and record
assertions and cleanup. Full release certification is an explicit workflow,
not automatic for `release-sensitive-or-runtime` paths. Its single recipe is:

```text
scripts/release-gate.sh
```

Implemented `inspect harness` rules:

```text
scenery inspect harness -o json
scenery inspect harness -o json --app-root <path>
scenery inspect harness -o json --repo-root <path>
scenery inspect harness artifact test-timing -o json
scenery inspect harness diagnostics --severity warning -o json
scenery inspect harness timing --top 10 -o json
```

- manifest output conforms to `scenery.inspect.harness`
- focused outputs use the same schema version and return bounded topic-specific JSON for artifacts, diagnostics, and timing
- from an app root, manifest output reports `.scenery/harness/latest.json`, `.scenery/harness/ui/latest.json`, and `.scenery/harness/artifacts/`
- from the scenery repo root, manifest output reports `.scenery/harness/self-latest.json`, `.scenery/harness/self-summary-latest.json`, `.scenery/harness/ui/latest.json`, and `.scenery/harness/artifacts/`
- focused artifact output reads known `.scenery/harness/*-latest.json` files by name (`self-harness`, `self-summary`, `toolchain`, `changed-area`, `drift`, `test-timing`, `fixture-matrix`, `schema-validation`, `agent-context`)
- diagnostics output caps returned diagnostics at 50 and supports `--severity error|warning`
- timing output reads `.scenery/harness/test-timing-latest.json`, sorts slow packages/tests by duration, and caps both lists with `--top`
- a missing named artifact file (including the timing artifact) is `failed_precondition` (`SCN8003`, exit 3) pointing at `go run ./scripts/verify --summary --write`; an unknown artifact name is `invalid_request` (`SCN8001`, exit 2), never an internal `SCN9000`
- manifest output reads latest harness outputs when present and returns their normalized `artifacts` and `evidence` arrays
- evidence records use `scenery.harness.artifact` and include `command`, `cwd`, `started_at`, `duration_ms`, `exit_code`, output tails, artifact references, and `repro_command`

Release gate:

```text
scripts/release-gate.sh
```

- this is the high-signal pre-release gate, not the normal inner-loop developer check
- it invokes `go run ./scripts/verify --release --summary --write` once for common Go/race/UI/release checks, without a second shell implementation of those suites. The shell retains lint, dashboard embed preparation, source-only working-tree snapshot build, fixture binary smoke, optional generic external-app read-only smoke, public-router safety and artifact exclusion. Embed preparation and the verifier's later source-build/actual-product freshness comparison are distinct lifecycle boundaries. The default shell target is the verifier's prepared `.scenery/harness/bin/scenery`; an explicit `SCENERY_BIN` remains a distinct selected target. Source-snapshot CLI proof uses a disposable `go build -o`, never `go install`; the snapshot includes nonignored new authored files and excludes tracked deletions and ignored caches.
- release-gate logs should use the same `scenery.harness.artifact` evidence shape for failed or expensive steps
- `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` may point at a read-only scenery app for the optional external app smoke
- `SCENERY_RELEASE_GATE_LOG_DIR` may override the log directory; otherwise logs are written under `.scenery/release-gate/`
- the release gate must not create or modify client-application worktrees; client-app validation belongs in that app's own repo and app-local gates
- artifact hygiene is intentionally strict and fails on local release artifacts such as `.DS_Store` and `__MACOSX`

Implemented `logs -o jsonl` rules:

```text
scenery logs -o jsonl
```

- `-o jsonl` emits JSON Lines
- each JSONL line is a `scenery.cli.event` envelope whose payload conforms to `scenery.dev.event`; the stream ends with one terminal summary event
- one JSON object is emitted per VictoriaLogs-backed structured dev event
- structured events include app id/root, session id, source id/kind/name/role/pid/stream/status, level, message, parsed fields, raw output, and parse metadata
- structured dev events are assigned a stable integer ID before export to VictoriaLogs
- human-readable raw output remains the default when neither flag is used

Implemented `traces clear -o json` rules:
- output conforms to `scenery.traces.clear`
- trace clearing is dev/admin beta; its existence does not make schedule, trace clearing, or queue deletion semantics stable

## Artifact Locations

### Current implemented locations

Use `scenery inspect paths -o json` as the source of truth.

Today scenery uses:
- app config: `<app-root>/.scenery.json`
- cache root:
  - `$SCENERY_DEV_CACHE_DIR`, if set
  - otherwise OS user cache + `/scenery`
- build workspace: `<cache-root>/build/<sanitized-app-name>-<hash>`
- built app binary: `<workspace>/scenery-app`
- build state: `<workspace>/.scenery-build-state.json`
- generated library facade and export shim:
  `<workspace>/<package>/scenerylib_<name>/`
- default library release directory:
  `<app-root>/dist/libraries/<artifact>/<version>/`
- library artifact manifest:
  `<output>/<artifact>.scenery-library.json`

`scenery build --lib` defaults to both supported targets: darwin/arm64
(`lib<artifact>_darwin_arm64.dylib`) and linux/amd64
(`lib<artifact>_linux_amd64.so`). Darwin builds natively on a darwin/arm64 host;
Linux builds in the pinned `golang:1.27.0-bookworm` linux/amd64 container and
records Go `go1.27.0` plus glibc floor `2.36`. `--platform all` is the same
matrix; `host` is accepted only when the host is one of those exact targets.
The manifest binds library/version/ABI identity and per-artifact platform,
relative path, SHA-256, Go version, and Linux glibc floor. The loader rejects
unknown fields, stale schema revisions, traversal paths, digest or ABI/version
mismatches, unsupported hosts, or missing symbols before routing calls.

### Repo-Local Cache Locations

Durable local runtime authority is deliberately outside the checkout:

```text
<agent-home>/worktrees/<sha256-canonical-absolute-app-root>/
  worktree.json
  owner.lock
  operation.lock
  owner.json
  control/
    sessions.json
    dashboard/
    dev/
  restore-<verified-manifest-hash>.zip
```

`worktree.json` binds the exact root, app ID, local user, and retained PostgreSQL
intent/credentials to its Docker daemon, immutable container ID, labeled volume,
pinned engine image, and authenticated SQL system identity. It is owner-only
authority, never a public inspection payload. Branch/environment/PID/version
changes do not create another key. Existing parent symlinks are resolved even
for a deleted checkout so its orphaned data stays discoverable. Unix sockets
normally live beside this state; platforms with short socket-path limits use
a private shortened temporary locator, still checked against full authority.
No decode failure authorizes replacing credentials or allocating empty data.
Verified restore archives remain retained outside the checkout, including after
successful restore; treat them as backups containing the selected application
data and remove them only through an explicit retention decision.

Implemented now:

```text
<app-root>/.scenery/
  build/
    latest.json
    runtime/
      <go-target>.json
    assets/
      <go-target>/
        runtime-descriptor-<capsule-digest>.json
  assistant-cache/
    <package-lock-digest>/
  assistants/
    <private helper state>
  run/
    assistants.json
  harness/
    latest.json
    validation/
      latest.json
      <profile>-latest.json
      artifacts/
        <run-id>/
    self-latest.json
```

Reserved for upcoming work:

```text
<app-root>/.scenery/
  state/
  logs/
```

Rules:
- Use `scenery inspect ... -o json` for app, route, service, endpoint, build,
  path, docs, generator, durable, storage, and UI metadata. Use bare
  `scenery inspect ui` only for its human rewrite queue. Use
  `scenery traces list -o json` and `scenery metrics list -o json` for local
  observability metadata.
- Worktree dashboard state uses `<worktree-state>/control/dashboard/devdash.json` for compact records and `app-model/<metadata|api-encoding>/sha256/<hash>.json` beneath that root for large app-model blobs. The embedded owner dashboard writes it; runtime components use its internal control endpoint. CLI inspection opens only existing state read-only and never falls back to a global store. Treat these files as internal cache artifacts; use dashboard APIs and CLI JSON instead of reading them directly.
- Use `scenery inspect build -o json` for build metadata. `build/latest.json` is a local cache pointer to the latest prepared or compiled build workspace.
- `build/runtime/<go-target>.json` is the exact runtime-bundle descriptor for the latest local build of that target. Treat it as build output, not a contract source; distribute the copied `<binary>.scenery.runtime-bundle.json` sidecar with an explicit binary output. When the application declares assistants, its optional `assistant_assets` array contains provider-neutral runtime-asset descriptors with the selected target plus Node/capsule archive and tree digests; the descriptor shape is strict and carries its exact structural schema revision.
- `assistant-cache/<package-lock-digest>/` is the content-addressed managed
  Node/npm dependency cache. It is never authored source and `assistant sync`
  reuses it only when the exact package and lock bytes match.
- `assistants/` and `run/assistants.json` are private helper/supervisor state.
  They may contain control tokens, process state, or implementation metadata;
  use `scenery assistant status` and `scenery inspect assistants` instead of
  reading them directly. The default inspection payload intentionally omits
  those private details.
- Every checked cross-process artifact binds `schema_revision` to the static
  digest of its complete self-normalized JSON Schema. Transient private
  artifacts without a checked schema bind a complete structural descriptor;
  readers reject any other identity instead of translating an older shape.
- Use `scenery harness -o json` for framework app-model proof, `scenery validate <profile> -o json` for app-owned quality gates, and `go run ./scripts/verify --summary` for scenery repo validation. `harness/latest.json`, `harness/validation/latest.json`, `harness/self-latest.json`, and `harness/self-summary-latest.json` are local snapshots written by `--write`; `-o json` is the explicit full archive stdout mode.
- Future implementation should keep cache paths predictable for debugging, but external tools and agents should integrate through command JSON output.

## JSON Schemas

Implemented now:
- [scenery.approval-token.schema.json](schemas/scenery.approval-token.schema.json)
- [scenery.approval-trust.schema.json](schemas/scenery.approval-trust.schema.json)
- [scenery.change-plan.schema.json](schemas/scenery.change-plan.schema.json)
- [scenery.change-receipt.schema.json](schemas/scenery.change-receipt.schema.json)
- [scenery.build.result.schema.json](schemas/scenery.build.result.schema.json)
- [scenery.build.desktop.schema.json](schemas/scenery.build.desktop.schema.json)
- [scenery.assistant.control.event.schema.json](schemas/scenery.assistant.control.event.schema.json)
- [scenery.assistant.control.request.schema.json](schemas/scenery.assistant.control.request.schema.json)
- [scenery.assistant.control.response.schema.json](schemas/scenery.assistant.control.response.schema.json)
- [scenery.assistant.init.schema.json](schemas/scenery.assistant.init.schema.json)
- [scenery.assistant.public-error.schema.json](schemas/scenery.assistant.public-error.schema.json)
- [scenery.assistant.public-event.schema.json](schemas/scenery.assistant.public-event.schema.json)
- [scenery.assistant.public.request.schema.json](schemas/scenery.assistant.public.request.schema.json)
- [scenery.assistant.public.response.schema.json](schemas/scenery.assistant.public.response.schema.json)
- [scenery.assistant.runtime-descriptor.schema.json](schemas/scenery.assistant.runtime-descriptor.schema.json)
- [scenery.assistant.status.schema.json](schemas/scenery.assistant.status.schema.json)
- [scenery.assistant.sync.schema.json](schemas/scenery.assistant.sync.schema.json)
- [scenery.cli.schema.json](schemas/scenery.cli.schema.json)
- [scenery.cli.event.schema.json](schemas/scenery.cli.event.schema.json)
- [scenery.deployment-plan.schema.json](schemas/scenery.deployment-plan.schema.json)
- [scenery.deployment-receipt.schema.json](schemas/scenery.deployment-receipt.schema.json)
- [scenery.generated.schema.json](schemas/scenery.generated.schema.json)
- [scenery.generate.result.schema.json](schemas/scenery.generate.result.schema.json) — `generate` envelope data
- [scenery.provider.lock.result.schema.json](schemas/scenery.provider.lock.result.schema.json) — `provider lock` envelope data
- [scenery.library-generated.schema.json](schemas/scenery.library-generated.schema.json)
- [scenery.library.artifact.schema.json](schemas/scenery.library.artifact.schema.json)
- [scenery.library.build.result.schema.json](schemas/scenery.library.build.result.schema.json)
- [scenery.go-build-input-manifest.schema.json](schemas/scenery.go-build-input-manifest.schema.json)
- [scenery.manifest.schema.json](schemas/scenery.manifest.schema.json)
- [scenery.package-generated.schema.json](schemas/scenery.package-generated.schema.json)
- [scenery.runtime-bundle.schema.json](schemas/scenery.runtime-bundle.schema.json)
- [scenery.typescript-client-generated.schema.json](schemas/scenery.typescript-client-generated.schema.json)
- [scenery.inspect.app.schema.json](schemas/scenery.inspect.app.schema.json)
- [scenery.inspect.routes.schema.json](schemas/scenery.inspect.routes.schema.json)
- [scenery.inspect.services.schema.json](schemas/scenery.inspect.services.schema.json)
- [scenery.inspect.endpoints.schema.json](schemas/scenery.inspect.endpoints.schema.json)
- [scenery.inspect.assistants.schema.json](schemas/scenery.inspect.assistants.schema.json)
- [scenery.inspect.traces.schema.json](schemas/scenery.inspect.traces.schema.json)
- [scenery.inspect.metrics.schema.json](schemas/scenery.inspect.metrics.schema.json)
- [scenery.inspect.observability.schema.json](schemas/scenery.inspect.observability.schema.json)
- [scenery.inspect.ui.schema.json](schemas/scenery.inspect.ui.schema.json)
- [scenery.logs.query.schema.json](schemas/scenery.logs.query.schema.json)
- [scenery.logs.tail.entry.schema.json](schemas/scenery.logs.tail.entry.schema.json)
- [scenery.help.schema.json](schemas/scenery.help.schema.json)
- [scenery.agent.cleanup.schema.json](schemas/scenery.agent.cleanup.schema.json)
- [scenery.down.schema.json](schemas/scenery.down.schema.json)
- [scenery.prune.schema.json](schemas/scenery.prune.schema.json)
- [scenery.metrics.query.schema.json](schemas/scenery.metrics.query.schema.json)
- [scenery.metrics.labels.schema.json](schemas/scenery.metrics.labels.schema.json)
- [scenery.metrics.series.schema.json](schemas/scenery.metrics.series.schema.json)
- [scenery.telemetry.schema.json](schemas/scenery.telemetry.schema.json)
- [scenery.inspect.docs.schema.json](schemas/scenery.inspect.docs.schema.json)
- [scenery.docs.index.schema.json](schemas/scenery.docs.index.schema.json)
- [scenery.inspect.build.schema.json](schemas/scenery.inspect.build.schema.json)
- [scenery.inspect.paths.schema.json](schemas/scenery.inspect.paths.schema.json)
- [scenery.inspect.generators.schema.json](schemas/scenery.inspect.generators.schema.json)
- [scenery.inspect.durable.schema.json](schemas/scenery.inspect.durable.schema.json)
- [scenery.durable.worker_token.create.schema.json](schemas/scenery.durable.worker_token.create.schema.json)
- [scenery.durable.jobs.schema.json](schemas/scenery.durable.jobs.schema.json)
- [scenery.db.apply.result.schema.json](schemas/scenery.db.apply.result.schema.json)
- [scenery.db.seed.result.schema.json](schemas/scenery.db.seed.result.schema.json)
- [scenery.db.setup.result.schema.json](schemas/scenery.db.setup.result.schema.json)
- [scenery.db.list.schema.json](schemas/scenery.db.list.schema.json)
- [scenery.db.server.status.schema.json](schemas/scenery.db.server.status.schema.json)
- [scenery.snapshot.save.schema.json](schemas/scenery.snapshot.save.schema.json)
- [scenery.snapshot.verify.schema.json](schemas/scenery.snapshot.verify.schema.json)
- [scenery.snapshot.load.schema.json](schemas/scenery.snapshot.load.schema.json)
- [scenery.snapshot.manifest.schema.json](schemas/scenery.snapshot.manifest.schema.json)
- [scenery.task.list.schema.json](schemas/scenery.task.list.schema.json)
- [scenery.task.inspect.schema.json](schemas/scenery.task.inspect.schema.json)
- [scenery.task.graph.schema.json](schemas/scenery.task.graph.schema.json)
- [scenery.inspect.validation.schema.json](schemas/scenery.inspect.validation.schema.json)
- [scenery.validation.list.schema.json](schemas/scenery.validation.list.schema.json)
- [scenery.validation.inspect.schema.json](schemas/scenery.validation.inspect.schema.json)
- [scenery.validation.graph.schema.json](schemas/scenery.validation.graph.schema.json)
- [scenery.validation.plan.schema.json](schemas/scenery.validation.plan.schema.json)
- [scenery.validation.result.schema.json](schemas/scenery.validation.result.schema.json)
- [scenery.traces.clear.schema.json](schemas/scenery.traces.clear.schema.json)
- [scenery.build.latest.schema.json](schemas/scenery.build.latest.schema.json)
- [scenery.run.event.schema.json](schemas/scenery.run.event.schema.json)
- [scenery.harness.result.schema.json](schemas/scenery.harness.result.schema.json)
- [scenery.harness.self.schema.json](schemas/scenery.harness.self.schema.json)
- [scenery.harness.self.summary.schema.json](schemas/scenery.harness.self.summary.schema.json)
- [scenery.dev.event.schema.json](schemas/scenery.dev.event.schema.json)
- [scenery.version.schema.json](schemas/scenery.version.schema.json)
- [scenery.doctor.result.schema.json](schemas/scenery.doctor.result.schema.json)
- [scenery.deploy.registry.schema.json](schemas/scenery.deploy.registry.schema.json)
- [scenery.deploy.status.schema.json](schemas/scenery.deploy.status.schema.json)
- [scenery.toolchain.schema.json](schemas/scenery.toolchain.schema.json)
- [scenery.toolchain.status.schema.json](schemas/scenery.toolchain.status.schema.json)
- [scenery.storage.inspect.schema.json](schemas/scenery.storage.inspect.schema.json)
- [scenery.storage.object.schema.json](schemas/scenery.storage.object.schema.json)
- [scenery.storage.list.schema.json](schemas/scenery.storage.list.schema.json)
- [scenery.storage.delete.schema.json](schemas/scenery.storage.delete.schema.json)

Schema rules:
- first-party command payloads carry an unversioned logical `kind` and deterministic digest `schema_revision`
- the outer `scenery.cli` or `scenery.cli.event` envelope owns `spec_revision` and producer identity; nested command data does not duplicate them
- any shape change produces a new digest revision without adding a selectable semantic version
- consumers should match on `kind` and the exact `schema_revision`, not on command name alone

### `scenery inspect ui`

```text
scenery inspect ui [--frontend <name>] [--app-root <path>] [-o human|json]
```

This read-only inspection scans hand-authored `.tsx` and `.jsx` files beneath
each configured frontend root. It excludes dependencies, build output, tests,
generated directories, the materialized `scenery-ui` catalog, and files carrying
Scenery's generated-source marker. `--frontend` selects one configured frontend;
an unknown name is invalid input. An app with no frontends succeeds with an
empty `frontends` array.

Each file reports separate markup and style axes. Markup classifies Astryx,
`@scenery/ui`, raw HTML, local components, third-party components, and SVG
internals. Style classifies references to identifiers imported from `*.stylex`
modules, hardcoded colors, hardcoded `px`/`rem`/`em` sizes inside
`stylex.create`, and JSX attributes named exactly `style`. `xstyle` is not an
inline style. `ds_share` and `token_share` are numbers from 0 through 1 and are
omitted when their denominator is zero.

Rows sort by descending `score`, then path. The score is
`raw + 2*raw_colors + raw_sizes + 3*inline_style_props`; it is only a rewrite
queue. It is not a check, threshold, or policy signal. Frontends with no Astryx,
`@scenery/ui`, or `*.stylex` imports report `design_system: "none"` with no file
rows. JSON uses kind `scenery.inspect.ui` and the payload schema
[scenery.inspect.ui.schema.json](schemas/scenery.inspect.ui.schema.json).

## Examples

### `scenery inspect app -o json`

```json
{
  "kind": "scenery.inspect.app",
  "schema_revision": "sha256:b03f14d1a3f67697f8a3f410bb037a43b6bd02e9119cd395466278fe6a21ea55",
  "app": {
    "name": "billing",
    "id": "billing-dev",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json",
    "module_path": "example.com/billing"
  },
  "config": {
    "name": "billing",
    "id": "billing-dev",
    "frontends": {
      "web": {
        "root": "apps/web"
      }
    },
    "observability": {
      "logs": {
        "include_endpoints": [],
        "exclude_endpoints": []
      },
      "tracing": {
        "include_endpoints": [],
        "exclude_endpoints": []
      }
    }
  },
  "counts": {
    "packages": 3,
    "services": 2,
    "endpoints": 7,
    "middleware": 1,
    "auth_handler": 1,
    "runtime_declarations": 3
  },
  "services": [
    "auth",
    "users"
  ],
  "auth_handler": {
    "service": "auth",
    "name": "AuthHandler"
  }
}
```

### `scenery inspect build -o json`

Add `--verify-generation` for read-only current-source verification of the existing
development candidate. It reuses the strict build candidate verification, checks
authored-source and workspace-content fingerprints against current build state,
resolves current declarations and target identity, and recomputes the Go input
manifest in module-readonly mode (including dependency, embed and native inputs).
Runtime-backed state uses a freshly read runtime watcher source projection, not
the standalone build projection (test files and Go module rebasing differ).
The workspace fingerprint and canonical input manifest both verify the actual
final post-tidy bytes; inspection does not reconstruct a pre-tidy projection.
It does not generate, compile/link Go, publish build state, or require a prior
standalone build. Changed source, missing state or concurrent rebuild fails closed.
These unmet verification prerequisites are `SCN8003` / `failed_precondition`, not
opaque internal errors.
The optional response fields `candidate_identity`, `verification_module_source`
and `verification_module_digest` provide the same identity/helper inline without
artifact writes. Import only the checksum-verified source bytes. Use this for
before/after fast feedback; retain explicit builds for full acceptance when needed.

```json
{
  "kind": "scenery.inspect.build",
  "schema_revision": "sha256:f34b323c11b11faa7c4000f779549ac859eb689c4d0f6b6073473d41924b6df3",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "build": {
    "workspace_dir": "/cache/scenery/build/billing-abcdef0123456789",
    "binary_path": "/cache/scenery/build/billing-abcdef0123456789/scenery-app",
    "workspace_exists": true,
    "binary_exists": true,
    "build_state_path": "/cache/scenery/build/billing-abcdef0123456789/.scenery-build-state.json",
    "build_state_exists": true,
    "build_state_version": "3",
    "dependency_fingerprint": "abc123",
    "graph_fingerprint": "def456",
    "metadata_present": true,
    "api_encoding_present": true,
    "source_file_count": 24,
    "generated_file_count": 6
  }
}
```

### `scenery inspect endpoints -o json`

```json
{
  "kind": "scenery.inspect.endpoints",
  "schema_revision": "sha256:af1066b46918c1a19a1e24c22e7316c35a406a6d8cfdd04c49e1a1623a797d13",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "endpoints": [
    {
      "id": "users.Get",
      "service": "users",
      "endpoint": "Get",
      "access": "public",
      "raw": false,
      "path": "/users/:id",
      "methods": ["GET"],
      "has_payload": true
    }
  ]
}
```

### `scenery traces list -o json`

Beta diagnostic subject. Use this when an agent needs concrete local traces
without scraping the dashboard UI. The JSON shape is versioned, but retention,
backend preference, span reconstruction, and clear semantics may change before
this is promoted to stable.

Example:

```text
scenery traces list -o json --endpoint SyncGet --min-duration-ms 2000 --since 1h --slowest
```

Example output:

```json
{
  "kind": "scenery.inspect.traces",
  "schema_revision": "sha256:f3a83468f7bc3d018825b0536c47515a3d6e5e9d053a3e3442a6fa3a6a9bd816",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "query": {
    "app_id": "billing",
    "session_id": "feature-a-123abc",
    "limit": 100,
    "since": "1h0m0s",
    "endpoint": "List",
    "min_duration_ms": 2000,
    "sort": "duration_desc",
    "available_filters": ["--app-root", "--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
  },
  "traces": [
    {
      "trace_id": "trace-1",
      "span_id": "span-1",
      "session_id": "feature-a-123abc",
      "kind": "RPC",
      "status": "ok",
      "service": "tasks",
      "endpoint": "List",
      "started_at": "2026-04-27T13:00:00Z",
      "duration_ms": 2310,
      "duration_nanos": 2310000000
    }
  ]
}
```

### `scenery metrics list -o json`

Beta diagnostic subject. Use this when an agent needs a metrics-style rollup
over locally captured traces and logs. The JSON shape is versioned, but rollup
definitions, percentile calculations, default limits, and Victoria source
selection may change before this is promoted to stable.

Example:

```text
scenery metrics list -o json --service tasks --since 15m
```

Example output:

```json
{
  "kind": "scenery.inspect.metrics",
  "schema_revision": "sha256:6af4d264dbb1fd08f82a3b69eac6114dd400100145476bae5f8cdd2fb8f337bd",
  "app": {
    "name": "billing",
    "root": "/repo/billing",
    "config_path": "/repo/billing/.scenery.json"
  },
  "query": {
    "app_id": "billing",
    "session_id": "feature-a-123abc",
    "limit": 10000,
    "since": "15m0s",
    "service": "tasks",
    "sort": "started_at_desc",
    "available_filters": ["--app-root", "--service", "--endpoint", "--trace-id", "--status ok|error", "--min-duration-ms", "--since", "--limit", "--slowest"]
  },
  "summary": {
    "trace_count": 12,
    "error_count": 1,
    "error_rate": 0.08333333333333333,
    "event_count": 34,
    "log_count": 9,
    "avg_duration_ms": 120.4,
    "min_duration_ms": 3.1,
    "max_duration_ms": 520.7,
    "p50_duration_ms": 88.2,
    "p95_duration_ms": 500.1
  },
  "services": [],
  "endpoints": [],
  "logs": [],
  "meta": {
    "trace_metric_limit": 10000
  }
}
```

### `scenery inspect observability -o json`

Beta diagnostic subject. Use this before ad hoc observability queries when an
agent needs to know whether the local Victoria backends are reachable and which
scope will be enforced.

Example:

```text
scenery inspect observability -o json
```

The response uses `scenery.inspect.observability` and includes `scope`,
`backends.logs`, `backends.metrics`, `backends.traces`, examples, and optional
warnings. Raw backend URLs are exposed only under the optional `debug.base_urls`
object for intentional substrate debugging.

### `scenery logs query -o json`

Beta query surface for scoped VictoriaLogs LogsQL. This is the preferred CLI
path for targeted log debugging when plain `scenery logs -o jsonl` is too broad.

Example:

```text
scenery logs query -o json --since 15m --limit 100 --query 'error OR panic'
```

The response uses `scenery.logs.query`, echoes the selected scope and query
bounds, and returns normalized entries with `time`, `level`, `source`,
`message`, `fields`, `trace_id`, `span_id`, and `raw` where available. Passing
`-o jsonl` writes only log entries as JSON Lines. `scenery logs tail -o jsonl`
emits one `scenery.logs.tail.entry` object per line and uses `--since` as the
VictoriaLogs live-tail `start_offset`.

### `scenery metrics query -o json`

Beta query surface for scoped PromQL/MetricsQL. Range queries are the default;
`--instant` uses the instant query endpoint.

Example:

```text
scenery metrics query -o json --since 15m --step 5s --promql 'max_over_time(scenery_request_duration_seconds[15m])'
```

The response uses `scenery.metrics.query`, echoes scope and bounds, reports
the backend `result_type`, and returns normalized metric series and samples.
`scenery metrics labels -o json --since 1h --match 'scenery_request_duration_seconds'` emits `scenery.metrics.labels`.
`scenery metrics series -o json --match 'scenery_request_duration_seconds'` emits
`scenery.metrics.series`.

### `scenery inspect docs -o json`

Use task-scoped inspection before changing a repository path:

```text
scenery inspect docs --for-path internal/generate/client.go -o json
```

The unfiltered command returns a compact catalog-health summary with no
documents. `--tag <tag>`, `--status
active|reference|completed|deprecated`, and `--review-due` select catalog
entries and may be combined. `--for-path` is exclusive with those catalog
filters. `--all` is exclusive with every filter and is the only command that
returns the complete catalog, child-index inventories, plan indexes, and
tech-debt reference.

Source files:

- [docs/index.md](index.md)
- [docs/knowledge.json](knowledge.json)
- [docs/plans/active.md](plans/active.md)
- [docs/plans/completed.md](plans/completed.md)
- [docs/tech-debt.md](tech-debt.md)

`docs/knowledge.json` is strict current source with `kind: scenery.docs.index`
and the exact digest `schema_revision`; old semantic `schema_version` labels and
unknown fields are rejected.

Path-query output contains only applicable `AGENTS.md` scopes, the owning
`ARCHITECTURE.md` section, matching current-contract sections, relevant active
ExecPlans, related schemas, and applicable verification commands. A completed
historical plan is omitted unless its own path is queried directly. Ordinary
path queries are capped at eight documents and should remain below 10 KiB.
Routing and commands reuse self-harness changed-area logic.

Scheduled freshness applies to living contracts, instructions, schemas, and
active plans. A completed numbered ExecPlan is immutable historical evidence:
its `review_due` value is always false even after its recorded `review_after`
date, it does not contribute to `review_due_count`, and ordinary tag or
review-due filters omit it. `--status completed`, `--all`, and a direct plan
path provide explicit archive access. A completed plan is still surfaced as an
actionable exception when `freshness: "stale"` records a known contradiction,
the completed index contains a broken link, or the active index references it
as current.

Example output:

```json
{
  "kind": "scenery.inspect.docs",
  "schema_revision": "sha256:cef606ca6894f7126a86ead96efcc6c7eacababa97be5c68b19db7514e734112",
  "repo": {
    "root": "/repo/scenery",
    "module_path": "scenery.sh",
    "go_mod_path": "/repo/scenery/go.mod"
  },
  "query": {
    "mode": "path",
    "for_path": "internal/generate/client.go"
  },
  "summary": {
    "document_count": 134,
    "selected_document_count": 5,
    "missing_count": 0,
    "review_due_count": 0,
    "stale_count": 0,
    "agent_scope_count": 20,
    "stale_child_index_entry_count": 0,
    "missing_child_index_entry_count": 0,
    "quality": {
      "A": 40,
      "B": 93
    }
  },
  "agents": {
    "scopes": [
      {
        "path": "AGENTS.md",
        "scope": "."
      },
      {
        "path": "internal/generate/AGENTS.md",
        "scope": "internal/generate"
      }
    ]
  },
  "documents": [
    {
      "path": "ARCHITECTURE.md",
      "title": "scenery Architecture",
      "owner": "scenery maintainers",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-07-23",
      "review_after": "2026-08-22",
      "summary": "High-level repository code map.",
      "tags": ["architecture", "agents", "boundaries", "codemap"],
      "exists": true,
      "review_due": false,
      "stale": false,
      "role": "architecture",
      "reason": "owning architecture section",
      "sections": [
        {
          "heading": "`internal/generate`",
          "anchor": "internalgenerate",
          "start_line": 147,
          "end_line": 169
        }
      ]
    }
  ],
  "verification_commands": [
    "go run ./scripts/verify --summary --write",
    "go test ./...",
    "go test ./internal/generate"
  ]
}
```

With `--all`, the `agents` object reports every discovered `AGENTS.md` scope,
compares child scopes against the root Child Agent Index, and reports stale or
missing index entries. Summary health counts always describe the complete
catalog; `selected_document_count` describes the current query.

### `scenery inspect harness -o json`

Use this when an agent needs the latest harness evidence without parsing
terminal output.

Source files:

- `.scenery/harness/latest.json`
- `.scenery/harness/self-latest.json`
- `.scenery/harness/self-summary-latest.json`
- `.scenery/harness/ui/latest.json`
- `.scenery/harness/ui/screenshots/*.png`
- `.scenery/harness/ui/dom/*.json`
- `.scenery/harness/ui/console.jsonl`
- `.scenery/harness/ui/network.jsonl`
- `.scenery/harness/artifacts/`

Example:

```text
scenery inspect harness -o json
scenery inspect harness artifact test-timing -o json
scenery inspect harness diagnostics --severity warning -o json
scenery inspect harness timing --top 10 -o json
```

Example output:

```json
{
  "kind": "scenery.inspect.harness",
  "schema_revision": "sha256:ae2471824579553b7dfe6c12a90c5e853ff229edc8dce253336aabf48aa9eb55",
  "scope": "repo",
  "root": "/repo/scenery",
  "latest": [
    {
      "name": "self-harness",
      "path": ".scenery/harness/self-latest.json",
      "kind": "scenery.harness.self",
      "schema_revision": "sha256:72760c18df9e47694cc73fc6eb62eef47dc7b15f1921b805936a4016018b262b",
      "exists": true
    }
  ],
  "evidence": [
    {
      "kind": "scenery.harness.artifact",
      "schema_revision": "sha256:5fdbd3fbabd171b9226331c8d821c2a59744e7682943593896c332b8ac69eaa8",
      "command": ["go", "test", "-json", "./..."],
      "cwd": "/repo/scenery",
      "started_at": "2026-06-07T20:45:00Z",
      "duration_ms": 1234,
      "exit_code": 1,
      "stdout_tail": "{\"Action\":\"fail\"}",
      "artifacts": [
        {
          "name": "go-tests-stdout",
          "path": ".scenery/harness/artifacts/20260607T204500Z/go-test.jsonl"
        }
      ],
      "repro_command": "cd /repo/scenery && go test -json ./..."
    }
  ]
}
```
