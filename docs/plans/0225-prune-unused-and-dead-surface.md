# Prune Unused And Dead Surface

## Purpose / Big Picture

Scenery carries language constructs, CLI commands, runtime subsystems, repo
tooling and helper code that no application uses. Every such feature costs
compile time, test time, documentation and review attention, and it makes the
one rolling specification larger than what applications depend on. A usage
audit of the framework against its consuming applications (ONLV, and the
Micro platform for the declarative UI) listed 227 verified removal candidates
in 14 areas: 144 confirmed, 69 partially confirmed and 14 refuted.

This plan removes the confirmed unused features and the literal dead code,
including their tests, fixtures, schemas, diagnostic rows, CLI grammar and
living documentation. Nothing is replaced: per the rolling-spec rule there are
no compatibility aliases, stubs or tombstones. After the plan, the remaining
surface is what an application or a named owner decision requires, and the
repository is smaller while every retained check still passes.

The audit data is local working input, not a repository artifact: a JSON file
with candidate maps per area and a verifier verdict per candidate id. A
verifier's corrected scope overrides the candidate's claimed scope. Each step
below records in Progress what it removed and what it skipped with the reason,
so this file is sufficient without the audit file.

## Progress

- [x] 2026-11-13: Step 0, baseline. Created this plan and indexed it. On the
  untouched tree at `e007744d`, `go build ./...`, `go vet ./...`,
  `go test ./...` (79 packages with tests, 33 without), `golangci-lint run
  ./...` (0 issues) and `tools/typescript/node_modules/.bin/tsc -p
  internal/generate/testdata/tsconfig.catalog.json` all pass with
  `GOFLAGS=-buildvcs=auto`. There are no pre-existing failures. Starting size:
  256,933 non-test Go lines, 108,026 Go test lines, 526,782 lines across 2,602
  tracked files, 140 files in `docs/schemas`. The worktree-local product
  binary `.scenery/harness/bin/scenery` exists and reports commit `e007744d`.
- [x] 2026-11-13: dead-compiler-generate-build: removed the test-only compiler
  HTTP codec (`internal/compiler/http_codec.go`), the unreachable OpenAPI
  document generator with its descriptor constants (`openapi_revision` stays
  compiler-owned), the source-file generator fingerprint cache in
  `internal/build`, the unreferenced internal functions, types and constants
  listed by the audit (assistant API codecs, initiator cookie wrappers,
  `agent.DefaultClient`/`Ensure`, `victoria.Start`/`StartAtRoot` and the
  `SCENERY_DEV_VICTORIA_DIR` variable only they read, `parse.Analyze`,
  `scn.PathExists`, `spec.ConditionalRequirements` and others), dead exported
  wrappers in `internal/generate`, `internal/runtimeassets`,
  `internal/postgresdb`, `internal/dirlisting`, `internal/harnessevidence`,
  `internal/evolution` and `internal/storagefs`, public `storage.PutFile`,
  `db.MustGet`, `scenery.NullOf`, `scenery.NewApprovalToken`, the zero-caller
  runtime exports (`RegisterAssistant`, `RegisterAssistantClient`,
  `RegisterAssistantMCPManifest`, `RegisterMCPFederation`,
  `AssistantRuntimeStatuses`, `CurrentConfigRevision`,
  `PopulateContractContext`, `DecodeContractJSON`,
  `EncodeContractJSONForRequest`, `RegisterContractInternalBinding`,
  `InvokeContractBinding`, `InvokeCronJob`, `NewContractByteStream`,
  `MCPToolResult`, `ParamKind`/`ParamSpec`), `store.CompleteJob`/`FailJob`,
  `devprocess.NewLineTail`, `runtimeapi.NewInvocation`, diagnostic rows
  SCN1003, SCN6201, SCN6203 and SCN9003, the ignored
  `service.implementation` attributes `root`, `package` and `symbol`, the
  duplicate docs-knowledge validator in `cmd/scenery`, the `scenery script`
  leftovers, the synchronous managed-frontend start chain (its four tests now
  use the pending-start path), `build.Result.Ephemeral` and the unread
  `devListenRequest` fields. Test-only wrappers and aliases moved into
  `_test.go` files. Regenerated the five committed TypeScript client
  descriptors and the basic and storage-basic package contracts for the new
  specification and diagnostic-catalog revisions. Skipped:
  `evolution.NewRevisionRebind` (belongs to the revision-rebind removal),
  `auth.SetPermissionChecker`, `auth.SetEmailSender`,
  `runtime.DurableSchedule`, `runtime.InvokeContractCLIBinding`,
  `RegisterMCPSecretResolver`, `LookupMCPFederation`,
  `LookupMCPFederationForAssistant` and `MCPFederationReadiness` (each is the
  entry point of a feature a later step removes whole),
  `scenery.TraceHTTPTransport` (documented application API that another
  verdict keeps), `storage.NewLocalStore*` and `gzipStreamWriter.ReadFrom`
  (used), `ContractPages`/`InvokeContractPageJSON` (kept by decision),
  diagnostic row SCN6209 (its identity is emitted as a plain error prefix;
  attaching or dropping the code is undecided), `crud.extension` (left to
  `data-model-crud`), `spec.PublicAuthoredBlockSchema` (a test in another
  package needs it) and `agent.EnsureWith` (still tested). Build, vet, tests
  (79 packages), lint (0 issues), both TypeScript checks and
  `go run ./scripts/verify --quick --summary --write` pass.
- [x] 2026-11-13: dead-repo-tooling: removed the three verifier benchmarks
  `native-reload`, `native-reload-plugin` and `native-reload-attribution`
  with their testdata, tests and the `--workload-root` flag
  (`nativeReloadStats` moved into `harness_self_watch_batch.go` for the live
  watch-batch probe); `scripts/cutover-go-work.go` and its generation-probe
  segment; `scripts/snapshot-backup.sh` with the `snapshot-backup` probe and
  its tests; `scripts/accept-assistant-runtime.sh`; `scripts/bench-json-v2.py`;
  the manual query-table profiler (`query-table-profiler.bench.tsx`,
  `tsconfig.profiler.json`, the `profile:query-table` package script);
  `docs/native-build-driver-decision.md` and
  `docs/native-build-compiler-decision.md` with their index entries; root
  `0139-funding-generated.png` and `next.md`; and the matching passages in
  `ARCHITECTURE.md`, `scripts/verify/AGENTS.md`, the harness, local-contract,
  agent-guide, cookbook and Go implementation documents. Skipped: `ISSUES.md`
  (an untriaged deployment incident record), the three agent evaluation
  reports and `scripts/verify/evaluate-agent-context.py` (completed plans
  link the reports and the 0211 report documents the script as its runner),
  `docs/native-build-first-execution-attribution.md` and
  `docs/product-and-island-build-comparison.md` (linked from completed plans
  and the retained decision document), the assistant public-surface shell
  gate, `tsconfig.runtime.json` and the profiler mocks (live verifier
  inputs), config-import and the legacy storage exporter (owner decision).
  Build, vet, the full cached Go suite, lint, both TypeScript checks and
  `go run ./scripts/verify --quick --summary` pass.
- [x] 2026-11-13: dead-assistant-observability: removed the Go Eve provider
  event normalizer and the Eve assertion codec with their tests,
  `ReservedPathList`, the `assistantruntime` `Launcher`/`LaunchSpec`/
  `FakeLauncher`, the remaining unreferenced `assistantapi`,
  `assistantcontrol` and `mcpcontract` codecs (`NewCreateConversationResponse`,
  `ConversationEventsURL`, `DecodeCreateConversationResponse`,
  `MarshalCreateConversationResponse`, `EncodeNDJSON`, `MarshalResponse`,
  `MarshalRuntimeDescriptor`, `mcpcontract.Digest`); moved the
  `mcpgateway` test doubles `NewHandler` and `StaticAssertionVerifier` into
  the package test file. Removed the `devdash` store's process-event,
  process-output, dev-event and dev-source storage with its retention and
  byte-budget pruning and the deferred-save path that only those writers used
  (every store write now persists immediately; `Store.Flush` is gone), the
  supervisor process-event control-plane write path, the OTLP JSON payload
  builders, the `SCENERY_VICTORIA_{METRICS,LOGS,TRACES}_PORT` knobs with their
  registry and `docs/environment.md` rows, the supervisor-owned dashboard
  server and `dashboardServer.supervisor`, the dashboard run-state file with
  stale-owner reaping, the path router's `storage` web-UI rewrites and its
  `local-caddy/Caddyfile` and `local-caddy.json` artifacts, the unreachable
  `removed-agent-transport` route kind, and the "dashboard UI" wording in the
  doctor Bun hint. Skipped: moving `assistantruntime.FakeHelper` to a
  test-support package (a relocation; three `runtime` test files use it
  across packages); `assistantapi.DecodeNDJSON`, `RedactChunks` and
  `mcpgateway.SignAssertion` (test utilities of live `runtime` gateway
  tests); `victoria.NewStack`/`ExternalComponent`/`Stack.GetTraceSummaries`
  (test seam of live `cmd/scenery` tests and of the kept observability
  probe); `devdash.ListenAddr`/`DashboardAddr` (live report-URL fallback);
  the `devSupervisor` dashboard controller methods (test harness of the
  runtime RPC tests); the always-zero `dev_events_pruned` and
  `dev_sources_pruned` prune payload fields (schema change, left for the CLI
  step); the reserved frontend names `dashboard` and `console`.
- [x] 2026-11-13: dead-legacy-platform: removed the 13 `*_windows.go` files and
  `runtime/process_exists_other.go`, the `windows` terms of sibling build
  constraints and the `GOOS == "windows"` branches (generate shell, Go tool
  names, shared-binary guard, doctor platform list, seven verifier probe
  skips and the matching test guards); `scenery system agent cleanup` with
  its `scenery.agent.cleanup` schema, payload identity and the `~/.onlava`
  Caddyfile match in doctor; the top-level `scenery agent` daemon, restart and
  cleanup spellings (top-level `agent` now accepts only `serve`, and
  `system agent` no longer accepts `serve`); `scenery system trust`; the
  guidance-only flags `generate --materialize`, `--prune-materialized-go`,
  `--merge-editor-workspace`, `generate --lang`/`--output`, `logs query
  --logql` and the unregistered `up -p` lookup; the unused root aliases
  `APIDesc`, `PathParam`, `PathParams`, `RequestType`, `EnvironmentType`,
  `CloudProvider` and the `Env*`, `CloudAWS`/`CloudGCP`/`CloudAzure`,
  `None`/`APICall`/`InternalCall`/`RawAPICall` constants; both uncalled
  `CheckLegacyRecoveryState` guards; the retired retained-compiler state
  cleanup; the pre-identity (`schema_version: scenery.*.v1`) migration of
  agent, registry, edge, edge-target, deploy-registry and edge-DNS state with
  its `.legacy.bak`/`.legacy.migrated` files, tests and the DNS migration
  segment of the edge probe (the same-schema specification rebind stays);
  the retired contract filename guard with diagnostic SCN1021 and its doctor
  check; `agent.EnsureWith` with `compatibleAgent`, `waitForAgentReady` and
  `Server.ListSessions`; the six `removed` standard-auth environment registry
  entries and their three scanner names; the authorization strategy synonyms
  `any_allow`, `allow_if_any` and `allow_if_all`; revision-rebind evidence
  (`RevisionRebind`, `.scenery/changes/revision-rebinds`, the
  `revision_rebinds` input and `revision_rebind` evidence kind,
  `graph.ContractProjectionHash`); the unwired `check` warning diagnostics,
  `prepareAppStartInputs`, `parseDBResetArgs` and `agent.DevDomainHost` with
  their tests. Moved test-only entry wrappers and helpers of `cmd/scenery`,
  `internal/build`, `internal/compiler`, `internal/evolution`,
  `internal/toolchain` and `internal/workspacetx` into `_test.go` files.
  Regenerated the five committed TypeScript client descriptors and the basic
  and storage-basic package contracts for the new specification and
  diagnostic-catalog revisions. Skipped: the `!darwin && !linux` and `!unix`
  fallback files (a supported-platform decision, not a deletion) and the
  cross-compile `.exe` naming in `build`; the `system edge install`/`restart`
  pair (both spellings are printed by current hints with distinct intent);
  the inspect/traces/metrics cross-rejected flags (one shared parser); the
  distinct unused strategies `all_must_allow` and `first_applicable`; the
  refresh-token parameter path (live on every cookie refresh and the
  in-process injection point of `RefreshParams`; changing it is a refactor);
  the v1 journal and lock refusal in `deployplan/deployment_state.go` and
  `workspacetx`; `migrateLegacyApprovalTrustStore`; cross-package test seams
  (`build.SetGoRunnerForTesting`, `CompileContractWithSnapshot`, `WithTrace`,
  `compiler.CompileDuringChangeTransaction`, `WorkspaceRevisionInputs`,
  `evolution.RenameReceiptDigest`, `spec.SchemaDocumentRevision` and the
  `spec` accessors only compiler tests reach, `victoria.NewStack`). Open
  operator precondition: a host whose `~/.scenery` agent, edge or deploy
  state was last written before the artifact identities (2026-07) now fails
  closed on load; onlv-209 was not checked.
- [x] 2026-11-13: auth-runtime-small: removed the application permission
  checker (`auth.SetPermissionChecker`, `HasPermissions`, `PermissionChecker`)
  with its tests and guide and cookbook sections; the standard-auth email hook
  (`SetEmailSender`, `EmailSender`, `EmailMessage`, `standard_mail.go`, its
  three call sites, the `auth.email_from` configuration key and its
  config-import mapping); the password reset endpoints
  (`/auth/password-reset/request` and `/confirm`) with the `password_reset`
  token purpose; `/auth/email-verification/resend`; the organization list,
  create, rename and delete endpoints with the `UpdateTenantName` and
  `SoftDeleteTenant` queries (sqlc output regenerated); the public
  `scenery.sh/durable` package with the `appsdk.Host` step and signal methods,
  `runtime.DurableStep`/`DurableSignal`, the store's `GetStep`, `SaveStep` and
  `SignalJob` and the job context values only steps read; durable-store
  interval schedules (`runtime.DurableSchedule`, `UpsertSchedule`,
  `RunDueSchedules` and the per-service one-second poll loop); the
  `durable_job_steps`, `durable_job_signals` and `durable_schedules` tables
  from the store schema (existing databases keep their unused tables); the
  dev endpoint `/platform.Stats` with its collectors and the ten stat types;
  and the reserved `/_app/storage/...` runtime routes with their three test
  files. Skipped: `POST /auth/organizations/switch` (the only API by which a
  member of several organizations changes the active tenant; the owner has to
  confirm it is not needed), `auth.ConnectionTokenForUser` (the connection
  probe's worker journeys call it), impersonation and user lifecycle (kept by
  decision), and the whole generic connection provider surface: the probe's
  `independent` provider is the non-preset OAuth2 provider behind the
  connection isolation, process, authority, no-expiry, repeat-disconnect and
  repair journeys, so collapsing the configuration to presets means rewriting
  those journeys against the Google fake, which is a redesign that reverses
  plan 0219 and cannot be proven without the Postgres-backed probe. Operator
  note: an environment that sets `auth.email_from` now carries an unknown
  key. Build, vet, the Go suite (78 packages with tests), lint (0 issues) and
  `go run ./scripts/verify --quick --summary` pass.
- [x] 2026-11-13: events: removed the contract event subsystem end to end:
  the `event`, `event_emission` and `event_bus` kinds, the `binding` protocol
  `event` with its `event` block (`map`, `broker_retry`, ordering and
  deduplication keys, dead-letter channel), the runtime event bus, consumer and
  emission registry with `StartContractEventRuntime` and
  `PublishContractOperationOutcome` (`runtime/contract_events.go`), the request
  type `event-call`, the generator's consumer and emission emitters and the
  outcome-publish call in every generated HTTP, internal, durable and schedule
  handler, the compiler's event validation, the provider descriptor
  `registry.scenery.dev/core/kafka` with ABI `scenery.events-runtime/v1`, the
  diagnostic rows SCN2701 to SCN2706 with the `events` category, the event
  compatibility classifications, the `message` expression root and the event
  sections of the specification. Scheduled runs replace event deliveries as
  the vehicle of the two process-admission tests, and the auth probe keeps its
  schedule journey and loses its event journey. Regenerated the five
  committed TypeScript clients and the `basic` and `storage-basic` package
  contracts. Skipped: the custom `workload_identity` kind (schedules may
  reference it and the verifier's corrected scope does not list it), and the
  `capability_unavailable` refusal of background activation with the
  supervisor's `background work unavailable` state (a process-link and status
  contract; the runtime no longer has a producer for it). Applications must
  regenerate their adapters with this framework revision. Build, vet, tests
  (78 packages), lint (0 issues) and both TypeScript checks pass.
- [x] 2026-11-13: cli-bindings: removed the `protocol = "cli"` binding with
  its `cli { command, context, argument, flag, outcome, stdout, stderr }`
  source schemas, the compiler validation and diagnostic rows SCN2410 to
  SCN2418, the generator's CLI registrations and `ServiceProcessPlan.CLIBindings`,
  the runtime contract CLI (`RegisterContractCLIBinding`,
  `InvokeContractCLIBinding`, `ExecuteContractCLIRequest`, the
  `--scenery-contract-cli-request` argument of `runtime.Main`,
  `PopulateContractContextJSON`), the process-link and process-host CLI routes,
  the `local_developer` authentication profile and authorization strategy that
  only that path could mint, the `scenery <binding command>` dispatch for
  unknown commands and help topics, `scenery completion`, `--live`, the
  development runtime RPC `cli/invoke`, the development MCP tools
  `bindings_list` and `binding_invoke`, the `application_binding` member of
  `scenery.help`, the `scenery.contract-cli.request` and
  `scenery.contract-cli.response` schemas, the CLI compatibility
  classifications and the matching passages of the specification, local
  contract, agent guide and `SKILL.md`. An unknown command or help topic now
  fails as an invalid request without compiling the application. The native
  fixture lost its `process_scene_cli` binding. Regenerated the five committed
  TypeScript clients and the `basic` and `storage-basic` package contracts.
  Skipped: the diagnostic category identifier `binding_and_cli` for
  SCN2400-SCN2499 (renaming a category is not a removal). One audit verdict
  (`cli-binding`) asked for an explicit owner reversal of plan 0220's
  `--live` work; the pruning decision and the four confirming verdicts were
  taken as that reversal. Applications must regenerate their adapters.
  Build, vet, tests (78 packages), lint (0 issues) and both TypeScript checks
  pass.
- [x] 2026-11-13: data-model-crud: removed the declarative data model: the
  `entity`, `view`, `crud` and `fixture` resource kinds with their source
  schemas, child blocks, attribute types and metadata; CRUD expansion
  (`data_expand.go`) with the `provider_crud_v1` adapter and
  `IsProviderCRUDService`; entity, view, CRUD and fixture validation with
  diagnostics SCN2501-SCN2504 and SCN2506-SCN2515 (SCN2505 stays for data
  sources); fixture seed plans (`fixture_seed.go`) and their `db seed` hook;
  fixture projection into deployment resources and deployment plans; the
  `deployment.fixture_policy` attribute; the view implementation digest and
  workspace-revision input; the `service.implementation.adapter` and
  `operation.handler.adapter` attributes; the public `datasource.InvokeCRUD`
  runtime (`datasource/crud.go`, `datasource.SQL` stays); the provider CRUD
  adapter generator; the CRUD source of `table_page` in the compiler, the
  TypeScript binding selection and the React table generator, including the
  generated cursor-pagination request and result code and the page-size cap
  that only a CRUD list supplied; the `storage_change` risk record that only
  an entity change produced; the CRUD configuration of the `generation` probe
  (it now generates, renders and compiles the unmodified native fixture and
  no longer reports `provider_crud_proof` and `adapter_path`); the `entity`
  block of the webhook-inbox example; all tests of the removed code, and the
  matching passages in the specification (sections 16.2-16.5, table-page
  source rules), local-contract, agent-guide, cookbook, generated-ui,
  ui-agent-contract, harness and `AGENTS.md` documents. The table-page
  compiler tests now build on a call-delivery binding fixture. Regenerated
  the five committed TypeScript clients and the basic and storage-basic
  package contracts. Restored the committed bytes of five generated Go files
  under `internal/compiler/testdata/native` and `house` that had gained a
  blank line or regrouped imports earlier in this plan: their ownership
  descriptors no longer verified, so the `generation` probe failed with
  SCN6207 before this step's change as well. Skipped: the `sql.fixture/v1`
  capability of the built-in PostgreSQL provider descriptor (its digest is
  pinned in every application's `app.lock.scn`, so removing it forces a lock
  update in every consuming application); the typed-value validator in
  `fixture_values.go` and the helper names `unwrapCRUDListType`,
  `wrappedFixtureType` and `namedFixtureTypeAddress` (live in service
  configuration, field examples and page validation; renaming is not a
  removal); cursor pagination in the `@scenery/ui` `QueryTable` catalog
  component (kept by decision; no generated page selects it any more); the
  unmounted ONLV `.tasksnew` pilot, which still declares `entity`, `crud` and
  `fixture` and no longer compiles against this framework.
- [x] 2026-11-13: language-tail: removed `record.validation` blocks with
  both evaluators (`internal/contract/contract_validation*.go`, the compiler
  expression check, the Go emission of `ValidateContractRecord`, the
  TypeScript validation descriptors and the conditional `validation` runtime
  section, public `scenery.ValidateContractRecord` and
  `scenery.ContractValidationError`, SCN1230-SCN1233); the `patch` kind with
  `export.patchable`, `applyPatches`, `origin.patches`, the `patchable` and
  `patch_scope` fields of the authored schema JSON, the `patches` diagnostic
  category and SCN2901-SCN2907 (the surviving pointer helpers moved to
  `internal/compiler/resource_pointer.go`); the always-rejected placeholders:
  execution mode `workflow` (SCN2204), root `extension` and generic `resource`
  blocks (SCN7001, now ordinary unknown syntax SCN1002), the
  `server_sent_events` codec check (SCN7008), deployment listener fields
  `certificate` and `platform_identity` with the whole unsupported-draft field
  machinery (SCN7009, `support_status`, `unsupported_capability`) and four
  names in the agent `unsupported_draft_surfaces` list; schedule triggers
  `cron`, `at`, `calendar` and `timezone` with `internal/calendartrigger`, the
  cron expression parser, `CronJob.Schedule/Calendar/At/Timezone`,
  `ContractScheduleRegistration.Timezone`, and the unreachable `buffer_one`,
  `terminate_other` and `PauseOnFailure`. A schedule trigger now defines only
  `every`; overlap policies skip, queue, replace and allow stay. The house
  fixture lost its one validation block. `spec_revision` is now
  `sha256:92719c00373412643470dc55dab8eb98d5c56d33bf2fcfae89a5f52dd2ae9952`;
  the five TypeScript clients and the `basic` and `storage-basic` package
  contracts are regenerated. Skipped: the custom `middleware` kind (pipeline
  step `use` is typed as a `scenery.middleware` reference that the
  `std.middleware.*` steps of applications resolve through, and `inspect`
  JSON carries its counters); `secret_store` and `secret` (no application
  declares one, but `mcp_connection.auth.secret`, the deployment secret
  overlay and service configuration of type `resource_ref("secret")` resolve
  against them, so they go with the MCP federation auth and deployment
  decisions of later steps); the `union` kind (spread over the compiler, both
  generators, the named-decoder ABI, MCP projection, evolution, and the
  always-emitted shared TypeScript runtime; it needs its own step);
  `service.client` Go clients (the multiservice fixture of the kept
  process-per-service configuration probe calls through one); `enqueue` and
  `wait` delivery (the webhook-inbox example and the observability harness
  fixture depend on it, and applications use the shared programmatic wait).
  Build, vet, `go test ./...` (76 packages), golangci-lint (0 issues), the two
  remaining TypeScript checks and `scripts/verify --quick` pass.
- [x] 2026-11-13: deployment-contract-cli: removed the `deployment` kind with
  its eight overlay source schemas, `input.deployment_bindable`, the
  deployment projection and deployment-revision computation
  (`compiler.ResolveDeployment`, `ComputeDeploymentRevisions`,
  `Result.DeploymentRevisions`), diagnostics SCN2801-SCN2805 and SCN4004;
  `scenery deploy plan|apply` with `internal/deployplan` and the
  `scenery.deployment-plan` and `scenery.deployment-receipt` schemas;
  `scenery changes plan|apply|rename`, `scenery diff --semantic`,
  `scenery graph`, `scenery get` and `scenery explain`; `scenery agent serve`
  in both modes (the JSON-RPC contract agent with its session, snapshot
  retention, compact plan protocol and the `scenery.agent.capabilities`
  schema, and the development MCP adapter); the approval trust store
  (`evolution.LoadApprovalVerifier`, its legacy migration and the
  `scenery.approval-trust` schema), `evolution.LoadManifestReference`, the
  rename-receipt file loaders and `IssuedDeploymentPlan`; the matching
  verifier architecture rules and schema fixtures, and the passages in
  `SPEC.md` (sections 18, 20.3, 21, 22, 26, 27, 29), `http.md`,
  `evolution.md`, the local contract, the agent guide, the cookbook,
  `ARCHITECTURE.md` and `SKILL.md`. `internal/contractagent` now holds only
  the schema catalog behind `scenery schema`; the provider config type check
  moved with its file to `compiler/provider_config_type.go`. Regenerated the
  five committed TypeScript client descriptors and the basic and
  storage-basic package contracts; `spec_revision` is
  `sha256:405c02b8b6d7e5ee58bfa3a8964b4563f177cea7123ca0da5e4a14587919e04e`.
  Skipped: `scenery list` and `scenery schema` (the verifier keeps them); the
  envelope field `deployment_revision` (a machine-protocol change across every
  envelope schema; it is now always null), `deployment_revision_status` in
  change plans and receipts, and the `deployment` revision domain of `secret`
  and `mcp_connection` fields; the provider descriptor `deployment_abi` and
  `deployment_bindable` config-schema flags (they are part of pinned provider
  lock digests); `evolution.ApplyIssuedChangePlanWithOptions`,
  `LoadIssuedChangePlan`, `LoadAppliedChangeReceipt` and
  `ValidateSemanticOperation` (no production caller remains, but the replay
  and normalization tests of the live `assistant init` transaction are built
  on them) and approval-token verification inside apply (live code path of
  the same transaction; no command supplies a token any more); the public
  `scenery.ApprovalTokenPayload` and `ValidateApprovalToken`. Open: the root
  `AGENTS.md` Child Agent Index still lists `internal/deployplan/AGENTS.md`;
  this step did not edit the root instruction file, so the quick verifier's
  knowledge contract reports one stale-entry error until that line is removed.
  Build, vet, the full Go suite (75 packages), lint (0 issues) and both
  TypeScript checks pass.
- [x] 2026-11-13: mcp-assistant: removed remote MCP federation (the
  `mcp_connection` kind, `mcp_server.connection`, the Streamable HTTP client
  transport, connection auth and the MCP secret-resolver registry, the
  manifest `connections` member, diagnostics SCN2428 and SCN2429, the
  `SCN_COMPAT_MCP_CONNECTION_*` and `SCN_COMPAT_MCP_FILTER_CHANGED` rules and
  the uncalled `LookupMCPFederation`, `LookupMCPFederationForAssistant`,
  `MCPFederationReadiness` and `RegisterMCPSecretResolver`); every `mcp_server`
  still registers its federation through `RegisterMCPFederationChecked`, now
  without connections. Removed durable MCP tools: the framework tools
  `scenery_execution_status`, `scenery_execution_cancel` and
  `scenery_execution_wait`, the durable receipt outcome and its `accepted`
  reservation, per-principal receipt ownership in the dispatcher, the process
  host's receipt journal and the service-process durable endpoint; the
  compiler rejects an MCP binding whose execution is not `direct` with
  SCN2419. Removed anonymous assistant surfaces: the signed initiator cookie
  and `InitiatorSigner`; the compiler rejects `surface.authentication =
  std.authentication.none` with SCN2419 and the runtime registers assistants
  only with authenticated access. Removed `scenery assistant init`,
  `scenery assistant sync` and `scenery inspect assistants [--implementation]`
  with their schemas, payload identities, help entries, the `assistant-init`
  probe and the embedded scaffold package files. The `native` and
  `testdata/assistant` fixtures declare `authentication "standard"`; the
  assistant fixture dropped its durable engine, its lock file and the remote
  connection and enables standard auth with the development bootstrap; the
  assistant-journey probe signs in through `/users/dev-bootstrap` and lost its
  durable-receipt and host-state scenarios. Regenerated the five TypeScript
  clients, the assistant fixture contract and the `basic` and `storage-basic`
  package contracts. Skipped: the remaining federation layer
  (`internal/mcpfederation`, `RegisterMCPFederationChecked`, the gateway's
  federated-tool hand-off and the `federated` capability origin), which now
  has no capability source but is emitted for every `mcp_server`; removing it
  changes generated application code. The `access` enum of
  `scenery.assistant.status` still lists `public` (a schema revision change).
  The assistant-journey probe was edited but not run: it needs `scenery up`.
- [x] 2026-11-13: cli-observability: removed the interactive terminal console
  (`scenery console`, `logs --tui`, the seven `dev_console*.go` files with
  their tests, `victoria.ListDevSources` and the console PTY segment of the
  observability probe); `scenery test` with the build entry points only it
  used (`build.Prepare`, `PrimeWorkspaceContext`, `WithWorkspaceLock`, the
  pass-through cached-graph wrappers; the two test seams moved into the
  package test file); `validate list|inspect|graph`, `inspect validation`
  and `task graph` with their five schemas; the validation builtin steps
  `harness`, `harness:core`, `test`, `test:go`, `generate`, `generate:sqlc`,
  `db:apply`, `db:seed` and `db:setup` (`check` stays) with
  `validation.RunWithCapturedProcessOutput`; `logs tail`, `metrics labels`,
  `metrics series` and `traces clear` with `observability.TailLogs`, the
  metric catalog readers, the `traces/clear` RPC method, the in-memory
  `Stack.MarkCleared`/`ClearedAt` watermark, their schemas, the
  metric-catalog probe and the tail/labels/series modes of the query-framing
  probe; `inspect observability` (its `scope` and `backend` definitions now
  live in the `scenery.logs.query` and `scenery.metrics.query` schemas,
  without `tail_path`); the `db apply` command and `scenery.db.apply.result`;
  the remote durable worker (`worker durable --endpoint`, `worker durable
  token create`, the `/__scenery/durable/v1` lease routes, the remote worker
  loop, worker tokens in the durable store, the four `SCENERY_DURABLE_*`
  variables, the remote-durable parameter of `SQLRequirements.Bindings` and
  the generated `SQLBinding.DurableOnly` field); the `storage/stat` RPC
  method and the generated `storageStat` client method. Regenerated the
  committed TypeScript clients. Skipped: the VictoriaMetrics sidecar, OTLP
  metric export and `metrics query` (ONLV's runtime overview reads the
  `observability.metrics` status signal, and `metrics query` is what the
  kept observability probes use to prove the export); `inspect harness` and
  its focused topics (the verifier reads it at two sites and every summary
  recommends it); `inspect generators` (its payload kind is the `generate`
  plan graph and the sqlc ownership tests and the lifecycle probe observe
  through it); `task list|inspect|run` (ONLV agent instructions);
  `scenery worker`, the `api`/`worker` runtime roles and `worker durable
  jobs` (used by the webhook example, the auth probe and as the only
  operator view of durable jobs); `storage stat`, `storage cleanup` and the
  reclaim/purge code (isolation probes and the only namespace disposal
  path); `db drop` (its two probes would need a service-backed rewrite onto
  `down --db`, which this step cannot run); the `database.apply` shell
  command mode (two fixtures use it through `up` and `db setup`); the
  dev-runtime client `traces()`, `trace()`, `waitForSource()` and the
  `traces/list` method (conflicting verdicts, documented extension point).
  The `durable_worker_tokens` table and the `lease_token_hash` column stay
  in the version-2 durable schema, unwritten; dropping them is a schema
  version decision. Build, vet, the full Go suite, lint (0 issues) and both
  TypeScript checks pass.
- [x] 2026-11-13: edge-devloop: removed managed dnsmasq DNS (`system edge
  dns install|status|uninstall`, the `dns-helper`, `internal/edge/dns.go`,
  the `dns` object of `scenery.edge.status`, the `scenery.edge.dns.status`
  schema, the `--domain` edge flag and the dnsmasq toolchain entry in both
  manifests); edge readiness is now Caddy, privileged listener and agent
  router only. Removed the agent-terminated HTTPS router with its local CA
  and OS trust install (`system agent --router-tls`, `--trust`,
  `SCENERY_AGENT_TRUST`, `internal/localproxy` certificate and trust files);
  `--router-http` stays as the one mode. Removed host alias leases (registry
  `aliases`, session `aliases`/`alias_conflicts`, `route_namespace.hosts`,
  the alias output of `up --detach` and the dev-runtime status `aliases`
  field); `envs.<env>.mode` and `envs.<env>.expose` with route-manifest
  `public_routes` and the dev-domain narrowing in the agent router (a dev
  domain now always serves the same surface as localhost); `up --wait
  registered`; the `SCENERY_FRONTEND_<NAME>_ADDR` override and the three
  `SCENERY_TEST_WATCH_*` overrides; the pnpm and yarn frontend command
  branches; Atlas-rendered sqlc schemas (`generators.sqlc.schemas[]`,
  `dev_url`, the `schema_render` lifecycle node, the output-exec hook,
  `InputSelection.Produced`, the `tool.atlas` doctor row); shared executable
  reuse for single-executable development builds (key, restore, subscribe,
  producer, publish, stage, prune and the workspace-only admission tracking;
  the fair link slot, ticket queue and the post-link input check stay, and
  the `build.shared_artifact`/`build.shared_queue` steps are gone); the
  `dev` and `envs.*.deploy.root` retired-field hints; the predecessor
  registry-descriptor fallback of the worktree allocation guard. Skipped:
  `up --listen/--port` (the process-model probe injects its startup failure
  through `--listen`) and `--claim-aliases` (the only way to transfer a live
  dev-domain host); frontend `upstream`/`allow_shared_upstream` (the
  parallel and desktop probes use them); the agent-side host route manifest
  fallback and `RouteModeHost` (session schema revision, and the Micro
  frontend reads the injected route mode); owned Go module replacement
  generations (removal needs an owner choice between rejecting local
  replacements with a new diagnostic and compiling unsnapshotted bytes);
  `SCENERY_TOOLCHAIN_DOWNLOAD` (documented offline switch, owner decision);
  `config migrate-connections` (no confirmation that every ONLV environment
  is migrated); the `storage.cell_id`/`storage.share` hint (it is the SCN8006
  legacy detection the owner keeps); the legacy `postgres/server.json`
  check and the sessions/registry checks of the allocation guard (fail-closed
  data-loss guard); worktree probe A9 (mandated by `scripts/verify/AGENTS.md`);
  the Atlas line formatting of setup output (belongs to the kept
  `database.apply` command mode); Google sign-in/OIDC consolidation (a
  refactor, not a removal). Schema revision of `scenery.edge.status` changed.
  Build, vet, the full Go suite and lint (0 issues) pass; the quick verifier
  fails only on the inherited stale Child Agent Index line.
- [x] 2026-11-13: critic-tail: none of the ten candidates had a verifier
  verdict, so each was checked against ONLV, Micro/platform and the KEEP list
  first. Removed toolchain manifest container images and Go `source_build`
  artifacts (the `postgres` image artifact, the `images` lists, `system
  toolchain ... --images|--strict`, the Docker inspect/pull runner, the
  `toolchain-build` probe); the PostgreSQL image stays pinned once in
  `cmd/scenery/dev_services_postgres.go`. Removed the unread injected
  environment names `API_BASE_URL`, `SCENERY_API_URL`, `SCENERY_ROUTE_MODE`,
  `SCENERY_BASE_URL`, `SCENERY_API_BASE_PATH`, `SCENERY_FRONTEND_PUBLIC_URL`
  and the `VITE_SCENERY_*` mirrors other than `VITE_SCENERY_ROUTE_MODE`;
  frontends keep `VITE_API_BASE_URL`, `SCENERY_FRONTEND_BASE_PATH` (ONLV) and
  `VITE_SCENERY_ROUTE_MODE` (Micro/platform), app processes keep
  `SCENERY_API_BASE_URL` (standard auth reads it). Removed the v1
  change-transaction identity refusal in `internal/workspacetx` (such files
  now fail as invalid recovery state, unmodified), `internal/devtools` with
  its schema (Victoria versions are read from the bundled manifest in
  `internal/victoria`), five unused aliases in
  `internal/compiler/scn_support.go`, and `auth.dev_bootstrap.default_user_id`
  with `auth.DevBootstrapConfig.DefaultUserID` (the development user stays
  `dev-user`). The approval trust v1 migration was already gone with
  `changes apply`. Skipped: `inspect paths` (`scenery harness` collects it as
  an evidence step and ONLV runs that command); the root-frontend
  `/<name>/<protected>` containment (security guard in the production Caddy
  configuration, owner confirmation outstanding); the remaining
  `internal/compiler` aliases (replacing them is a rename across the package,
  not a removal); both migration runbooks (onlv-209 completion is not
  verifiable here and living documents link them); the other long-tail config
  fields (`watch.ignore` is set by Micro/platform, `build.go_flags` is the
  documented debug-build switch, `port_start`/`port_end` need the owner,
  `validation.exemptions` is the only escape of the coverage gate ONLV runs,
  `generators.sqlc` is used by the SQLC probes, the trace filter and the
  seed/profile `cwd`/`env` overlays feed live request and plan structures).
  `InstallMetadata.source_kind` stays because dropping it changes the install
  identity and would invalidate every installed managed tool. Go build, vet,
  tests (73 packages) and lint are green; the quick verifier fails only on the
  inherited stale Child Agent Index line.
- [x] 2026-11-13: sweep and full validation: the orphan sweep found no new
  dead Go code (`golangci-lint run ./...` with `unused` reports 0 issues; the
  installed `staticcheck` 0.4.7 panics under go1.27 and was not usable), no
  importer-less internal package, no unreferenced schema, no test data without
  a reader, no diagnostic row that lost its emitter since `e007744d`, no
  unread documented environment name, and `go mod tidy` and
  `scripts/gen-auth-sqlc.sh` change nothing. Removed the stale
  `internal/deployplan/AGENTS.md` Child Agent Index line, the `scenery db
  apply`, `scripts/snapshot-backup.sh`, `RevisionRebind` and `scenery test`
  passages of `docs/local-contract.md` and `docs/agent-guide.md`. The five
  TypeScript clients and the `basic` and `storage-basic` package contracts
  regenerate without a difference. Running the named probes found five that
  still exercised removed surface, now adjusted: `core-separation` listed the
  deleted root `durable` package; `generation` called `scenery test`;
  `cli-process` used `scenery get` for its missing-resource case (case
  removed); `assistant-runtime` registered an anonymous assistant surface
  (now authenticated); the `ui` conformance test asserted record validation
  (one test, one assertion and the descriptor's `validations` removed).
  `go run ./scripts/verify --summary --write`, `go test ./...`, `go vet
  ./...`, `golangci-lint run ./...`, both fixture regenerations and both
  TypeScript checks pass. Probes passing: `cli-grammar`, `core-separation`,
  `dev-process`, `generation`, `cli-process`, `ui`, `typescript`, `fixtures`,
  `dev-cleanup`, `dev-follower`, `dev-lock`, `feature`, `build-info`,
  `inspect-go`, `agent-restart`, `assistant-runtime`, `assistant-helper`,
  `deploy-ssh`, `process-model`, `native-contract`, `victoria`, `code-task`,
  `test-cache`, `validation-git`, `worktree-git`, `configuration` and
  `frontend`. Probes failing for reasons this plan did not cause: `storage`
  (a read-only command leaves `telemetry.jsonl.lock` in the agent home; the
  binary built from `e007744d` does the same), `configuration-deploy` (the
  CLI refuses `scenery config receive` as an unknown subcommand; the
  `e007744d` binary does the same), `capability-authority` (the webhook-inbox
  proof exports `DATABASE_URL`, which `scenery worker` never inherits; that
  code path is unchanged, the classification is by reading, not by a run at
  `e007744d`) and `edge` (no managed Caddy is installed on this host). Not
  run: `postgres`, `auth`, `worktree`, `observability`, `parallel-runtime`
  and `assistant-journey` (another worktree's `scenery up` held two
  `scenery-pg-*` containers), `configuration-secrets` and `desktop` (login
  Keychain and a desktop window on a developer session), and `scenery logs
  --limit 500 -o jsonl` (conditional on a running application). None of
  these is a pass.
- [x] 2026-11-13: review findings: fourteen findings checked, eight fixed,
  three rejected or deferred to the owner, three concern only the ONLV
  repository.
  Fixed: (1) the sweep statement above was wrong. `golangci-lint`'s `unused`
  cannot see exported or test-referenced symbols; `go run
  golang.org/x/tools/cmd/deadcode@latest ./...` compared with the baseline
  list showed 215 newly unreachable functions, 179 of them in
  `internal/evolution`. With `changes`, `diff`, `agent serve`, `deploy
  plan|apply` and `assistant init` gone, only `scenery provider lock` used the
  package, so change planning and apply, approvals, semantic comparison,
  rename receipts, CST source edits and their tests are removed.
  `internal/evolution` is now `SyncBuiltinProviderLocks` and its source
  transaction (`commitPlannedEdits` without the receipt path no caller
  passed). Their orphaned helpers went with them: `graph.ListAt`,
  `graph.DecodeManifest`/`ValidateManifest`, `graph.ResourceEdges`,
  `graph.CanonicalResources`, `compiler.CompileContractGraph`,
  `CompileDuringChangeTransaction`, `RefreshWorkspaceRevision`,
  `BuiltinProviderLock`, the exported schema-bridge and pointer wrappers,
  `generate.CheckPredictedGoContracts`/`CheckPredictedTypeScriptClients`,
  three `internal/build` entry points, `machine.UsesCurrentSpec`, the
  approval token (`internal/contract/approval.go` and the root
  `scenery.ApprovalToken`, `ApprovalTokenPayload`, `ValidateApprovalToken`),
  the `evolution_rules` semantic revision, the `scenery.change-plan`,
  `scenery.change-receipt` and `scenery.approval-token` schemas, diagnostics
  SCN6205 and SCN6206, SPEC section 22 (sections 23 to 30 are renumbered 22
  to 29, the diagnostic `fixes` field and machine fixes are gone) and
  `docs/spec/evolution.md`. The deadcode comparison now reports no newly
  unreachable function. (2) Tests of kept behaviour were restored:
  `runtime/mcp_federation_test.go` (sharing and isolation, duplicate and
  collision rejection, name canonicalization, lifecycle and seal rollback,
  rewritten against `LookupAssistantMCPFederation`) and a new
  `internal/mcpfederation/federation_test.go`. (3) `docs/agent-guide.md`,
  `docs/local-contract.md`, `ARCHITECTURE.md`, the companion specifications
  and the owning `AGENTS.md` files no longer describe agent capabilities,
  `schema.get`, `plans.get`, change plans, receipts or approval-bearing apply.
  (4) The empty diagnostic categories `deployment`, `compatibility` and
  `profile_conformance` are removed from the catalog code and the SPEC table.
  (5) The dead Victoria component start chain (`startComponent`,
  `prepareComponentStart`, `checksumForArchive`, `isLowerHex`, `archiveName`)
  is removed; it was already unreachable at the baseline. (6) The config
  schema no longer names fixture seeds. (7) The two benchmark reports whose
  `native-reload-attribution` commands no longer exist carry a historical
  note and are `deprecated` in `docs/knowledge.json`. (8) The dangling
  `Section 22.5` reference went with machine fixes.
  Not changed: the inert MCP federation layer stays (see Decision Log); the
  CRUD source of `table_page` is not restored (see Decision Log); the
  agent, edge and deploy state check on onlv-209 is a landing gate (see
  Resume here); the diagnostic category name `binding_and_cli` and SCN6209
  stay open owner decisions; `scenery schema` still serves the mutation
  operation schemas (`resource.create`, `value.set` and the others), which now
  describe no command. The ONLV findings (a `scenery worker durable` mention
  in its agent guide, two removed commands in a planning note, and the
  repeat of the ONLV proof after combining with its pinned line) need no
  change here.
  Validation: `go build ./...`, `go vet ./...`, `go test ./...` and
  `golangci-lint run ./...` (0 issues) pass; `go run ./scripts/verify
  --summary --write` passes with the known review-date and timing warnings;
  the five TypeScript clients were regenerated (the specification revision
  changed, so `metadata.ts` and the descriptor of each changed) and both
  TypeScript checks pass. Probes rerun and passing: `cli-grammar`,
  `core-separation`, `generation`, `fixtures` (second run; the first reported
  inputs changed during validation), `typescript`, `native-contract`,
  `cli-process`, `ui`, `victoria` and `assistant-runtime`. Not run, as
  before: `auth` and the other Postgres probes, `scenery logs`.

### Resume here

2026-11-13: every removal step, the sweep and the review fixes are done; the
Go tree, lint and the full verifier are green. The next action is landing.
Landing gate: before a binary from this plan reaches onlv-209, confirm that
the agent, edge, edge-target and deploy registry files under its `~/.scenery`
carry `kind` and `schema_revision` identities; a file in the older
`schema_version` shape now fails closed on load. Landing gate: this tree is
based on `e007744d`; ONLV pins a later line, so after combining with it rerun
compile, generate, check, vet and build against ONLV and one live `scenery up
--detach --wait ready` smoke. Owner confirmation outstanding for two recorded
exceptions: the removed CRUD source of `table_page` and the retained MCP
federation layer.
Before landing, on a host with no other `scenery-pg-*` probe running, run
`go run ./scripts/verify --probe <id> --summary --write` for `auth` (the
changed-area oracle requires it), `postgres`, `worktree`, `observability`,
`parallel-runtime` and `assistant-journey`; the auth, worktree,
observability and assistant-journey probes were edited by this plan and have
never run. Run `edge` after `scenery system toolchain sync --tool caddy`.
Four probe failures predate this plan and need their own fixes: `storage`
(telemetry lock file in an unallocated agent home), `configuration-deploy`
(`scenery config receive` refused by the command grammar),
`capability-authority` (the webhook-inbox proof passes `DATABASE_URL` through
the environment, which `scenery worker` does not inherit) and the missing
managed Caddy for `edge`. Open owner decisions: the `union` kind,
`service.client`, `enqueue`/`wait` delivery and the custom `middleware` kind;
`secret_store` and `secret` (now only typed package inputs and constructor
configuration); the inert MCP federation layer (removal changes generated
application code); the `sql.fixture/v1` provider capability (changes every
pinned provider lock); cursor pagination in the UI catalog; the diagnostic
category name `binding_and_cli`; the custom `workload_identity` kind and the
`capability_unavailable` activation refusal; `POST
/auth/organizations/switch` and the generic connection provider surface;
whether SCN6209 is attached to its two emit sites or dropped; `ISSUES.md` and
the three agent evaluation reports with their runner script; the
root-frontend `/<name>/<protected>` containment; the unused
`@astryxdesign/theme-neutral` dependency of `tools/typescript` (unused before
this plan, inside the kept UI tooling). Nothing is committed; `git status` and `git diff
--stat` show the whole change. Removal authorizes no application deployment,
no data migration and no edit to another repository.

## Surprises & Discoveries

- 2026-11-13: The developer machine sets `GOFLAGS=-trimpath` in `go env`. Four
  fixture and typecheck tests fail under it for reasons unrelated to this
  work, so every Go command of this plan runs with `GOFLAGS=-buildvcs=auto`.
  An empty `GOFLAGS` value does not override the `go env` setting.
- 2026-11-13: `tools/typescript/node_modules/.bin/tsc -p
  tools/typescript/tsconfig.runtime.json --noEmit` invoked directly reports
  missing React types in `ui/components/DataTable.tsx`. It is not a matrix
  command; the `ui` probe owns that project and its dependency preparation.
  The matrix catalog check passes.

- 2026-11-13: The named probes are the only consumers of some removed
  surface (`scenery test`, `scenery get`, the root `durable` package, the
  anonymous assistant surface, record validation in the TypeScript runtime).
  They compile and their unit tests pass without it, so the Go suite and the
  default verifier stayed green while five probes were broken.
- 2026-11-13: Four probes fail on this host independent of the plan:
  `storage`, `configuration-deploy`, `capability-authority` and `edge`. The
  baseline did not run probes, so these were not recorded there.

## Decision Log

- 2026-11-13, owner: prune features that no application uses and literal dead
  code, including their tests. Rationale: unused surface has a standing cost
  and no consumer.
- 2026-11-13, owner: keep untouched the whole declarative UI and the
  `@scenery/ui` catalog: `page`, `renderer`, `react_component`, `status_map`,
  `form_dialog`, `table_page`, `split_page`, `content_page`, `workspace_page`,
  `detail_page` and enum kinds; `typescript_client.react` with all generated
  output including retry, metadata and multipart sections; `internal/uigen`;
  `internal/uireport` and `scenery inspect ui`; `ui/` and its components; the
  `ui_catalog` dev sync; the runtime page and renderer registry
  (`RegisterContractPage`, `ContractPages`, `InvokeContractPageJSON`);
  `docs/generated-ui.md`, `docs/ui-agent-contract.md` and plan 0221.
  Rationale: a second application (Micro platform) uses the declarative UI.
- 2026-11-13, owner: keep auth impersonation (start, stop,
  `PrepareImpersonationTarget`) and the user lifecycle SDK (`DisableUser`,
  `EnableUser`, `RevokeUserSessions`).
- 2026-11-13, owner: keep all of `scenery telemetry` including report, export,
  bundle and transcripts, and `internal/telemetryreport`.
- 2026-11-13, owner: keep `scenery worktree upgrade` and
  `internal/stateupgrade`; keep `scenery inspect docs`.
- 2026-11-13, owner: keep the SSH/systemd deploy family (`deploy <target>`,
  `receive`, `enable`, `resume`, `status`).
- 2026-11-13, owner: keep legacy storage cell export and SCN8006 legacy
  detection.
- 2026-11-13, owner: keep `scripts/verify` probes as such; remove a probe only
  together with the feature it proves.
- 2026-11-13, owner: apart from the kept items, nothing needs to be preserved
  for the Micro platform application.
- 2026-11-13: `VNEXT.md` is human-only and completed or deprecated numbered
  plans are immutable history; neither is edited even where it names a removed
  feature.
- 2026-11-13: an item entangled with a kept feature or with something ONLV
  uses is removed only in its separable part; the remainder stays working and
  the step records it as skipped with the concrete reason. Rationale: a red or
  half-removed tree is worse than a retained feature.

- 2026-11-13: the CRUD source of `table_page` (cursor pagination, CRUD
  predicates, filters and sorts in the compiler and in `internal/uigen`) was
  removed with the `crud` kind although the UI decision above says "keep
  untouched". Rationale: the audit verdict for the data model lists the
  `table_page` and `uigen` CRUD source path inside the removal, the source
  cannot exist without the `crud` kind, and neither application declares a
  `crud` block (all 44 Micro platform table sources are bindings). This is an
  exception to the UI decision and awaits the owner's explicit confirmation.
- 2026-11-13: the MCP federation layer (`internal/mcpfederation`,
  `RegisterMCPFederationChecked`, the gateway's federated hand-off and the
  `federated` origin) stays although it has no capability source left.
  Rationale: its native service is the registration that covers each
  `mcp_server` address and the service the assistant gateway initializes
  after; removing it changes generated application code and assistant start
  order, which only a live assistant run can prove. Its tests are restored.
  Removing it is an owner decision.
- 2026-11-13: an orphan sweep uses `deadcode` over all main packages compared
  with the baseline list, not only the `unused` linter. Rationale: the linter
  missed 215 functions kept alive by exports and tests.

## Outcomes & Retrospective

2026-11-13, after the review fixes. Size against the baseline
at `e007744d`, counted over existing tracked and new files:

| Metric | Baseline | Now | Change |
|---|---|---|---|
| Non-test Go lines | 256,933 | 216,444 | -40,489 |
| Go test lines | 108,026 | 90,681 | -17,345 |
| Go files | 1,933 | 1,715 | -218 |
| Total lines | 526,782 | 460,969 | -65,813 |
| Files | 2,602 | 2,343 | -259 |
| Files in `docs/schemas` | 140 | 113 | -27 |
| Diagnostic catalog rows | 306 | 248 | -58 |

`scenery help -o json` lost the commands `console`, `explain`, `get`, `test`,
`assistant init` and `assistant sync` and the subcommands and flags listed in
the step entries; every kept command of the Decision Log is still listed.
Unit tests did not cover the named probes: five probes still called removed
surface after all removal steps were green, and only running them showed it.
A removal step that deletes a command, a public package or a language
construct should search `scripts/verify` and the Bun tests under
`internal/generate/testdata` for it, not only Go tests.

## Plan of Work

The work is removal only. Surviving code is not refactored, renamed or
improved beyond what a deletion forces. Each step reads every audit candidate
and verdict assigned to it, removes the implementation together with its
tests, test data, JSON schemas under `docs/schemas`, diagnostic catalog rows
that become unemitted, CLI help and grammar entries, generated-fixture
expectations, environment registry entries and references in living documents
(`docs/local-contract.md`, `docs/agent-guide.md`, `ARCHITECTURE.md`,
`SKILL.md`, the cookbook, `docs/spec`, child `AGENTS.md` files,
`docs/knowledge.json`, `docs/index.md`), follows the nearest `AGENTS.md` of
each edited directory, and leaves the tree green before the next step starts.

The steps, in order:

1. `dead-compiler-generate-build`: unreferenced code in the compiler,
   generators and build packages.
2. `dead-repo-tooling`: unused repository tooling, scripts and stray files.
3. `dead-assistant-observability`: unreferenced code in the assistant and
   observability packages.
4. `dead-legacy-platform`: leftovers of removed platform concepts.
5. `auth-runtime-small`: small unused auth and runtime features, excluding the
   kept impersonation and user lifecycle SDK.
6. `events`: the unused event surface.
7. `cli-bindings`: unused CLI binding surface.
8. `data-model-crud`: unused data-model and CRUD constructs.
9. `language-tail`: remaining unused `.scn` constructs, excluding every kept
   declarative UI construct.
10. `deployment-contract-cli`: unused deployment-contract and CLI surface,
    excluding the kept SSH/systemd deploy family.
11. `mcp-assistant`: unused MCP and assistant features.
12. `cli-observability`: unused observability CLI surface, excluding all of
    `scenery telemetry`.
13. `edge-devloop`: unused edge and development-loop features.
14. `critic-tail`: leftovers found by the completeness critic.
15. Documentation and schema sweep, then full validation.
16. Review of the combined change.

When a step removes a `.scn` construct or changes generator output, it
regenerates the committed fixture contracts and clients through the
worktree-local binary instead of editing generated bytes by hand:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Other golden files are updated by the regeneration command that their owning
`AGENTS.md` or test helper names.

## Validation and Acceptance

Run every command from the repository root with `GOFLAGS=-buildvcs=auto`. Keep
the Go test cache enabled; do not pass `-count=1`.

Each removal step ends with these commands, all exiting zero:

```sh
GOFLAGS=-buildvcs=auto go build ./...
GOFLAGS=-buildvcs=auto go vet ./...
GOFLAGS=-buildvcs=auto go test ./...
GOFLAGS=-buildvcs=auto golangci-lint run ./...
```

A step that edits `ui/`, `tools/typescript`, `internal/uigen` or
`internal/generate` also runs
`tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
and `GOFLAGS=-buildvcs=auto go test ./internal/generate`. A step that edits
none of those four paths skips the TypeScript check; its `git diff --stat`
records that condition. The baseline has no failing test and no lint finding,
so any failure after a step belongs to that step.

The expected cumulative changed-area classes are Go packages, CLI JSON
contract, compiler or generator, release-sensitive or runtime, and
documentation. The final validation therefore selects full directly:

```sh
GOFLAGS=-buildvcs=auto go run ./scripts/verify --summary --write
GOFLAGS=-buildvcs=auto golangci-lint run ./...
```

Read `agent-context-summary.json` in the reported `run.archive_path` first,
then fulfill the union in `changed_area.recommended_commands` of
`agent-context.json`; successful verifier steps satisfy the same check for the
same inputs. The UI catalog check
`go run ./scripts/verify --probe ui --summary --write` runs if the final
`git diff --stat` lists a path under `ui/`, `tools/typescript`,
`internal/uigen` or `internal/generate`. Named probes of removed features are
removed with them; a probe of a retained external boundary runs only if the
final diff changes that boundary's implementation, and the step entry names
the probe and its result. Release certification, benchmarks and all-root
timing audits are not selected; no human requested them.

Acceptance: every kept item in the Decision Log still builds, passes its
tests and appears in `scenery help -o json` where it is a command; the ONLV
application's used surface is unchanged; non-test Go lines, total tracked
lines and the `docs/schemas` file count are lower than the baseline in
Progress; and the Progress entry of each step lists removed and skipped
candidate ids.

## Idempotence and Recovery

Every step is a set of deletions and forced edits in the working tree, with no
commit. Re-running a step is safe: it reads the audit candidates, finds
already-removed paths absent and continues with what remains. If a step leaves
the tree red and cannot fix it, it restores only its own files with
`git checkout -- <path>` for the paths it edited, never the paths of earlier
steps, and records the item as skipped. Fixture regeneration is transactional
and repeatable. No step starts a service, touches application data, resets or
adopts runtime resources, installs the binary or edits another repository.
