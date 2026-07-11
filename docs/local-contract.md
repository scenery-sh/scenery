# scenery Local Contract

## Edition-2027 vNext contract

An app containing `scenery.scn` opts into the edition-2027 compiler described by [the normative vNext specification set](specs/vnext/SCENERY_LANGUAGE_SPEC.md). A mixed app that also retains `.scenery.json` or `.config.json` must contain `scenery.migration.scn`; compilation fails rather than choosing implicit frontend precedence.

The implemented edition-2027 command surface is:

```text
scenery fmt [--check] [--app-root <path>] [-o human|json]
scenery check [--app-root <path>] -o human|json
scenery compile [--view source|effective|expanded] [--app-root <path>] -o human|json
scenery schema <kind> [-o human|json]
scenery list <kind> [--module <name>] [--view source|effective|expanded] [-o human|json]
scenery get <address> [--view source|effective|expanded] [-o human|json]
scenery explain <address> [--view source|effective|expanded] [-o human|json]
scenery diff --semantic <base-manifest-or-revision> <target-manifest-or-revision> [--view source|effective|expanded] [--exit-code] [-o human|json]
scenery graph <address> [--direction dependencies|dependents|both] [--depth <n>] [--max-resources <n>] [-o human|json]
scenery agent serve [--app-root <path>]
scenery changes plan --changes <file> --base-workspace-revision <rev> --base-contract-revision <rev|null> --out <plan> [-o human|json]
scenery changes apply <plan> --expect-workspace-revision <rev> --expect-contract-revision <rev|null> [--approval-token <file>] [-o human|json]
scenery changes rename <address> <new-name> [--dry-run] [--approval-token <file>] [-o human|json]
scenery migrate init|status|verify|compare [<service>] [--app-root <path>] [-o human|json]
scenery migrate service <service> --generate|--shadow|--retire [--dry-run] [--evidence <class>=<reference>] [-o human|json]
scenery migrate activate <service> --native [--dry-run] [--evidence <class>=<reference>] [-o human|json]
scenery migrate rollback <service> --legacy --activation-receipt <plan-id> [--evidence <class>=<reference>] [-o human|json]
scenery migrate finish [--dry-run] [--evidence <class>=<reference>] [-o human|json]
scenery generate [--target contracts|typescript_client.<name>] [--check] [--app-root <path>] [-o human|json]
scenery build [--target <go-target>] [-o <binary>]
scenery deploy plan <deployment> --out <plan> [-o human|json]
scenery deploy apply <plan> --expect-workspace-revision <rev> --expect-contract-revision <rev> [--approval-token <file>] [-o human|json]
```

`-o json` selects the `scenery.cli.v1` edition-2027 envelope. Existing `--json` commands retain their exact v0 schemas and project the same validated active graph in mixed mode. `--api-version scenery.cli.v0|scenery.cli.v1` is an explicit override; conflicting v0/v1 selectors fail. A v1 envelope always carries `api_version`, `diagnostic_catalog`, `ok`, nullable revision fields, `data`, and ordered `diagnostics`. Exit status is 0 for success, 1 for a false diff/check predicate, 2 for invalid input, 3 for revision conflict or failed precondition, 4 for unavailable capability, 5 for denied permission/approval, and 10 for internal failure.

The implemented profile set is `scenery.compiler-core/v1`, `scenery.go-implementation/v1`, `scenery.http-codec/v1`, `scenery.runtime-http/v1`, `scenery.runtime-durable/v1`, `scenery.events/v1`, `scenery.data/v1`, `scenery.deployment/v1`, `scenery.inspection-core/v1`, `scenery.agent-read/v1`, `scenery.agent-mutation/v1`, `scenery.patches/v1`, `scenery.ui/v1`, `scenery.legacy-bridge/v1`, `scenery.compatibility-core/v1`, and `scenery.typescript-client/v1`. Profile dependencies are resolved into the manifest. Workflow execution and unknown profiles are rejected; they are never approximated.

Edition schemas are enforced recursively against authored blocks before lowering. Unknown nested attributes or blocks, wrong label counts, repeated singleton blocks, and duplicate named children are errors. Workspace revision globs implement only `*`, `?`, and whole-segment `**`; character classes, escapes, embedded `**`, and host glob semantics are rejected.

Local module sources and generated outputs are workspace trust boundaries. `scenery fmt` rejects traversal and every symlink crossing before reading or rewriting a package source. Edition TypeScript `output_root` values must be normalized workspace-relative paths beneath an explicitly declared `workspace.managed_generated_roots` entry. A target-specific generation command validates every declared output root, and top-level `scenery generate` renders and verifies all Go and TypeScript families before one recoverable artifact-set commit; any failure leaves the prior workspace byte-identical.

The compiler retains lossless CST/source maps and exposes source, effective, and expanded graphs. Canonical `contract_revision` excludes implementation and deployment inputs; compilation therefore reports `implementation_revision` as null. `scenery build` selects an exact declared Go target, hashes its complete non-standard package/module/embed/native-input graph, and combines that build-input digest with the resolved target to produce the target-specific `implementation_revision`. The resolved target records the selected Go command and compiler paths and SHA-256 identities; host CGO additionally records the resolved C and C++ compiler paths and identities while ambient compiler, linker, include, library, and pkg-config settings are scrubbed. A fixed non-host target with CGO enabled fails until the separately versioned native-toolchain schema exists. The runtime bundle is written to `.scenery/build/vnext/<target>.json` and copied beside an explicit build output as `<binary>.scenery.runtime-bundle.v1.json`; its schemas are `scenery.go-build-input-manifest.v1` and `scenery.runtime-bundle.v1`.

Resolved `deployment_revision` and artifact/profile revisions are reported independently. Semantic diff, agent reads, and mutation plans use the same canonical graph and compatibility classifications. Change, deployment, activation, rollback, and finish plans are immutable, revision-bound, caller-bound, expiring, single-use transactions with staged validation and receipt output.

Risk-bearing apply commands accept repeatable `--approval-token <file>` values. Each file conforms to `docs/schemas/scenery.approval-token.v1.schema.json`. Scenery verifies its detached Ed25519 signature against the app-local, non-symlink trust store `.scenery/approval-trust.json`, whose exact shape is `docs/schemas/scenery.approval-trust.v1.schema.json`. Key values are raw 32-byte Ed25519 public keys encoded with standard padded or unpadded base64. A signature has the form `ed25519:<key-id>:<base64-signature>`.

The signed bytes are canonical JSON of exactly `plan_id`, `caller`, the sorted unique `risk_scopes`, and UTC `expires_at`; `signature` is excluded. A trusted approval service uses the public `scenery.ApprovalTokenPayload` function to produce those bytes, signs them with Ed25519, and writes the token file. Tokens are accepted only for the exact plan, caller, requested scopes, and unexpired timestamp. Trust stores and private signing keys are operational state and must not be committed; only public keys belong in the trust store.

Source, lockfile, migration, and generated-artifact sets use one recoverable per-workspace transaction. Scenery readers honor its process-fingerprinted lock; a durable journal restores the prior byte-for-byte state after interruption unless the synced receipt proves commit.

Go generation stages contract packages, provider/application adapters, composition, ABI/provider locks, and descriptor coverage before atomically materializing verified bytes. `std.type.unit` is the exact no-input/no-body type and encodes as `{}` in Go (`scenery.Unit`) and TypeScript (`Unit`); user types merely ending in `unit`, `problem`, or `execution_receipt` are not standard types. TypeScript generation provides exact scalar codecs, immutable decoded values, typed outcomes/errors, record constraints and cross-field validation, retry semantics, metadata, and one selection-manifest owner per active operation. Data, durable, schedule, event, HTTP, CLI, page/renderer, and internal-call runtime adapters register through the same generated composition root.

The HTTP effective graph fixes the v1 defaults at 64 KiB request headers, 8 MiB buffered request bodies, 16 MiB decompressed requests, 32 MiB multipart bodies, 16 MiB file parts, 1 MiB non-file parts, 128 parts, and 16 MiB buffered responses. Typed responses may split one outcome across body, header, and cookie mappings; generated Go adapters encode every declared scalar and generated TypeScript clients reconstruct the original camel-cased typed payload. Distinct same-status completion mappings are decoded independently and exactly one must validate; the compiler proves disjointness from observable media types and structural wire shapes, never nominal type or destination names, and rejects mappings where overlap cannot be excluded. Multipart clients encode only declared parts and enforce their exact names, kinds, accepted media, byte limits, filename retention, and multiplicity. Optional absent metadata stays absent. Effective response-cookie defaults are path `/`, empty domain, session expiry (`max_age=0`, no `expires`), `secure=true`, `http_only=true`, and `same_site=lax`. Fetch cannot preserve repeated request-header field lines, so a TypeScript target selecting a repeated list/set request header is rejected with `SCN6316`; use explicit comma encoding only when the scalar codec permits it. Repeated response headers require a Fetch `Headers.getAll(name)` extension, and response cookies require `Headers.getSetCookie()`; a runtime that cannot preserve the declared repetitions fails with `unsupported_runtime` instead of silently collapsing values. `std.authorization.none` is a valid explicit deny-all policy; it does not make a binding anonymous. `dispatch.wait_timeout` is the canonical wait outcome. Stream delivery and `server_sent_events` declarations fail with `unsupported_profile` until a separately negotiated streaming profile exists. Generated TypeScript sets encode and validate canonical JSON element order by UTF-8 bytes across JSON, query, form, and header mappings. Declared transport, admission, and dispatch failures are returned as closed typed failure outcomes; only undeclared/system failures throw, and clients never add an implicit retry. Public `system.internal` responses always use the stable message `contract implementation failure`; the wrapped implementation cause remains available to internal error handling but is never serialized to the caller.

Native `protocol = "cli"` bindings execute directly as `scenery <declared command...>` from the app root. Command and flag names are lower-kebab-case, command paths are unique, and their first segment cannot collide with a built-in Scenery command. `--help`, `scenery completion <words...>`, human output, `-o json`, and exit codes are derived from the binding outcome map. Argument and flag values are decoded with the operation's declared type; required fields must be mapped exactly once. Scenery builds the declared development target, mints the local-developer principal from the OS user, injects only runtime-trusted context fields, runs authorization, and invokes call, wait, or enqueue delivery through the generated composition. Caller input cannot overwrite a context-mapped field.

Edition-2027 fixtures are typed contract resources, not arbitrary SQL. Deployment projection includes only fixtures whose `environments` contain the selected deployment environment. `scenery db seed --env <environment>` uses the same selection and deterministically projects validated PostgreSQL `INSERT`/`ON CONFLICT` statements under `.scenery/vnext/fixtures/`; the ordinary seed ledger and destructive-SQL checks still apply.

Mixed startup validates exactly one active owner for every service, route, lifecycle, durable identity, schedule, schema/event owner, and generated-client surface. While the shared legacy app config exists, `legacy_config` must be its exact normalized regular non-symlink path; the field is omitted only after that config is removed. Every legacy service uses a canonical `./...` package path, its declared Go target, and its declared namespace for stable lowered addresses. `legacy_gateway "default"` explicitly selects the root HTTP gateway used by lowered legacy bindings. Static legacy lowering is advisory until executable behavioral fixtures establish exactness; a resource is never marked `legacy_exact` merely because its shape was discovered. `migrate status` projects receipt-bound drain, fence, and cursor state, external identities and aliases, deployed-client gates, CLI protocol dependencies, rollback safety, and blocking diagnostics for every construct; missing required operational evidence makes active shadow-cutover readiness false. A source-declared `native_service` is already retired, has no rollback owner, and remains operationally ready without machine-local activation receipts, but its package must contain no undeclared legacy model, page, or reference to the package-init builder symbols `durable.NewTask` or `cron.NewJob`; non-registering APIs from those packages remain valid. Those runtime identities must be native `.scn` resources. Explicit `legacy_go_v0` handler adapters are allowed only while the migration graph reports them and block `migrate finish`. `migrate finish` additionally requires every service retired to native, no compatibility adapter or incomplete construct, explicit evidence clearing v0 CLI and legacy generated-client consumers plus every stateful cutover class, and no activation receipt that still authorizes rollback. Operational receipt state and evidence are included in the finish plan identity and rechecked at apply.

Generated durable executions may set `external_name` to preserve an existing durable-store task namespace; the name must be unique per engine. The execution `revision` is the persisted input/handler ABI revision. Reusing an external name with an incompatible serialized input requires a new revision and an explicit drain or migration of active jobs; startup reconciliation fails closed when active jobs remain at a different revision.

This document freezes the local developer and agent-facing contract for scenery v0.

The goal is to make scenery deterministic and inspectable:
- app shape is explicit
- CLI grammar is explicit
- machine-readable JSON outputs have versioned schemas
- inspect commands are the API; generated files are cache
- app roots, dev runtimes, and capabilities are the user-facing model; substrate paths, ports, backing services, and internal session IDs are debug details

If implementation and this document disagree, treat that as a bug.

## Status

Implemented now. This list describes what the CLI can do today; it is not the
same as the stable v0 support surface.

- `.scenery.json` app config, with `.config.json` accepted as an alias
- `scenery up --json`
- `scenery worker`
- `scenery worker durable`
- `scenery worker durable jobs ... --json`
- `scenery worker durable token create --json`
- `scenery version --json`
- `scenery help --json`
- `scenery system toolchain list|sync|verify|path`
- `scenery doctor --json`
- `scenery check --json`
- `scenery generate`
- `scenery generate client`
- `scenery generate sqlc`
- `scenery db shell`
- `scenery db apply`
- `scenery db seed`
- `scenery db setup`
- `scenery db reset`
- `scenery db drop`
- `scenery db snapshot create|restore`
- `scenery db diff --generated`
- `scenery worktree create|list|remove`
- `scenery task list|inspect|run|graph`
- `scenery task run <name>`
- `scenery task run <domain>:<name>`
- `scenery validate list|inspect|graph|changed`
- `scenery validate <profile> --json`
- `scenery harness --json`
- `scenery harness self --json`
- `scenery harness ui --json`
- `scenery traces clear --json`
- `scenery inspect app --json`
- `scenery inspect routes --json`
- `scenery inspect services --json`
- `scenery inspect endpoints --json`
- `scenery inspect models --json`
- `scenery inspect views --json`
- `scenery inspect build --json`
- `scenery inspect paths --json`
- `scenery inspect generators --json`
- `scenery inspect durable --json`
- `scenery inspect storage --json`
- `scenery inspect validation --json`
- `scenery storage status|webui|ls|stat|put|get|rm|cleanup --json`
- `scenery traces list --json`
- `scenery metrics list --json`
- `scenery inspect docs --json`
- `scenery logs --jsonl`

Reserved by contract, implementation pending:
- repo-local runtime and state manifests beyond the command JSON surfaces above

Stable v0 surface:
- `.scenery.json` app config, with `.config.json` accepted as an alias
- `scenery build`
- `scenery version --json`
- `scenery help --json`
- `scenery check --json`
- `scenery inspect app|routes|services|endpoints|build|paths|docs --json`
- `scenery logs --jsonl`
- `scenery test`
- `scenery generate client`
- typed/raw HTTP endpoints
- auth handler
- service struct initialization and shutdown
- private/internal calls
- secrets from process env and local `.env`
- basic runtime logs and trace emission

Dev-only or beta surface:
- `scenery up`
- Postgres-only data platform: `dev.services`, managed app database naming, service schemas, `scenery` schema, and DB lifecycle commands
- `scenery db shell`
- `scenery db apply`
- `scenery db seed`
- `scenery db setup`
- `scenery db reset`
- `scenery db drop`
- `scenery db snapshot create|restore`
- `scenery worktree create|list|remove`
- `scenery generate`
- `scenery task list|inspect|run|graph`
- `scenery task run <name>`
- `scenery task run <domain>:<name>`
- `scenery validate`
- `scenery inspect validation --json`
- `scenery traces list|metrics --json`
- `scenery inspect generators --json`
- `scenery inspect durable --json`
- `scenery inspect storage --json`
- `scenery storage status|webui|ls|stat|put|get|rm|cleanup --json`
- `scenery system toolchain list|sync|verify|path`
- `scenery doctor --json`
- `scenery system edge install|trust|status|restart|uninstall|dns|privileged --json`
- `scenery worker`
- `scenery worker durable`
- `scenery worker durable jobs ... --json`
- `scenery worker durable token create --json`
- `scenery traces clear --json`
- `scenery harness ui --json`
- dashboard and API Explorer
- local HTTPS edge and frontend routing
- trust-store installation
- native local observability capabilities, backed today by Victoria substrate and managed binary downloads
- cron UI
- `scenery.sh/durable` typed task declarations, startup DB reconciliation into the app Postgres database's `scenery` schema, queued job starts, interval schedules, retrying local Go handler execution, durable step/signal helpers, authenticated durable worker lease/heartbeat/complete/fail HTTP endpoints, durable job admin, and `scenery inspect durable --json` while the Postgres durable execution runtime is implemented under ExecPlan 0097
- `scenery.sh/storage`, app config storage declarations, `scenery inspect storage --json`, and `scenery storage ... --json` while the storage runtime boundary and generated browser routes mature
- `scenery.sh/model` and `scenery.sh/page` static IR vocabulary, `//scenery:model`, `//scenery:page`, `model.Table`, `model.ExistingTable`, `model.Generate|Disable|Override` CRUD action policy, `scenery inspect models|views --json`, generated model endpoint markers, and beta generated data/web packages while the model/view surface matures
- `scenery generate data --dry-run --json`, generated desired schema files under `.scenery/gen/db/<service>/schema.hcl`, generated seed files under `.scenery/gen/db/<service>/seed.sql`, generated frontend packages under `.scenery/gen/web/<frontend>/`, `scenery db diff --generated --json`, and `scenery check` model-schema drift diagnostics
- generated model CRUD endpoints/stores in the transient build workspace. These endpoints appear in `scenery inspect endpoints|routes --json` with `"generated": true`; generated CRUD access defaults to `auth` for every action, default generated CRUD route bases are service-scoped as `/<service>/<table>` and collision-checked against reserved route prefixes (`/runtime`, `/__scenery`, `/api`, `/sync`) plus handwritten and generated app routes, while `model.Table(...)` remains the physical table name for generated Scenery-owned tables. `model.ExistingTable(schema, table)` binds an entity to an existing physical table, exposes that source through `scenery inspect models --json`, skips generated schema/seed ownership artifacts, allows generated list/get only, and rejects generated create/update/delete or `model.Seed(...)` rows. Generated list endpoints accept `limit` and `offset` query parameters, default to `limit=100`, reject `limit < 1`, `limit > 500`, and negative offsets, and always emit bounded `LIMIT/OFFSET` SQL after tenant filtering. Generated create/patch payloads accept both generated response field names such as `CreatedAt` and DB-column JSON names such as `created_at`, so stored `time.Time` fields use Go's normal RFC3339 JSON parsing and reject malformed timestamps as invalid JSON. Generated stores use `database/sql` against the configured managed Postgres database URL env. App services can use `scenery.sh/db.Get(ctx)` or `scenery.sh/db.MustGet(ctx)` for the same default-database selection and a process-shared `*sql.DB`. Entities with a convention `TenantID`/`tenant_id` field additionally derive the active tenant from standard auth, scope list/get/update/delete SQL by `tenant_id`, and inject `tenant_id` on create so tenant IDs are not client-writable in generated create/patch payloads. Generated tenant fields support `string`, named string types, and `github.com/google/uuid.UUID`; unsupported tenant field types fail parse/check with an explicit diagnostic.
- migration compatibility for older app shapes

Compatibility posture:
- scenery-native syntax and imports are the stable API.
- Non-scenery directives/imports are not part of the v0 API.

## App Config

The preferred app config filename is `.scenery.json`. `.config.json` is accepted as an alias when `.scenery.json` is absent in the same directory.

Schema:
- [scenery.config.v1.schema.json](schemas/scenery.config.v1.schema.json)

Current shape:

```json
{
  "name": "myapp",
  "id": "myapp-dev",
  "frontends": {
    "app": {
      "root": "apps/app"
    }
  },
  "deploy": {
    "domain": "app.example.com",
    "root": "app"
  },
  "watch": {
    "ignore": ["reference/"]
  },
  "generators": {
    "clients": [
      {
        "id": "web",
        "kind": "typescript-client",
        "target": "myapp-dev",
        "output": "apps/web/src/scenery-client.ts"
      }
    ],
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
    "cell_id": "myapp",
    "share": "worktree",
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
  "tasks": {
    "harness": {
      "steps": ["check", "test:go"]
    },
    "ui-harness": {
      "cwd": "apps/web",
      "run": "bun run ui-harness"
    }
  },
  "validation": {
    "default": "quick",
    "profiles": {
      "quick": {
        "description": "Fast agent handoff gate.",
        "cost": "low",
        "steps": ["harness:core", "task:harness"]
      },
      "frontend": {
        "description": "Frontend validation.",
        "cost": "medium",
        "paths": ["apps/web/**"],
        "steps": ["task:ui-harness"],
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
    "database_url_env": "DatabaseURL",
    "jwt_secret_env": "JWTSecret",
    "refresh_cookie_name": "onlv_refresh",
    "auto_bootstrap_database": true,
    "google_oauth": {
      "enabled": false,
      "client_id_env": "GoogleOAuthClientID",
      "client_secret_env": "GoogleOAuthClientSecret",
      "allowed_scopes": ["https://www.googleapis.com/auth/gmail.modify"],
      "token_cipher_key_env": "AuthTokenCipherKey"
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

Rules:
- App root discovery walks from the start directory upward. In each directory, `.scenery.json` is checked first and wins when both files exist.
- `.config.json` is considered a Scenery app config only when it is valid JSON with at least one recognized top-level Scenery config key. This prevents unrelated tool-level `.config.json` files inside app subdirectories from shadowing a parent Scenery app root.
- JSON outputs such as `scenery inspect app --json`, build manifests, harness results, and generator records report the actual config file path/input used.
- `name` or `id` must be non-empty.
- If `name` is empty, scenery falls back to `id`.
- App identity for runtime environment, dashboard routes, local logs, browser harness routes, and local observability is `id` when present, otherwise `name`. `name` remains the display name and source/build package identity.
- `frontends` is optional.
- `build.go_flags` is an optional array of literal Go argv entries used for Scenery-owned app compilation. Values are not shell-split; write one argument per item, for example `["-tags=roofmapnet_native"]`. Scenery passes these flags to generated app `go build` invocations and generated-workspace `scenery test` `go test` invocations, while process `GOFLAGS` still applies for local one-off overrides. The normalized flag list participates in the build fingerprint/cache key.
- `watch.ignore` is an optional array of app-root-relative exclusion patterns for `scenery up`. Directory patterns such as `reference/` skip that subtree during watcher setup and rebuild fingerprint scans while leaving Git tracking untouched. `watch.ignore` is exclusion-only; use `.gitignore` for Git behavior.
- `auth` is optional. When `auth.enabled` is true, scenery registers the built-in standard auth handler and standard auth endpoints. Google OAuth endpoints are registered only when `auth.google_oauth.enabled` is true.
- `observability` is optional.
- Unknown fields are rejected. Runtime diagnostics include the config file path, active config filename, and JSON field path, for example `/repo/app/.config.json: unknown .config.json field "frontends.app.extra"`.
- The removed `proxy` app config has no compatibility behavior. Use `frontends` for frontend roots and dev runtime routes for local URLs.
- `dev.routing` controls browser-facing local dev routing. `dev.routing.mode` accepts `path` or `host`; the default is `path`. Path mode assigns one stable unprivileged localhost base URL per app root/session, optionally constrained by `dev.routing.port`, `dev.routing.port_start`, and `dev.routing.port_end`. Under that base URL, `/` serves the Scenery route index, `/api/` proxies the app API, `/consolenext/` serves the Scenery dashboard, `/<frontend>/` proxies configured frontends, and `/runtime/` is reserved for Scenery-owned runtime/control surfaces. `scenery check` rejects path-mode frontend names that normalize to reserved routes such as `api`, `dashboard`, `runtime`, `sync`, `root`, or `__scenery`, and rejects duplicate normalized frontend route names.
- Agent dev-runtime manifests include `route_namespace`, the app-derived local browser namespace used by routed URLs. `route_namespace.workspace` comes from app identity. `route_namespace.base_domain` defaults to `local.dev`.
- Agent dev-runtime manifests include `route_manifest`, with `mode`, `base_url`, `worktree`, route records, and an optional path-mode port lease. Existing string-only `routes` remain for compatibility; in path mode they contain the browser-facing URLs derived from the manifest, for example `http://localhost:4001/api/` and `http://localhost:4001/web/`.
- `frontends` is a map keyed by frontend name. `root` defaults to `apps/<name>`; `upstream` is optional but ignored by agent dev unless that frontend also sets `allow_shared_upstream: true`. With an active agent, `scenery up` prefers to start supported Vite/Astro frontends on hidden loopback ports, inject routed API/base-path URLs into their process environment, register those hidden ports as runtime backends, and expose `/<frontend>/` under the runtime base URL in path mode. Managed Vite/Astro frontends receive allowed-host controls for the current route host. If a managed frontend process exits while the dev supervisor is still running, Scenery restarts that frontend on a fresh hidden loopback port and re-registers the session backend/process metadata. Managed frontend routes serve the frontend shell for HTML SPA deep links, while `/runtime/*`, `/__scenery/*`, `/api/*`, `/sync/*`, and concrete asset paths are not history-fallback routes. `SCENERY_FRONTEND_<NAME>_ADDR` still overrides scenery-owned frontend startup for manual debugging.
- `storage` is a Scenery-owned app capability config. App declarations accept `kind: "local"` (an empty kind also defaults to `local`), a Scenery-owned directory tree with atomic temp-file+rename writes, checked fsync on objects and parent directories, and sidecar object metadata; app code depends on `scenery.sh/storage`, not on the backend. `cell_id` is optional and defaults to a stable app identity; it must not include a worktree path, branch, session ID, or process ID. `share` defaults to `worktree`, meaning Scenery resolves shared storage-cell paths under the agent storage root for the same app/storage cell. `default` names the default store, and `stores.<name>` accepts `kind`, `access`, `tenant_scoped`, and `max_object_bytes`. Store `access` defaults to `auth`; `private` stores are available to app/runtime helpers but are not externally reachable through the reserved storage HTTP routes. Store names and cell IDs use identifier-safe strings. Unknown storage fields and unsupported storage kinds fail config validation. (ZeroFS was removed in plan 0094; offsite durability is an operator concern — replicate the store root to S3 with `rclone`/`restic`, see `docs/app-development-cookbook.md`.)
- App processes, workers, setup commands, and app-local code tasks receive `SCENERY_STORAGE_CONFIG` and `SCENERY_STORAGE_CELL_ID` when storage is configured. `SCENERY_STORAGE_CONFIG` is consumed by `scenery.sh/storage` and contains store capability metadata, not raw object-store credentials. Standalone `scenery worker` runtimes require an explicit operator-provided runtime config and fail closed when storage is declared and the config is missing or empty; each store uses either `kind: "local"` with an absolute `root`, or `kind: "proxy"` with a `proxy_socket`. Agent-backed dev sessions use a session-local Scenery storage proxy socket in that config so app code receives capability metadata rather than direct object-root paths; the proxy serves the local backend from the shared storage-cell object directories. Non-session CLI/task validation paths use Scenery-owned local storage-cell roots directly. The app runtime also uses this env to mount reserved storage object routes when storage is configured.
- Stores with `tenant_scoped: true` physically namespace object keys under a Scenery-owned tenant prefix while keeping caller-visible keys unchanged in `Put`, `Head`, `Get`, `List`, `Delete`, and `DeletePrefix` results. Authenticated external storage routes derive the tenant from standard auth data. Private/internal tenant-scoped calls must set `storage.WithTenantID(ctx, tenantID)` or run inside a standard-auth request context; missing tenant context fails closed.
- Storage `ContentType` and user metadata are durable object metadata. The local store persists that metadata in Scenery-owned sidecars under `__scenery/metadata/`, hides sidecars from `List`, and removes sidecars on `Delete` and `DeletePrefix`. Reserved HTTP/proxy routes carry metadata through `X-Scenery-Storage-Meta-*` headers. Offsite replication must copy the sidecars alongside the object files.
- Reserved storage HTTP routes are app data-plane runtime routes mounted only when `SCENERY_STORAGE_CONFIG` is present. They are production-supported under the same operator-proxy storage runtime contract as `scenery.sh/storage`. `GET /__scenery/storage/<store>?prefix=<prefix>&delimiter=/&cursor=<cursor>&limit=<n>` lists objects. `PUT /__scenery/storage/<store>/<key>` uploads a streamed object and returns the object metadata as JSON. `GET` and `HEAD /__scenery/storage/<store>/<key>` download object bytes with `Content-Length`, `Content-Type`, `ETag`, `Last-Modified`, `Accept-Ranges`, and byte-range support. `DELETE /__scenery/storage/<store>/<key>` deletes one object, and `DELETE /__scenery/storage/<store>/<prefix>?recursive=1` deletes by prefix. Public routes enforce the store access policy: `auth` requires the app auth handler and `private` returns permission denied on the external HTTP surface. The same reserved storage routes are also registered on the runtime private route table for Scenery-internal, non-external storage work.
- `dev.services` is a beta local-development config surface for scenery-owned Postgres schemas in one app database. If the app-level `DATABASE_URL` is present in the app/setup environment, Scenery treats that `postgres://` or `postgresql://` URL as external and manages no server or database; otherwise `scenery up` ensures one machine-wide Docker-backed shared Postgres server and creates one database per app root/worktree with one schema per service plus `scenery`. Managed Postgres database names are derived from app ID and a short hash of the absolute app root. Storage no longer needs a `dev.services` entry: declaring `storage.stores` is sufficient and `scenery up` serves those stores from the local backend.
- App processes, setup commands, DB setup, and workers receive `DATABASE_URL` plus per-service `<SERVICE>_DATABASE_URL` values for the app database. `SCENERY_DATABASE_JSON` describes the app database, URL, source (`managed` or `external`), and service schemas. Headless runtimes fail closed when database services are configured and no explicit `DATABASE_URL` is present.
- `scenery up` prepares declared local DB setup before the app process starts. When app config declares `database.apply` or service-local seed files are discovered, the supervisor runs the same split lifecycle as `scenery db setup`: apply first, then seed. It passes the same managed database URL env values that the app child receives, so setup targets the dev-runtime database. Successful setup is fingerprinted from `database.apply` config and seed file hashes; ordinary rebuilds skip setup until those inputs change. Apps can set `database.seed.enabled: false` to opt out of seed discovery when local seed files target a database dialect or lifecycle outside Scenery-managed services.
- `dev.setup` is an optional beta list of shell commands that `scenery up` runs from the app root after managed dev services and the DB setup lifecycle are prepared, but before the app process starts. Setup commands receive the same managed Postgres database URL env values as the app child, so target apps can keep existing app-local setup during migration.
- `generators.clients` is a beta lifecycle config for generated TypeScript clients. `kind` defaults to `typescript-client`, `lang` defaults to TypeScript, and `output` is required. `scenery generate client` uses these entries when no explicit `--output` is passed.
- Generated TypeScript clients expose `WithMeta` methods that include response headers, status, `Response`, and parsed `txid` metadata from `X-Txid`/`X-TXID`. Apps that observe committed writes asynchronously should treat the API response and later observation as separate phases: an HTTP success with `X-Txid` means the mutation committed, while `observeAPIResponseTxid(...)` reports later observer failures as `SyncObservationError` with `kind: "sync_observation_failure"`, `mutation_committed: true`, app/session/API/sync context, txid, and observer error details.
- `generators.sqlc` is a beta lifecycle config for SQLC generation. `provider` may be empty or `sqlc`; `config` defaults to `sqlc.yaml`; schema files listed in `sqlc.yaml` are treated as inputs. Explicit `atlas_source` schemas are refreshed only when an explicit `dev_url` is configured; `postgres://`, `postgresql://`, and `docker://` Atlas dev URLs pass through to Atlas unchanged. SQLC schema blocks whose schema path belongs to a configured database service must use a Postgres SQLC engine (`postgresql`/`postgres`). SQLC generation is a generated-source lifecycle and must not apply database schema or seed data.
- Static model data generation is a beta read-only data lifecycle. `scenery generate data --dry-run --json` parses `//scenery:model` IR and writes desired Atlas HCL to disposable generated files at `.scenery/gen/db/<service>/schema.hcl`; generated model tables live in the app-owned schema derived from `<service>` rather than `public`, and generated Atlas resource labels are schema-qualified (`table "<service>" "<table>"`, `enum "<service>" "<enum>"`) to avoid cross-schema label collisions in apps with existing multi-schema HCL. `model.ExistingTable(schema, table)` marks an entity as an existing physical table source: `scenery inspect models --json` reports `source.kind`, `source.schema`, `source.table`, and `source.qualified_table`; generated schema and seed output skips that entity; generated source metadata and read-only stores use the explicit qualified table; and generated create/update/delete actions plus `model.Seed(...)` rows fail closed. Typed `model.Seed(...)` rows on generated-source entities write deterministic idempotent upsert SQL to `.scenery/gen/db/<service>/seed.sql` using the same schema-qualified table, and generated CRUD stores use that table through one shared `database/sql` connection for the configured app database URL env or Scenery's managed database env. Generated CRUD endpoints default to `auth` for every action; generated list endpoints are capped to 100 rows by default and accept validated `limit`/`offset` query parameters up to a maximum limit of 500; generated create/patch payloads accept response field names and DB-column JSON names, so `time.Time` fields round-trip RFC3339 JSON timestamps or fail with a field-scoped JSON decode error; tenant-shaped entities add standard-auth tenant scoping on top of that access requirement and support `string`, named string, or `github.com/google/uuid.UUID` tenant fields. `scenery inspect views --json` exposes each collection view's generated page-record projection as model/view IR: source row type, projection record type, projected fields, static column display hints, static filters, and static sorts. Static page filters are field/operator/literal rules limited to `eq`, `neq`, `is_null`, and `is_not_null`; static page sorts are field plus `asc` or `desc`; display hints are `text`, `datetime`, or `badge`. When a configured frontend and `//scenery:page` collection view are present, the same command also writes a beta hidden TypeScript package under `.scenery/gen/web/<frontend>/` with row/create/patch types, page projection records in `projections.ts`, entity source definitions that expose `schema`, `table`, and `qualifiedTable`, collection descriptors/materializers with static query/display metadata, runtime adapter factories over explicit row sources, default page components, route/default page factories, route registration helpers, and slot type assertions against the frontend's component files. Frontends consume the package through app-owned aliases such as `@scenery/generated` and provide the `@scenery/layout-kit` contract; ordinary page mounting imports generated pages or routes from that alias instead of deep `.scenery/gen` paths. `--dry-run` means no database mutation. `scenery db diff --generated --json` compares desired schemas with app-owned `SERVICE/db/schema.hcl` files and emits `scenery.db.generated_diff.v1`. `scenery check --json` reports `model-schema` diagnostics when the app-owned schema is missing or drifts from the generated desired schema. Apps without model directives have no generated model data work.
- `database.apply` is a beta DB lifecycle escape hatch with an explicit shell `command`, optional `cwd`, and string `env` overlay. The accepted split lifecycle moves database mutation to `scenery db apply`; SQLC refresh stays under `scenery generate sqlc`.
- Service-local `SERVICE/db/seed.sql` and generated `.scenery/gen/db/<service>/seed.sql` files are initial data. They are not Atlas schema input or SQLC input. The accepted lifecycle applies seed data through `scenery db seed`; generated model seed files are materialized before seed discovery. The first implementation fails closed on changed previously-applied seed files and obviously destructive seed SQL rather than adding force or reseed escape hatches.
- `tasks` is a beta thin repo-task layer. Each configured task can define either `run` or `steps`, plus optional `cwd` and string `env`. `run` uses the platform shell from the app root or task cwd. `steps` currently accepts `task:<name>`, `task:<domain>:<name>`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`.
- Code tasks are beta app-local targets under `<domain>/tasks/`. Targets use `<domain>:<name>`, and both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`. `scenery task list`, `scenery task inspect`, and `scenery task run <domain>:<name> [-- task args...]` discover and execute them without requiring the app model to parse cleanly.
- `validation` is a beta app-owned quality-gate layer. It has `default` and `profiles`; each profile can define `description`, `cost` (`low`, `medium`, or `high`), `paths`, `steps`, string `env`, and advisory `artifacts`. Profile names use the configured-task name rule and cannot contain `:`.
- Validation profile steps are not shell. They accept `profile:<name>`, `task:<name>`, `task:<domain>:<name>`, `harness:core`, `harness:ui`, `harness`, `check`, `test`, `test:go`, `generate`, `generate:client`, `generate:sqlc`, `db:apply`, `db:seed`, and `db:setup`. Shell commands must live behind configured `tasks.<name>.run`.
- `scenery db branch` and `scenery db path` are removed. Worktree isolation uses per-worktree managed Postgres database names, and `scenery worktree create` only creates the Git worktree. Database save/restore uses `scenery db snapshot create|restore`.
- Declaring `storage.stores` is sufficient for `scenery up`; there is no managed storage process. Scenery creates the shared storage-cell object directories under the agent storage root (`<agent-home>/agent/storage/<cell-id>/objects/<store>/`) and, in agent-backed dev sessions, starts an in-process storage proxy over a session-local Unix socket that serves those directories through the `scenery.sh/storage` capability boundary. The proxy backs each store with the local filesystem backend (atomic temp-file+rename writes, checked object and directory fsync, sidecar metadata under `__scenery/metadata/`). App code receives capability metadata through `SCENERY_STORAGE_CONFIG` and never a raw object-root path. Objects are plain files on disk: on-disk bytes track object bytes plus small sidecars, with no cache layer or write amplification. There is no managed toolchain artifact, encryption password, 9P socket, Web UI, substrate record, or lease for storage. Durability across a crash comes from fsync; offsite durability is an operator concern — replicate the storage-cell object directories (objects plus sidecars) to S3 with `rclone`/`restic`, as described in `docs/app-development-cookbook.md`.
- Standard auth uses the `scenery.sh/auth` top surface and stores DB-backed auth state in the app Postgres database's `scenery` schema.
- Standard auth owns its framework tenant tables, including `scenery.scenery_auth_tenants`. Apps do not need an app-local `tenants` service, package, or table for standard auth; app-local tenant services are product-domain APIs and schema only.
- Standard auth registers `/auth/signup/email`, `/auth/login/email`, `/auth/refresh`, `/auth/logout`, `/auth/me`, organization/invite/impersonation endpoints, and local `/users/dev-bootstrap`. When `auth.google_oauth.enabled` is true, it also registers raw `GET /auth/google/start`, raw `GET /auth/google/callback`, typed `POST /auth/google/connect/start`, raw `GET /auth/google/connect/callback`, typed `GET /auth/google/connection`, and typed `POST /auth/google/connection/disconnect`.
- Standard auth endpoints appear in `scenery inspect routes|services|endpoints --json` and in generated TypeScript clients. Disabled Google OAuth endpoints are absent from inspect output and generated clients. When Google OAuth is enabled but the configured client ID or secret env is missing, `scenery check --json` returns an `auth` warning. `auth.google_oauth.allowed_scopes` declares the Google API scopes an app may request through the connection flow. `POST /auth/google/connect/start` returns a Google authorize URL whose `redirect_uri` is the shared `/auth/google/callback`; the callback dispatches connection states by OAuth state purpose so apps can reuse the sign-in redirect URI registered in Google Cloud. `auth.google_oauth.token_cipher_key_env` defaults to `AuthTokenCipherKey`, a base64 32-byte AES-GCM key used to encrypt stored Google refresh/access tokens; local development derives a dev key from the local JWT secret when this env is absent.
- `auth.auto_bootstrap_database` applies the first standard-auth schema bootstrap at runtime. It is useful for local fixtures; production deployments should manage schema changes deliberately.
- Generated binaries accept `SCENERY_ROLE=all|api|worker`. `scenery up` uses the default combined role. `scenery worker` uses `worker`.
- Packages that declare `scenery.sh/durable` tasks with `durable.NewTask` are imported into the generated main so their declarations register at startup; `durable.TaskConfig.Service` is required for static literal configs. Runtime startup requires `DATABASE_URL` and reconciles declarations into the app Postgres database's `scenery` schema: `durable_jobs` is keyed by `service` with inline lease columns, and satellite tables store tasks, events, steps, signals, schedules, and worker tokens. `durable.Start` writes queued JSON-input jobs into the shared durable store. Generated `all` and `worker` roles run a local durable worker loop for registered Go handlers; failed attempts requeue until `MaxAttempts` using `Retry.InitialInterval`, `Retry.MaxInterval`, and `Retry.BackoffFactor`; `api` does not execute durable jobs. `durable.Schedule` records an interval schedule that API/all roles materialize into queued jobs. `durable.Step` persists local handler step results by job/key and reuses succeeded results, while `durable.Signal` appends a JSON signal row and event for a run. The API role exposes `/__scenery/durable/v1/<service>/lease`, `/__scenery/durable/v1/<service>/jobs/<job>/heartbeat`, `/complete`, and `/fail` as JSON POST endpoints for remote workers. Those endpoints require bearer tokens stored only as hashes and fence heartbeat/complete/fail with `worker_id` plus `lease_id`. `scenery inspect durable --json` emits `scenery.inspect.durable.v2` with durable task declarations, service schemas, and redacted app database metadata.
- Cron jobs can set `cron.JobConfig.OverlapPolicy`, `CatchupWindow`, and `PauseOnFailure`. Defaults are overlap `skip`, catchup window `1m`, and pause-on-failure `false`.

## CLI Grammar

Current implemented grammar:

```text
scenery up [--port <n>] [--listen <addr>] [--app-root <path>] [--claim-aliases] [-v|--verbose] [--json] [--detach] [--wait ready|registered]
scenery logs --follow [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--jsonl|--json]
scenery logs query [--app-root <path>] --query <logsql> [--since <duration>] [--start <time>] [--end <time>] [--limit <n>] [--timeout <duration>] [--fields <csv>] [--json|--jsonl]
scenery logs tail [--app-root <path>] --query <logsql> [--since <duration>] [--timeout <duration>] [--fields <csv>] [--jsonl]
scenery console [--app-root <path>] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>]
scenery system agent [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
scenery system agent restart [--socket <path>] [--router-listen <addr>] [--router-tls|--router-http] [--trust] [--json]
scenery system edge install|trust|status|restart|uninstall|dns|privileged [--json]
scenery help <command>
scenery help all
scenery help --json
scenery ps [--json] [--app-root <path>] [--watch]
scenery down [--app-root <path>] [--db] [--state] [--all] [--json]
scenery prune --older-than <duration> [--app-root <path>] [--json]
scenery worker [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker durable --endpoint <url> --token <token> [--service <name>]... [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker durable jobs list|inspect|cancel|retry [job-id] --service <name> [--app-root <path>] --json
scenery worker durable token create --service <name> [--name <name>] [--id <id>] [--app-root <path>] --json
scenery version [--json]
scenery upgrade [--version latest|vX.Y.Z] [--target <path>] [--toolchain installed|all|none] [--force] [--dry-run] [--json]
scenery deploy enable [--app-root <path>] [--json]
scenery deploy disable [--app-root <path>] [--json]
scenery deploy status [--json]
scenery deploy setup [--acme-email <email>] [--acme-ca production|staging] [--json]
scenery deploy resume [--json]
scenery deploy teardown [--json]
scenery system toolchain list [--json] [--include-source-locks] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
scenery system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
scenery doctor [--app-root <path>] [--json]
scenery build [--app-root <path>] [-o <path>]
scenery check [--app-root <path>] [--json]
scenery db list [--app-root <path>] [--json]
scenery db shell [service] [--app-root <path>] [psql args...]
scenery db apply [--app-root <path>] [--json]
scenery db seed [--app-root <path>] [--env <name>] [--dry-run] [--json]
scenery db setup [--app-root <path>] [--json]
scenery db reset [service] [--app-root <path>] [--yes]
scenery db drop [--app-root <path>] [--yes]
scenery db snapshot create|restore <name> [--app-root <path>] [--yes]
scenery db server status|start|stop|logs [--json] [--yes]
scenery generate [--app-root <path>] [--dry-run] [--json]
scenery generate client [<app-id>] [--lang typescript] [--output <path>] [--app-root <path>] [--dry-run] [--json]
scenery generate sqlc [--app-root <path>] [--dry-run] [--json]
scenery storage status [--app-root <path>] --json
scenery storage webui [--app-root <path>] --json
scenery storage ls <store> [--prefix <prefix>] [--cursor <cursor>] [--limit <n>] [--app-root <path>] --json
scenery storage stat <store> <key> [--app-root <path>] --json
scenery storage put <store> <key> <file> [--app-root <path>] --json
scenery storage get <store> <key> --output <file> [--app-root <path>] --json
scenery storage rm <store> <key> [--recursive] [--app-root <path>] --json
scenery storage cleanup [--yes] [--app-root <path>] --json
scenery symphony auto --on|--off [--app-root <path>]
scenery task list [--app-root <path>] [--json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery task graph --json [--app-root <path>]
scenery validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery validate list [--app-root <path>] [--json]
scenery validate inspect <profile> [--app-root <path>] [--json]
scenery validate graph [<profile>] [--app-root <path>] --json
scenery validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery harness [--app-root <path>] [--json] [--write] [--with-validation[=<profile>]]
scenery harness self [--repo-root <path>] [--summary|--json|--json=summary|--json=full] [--write] [--quick|--race|--release] [--fresh-tests]
scenery harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]
scenery inspect app|routes|services|endpoints|models|views|build|paths|generators|durable|storage|observability|validation --json [--app-root <path>]
scenery inspect docs --json [--repo-root <path>]
scenery inspect harness [artifact <name>|diagnostics --severity error|warning|timing --top <n>] --json [--app-root <path>] [--repo-root <path>]
scenery traces list --json [--app-root <path>] [--service <name>] [--endpoint <name>] [--trace-id <id>] [--status ok|error] [--min-duration-ms <n>] [--since <duration>] [--limit <n>] [--slowest]
scenery metrics list --json [--app-root <path>] [--service <name>] [--endpoint <name>] [--status ok|error] [--since <duration>] [--limit <n>]
scenery metrics query --json [--app-root <path>] --promql <query> [--instant] [--since <duration>] [--start <time>] [--end <time>] [--step <duration>] [--timeout <duration>] [--limit <n>]
scenery metrics labels --json [--app-root <path>] [--match <selector>] [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
scenery metrics series --json [--app-root <path>] --match <selector> [--since <duration>] [--start <time>] [--end <time>] [--timeout <duration>] [--limit <n>]
scenery traces clear --json [--app-root <path>]
scenery logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [-f|--follow] [--jsonl|--json]
scenery test [--app-root <path>] [go test flags/packages...]
scenery generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
```

Implemented beta/dev helper grammar:

```text
scenery db list [--app-root <path>] [--json]
scenery db shell [service] [--app-root <path>] [psql args...]
scenery db server status|start|stop|logs [--json] [--yes]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [--json]
scenery worktree list [--app-root <path>] [--json]
scenery worktree remove <name> [--app-root <path>] [--db] [--json]
```

`scenery db list --json` reports the app Postgres database as `scenery.db.list.v3`; the record includes the database name, redacted URL, source (`managed` or `external`), optional size, and the configured service schemas. `scenery db shell [service]` opens `psql` on the app database; a service argument pins `search_path` to `<service_schema>,scenery`. `scenery db snapshot create|restore` stores Postgres custom dumps under `.scenery/db/snapshots/<name>/`; snapshots use `pg_dump -Fc` and `pg_restore --clean --if-exists`, and restore requires `--yes`. `scenery db reset [service]` resets one service schema with `ResetSchema`; without a service it resets the managed app database and requires `--yes`. `scenery db drop` drops the managed app database. Destructive reset/drop operations refuse external DSNs. `scenery db server status|start|stop|logs` manages only the shared local Postgres server and reports `scenery.db.server.status.v1`. `scenery db apply` runs only `database.apply.command` and does not run seed files or SQLC generation.

`scenery down --db` drops the app root's managed app database even when no live dev runtime is registered. Runtime-stop output still reports when no runtime was found. It refuses external DSNs. `scenery down --state` removes only the app root's local runtime state when a runtime record exists.

`scenery worktree create <name> --json` runs `git worktree add -b <name>` next to the current app root and emits `scenery.worktree.create.v1`. `scenery worktree list --json` emits `scenery.worktree.list.v1` from `git worktree list --porcelain`. `scenery worktree remove <name> --db --json` first resolves the target from `git worktree list --porcelain`, then removes local `.scenery` state before `git worktree remove`, and emits `scenery.worktree.remove.v1`.

DB lifecycle split:
- `scenery db apply` mutates schema or app-owned database setup only. It does not run seed files or SQLC generation.
- `scenery db seed` applies service-local initial data such as `SERVICE/db/seed.sql` and generated model seed data at `.scenery/gen/db/<service>/seed.sql` to that service's schema in the app database with `search_path=<service_schema>,scenery`. If the seed service does not match a configured database service, Scenery falls back only when there is one database service or a conventional `db` service; mixed-service apps must declare matching service databases. It runs after schema exists and does not participate in Atlas or SQLC generation. Seed ledgers use `scenery.seed_runs`, keyed by app ID and seed path. Unchanged seeds are skipped; changed previously-applied seeds fail closed with status `changed`. Seed validation also fails closed before opening the database when SQL contains destructive setup patterns such as `DROP`, `TRUNCATE`, `DELETE FROM ...` without `WHERE`, `WHERE true`, or `WHERE 1 = 1`; diagnostics include the seed path, line, message, and statement context.
- `scenery db setup` runs `db apply`, then `db seed`. It reports both phases in JSON mode and stops before seed if apply fails.
- `scenery generate sqlc` remains the SQLC generated-source command. It may refresh generated schema SQL from schema definitions and run `sqlc generate`; it must not mutate a database or consume seed files.
- `scenery up` runs the setup lifecycle before starting the app when DB setup inputs exist, and reruns it on rebuild only when the `database.apply` config or discovered seed file hashes change. Setup failures are reported through the existing compile/setup failure path and dev event stream, and the previous successful fingerprint is not advanced so the next rebuild can retry.

Doctor rules:
- `scenery doctor` is a fast, read-only local environment diagnostic. It does not install tools, download managed artifacts, start services, run builds, connect to databases, or mutate `.scenery/`.
- `scenery doctor --json` emits `scenery.doctor.result.v1` and exits non-zero only when required checks have status `error`.
- Local storage needs no managed toolchain artifact, so `scenery doctor --json` has no storage-specific readiness check; the local filesystem and standard disk/memory checks cover it.
- Check statuses are `ok`, `warn`, `error`, and `skipped`. Check severities are `required`, `optional`, and `informational`.
- Required failures currently cover baseline host readiness such as missing/old Go, very low memory, very low disk space, or an explicitly invalid `--app-root`.
- Doctor reports local state size through informational `storage.scenery_home` checks. `storage.scenery_home` walks the resolved Scenery agent home (`~/.scenery` by default or `SCENERY_AGENT_HOME` when set).
- Optional missing tools such as `bun`, `atlas`, `sqlc`, `git`, and Postgres client tools warn by default. `psql` and `pg_dump` are relevant only when app config declares Postgres services. App configuration can make messages more specific, but the initial doctor contract does not make optional tools fatal. Doctor reports Docker through `docker.context` and `docker.engine` checks instead of a generic host `tool.docker` line. `docker.context` reports the selected Docker context from `docker context show`. `docker.engine` warns when the Docker CLI is missing or the engine is unreachable, and when reachable it probes with `docker info --format '{{json .}}'` and reports engine details such as server version, OS/type, architecture, CPU/memory, root dir, storage driver, cgroup version, kernel version, and engine name when available. When Postgres services are configured, `db.postgres_server` is a required readiness check for the managed dev server path and points users to Docker or explicit external DSNs.
- `--app-root` tunes app-sensitive diagnostics from the app config. If omitted, doctor tries current-directory app discovery and silently continues with environment-only checks when no app is found.
- When the deploy registry exists, `scenery doctor --json` includes a `deploy` section summarizing `scenery deploy status` diagnostics. Deploy doctor checks may perform explicit reachability/DNS probes only because `doctor` is an operator-invoked diagnostic command.

Deploy rules:
- `deploy.domain` in app config is a beta public FQDN claim for `scenery deploy enable`. It must be lowercase, must not be localhost or an IP address, and must not use the local route-base domain. `deploy.root` optionally names the frontend/service that owns `/` on that domain; when omitted, Scenery can infer it only if exactly one frontend is configured.
- `scenery deploy enable|disable --json` records intent in the machine deploy registry at `<agent home>/agent/deploy.json` and emits `scenery.deploy.target.v1`. Enabling rejects a domain already enabled for another app root.
- `scenery deploy setup` is macOS-only, must run as the normal user, asks sudo only for the privileged helper install, configures the helper for wildcard TCP 80/443, records ACME email/CA, installs the login resume LaunchAgent, and restarts the user-owned edge. `scenery deploy teardown` reinstalls the helper in loopback-only mode, removes the resume LaunchAgent, restarts the edge, and keeps the registry plus Caddy certificates.
- `scenery deploy resume` starts the agent and edge, then starts missing enabled app roots with `scenery up --detach --app-root <root>` while leaving already-running roots alone. It appends JSON lines to `<agent home>/deploy-resume.log`.
- `scenery deploy status --json` emits `scenery.deploy.status.v1`. It reports helper state/version, wildcard listener truth for 80/443, edge/agent/LaunchAgent state, ACME settings, target live-session/cert state, and structured diagnostics for LAN/public reachability, DNS A/AAAA mismatch, Cloudflare-proxied DNS, power sleep, macOS firewall, and helper contract drift. Public IP discovery and DNS lookups happen only inside `scenery deploy status` or deploy-aware `scenery doctor`.
- Public deploy routing is strict: public edge requests require the trusted edge token plus `X-Scenery-Public-Edge: 1`, exact host match against an enabled registry target, and a live session for that target app root. Public dispatch serves `/`, `/api/`, `/sync/`, and configured frontend prefixes, returns 503 for enabled-but-down apps, and does not expose Scenery runtime/dashboard/control paths.

Inspect rules:
- `scenery inspect` requires a subject.
- `scenery inspect` currently requires `--json`.
- `--app-root` is optional. When omitted, scenery walks upward from the current working directory to find the app config.
- Stable inspect subjects for v0 are `app`, `routes`, `services`, `endpoints`, `build`, `paths`, and `docs`.
- `generators`, `durable`, `storage`, `traces`, `metrics`, and `observability` are beta diagnostic subjects. `generators` reports configured generation graph inputs and outputs. `durable` reports discovered durable task declarations, service schemas, the durable `scenery` schema, and redacted app database metadata. `storage` reports declared stores, the resolved storage cell ID, default/share policy, per-store object counts and total bytes, and readiness. `storage.runtime` reports the storage-cell `cell_root`, `objects_dir`, and whether the objects directory exists; readiness is `ready` once it does. Raw object-store credentials are never exposed (the local backend has none). `traces`, `metrics`, and `observability` read scenery-managed local observability data. Victoria is the current backing substrate, not the integration API. If no local state exists, query/discovery commands return valid JSON with warnings and empty result sets where possible.
- `scenery storage status|webui|ls|stat|put|get|rm|cleanup --json` is a beta storage capability CLI. Object commands operate on configured stores, validate keys with Scenery storage rules, and enforce configured `max_object_bytes`. `cleanup` reports the current storage cell path and existence by default and removes the storage cell directory only with `--yes`. The JSON-only CLI operates directly on the local storage-cell object directories. `webui` reports that the local backend has no managed Web UI. `get` requires `--output` in JSON mode. The app runtime exposes the same configured stores through reserved `/__scenery/storage/<store>/...` HTTP routes when storage env is present; these routes are app data-plane routes, not dev/admin endpoints. Generated TypeScript clients include `client.storage` and `client.storage.store(name)` helpers for list, put, get, getText, getBlob, head, delete, and deletePrefix over those reserved auth storage routes. Stores with `access: "private"` are deliberately absent from the generated browser contract and are only available through app/runtime helpers or the runtime private route table.
- `scenery inspect observability --json` emits `scenery.inspect.observability.v1` with backend readiness for logs, metrics, and traces; native dialect names; examples; and the exact enforced query scope for the selected app/session.
- The `scenery.inspect.traces.v1`, `scenery.inspect.metrics.v1`, `scenery.inspect.observability.v1`, `scenery.logs.query.v1`, `scenery.logs.tail.entry.v1`, `scenery.metrics.query.v1`, `scenery.metrics.labels.v1`, and `scenery.metrics.series.v1` schemas are useful for agents, but their source-selection, retention, rollup, percentile, and clear/delete semantics are not stable v0 API yet.
- `--since` accepts Go duration strings such as `15m`, `1h`, or `24h`.
- `--min-duration-ms` filters root traces by duration in milliseconds.
- `--status` accepts `ok` or `error`.
- `metrics` defaults to `--since 24h` and `--limit 10000` so agents get useful local summaries without scanning unbounded history.
- User-facing dev lifecycle and observability commands scope to the app root. Internal session IDs remain in JSON records, manifests, routes, and state paths for compatibility, but users should not select or create runtime sessions directly.
- `logs query` defaults to the app root's live runtime, `--since 15m`, `--limit 200`, `--timeout 3s`, and JSON envelope output. `--limit` is capped at 2000 and reports a JSON warning when clamped. It accepts native VictoriaLogs LogsQL through `--query`; `--logql` is rejected rather than silently treating Loki LogQL as LogsQL. Finite queries use an HTTP context deadline derived from `--timeout`.
- `logs tail` streams scoped `scenery.logs.tail.entry.v1` JSONL log entries from the VictoriaLogs live-tail endpoint, maps `--since` to VictoriaLogs `start_offset`, rejects `--start` and `--end`, and exits through normal context cancellation or interrupt handling.
- `metrics query` defaults to range mode for the app root's live runtime with `--since 15m`, `--step 5s`, `--timeout 3s`, `--limit 100`, and JSON output. `--limit` is capped at 10000 and reports a JSON warning when clamped. `--instant` switches to the instant Prometheus API endpoint. Finite queries use an HTTP context deadline derived from `--timeout`.
- `metrics labels` and `metrics series` default to the app root's live runtime with `--since 1h`, `--timeout 3s`, and `--limit 1000`; catalog limits are capped at 10000 and report a JSON warning when clamped. `metrics labels` accepts optional `--match`, and `metrics series` requires `--match`.
- Query commands are scoped by default. Scenery applies LogsQL scope through VictoriaLogs `extra_filters` and metrics scope through repeated VictoriaMetrics `extra_label` query parameters, and every JSON envelope echoes `scope.enforced=true`.
- `docs` inspects the scenery repo knowledge base, not a target scenery app. It accepts `--repo-root` and otherwise walks upward to the `module scenery.sh` repo root.

Toolchain rules:
- `scenery.toolchain.json` is the root checked-in manifest for Scenery-owned development executables, Docker images, plugins, and source lock references.
- The manifest uses `scenery.toolchain.v1`; `scenery system toolchain ... --json` emits `scenery.toolchain.status.v1`.
- Binary artifacts may use `platforms` for downloaded archives or `source_build: {kind: "go", package: "./cmd/..."}` for source-built Scenery binaries. Source-built artifacts are compiled with `go build` into the managed toolchain store and report `source: "source-build"` in toolchain status.
- `--tool <name>` selectors must match a manifest artifact exactly. Unknown selectors fail closed with `unknown toolchain artifact "<name>"` instead of returning an empty successful status.
- `scenery version --json` includes `toolchain_manifest.schema_version`, `sha256`, `artifact_count`, and `source_lock_count` for the bundled manifest.
- `scenery upgrade --json` emits `scenery.upgrade.v1`. It fetches the latest GitHub release by default, selects the release asset for the current `GOOS/GOARCH`, verifies it against the release `checksums.txt`, and replaces the current executable path unless `--target <path>` is set. If the current version already matches the selected release, it skips binary replacement unless `--force` is set. `--dry-run` reports the selected release and target path without downloading the archive or mutating the binary.
- After a successful binary upgrade, `scenery upgrade` runs the upgraded binary's bundled toolchain sync unless `--toolchain none` or `--skip-toolchain` is set. The default `--toolchain installed` syncs manifest entries already present in the local managed store. `--toolchain all` runs `scenery system toolchain sync --images --json` with the upgraded binary, so every manifest binary artifact and image is pulled or built from the upgraded manifest.
- When the public deploy helper is installed and its target metadata schema no longer matches the current helper contract, successful `scenery upgrade` output includes a `deploy` notice and the human text tells the operator to run `scenery deploy setup` to refresh the privileged listener. Helper version drift alone is informational; re-setup is required only for helper-contract drift.
- The default local store is `.scenery/toolchain/` under the app/repo root. Machine-level edge tools use `~/.scenery/toolchain/` under the local agent home. `SCENERY_TOOLCHAIN_DIR` overrides both store roots.
- `SCENERY_TOOLCHAIN_DOWNLOAD=0` disables automatic managed binary downloads. Per-tool download disable variables such as `SCENERY_DEV_VICTORIA_DOWNLOAD=0` still apply to their startup paths.
- Managed Caddy resolves from the managed store or manifest-driven download. Managed Victoria binaries resolve from explicit env overrides, the managed store, or manifest-driven download. They do not use implicit system `PATH` binaries.
- `scenery system toolchain verify --strict --images` fails for tag-only image refs. Tag-only image refs marked `stability: "unstable"` are accepted only outside strict verification during the migration to digest-pinned images.
- Go modules and UI package-manager files are source locks. Commands such as `go`, `bun`, `npm`, `node`, and `tsx` used to run source/package-manager workflows are not hidden Scenery-managed toolchain downloads.

Command split:

- `scenery up` starts the app root's one live dev runtime: app process, file watching, and rebuild/restart supervision. The file watcher treats `.gitignore`-ignored paths and app config `watch.ignore` paths as outside the watch surface and does not descend into ignored directories. `watch.ignore` also excludes those paths from the rebuild/change fingerprint used by the dev loop, but it does not affect Git tracking. A second live code copy requires a separate Git worktree.
- `scenery up --detach` requires the local agent, starts the same dev supervisor in a background child process, and by default (`--wait ready`) waits up to two minutes until the child session is registered, its status is `running`, the API backend accepts connections, and configured frontend backends are registered and accepting, then prints a Docker-style app action summary, status/log/stop commands, and currently registered routes/aliases before returning. `--wait registered` keeps the 30-second registration budget and returns as soon as the child PID registers as the app root's runtime owner, without waiting for readiness. Wait timeout errors report the real child PID and last reached state. Detached child stdout/stderr from the supervisor is written under the agent directory; app process output continues to flow through the scoped dashboard log store.
- `scenery logs --follow` follows the app root's live runtime logs by default with the same app-root, limit, stream, source, kind, level, grep, since, and JSONL options, and it does not mutate runtime state.
- `scenery logs`, plain `scenery logs --follow`, and `scenery console` read structured dev events from the Victoria-backed substrate for the selected app root's live runtime.
- If the backing dev-event substrate is unavailable, structured dev-event read commands fail loudly instead of falling back to the deprecated local process-output cache.
- `scenery console` opens the source-aware terminal console when stdin/stdout are real TTYs. In CI, dumb terminals, or redirected output it falls back to normal log following with the same filters.
- Structured dev logs carry source identity. Current source ids include `api`, `worker`, `build`, `supervisor`, `victoria`, and `frontend:<name>`.
- `scenery system agent restart` stops the currently reachable local agent process, starts a new background agent, waits until the control socket is reachable, and returns. The same `--socket`, `--router-listen`, `--router-tls`, `--router-http`, `--trust`, and `--json` options apply to the restarted agent.
- Commands that ensure the local agent compare the running agent's reported build identity (`version`, `commit`, `built_at`) with the invoking CLI's build identity and transparently restart the agent once when the running agent is older or predates identity reporting. Semver versions compare by semver; equal or non-semver versions fall back to `built_at`. The automatic restart preserves the running agent's router address and internal router scheme so registered route URLs stay valid, and an older CLI never restarts a newer agent.
- `scenery system edge dns install` resolves the managed `dnsmasq` toolchain artifact, syncing/building it automatically unless managed downloads are disabled, starts user-owned dnsmasq for the configured wildcard dev domain plus other Scenery-managed resolver domains already present on the machine, and on macOS invokes a privileged helper only when `/etc/resolver/<domain>` is missing or mismatched. `scenery system edge privileged install` installs the macOS root-owned loopback helper that listens on `127.0.0.1:443` and `[::1]:443` and forwards raw TCP only to a validated user-owned Caddy target recorded under the helper's configured agent run directory. Run it as the normal user; it invokes `sudo` only for the minimal helper install. `scenery system edge privileged uninstall` removes that helper. `scenery system edge install` and `scenery system edge restart` refuse root, start user-owned Caddy on an unprivileged high loopback port, ensure the local agent router is running as an unprivileged HTTP upstream on its internal loopback address, disable Caddy response buffering for streaming routes while preserving upstream cache headers, and write both edge state and helper target metadata under the current agent run directory; when a previously installed macOS privileged helper is bound to another agent home, Scenery also publishes the active Caddy target to that helper's configured metadata path because port `443` is machine-global. If wildcard DNS or the privileged helper is missing or unhealthy, install prepares Caddy but fails with the actionable setup command because browser-ready default-port HTTPS requires both. They resolve Caddy from the managed `caddy` toolchain artifact, syncing it automatically unless managed downloads are disabled. `scenery system edge trust` resolves the same managed Caddy artifact, starts a temporary admin-only Caddy process with `local_certs`, runs Caddy's trust flow against that temporary admin endpoint, and does not require the port-443 edge to be running. `scenery system edge status --json` reports `scenery.edge.status.v1`, including the privileged helper target metadata path and PID used to decide whether the helper is forwarding to the active Caddy edge. `scenery system edge uninstall` stops user-owned Caddy, removes helper target metadata only when it still points at that Caddy, leaves DNS and the privileged helper alone, and reports `scenery system edge privileged uninstall` as the helper removal command.
- `scenery down` stops and unregisters the selected app root's live dev runtime but is non-destructive by default. It preserves shared storage-cell data. `--db` resolves and drops the app root's managed Postgres app database directly, so it works even when no runtime record exists; it refuses external DSNs. `--state` removes that runtime's internal `.scenery/sessions/<id>` state root when a runtime record exists, and `--all` enables both; `--state` still does not delete shared storage-cell data. `--json` reports `scenery.down.v1` and still includes `session_id` for state compatibility. To delete storage-cell data, use `scenery storage cleanup --yes`.
- `scenery prune --older-than <duration>` prunes old agent sessions whose recorded owner is gone or mismatched and removes their `.scenery/sessions/<id>` state roots. It accepts Go durations such as `336h` plus day shorthand such as `14d`. It does not drop managed databases or delete VictoriaLogs storage; use `scenery down --db` or `scenery db drop` for destructive database cleanup.
- Starting `scenery up` for an app root requires exclusive ownership of that app root's live dev runtime. If another live owner already controls the same app root, startup fails with an "already running" error that points to `scenery down --app-root <path>` and Git worktrees. If the recorded owner is dead or its fingerprint no longer matches, the new owner may claim the runtime and clean recorded app, worker, and managed frontend child processes from the stale owner, plus Scenery-owned runtime processes whose injected app root/internal session environment matches. It must not clean other app roots, other worktrees, or unrelated user processes.
- Session owner checks treat `owner_pid` as the effective owner. `owner.pid` is the fingerprint for that same PID, not an independent owner field. If the stored owner fingerprint object points at a different stale PID, Scenery refreshes it on the next registration and must not delete or prune the session while the effective `owner_pid` is still live. Dev supervisors unregister sessions with an owner-conditional delete that includes the recorded owner fingerprint; if an older owner exits after ownership moved, or if the same PID now has a different recorded fingerprint, the delete is ignored and the newer session record remains registered.
- `scenery help --json` returns `scenery.help.v1`, a machine-readable command manifest for agents and contract checks. Human root help is intentionally orienting and does not contain the full command grammar; use `scenery help all` for the grouped command reference and `scenery help <command>` for exact flags and subcommands.
- `scenery ps` renders a headed table with app, worktree, status, base URL, service URLs, and update age by default. `scenery ps --json` treats a `starting` or `running` runtime with a missing or dead effective owner as `stale`, and a live but fingerprint-mismatched owner, dead app PID, dead registered child process, registered child process owner mismatch, or configured custom route base domain whose routes point at a non-default internal router port as `degraded`. Duplicate `scenery up` startup prevention uses the recorded runtime owner and owner fingerprint, not shell command text. Status JSON includes `status_reason` when scenery rewrites the runtime status. Status JSON also includes the agent substrate registry as `substrates`; failed shared substrates expose `status`, `last_exit`, and `component_exits` with component, PID, started/exited timestamps, exit code or signal, error text, and stdout/stderr log paths.
- When the local agent is active, the agent starts the visible dashboard backend and exposes the dashboard through the console route from `route_namespace`, for example `https://console.<route-id>.<route_namespace.base_domain>/`. Release binaries serve the embedded dashboard UI produced from `apps/consolenext/` before the Go binary is compiled; dashboard startup does not build UI assets at runtime, though `SCENERY_DEV_DASHBOARD_UI_DIR` may point at an explicit local UI build. The old path-shaped `console.../s/<session_id>` form is not the canonical dashboard URL. The Unix-socket control API remains protected by filesystem permissions.
- The dashboard `version` RPC additionally reports the embedded console bundle identity: `dashboard_bundle_hash` and a `dashboard_bundle` object with `runningHash`, `diskHash`, `diskPath`, `stale`, and `warning`. Dashboard HTTP responses carry `X-Scenery-Dashboard-Bundle-Hash`, plus `X-Scenery-Dashboard-Bundle-Stale: true` and `X-Scenery-Dashboard-Bundle-Warning` when the running binary's embedded bundle differs from `apps/consolenext/dist` in a scenery repo checkout; dashboard HTML includes matching meta tags, and `devdash.AppStatus` exposes the same object as optional `dashboardBundle`. Staleness detection is a no-op outside a scenery repo checkout. The self-harness `dashboard ui fresh` step uses the same hash comparison.
- ConsoleNext is the only runnable dashboard source. Its browser transport is the same-origin `/__scenery` WebSocket RPC. GraphQL and the former compatibility RPCs for trace events, transaction wrappers, editors, onboarding, and telemetry are not supported dashboard surfaces; use the current typed RPCs such as `traces/list`, `db/query`, and `stored-requests/*`.
- The consolenext `Symphony` page stores local task-board state in the managed Postgres server's `scenery_symphony` database. Tables are bootstrapped idempotently in that database's `public` schema. Rows are scoped by stable base app ID when present; direct dashboards with no session id fall back to the dashboard app ID. Worktrees for the same app share a board and different apps do not. The local dashboard RPC methods `symphony/state`, `symphony/task/create`, `symphony/task/update`, `symphony/task/move`, `symphony/task/delete`, `symphony/statuses/update`, `symphony/workflow/get`, `symphony/workflow/update`, and read-only `symphony/run/detail` cover board, workflow, and run-detail persistence. Browser WebSocket upgrades must be same-origin, and dashboard RPC cannot change a non-auto workflow to `auto`; use the local trust path `scenery symphony auto --on --app-root <path>` to enable auto mode and `scenery symphony auto --off --app-root <path>` to return to manual mode. Workflow mode accepts `manual`, `auto`, and `disabled`; when mode is `auto`, the dashboard server requires saved workflow markdown or app-root `WORKFLOW.md`, claims eligible `Todo` tasks with no active run and fewer than `agent.max_attempts` previous attempts (default `3`), respects `agent.max_concurrent_agents` from workflow front matter before stored `max_concurrency`, creates or reuses a detached Git worktree under `<dashboard-cache-root>/workspaces/<app-id>/<task-identifier>/repo`, resets existing worktrees before retry, moves claimed tasks to `In Progress`, renders the workflow prompt body, runs one Codex app-server turn over stdio in the app workspace, records queued/running/turn/completed lifecycle rows plus changed-file and diff artifacts in `symphony_runs` and `symphony_run_events`, and heartbeats a run lease while active. Active run statuses are exactly `queued` and `running`; `succeeded`, `failed`, `stalled`, and `timed_out` are terminal. Expired active leases are marked `stalled`, receive a `run.stalled` event, and release tasks still in `Todo` or `In Progress` back to `Todo`; Codex app-server no-notification stalls also complete the run as `stalled` and route the task to `Rework`; turn timeouts complete the run as `timed_out` and route the task to `Rework`. Succeeded tasks move to `Human Review`; failed tasks move to `Rework`. Backlog, manual, and disabled workflows do not auto-run. `WORKFLOW.md` front matter supports `agent.max_concurrent_agents`, `agent.max_attempts`, `agent.max_turns` (default `20`, currently parsed and carried for the future multi-turn loop while Scenery runs one turn per session), `agent.turn_timeout_ms` (default `3600000`), and `agent.stall_timeout_ms` (default `300000`). Process-starting runner methods such as `symphony/run/start` are intentionally unavailable until the runner channel is authenticated.
- The direct agent router serves HTTP by default. Default path-mode local dev is reached through the per-runtime localhost listener and does not require dnsmasq, port 443, or wildcard HTTPS. Host mode (`dev.routing.mode = "host"`) uses the `scenery system edge` path under `local.dev`: browser DNS is provided by `scenery system edge dns install` through managed dnsmasq and a macOS scoped resolver, browser HTTPS reaches the privileged loopback helper on `127.0.0.1:443`, the helper forwards raw TCP to user-owned Caddy on an unprivileged loopback port, and Caddy proxies to the agent router on internal HTTP. API and console routes are generated from the app-derived `route_namespace`, and router requests resolve by exact registered route-host lookup instead of parsing a fixed localhost suffix. Entries in `routes` are canonical. Direct router URLs remain internal/diagnostic only in that mode. Friendly app-derived hosts are optional alias leases exposed in a separate `aliases` map only for the live app root that owns the free alias; a second worktree keeps its canonical routes, does not steal the alias, and reports held aliases in `alias_conflicts`. Same-app-root duplicate runtimes are rejected before alias ownership comes into play. Stale alias leases are reclaimed only after owner fingerprint verification proves the old owner is gone or mismatched. Live alias leases transfer only through `scenery up --claim-aliases`. Alias routing, router TLS host validation, and the Caddy on-demand TLS ask endpoint use the same exact registry lookup as canonical routes. Caddy forwards `X-Scenery-Edge-Token`; the agent trusts incoming forwarded proto/port headers only when that token matches and the request comes from loopback. Agent health and state distinguish the internal `router_addr`, browser-facing `public_router_addr`, public `router_scheme`, internal `internal_router_scheme` (health only), `edge`, and edge DNS state, and report the agent's build identity as optional `version`, `commit`, and `built_at` fields. `scenery system edge status --json` reports dnsmasq and resolver readiness; DNS is ready when the current managed dnsmasq state is running, or when an installed resolver functionally resolves the managed wildcard domain to the expected loopback address even though dnsmasq is owned by another agent home. `scenery system agent --router-http` keeps or forces the default direct HTTP router. `scenery system agent --router-tls` enables direct HTTPS when an explicit setting is needed. `scenery system agent --trust` also enables direct router TLS and attempts to trust the existing scenery local CA; `SCENERY_AGENT_TRUST=1` only requests trust installation when direct router TLS is enabled. Trust installation failures are logged; the router still starts. Direct router TLS certificates are issued for `localhost` and registered route or alias hosts, not for arbitrary local names. Public HTTPS route URLs omit the port when the active public edge is on port `443`; non-default router ports stay explicit, and explicit occupied direct router addresses fail instead of silently falling back.
- Agent dev-runtime manifests always include a `dashboard` route for the global agent-owned dashboard. With the agent dashboard active, the manifest does not need a matching per-runtime `dashboard` backend; direct/per-runtime dashboard endpoints are kept for agent-disabled or unavailable-agent paths.
- `scenery up` exposes native local observability for the dev runtime. The current substrate may start local VictoriaMetrics, VictoriaLogs, and VictoriaTraces when their managed toolchain binaries are installed or can be downloaded. When the local agent is active, shared substrates are registered through one managed substrate lifecycle: owner fingerprint verification before reuse, service-specific reachability probing, stale-record deletion, ready/degraded/exited upserts, component exit monitoring, and structured dev events. Dashboard runtime metadata is stored as compact, bounded JSON under the agent directory when the agent is active and `SCENERY_DEV_CACHE_DIR` is unset, with large app-model `Metadata` and `APIEncoding` blobs stored content-addressed under the same dashboard cache root. The agent dashboard process owns global dashboard-store writes; agent-backed dev supervisors send app/session and small process-diagnostic mutations to its authenticated internal control-plane endpoint instead of opening the store directly. Agent/global dashboard app summaries and app status payloads expose `sessionStatus` and `sessionStatusReason` computed from the same owner/process/edge-route classification as `scenery ps`, so dashboard status indicators do not mark degraded or stale sessions as running. Trace summaries, trace events, and report log events are not persisted in `devdash.json`; they are exported to Victoria. Multiple worktrees for the same base app can appear in the global dashboard without session records duplicating full app models or report writes growing unbounded. These details are documented for intentional substrate debugging and are not the stable app-facing API.
- The local agent home defaults to `~/.scenery` unless `SCENERY_AGENT_HOME` is set. `SCENERY_DEV_CACHE_DIR` controls build and dashboard cache locations, not machine-wide agent identity.
- Managed frontend services start on runtime-private hidden loopback ports and are restarted by the dev supervisor if their process exits unexpectedly. A manual `SCENERY_FRONTEND_<NAME>_ADDR` override is accepted, but configured frontend upstreams are ignored unless that frontend sets `"allow_shared_upstream": true`.
- Dev app children are launched through an internal runtime executable path under `.scenery/sessions/<session_id>/run/app/` so stale same-runtime app processes can be identified without broad process-name matching.
- Use default agent-routed app URLs, and run `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, and `scenery system edge trust` when trusted local HTTPS on the default port is needed.
- `scenery up --port <n>` and `scenery up --listen <addr>` force a manual TCP app backend. The default agent path uses a runtime-private Unix socket and should be preferred for worktree-safe development.
- `scenery worker` never starts the managed shared Postgres server. If an app declares a Postgres service, each service's configured database URL env must be set to a valid Postgres URL before startup; otherwise startup fails closed and points back to `scenery up` as the dev-substrate path.
- `scenery task list|inspect|run|graph` is the canonical task surface. Plain targets resolve only to configured tasks from app config; `<domain>:<name>` targets resolve only to code tasks under `<app-root>/<domain>/tasks/...`. Configured task names containing `:` are rejected. Code task target segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`.
- Scenery task flags must appear before the target. Code task arguments must appear after `--`, for example `scenery task run --env production billing:reconcile -- --dry-run`. Configured tasks do not accept `--env`, `--lang`, or extra runtime arguments.
- Supported code task layouts are `<domain>/tasks/<name>.task.go`, `<domain>/tasks/<name>.task.ts`, `<domain>/tasks/<name>/main.go`, and `<domain>/tasks/<name>/index.ts`. Single-file Go tasks must start with `//go:build ignore` so normal app package loading cannot accidentally include them. If multiple candidates match a target, scenery fails unless `--lang go|typescript` selects a single language.
- Code tasks execute with cwd set to the app root. Go tasks use `go run`; TypeScript tasks prefer `bun` and fall back to `node --import tsx`. Task processes receive `SCENERY_APP_ID`, `SCENERY_APP_ROOT`, and `SCENERY_ENV`/`SCENERY_RUNTIME_ENV` when `--env` is set, with `.env` and `.env.local` loaded when present.
- `scenery inspect validation --json` is read-only and returns `scenery.inspect.validation.v1` with app metadata, default profile, profile records, advisory artifacts, and diagnostics.
- `scenery validate list|inspect|graph --json` returns `scenery.validation.list.v1`, `scenery.validation.inspect.v1`, and `scenery.validation.graph.v1`. `scenery validate <profile> --dry-run --json` returns `scenery.validation.plan.v1` and must not execute shell, task, code-task, harness, database, or generation steps.
- `scenery validate [<profile>] --json --write` runs the resolved profile sequentially, fails fast, keeps stdout as one JSON document, captures child output as bounded evidence tails and artifacts, returns `scenery.validation.result.v1`, and writes `.scenery/harness/validation/latest.json` plus `.scenery/harness/validation/<profile>-latest.json`.
- `scenery validate changed --base <ref>` computes `git diff --name-only <base>...HEAD`, includes the default profile, adds profiles whose `paths` globs match changed files, resolves nested `profile:` steps, deduplicates profiles, and reports selection reasoning in JSON.
- Cron declarations run through the in-process scheduler. The API role reconciles schedules, while `scenery worker` executes cron jobs without starting the public HTTP server.
- `scenery worker` builds once and starts the app runtime in worker-only mode with no public HTTP server. In this beta implementation it runs cron jobs and local durable workers; generated binaries use `SCENERY_ROLE=worker`.
- `scenery worker durable --endpoint <url> --token <token>` builds once and starts the app runtime as a remote durable worker. The generated binary receives `SCENERY_ROLE=worker`, `SCENERY_DURABLE_ENDPOINT`, `SCENERY_DURABLE_TOKEN`, and optional `SCENERY_DURABLE_SERVICES`, then polls remote durable lease endpoints and executes registered Go handlers.
- `scenery worker durable jobs list|inspect|cancel|retry ... --json` reads or mutates jobs for one service in the app database's shared durable store and emits `scenery.durable.jobs.v1`; `inspect` includes job events.
- `scenery worker durable token create --service <name> --json` creates or rotates a remote durable worker bearer token for one service in the app database's shared durable store, stores only the token hash, and prints the raw secret once in `scenery.durable.worker_token.create.v1`.
- `scenery build` produces the deployable binary and remains the preferred deployment artifact path.
- `scenery harness ui --json` is an optional browser-backed dashboard check. It starts a temporary `scenery up` process unless `--dashboard-url` points at an existing dashboard, visits core dashboard routes, runs route-specific semantic journeys, checks stable `data-scenery-ui` markers, captures screenshots, writes compact DOM snapshots, and writes console/network artifacts under `.scenery/harness/ui/`.

Runtime safety:

- Generated binaries do not expose dev/admin endpoints by default.
- Dev/admin endpoints such as `/__scenery/config`, `/platform.Stats`, and `/debug/pprof/*` are enabled only for the development child process launched by `scenery up` or when `SCENERY_DEV_ENDPOINTS=1` is set explicitly.
- Runtime CORS reflection is enabled in dev endpoint mode. Outside dev mode, CORS origins must be explicitly allowlisted with `SCENERY_CORS_ALLOW_ORIGINS`.
- Build workspaces skip local secret and machine artifacts such as `.env`, `.env.*`, `.git`, `.scenery`, `node_modules`, `.DS_Store`, `__MACOSX`, and `coverage`.

Local observability:

- The user-facing observability surface is `scenery logs`, `scenery logs query`, `scenery logs tail`, `scenery traces list --json`, `scenery metrics list --json`, `scenery metrics query`, `scenery metrics labels`, `scenery metrics series`, `scenery inspect observability --json`, and the dashboard. The current backing substrate exports local observability to Victoria sidecars:
  - VictoriaMetrics: `/opentelemetry/v1/metrics`
  - VictoriaLogs: `/insert/opentelemetry/v1/logs`
  - VictoriaTraces: `/insert/opentelemetry/v1/traces`
- Dashboard trace reads and `scenery traces list|metrics --json` use scenery-managed observability data. Victoria is the current substrate when local sidecars are available; `devdash.json` is not a fallback trace or report-log history store.
- Victoria sidecars store data under `.scenery/victoria/` by default when running without the agent. With an active agent, Victoria is a shared substrate per agent state root, effectively per user/machine where that agent runs: state is stored under the agent directory and registered in the agent substrate registry, and the dev supervisor reuses registered endpoints instead of owning per-worktree Victoria processes. It is not an OS-level service and is not started once for all users or all possible agent homes. Reuse requires verified owner fingerprints and reachable metrics/logs/traces listeners. Managed Victoria stdout and stderr are always written to stable substrate log files, and component exits update the substrate to `degraded` with `last_exit` and per-component exit metadata. Substrate exit events are exported to the structured dev log stream with component name, PID, exit code or signal, and log paths.
- `SCENERY_DEV_VICTORIA=0` disables Victoria sidecars. `SCENERY_DEV_VICTORIA_DOWNLOAD=0` disables automatic Victoria binary downloads. When enabled, missing Victoria binaries are downloaded into `.scenery/toolchain/` or `SCENERY_TOOLCHAIN_DIR`.
- Victoria binary names, versions, ports, storage layout, download behavior, and Victoria query semantics are beta substrate details. They are documented so local development is debuggable, but they are hidden during ordinary app work and are not part of the stable v0 runtime contract.
- Default Caddy, Victoria sidecar, and managed image versions are pinned in `scenery.toolchain.json`; environment variables override explicit startup controls for local testing where documented. Caddy edge is managed-toolchain only.
- Agent sessions inject `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, `SCENERY_RUNTIME_APP_ID`, `SCENERY_APP_ROOT_HASH`, `SCENERY_BRANCH`, and `SCENERY_WORKTREE` into the app process. Local development reports carry that identity and the reporter PID into Victoria trace, metric, and log exports.
- Dev report endpoints reject missing-session, stale-session, and invalid-token reports before store work. Rejections are exported as structured warning log events with `kind=dev-report-rejected`, and app-side report clients back off after repeated deadline/unauthorized/stale-report failures so old processes cannot hot-loop the dashboard.
- The emitted VictoriaMetrics request duration contract is `scenery_request_duration_seconds` with labels `scenery_app`, `scenery_trace_type`, `scenery_is_root`, `scenery_is_error`, `scenery_service`, optional `scenery_session_id`, optional `scenery_app_root_hash`, optional `scenery_branch`, optional `scenery_worktree`, optional `scenery_endpoint`, and optional `scenery_message_id`.
- The emitted VictoriaTraces and VictoriaLogs attribute contract includes `scenery.application_id`, optional `scenery.session_id`, optional `scenery.app_root_hash`, optional `scenery.branch`, and optional `scenery.worktree`.
- `scenery up` writes local ignore markers under `.scenery/` and the Victoria state roots so downloaded binaries, local databases, logs, generated build outputs, and other machine-local state are not accidentally committed by target apps.

Secrets and environment:

- The human env-var reference is [Environment Reference](environment.md). The machine-readable env contract is [environment.registry.json](environment.registry.json), and `scenery harness self` fails on unregistered production env usage.
- Do not add a new scenery-owned production env var as a convenience escape hatch. Prefer app config, explicit CLI flags, or checked-in manifests; if env is truly required, add a registry entry with rationale, docs, and tests in the same change.
- Process environment always wins over values loaded from local files.
- The stable runtime path reads `.env` from the app root for local secret population when a value is not already present in the process environment.
- Local startup requires `.env` to exist in the app root. If `.env` is missing, `scenery up`, local `scenery task run`, and local `scenery worker` fail before serving or running with a clear error. `.env.local` is optional.
- `scenery up` passes local file values into the child process before Go package initialization so package-level declarations can read them through `os.Getenv`.
- `scenery up` loads `.env` first and `.env.local` second. `.env.local` overrides `.env` only for keys that are not already present in the parent process environment.
- Missing declared secrets warn in local development mode.
- With `--env production`, `scenery worker` can use process environment without a `.env` file; operator-run generated binaries likewise use process environment directly. Both fail before startup if any declared secret is missing.
- `.env`, `.env.*`, and secret-bearing local files are not copied into build workspaces.

Standard auth:

- Apps may enable the built-in standard auth module from app config instead of writing a `//scenery:authhandler`.
- Auth-protected app code can use `auth.UserID()`, `auth.Data()`, or `auth.CurrentAuthData()` from `scenery.sh/auth`.
- Access tokens are HMAC JWTs with required expiration and `tenant_id` claims.
- Standard auth tenant state is framework-owned and lives in `scenery_auth.tenants`; an app-local `tenants` service or table is only an app-domain concern.
- Refresh sessions are stored in PostgreSQL and rotate by hashing refresh tokens. The refresh cookie name defaults to `onlv_refresh` for ONLV compatibility and is configurable.
- Google connections are per standard-auth user and live in `scenery.scenery_auth_google_connections`. The raw Google refresh token is encrypted at rest and never returned to clients. `GET /auth/google/connection` returns `{status, email, scopes, connected_at, last_refresh_at, reauth_reason}` with status `active`, `reauth_required`, or `disconnected`. App Go code calls `auth.GoogleAccessToken(ctx, scopes...)` for request-authenticated work or `auth.GoogleAccessTokenForUser(ctx, userID, scopes...)` from workers; requested scopes must be present in `auth.google_oauth.allowed_scopes`. Expired access tokens refresh under a Postgres row lock; permanent Google revocation marks the connection `reauth_required` and returns `google_reauth_required`, while missing grants return `google_scope_missing`.
- Email delivery is a pluggable `auth.EmailSender`; the default sender is a no-op.
- `/users/dev-bootstrap` is local-only. Without `dev_bootstrap.default_user_email`, it can mint a development token without opening PostgreSQL. With `default_user_email`, it opens standard auth lazily and creates the configured default tenant, verified user, and owner membership on first use when missing.
- DB-backed auth endpoints require a database URL from `auth.database_url_env`, `DATABASE_URL`, or `SCENERY_AUTH_DATABASE_URL`.

Implemented `up --json` rules:

```text
scenery up --json
```

- output is JSONL
- each line conforms to `scenery.run.event.v1`
- human-readable console output is suppressed in this mode
- child stdout/stderr are emitted as structured `process.output` events instead of raw terminal writes

Implemented `check --json` rules:

```text
scenery check --json
```

- output is a single JSON document
- output conforms to `scenery.check.result.v1`
- success returns `ok: true` and an empty `diagnostics` array
- failure returns `ok: false` and structured diagnostics
- diagnostics may include `stage`, `file`, `line`, `column`, `severity`, `message`, and `suggested_action`

Implemented `harness --json` rules:

```text
scenery harness --json
scenery harness --json --write
```

- output is a single JSON document
- output conforms to `scenery.harness.result.v1`
- it composes `scenery check --json` and the stable `scenery inspect ... --json` surfaces
- success returns `ok: true`
- failure returns `ok: false`, per-step errors, diagnostics, and `next_actions`
- failed and expensive steps include `evidence` conforming to `scenery.harness.artifact.v1`
- `--write` persists the same result to `.scenery/harness/latest.json`
- `--write` persists large evidence payloads under `.scenery/harness/artifacts/<run-id>/`
- `--with-validation` and `--with-validation=<profile>` run app validation after the core harness and add a small `validation` pointer with `profile`, `ok`, and `result_path`; the validation result itself stays in `.scenery/harness/validation/latest.json`

Implemented `harness self` JSON rules:

```text
scenery harness self --summary
scenery harness self --json
scenery harness self --json=summary
scenery harness self --json=full
scenery harness self --summary --write
scenery harness self --json --write
```

- `--summary`, `--json`, and `--json=summary` output a single compact JSON document conforming to `scenery.harness.self.summary.v1`
- `--json=full` outputs the full archive JSON document conforming to `scenery.harness.self.v1`
- summary output is the agent-facing default and must reference artifacts instead of embedding full drift inventories, successful stdout/stderr tails, complete timing package lists, or full large-file lists
- green summary output should stay under 12 KB; failed summary output should stay under 32 KB while preserving the first actionable failure and artifact references
- it validates the scenery repo itself instead of a target app
- it runs docs knowledge validation, `scenery inspect docs --json`, architecture checks, UI static architecture checks, Go package tests, parallel dev-session safety, dashboard UI typecheck/build, UI freshness checks, worktree-local `go build -o .scenery/harness/bin/scenery ./cmd/scenery`, and local binary freshness checks
- it validates committed examples for every edition-2027 JSON schema, runs the Bun TypeScript client conformance suite, and typechecks both committed native and mixed/House generated clients against the shared generated-client configuration
- self-harness Go test steps discover the complete `./...` graph, reuse linked test binaries by Go build ID, and execute every test body with `-test.count=1`. The cache never reuses test results. Packages without tests remain represented in JSON evidence.
- cached and `--fresh-tests` lanes have the same fresh execution semantics. The flag retains the explicit fresh timing-lane label; both lanes use package parallelism three, selected by repeated measurement on the maintainer machine.
- linked binaries, the workspace manifest, and package timing estimates are disposable under `.scenery/harness/test-binaries/`. The manifest covers toolchain/build environment and tracked/untracked workspace contents. Disposable test binaries disable VCS stamping so committing unchanged contents does not relink the repository.
- `.scenery/harness/test-timing-latest.json` identifies the timing lane. Cached and fresh runs use a five-second advisory budget and target; release runs keep the 30-second enforced budget and five-second optimization target.
- package and test hotspots are not reported from contended full-suite timing alone. Packages over their budget are rerun once through one serial `-p 1` confirmation process; tests observed at or above 500ms are rerun three times and reported only when the isolated median remains over budget. Same-package test candidates share one serial `-parallel=1` confirmation process. The default package budget is two seconds, with an explicit five-second baseline for `scenery.sh/cmd/scenery`.
- `total_seconds` covers the full-suite command only. `confirmation_seconds` records the extra isolated confirmation work; observed candidates and confirmed slow tests are stored separately.
- the default, race, and release self-harness modes exercise parallel managed Postgres dev sessions and tear the temporary state down. `--quick` intentionally skips the heavier live-runtime checks.
- the default self-harness storage probe exercises configured storage through an app task, storage CLI import/export, and a live local-backend app route. The restart proof writes an object through the app route, stops the dev runtime with `scenery down`, restarts it, and reads the same fsync'd object back through the app route.
- agents must not run `go install ./cmd/scenery` unless a human explicitly requests updating the shared installed `scenery` binary; multiple worktrees may otherwise overwrite each other's CLI
- architecture checks fail on unapproved direct dependencies, forbidden framework imports, CLI package boundary violations, missing generated/vendored ignore markers, and non-generated source/code files over 2500 lines; Markdown docs are not subject to line-count size checks
- architecture checks warn on non-generated source/code files over 1000 lines, cgo imports, `.DS_Store` artifacts, and compatibility imports outside known migration paths; unchanged warnings outside the changed area are debt summary in compact output, not agent attention
- local harness/report artifacts matching `.scenery/**`, `coverage/**`, `test-results/**`, `*.harness*.json`, or `scenery-harness-self-*.json` are reported as ignored local artifacts and do not drive changed-area recommended commands
- UI static architecture checks fail on raw shadcn install scripts, non-`@scenery` registries, unsafe registry item source/target declarations, legacy `components/ui` imports, direct vendor shadcn imports from screens, and direct Radix/styling utility imports outside scenery primitives/layouts/vendor
- UI static architecture checks scan multiline imports, re-exports, dynamic imports, and CommonJS requires for forbidden UI boundary bypasses
- UI static architecture checks warn on long or advanced `className` literals and common expression forms such as `cn(...)`, template literals, and conditional literals outside scenery primitives/layouts/vendor while the dashboard is migrated into the stricter slot-layout model
- `scenery harness ui --json` is not part of the default self-harness path. It needs a local Chrome/Chromium-compatible browser and is intended for explicit dashboard route validation. The route journeys cover dashboard home app selector/status, API Explorer endpoint/form behavior, service catalog metadata, traces empty/table/detail behavior, DB list or unavailable states, cron status/empty states, and durable/worker status cards.
- `--write` persists the full archive to `.scenery/harness/self-latest.json`, the compact summary to `.scenery/harness/self-summary-latest.json`, and topic artifacts such as `.scenery/harness/test-timing-latest.json`
- failed and expensive steps include `evidence` conforming to `scenery.harness.artifact.v1`; Go test JSONL evidence is written as `.scenery/harness/artifacts/<run-id>/go-test.jsonl` when `--write` is present
- `--write` refreshes `.scenery/harness/agent-context.json` as the one-file agent handoff. It includes current failing steps, first files to read, exact rerun commands, changed-area recommended commands, relevant active ExecPlans, recent failed harness artifacts, docs freshness, and risk classifications: `runtime`, `CLI contract`, `dashboard`, `schema`, `release`, and `onlv-impacting`.

Default agent loop:

```text
scenery doctor --json
scenery harness self --quick --summary --write
cat .scenery/harness/agent-context.json
# implement
scenery harness self --summary --write
```

Release-risk loop:

```text
scenery harness self --release --summary --write
scripts/release-gate.sh
```

Implemented `inspect harness` rules:

```text
scenery inspect harness --json
scenery inspect harness --json --app-root <path>
scenery inspect harness --json --repo-root <path>
scenery inspect harness artifact test-timing --json
scenery inspect harness diagnostics --severity warning --json
scenery inspect harness timing --top 10 --json
```

- manifest output conforms to `scenery.inspect.harness.v1`
- focused outputs use the same schema version and return bounded topic-specific JSON for artifacts, diagnostics, and timing
- from an app root, manifest output reports `.scenery/harness/latest.json`, `.scenery/harness/ui/latest.json`, and `.scenery/harness/artifacts/`
- from the scenery repo root, manifest output reports `.scenery/harness/self-latest.json`, `.scenery/harness/self-summary-latest.json`, `.scenery/harness/ui/latest.json`, and `.scenery/harness/artifacts/`
- focused artifact output reads known `.scenery/harness/*-latest.json` files by name (`self-harness`, `self-summary`, `toolchain`, `changed-area`, `drift`, `test-timing`, `fixture-matrix`, `schema-validation`, `agent-context`)
- diagnostics output caps returned diagnostics at 50 and supports `--severity error|warning`
- timing output reads `.scenery/harness/test-timing-latest.json`, sorts slow packages/tests by duration, and caps both lists with `--top`
- manifest output reads latest harness outputs when present and returns their normalized `artifacts` and `evidence` arrays
- evidence records use `scenery.harness.artifact.v1` and include `command`, `cwd`, `started_at`, `duration_ms`, `exit_code`, output tails, artifact references, and `repro_command`

Release gate:

```text
scripts/release-gate.sh
```

- this is the high-signal pre-release gate, not the normal inner-loop developer check
- it runs the Scenery repo release checks only: Go tests, race tests, lint, dashboard UI typecheck/build, dashboard UI embed generation, worktree-local binary freshness checks, self-harness, clean-checkout install, fixture runtime smoke, optional generic external app smoke, public-router safety, production secrets checks, and artifact hygiene checks
- release-gate logs should use the same `scenery.harness.artifact.v1` evidence shape for failed or expensive steps
- `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` may point at a read-only scenery app for the optional external app smoke
- `SCENERY_RELEASE_GATE_LOG_DIR` may override the log directory; otherwise logs are written under `.scenery/release-gate/`
- the release gate must not create or modify client-application worktrees; client-app validation belongs in that app's own repo and app-local gates
- artifact hygiene is intentionally strict and fails on local release artifacts such as `.DS_Store` and `__MACOSX`

Implemented `logs --jsonl` rules:

```text
scenery logs --jsonl
scenery logs --json
```

- `--json` is an alias for `--jsonl`
- output is JSONL
- each line conforms to `scenery.dev.event.v1`
- one JSON object is emitted per VictoriaLogs-backed structured dev event
- structured events include app id/root, session id, source id/kind/name/role/pid/stream/status, level, message, parsed fields, raw output, and parse metadata
- structured dev events are assigned a stable integer ID before export to VictoriaLogs
- human-readable raw output remains the default when neither flag is used

Implemented `traces clear --json` rules:
- output conforms to `scenery.traces.clear.v1`
- trace clearing is dev/admin beta for v0; its existence does not make cron, trace clearing, or queue deletion semantics stable

## Artifact Locations

### Current implemented locations

Use `scenery inspect paths --json` as the source of truth.

Today scenery uses:
- app config: `<app-root>/.scenery.json` or `<app-root>/.config.json`
- cache root:
  - `$SCENERY_DEV_CACHE_DIR`, if set
  - otherwise OS user cache + `/scenery`
- build workspace: `<cache-root>/build/<sanitized-app-name>-<hash>`
- built app binary: `<workspace>/scenery-app`
- build state: `<workspace>/.scenery-build-state.json`

### Repo-Local Cache Locations

Implemented now:

```text
<app-root>/.scenery/
  gen/
    app.json
    routes.json
    services.json
    endpoints.json
    manifest.json
  build/
    latest.json
    vnext/
      <go-target>.json
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
- Use `scenery inspect ... --json` for app, route, service, endpoint, build, path, docs, generator, durable, and storage metadata. Use `scenery traces list --json` and `scenery metrics list --json` for local observability metadata.
- Do not read `.scenery/gen/*` directly unless debugging scenery generation. These files are internal cache artifacts that may mirror inspect output today, but they are not the supported API.
- `models.json` and `views.json` are internal caches for `scenery inspect models --json` and `scenery inspect views --json`. Generated static-model schema, seed, and web package files under `.scenery/gen/db/` and `.scenery/gen/web/` are disposable generator outputs; integrate through `scenery generate data --dry-run --json`, `scenery inspect generators --json`, `scenery inspect models --json`, and `scenery inspect views --json` rather than reading cache files directly.
- `manifest.json` ties generated cache artifacts to schema versions, artifact paths, and deterministic content hashes for debugging generation.
- Agent/global dashboard state uses `<dashboard-cache-root>/devdash.json` for compact control-plane records and `<dashboard-cache-root>/app-model/<metadata|api-encoding>/sha256/<hash>.json` for large app-model blobs. The agent dashboard process is the global dashboard-store writer; other agent-backed runtime processes mutate it through the internal dashboard control-plane endpoint. Treat these files as internal cache artifacts; use dashboard APIs and CLI JSON instead of reading them directly.
- Use `scenery inspect build --json` for build metadata. `build/latest.json` is a local cache pointer to the latest prepared or compiled build workspace.
- Edition-2027 `build/vnext/<go-target>.json` is the exact runtime-bundle descriptor for the latest local build of that target. Treat it as build output, not a contract source; distribute the copied `<binary>.scenery.runtime-bundle.v1.json` sidecar with an explicit binary output.
- Use `scenery harness --json` for framework app-model proof, `scenery validate <profile> --json` for app-owned quality gates, and `scenery harness self --summary` for scenery repo validation. `harness/latest.json`, `harness/validation/latest.json`, `harness/self-latest.json`, and `harness/self-summary-latest.json` are local snapshots written by `--write`; `--json=full` is the explicit full archive stdout mode.
- Future implementation should keep cache paths predictable for debugging, but external tools and agents should integrate through command JSON output.

## JSON Schemas

Implemented now:
- [scenery.approval-token.v1.schema.json](schemas/scenery.approval-token.v1.schema.json)
- [scenery.approval-trust.v1.schema.json](schemas/scenery.approval-trust.v1.schema.json)
- [scenery.change-plan.v1.schema.json](schemas/scenery.change-plan.v1.schema.json)
- [scenery.change-receipt.v1.schema.json](schemas/scenery.change-receipt.v1.schema.json)
- [scenery.cli.v1.schema.json](schemas/scenery.cli.v1.schema.json)
- [scenery.client-selection.v1.schema.json](schemas/scenery.client-selection.v1.schema.json)
- [scenery.deployment-plan.v1.schema.json](schemas/scenery.deployment-plan.v1.schema.json)
- [scenery.deployment-receipt.v1.schema.json](schemas/scenery.deployment-receipt.v1.schema.json)
- [scenery.generated.v1.schema.json](schemas/scenery.generated.v1.schema.json)
- [scenery.go-build-input-manifest.v1.schema.json](schemas/scenery.go-build-input-manifest.v1.schema.json)
- [scenery.legacy-bridge-generated.v1.schema.json](schemas/scenery.legacy-bridge-generated.v1.schema.json)
- [scenery.manifest.v1.schema.json](schemas/scenery.manifest.v1.schema.json)
- [scenery.migrate.status.v1.schema.json](schemas/scenery.migrate.status.v1.schema.json)
- [scenery.package-generated.v1.schema.json](schemas/scenery.package-generated.v1.schema.json)
- [scenery.runtime-bundle.v1.schema.json](schemas/scenery.runtime-bundle.v1.schema.json)
- [scenery.typescript-client-generated.v1.schema.json](schemas/scenery.typescript-client-generated.v1.schema.json)
- [scenery.inspect.app.v1.schema.json](schemas/scenery.inspect.app.v1.schema.json)
- [scenery.inspect.routes.v1.schema.json](schemas/scenery.inspect.routes.v1.schema.json)
- [scenery.inspect.services.v1.schema.json](schemas/scenery.inspect.services.v1.schema.json)
- [scenery.inspect.endpoints.v1.schema.json](schemas/scenery.inspect.endpoints.v1.schema.json)
- [scenery.inspect.models.v1.schema.json](schemas/scenery.inspect.models.v1.schema.json)
- [scenery.inspect.views.v1.schema.json](schemas/scenery.inspect.views.v1.schema.json)
- [scenery.inspect.traces.v1.schema.json](schemas/scenery.inspect.traces.v1.schema.json)
- [scenery.inspect.metrics.v1.schema.json](schemas/scenery.inspect.metrics.v1.schema.json)
- [scenery.inspect.observability.v1.schema.json](schemas/scenery.inspect.observability.v1.schema.json)
- [scenery.logs.query.v1.schema.json](schemas/scenery.logs.query.v1.schema.json)
- [scenery.logs.tail.entry.v1.schema.json](schemas/scenery.logs.tail.entry.v1.schema.json)
- [scenery.help.v1.schema.json](schemas/scenery.help.v1.schema.json)
- [scenery.down.v1.schema.json](schemas/scenery.down.v1.schema.json)
- [scenery.metrics.query.v1.schema.json](schemas/scenery.metrics.query.v1.schema.json)
- [scenery.metrics.labels.v1.schema.json](schemas/scenery.metrics.labels.v1.schema.json)
- [scenery.metrics.series.v1.schema.json](schemas/scenery.metrics.series.v1.schema.json)
- [scenery.inspect.docs.v1.schema.json](schemas/scenery.inspect.docs.v1.schema.json)
- [scenery.docs.index.v1.schema.json](schemas/scenery.docs.index.v1.schema.json)
- [scenery.inspect.build.v1.schema.json](schemas/scenery.inspect.build.v1.schema.json)
- [scenery.inspect.paths.v1.schema.json](schemas/scenery.inspect.paths.v1.schema.json)
- [scenery.inspect.generators.v1.schema.json](schemas/scenery.inspect.generators.v1.schema.json)
- [scenery.inspect.durable.v2.schema.json](schemas/scenery.inspect.durable.v2.schema.json)
- [scenery.durable.worker_token.create.v1.schema.json](schemas/scenery.durable.worker_token.create.v1.schema.json)
- [scenery.durable.jobs.v1.schema.json](schemas/scenery.durable.jobs.v1.schema.json)
- [scenery.db.apply.result.v1.schema.json](schemas/scenery.db.apply.result.v1.schema.json)
- [scenery.db.seed.result.v1.schema.json](schemas/scenery.db.seed.result.v1.schema.json)
- [scenery.db.setup.result.v1.schema.json](schemas/scenery.db.setup.result.v1.schema.json)
- [scenery.db.list.v3.schema.json](schemas/scenery.db.list.v3.schema.json)
- [scenery.db.server.status.v1.schema.json](schemas/scenery.db.server.status.v1.schema.json)
- [scenery.task.list.v1.schema.json](schemas/scenery.task.list.v1.schema.json)
- [scenery.task.inspect.v1.schema.json](schemas/scenery.task.inspect.v1.schema.json)
- [scenery.task.graph.v1.schema.json](schemas/scenery.task.graph.v1.schema.json)
- [scenery.inspect.validation.v1.schema.json](schemas/scenery.inspect.validation.v1.schema.json)
- [scenery.validation.list.v1.schema.json](schemas/scenery.validation.list.v1.schema.json)
- [scenery.validation.inspect.v1.schema.json](schemas/scenery.validation.inspect.v1.schema.json)
- [scenery.validation.graph.v1.schema.json](schemas/scenery.validation.graph.v1.schema.json)
- [scenery.validation.plan.v1.schema.json](schemas/scenery.validation.plan.v1.schema.json)
- [scenery.validation.result.v1.schema.json](schemas/scenery.validation.result.v1.schema.json)
- [scenery.traces.clear.v1.schema.json](schemas/scenery.traces.clear.v1.schema.json)
- [scenery.gen.manifest.v1.schema.json](schemas/scenery.gen.manifest.v1.schema.json)
- [scenery.build.latest.v1.schema.json](schemas/scenery.build.latest.v1.schema.json)
- [scenery.run.event.v1.schema.json](schemas/scenery.run.event.v1.schema.json)
- [scenery.check.result.v1.schema.json](schemas/scenery.check.result.v1.schema.json)
- [scenery.harness.result.v1.schema.json](schemas/scenery.harness.result.v1.schema.json)
- [scenery.harness.self.v1.schema.json](schemas/scenery.harness.self.v1.schema.json)
- [scenery.harness.self.summary.v1.schema.json](schemas/scenery.harness.self.summary.v1.schema.json)
- [scenery.dev.event.v1.schema.json](schemas/scenery.dev.event.v1.schema.json)
- [scenery.logs.event.v1.schema.json](schemas/scenery.logs.event.v1.schema.json)
- [scenery.version.v1.schema.json](schemas/scenery.version.v1.schema.json)
- [scenery.doctor.result.v1.schema.json](schemas/scenery.doctor.result.v1.schema.json)
- [scenery.deploy.registry.v1.schema.json](schemas/scenery.deploy.registry.v1.schema.json)
- [scenery.deploy.status.v1.schema.json](schemas/scenery.deploy.status.v1.schema.json)
- [scenery.toolchain.v1.schema.json](schemas/scenery.toolchain.v1.schema.json)
- [scenery.toolchain.status.v1.schema.json](schemas/scenery.toolchain.status.v1.schema.json)
- [scenery.storage.inspect.v1.schema.json](schemas/scenery.storage.inspect.v1.schema.json)
- [scenery.storage.object.v1.schema.json](schemas/scenery.storage.object.v1.schema.json)
- [scenery.storage.list.v1.schema.json](schemas/scenery.storage.list.v1.schema.json)
- [scenery.storage.delete.v1.schema.json](schemas/scenery.storage.delete.v1.schema.json)

Schema rules:
- top-level schema field is `schema_version`
- schema names are versioned strings like `scenery.inspect.app.v1`
- additive fields are allowed in future versions only by introducing a new schema version when needed
- consumers should match on `schema_version`, not on command name alone

## Examples

### `scenery inspect app --json`

```json
{
  "schema_version": "scenery.inspect.app.v1",
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

### `scenery inspect build --json`

```json
{
  "schema_version": "scenery.inspect.build.v1",
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

### `scenery inspect endpoints --json`

```json
{
  "schema_version": "scenery.inspect.endpoints.v1",
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

Generated model CRUD endpoints include `"generated": true`. Handwritten endpoints omit the field.

### `scenery traces list --json`

Beta diagnostic subject. Use this when an agent needs concrete local traces
without scraping the dashboard UI. The JSON shape is versioned, but retention,
backend preference, span reconstruction, and clear semantics may change before
this is promoted to stable v0.

Example:

```text
scenery traces list --json --endpoint SyncGet --min-duration-ms 2000 --since 1h --slowest
```

Example output:

```json
{
  "schema_version": "scenery.inspect.traces.v1",
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
    "endpoint": "SyncGet",
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
      "service": "sync",
      "endpoint": "SyncGet",
      "started_at": "2026-04-27T13:00:00Z",
      "duration_ms": 2310,
      "duration_nanos": 2310000000
    }
  ]
}
```

### `scenery metrics list --json`

Beta diagnostic subject. Use this when an agent needs a metrics-style rollup
over locally captured traces and logs. The JSON shape is versioned, but rollup
definitions, percentile calculations, default limits, and Victoria source
selection may change before this is promoted to stable v0.

Example:

```text
scenery metrics list --json --service sync --since 15m
```

Example output:

```json
{
  "schema_version": "scenery.inspect.metrics.v1",
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
    "service": "sync",
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

### `scenery inspect observability --json`

Beta diagnostic subject. Use this before ad hoc observability queries when an
agent needs to know whether the local Victoria backends are reachable and which
scope will be enforced.

Example:

```text
scenery inspect observability --json
```

The response uses `scenery.inspect.observability.v1` and includes `scope`,
`backends.logs`, `backends.metrics`, `backends.traces`, examples, and optional
warnings. Raw backend URLs are exposed only under the optional `debug.base_urls`
object for intentional substrate debugging.

### `scenery logs query --json`

Beta query surface for scoped VictoriaLogs LogsQL. This is the preferred CLI
path for targeted log debugging when plain `scenery logs --jsonl` is too broad.

Example:

```text
scenery logs query --json --since 15m --limit 100 --query 'error OR panic'
```

The response uses `scenery.logs.query.v1`, echoes the selected scope and query
bounds, and returns normalized entries with `time`, `level`, `source`,
`message`, `fields`, `trace_id`, `span_id`, and `raw` where available. Passing
`--jsonl` writes only log entries as JSON Lines. `scenery logs tail --jsonl`
emits one `scenery.logs.tail.entry.v1` object per line and uses `--since` as the
VictoriaLogs live-tail `start_offset`.

### `scenery metrics query --json`

Beta query surface for scoped PromQL/MetricsQL. Range queries are the default;
`--instant` uses the instant query endpoint.

Example:

```text
scenery metrics query --json --since 15m --step 5s --promql 'max_over_time(scenery_request_duration_seconds[15m])'
```

The response uses `scenery.metrics.query.v1`, echoes scope and bounds, reports
the backend `result_type`, and returns normalized metric series and samples.
`scenery metrics labels --json --since 1h --match 'scenery_request_duration_seconds'` emits `scenery.metrics.labels.v1`.
`scenery metrics series --json --match 'scenery_request_duration_seconds'` emits
`scenery.metrics.series.v1`.

### `scenery inspect docs --json`

Use this when an agent needs to understand the repo knowledge base before making changes.

Source files:

- [docs/index.md](index.md)
- [docs/knowledge.json](knowledge.json)
- [docs/plans/active.md](plans/active.md)
- [docs/plans/completed.md](plans/completed.md)
- [docs/tech-debt.md](tech-debt.md)

Example:

```text
scenery inspect docs --json
```

Example output:

```json
{
  "schema_version": "scenery.inspect.docs.v1",
  "repo": {
    "root": "/repo/scenery",
    "module_path": "scenery.sh",
    "go_mod_path": "/repo/scenery/go.mod"
  },
  "summary": {
    "document_count": 9,
    "missing_count": 0,
    "review_due_count": 0,
    "stale_count": 0,
    "agent_scope_count": 1,
    "stale_child_index_entry_count": 0,
    "missing_child_index_entry_count": 0,
    "quality": {
      "A": 4,
      "B": 5
    }
  },
  "agents": {
    "scopes": [
      {
        "path": "AGENTS.md",
        "scope": "."
      }
    ],
    "child_index_path": "AGENTS.md#child-agent-index",
    "child_index_entries": [],
    "stale_child_index_entries": [],
    "missing_child_index_entries": []
  },
  "documents": [
    {
      "path": "docs/local-contract.md",
      "title": "scenery Local Contract",
      "owner": "scenery runtime",
      "status": "active",
      "quality": "A",
      "freshness": "current",
      "last_reviewed": "2026-04-27",
      "review_after": "2026-05-27",
      "summary": "Frozen local developer and agent-facing contract.",
      "tags": ["contract", "cli", "agents", "schemas"],
      "exists": true,
      "review_due": false,
      "stale": false
    }
  ],
  "plans": {
    "active": {
      "path": "docs/plans/active.md",
      "exists": true
    },
    "completed": {
      "path": "docs/plans/completed.md",
      "exists": true
    }
  },
  "tech_debt": {
    "path": "docs/tech-debt.md",
    "exists": true
  }
}
```

The `agents` object reports every discovered `AGENTS.md` scope, compares child
scopes against the root `AGENTS.md` Child Agent Index, and reports stale index
entries plus discovered child scopes that are missing from the index.

### `scenery inspect harness --json`

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
scenery inspect harness --json
scenery inspect harness artifact test-timing --json
scenery inspect harness diagnostics --severity warning --json
scenery inspect harness timing --top 10 --json
```

Example output:

```json
{
  "schema_version": "scenery.inspect.harness.v1",
  "scope": "repo",
  "root": "/repo/scenery",
  "latest": [
    {
      "name": "self-harness",
      "path": ".scenery/harness/self-latest.json",
      "schema_version": "scenery.harness.self.v1",
      "exists": true
    }
  ],
  "evidence": [
    {
      "schema_version": "scenery.harness.artifact.v1",
      "command": ["go", "test", "-json", "./..."],
      "cwd": "/repo/scenery",
      "started_at": "2026-06-07T20:45:00Z",
      "duration_ms": 1234,
      "exit_code": 1,
      "stdout_tail": "{\"Action\":\"fail\"}",
      "artifacts": [
        {
          "name": "go-tests-stdout",
          "path": ".scenery/harness/artifacts/20260607T204500Z/go-test.jsonl",
          "schema_version": "go.test.jsonl"
        }
      ],
      "repro_command": "cd /repo/scenery && go test -json ./..."
    }
  ]
}
```
